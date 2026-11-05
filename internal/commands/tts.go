package commands

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"

	"github.com/Sonty15/SONNIE/internal/readaloud"
	"github.com/Sonty15/SONNIE/internal/tts"
	"github.com/Sonty15/SONNIE/internal/usage"
)

// TTSController is what /tts needs from the read-aloud manager.
type TTSController interface {
	Join(ctx context.Context, req readaloud.JoinRequest) (readaloud.JoinResult, error)
	Leave(guildID, userID string) (readaloud.LeaveResult, error)
}

// UsageReporter is what /tts usage needs from the TTS usage meter.
type UsageReporter interface {
	Snapshot() usage.Snapshot
}

var usageReporter UsageReporter

// SetUsage gives /tts usage access to the usage meter. Same rules as SetTTS.
func SetUsage(u UsageReporter) {
	usageReporter = u
}

// ttsController is set by SetTTS during startup. It stays nil when the
// feature is disabled, and /tts then says so.
var ttsController TTSController

// SetTTS gives /tts access to the read-aloud manager. Call it before the
// session is opened; it is not safe to call while handlers are running.
func SetTTS(c TTSController) {
	ttsController = c
	var pool []string
	if vp, ok := c.(interface{ VoicePool() []string }); ok && vp != nil {
		pool = vp.VoicePool()
	}
	setJoinVoiceChoices(pool)
}

const (
	ttsJoinTimeout  = 15 * time.Second
	maxVoiceChoices = 25
)

var joinVoiceOption = &discordgo.ApplicationCommandOption{
	Type:         discordgo.ApplicationCommandOptionString,
	Name:         "voice",
	Description:  "Pick a voice, male or female (default: a random male voice)",
	Autocomplete: true,
}

// pickableVoice is one voice offered by /tts join voice:.
type pickableVoice struct {
	Name  string
	Label string // e.g. "Puck (ชาย)"
}

// pickableVoices is what /tts join voice: offers: the random pool (male by
// default) and the female voices. Set by SetTTS.
var pickableVoices = voiceList(nil)

func setJoinVoiceChoices(pool []string) {
	pickableVoices = voiceList(pool)
}

// voiceList returns the pool voices, then the female voices not in the pool,
// each labelled with its gender when it is known.
func voiceList(pool []string) []pickableVoice {
	if len(pool) == 0 {
		pool = tts.DefaultVoicePool
	}
	male := make(map[string]bool)
	for _, v := range tts.DefaultVoicePool {
		male[strings.ToLower(v)] = true
	}
	female := make(map[string]bool)
	for _, v := range tts.FemaleVoices {
		female[strings.ToLower(v)] = true
	}
	label := func(v string) string {
		switch k := strings.ToLower(v); {
		case male[k]:
			return v + " (ชาย)"
		case female[k]:
			return v + " (หญิง)"
		}
		return v
	}
	var out []pickableVoice
	seen := make(map[string]bool)
	for _, list := range [][]string{pool, tts.FemaleVoices} {
		for _, v := range list {
			if k := strings.ToLower(v); !seen[k] {
				seen[k] = true
				out = append(out, pickableVoice{Name: v, Label: label(v)})
			}
		}
	}
	return out
}

// matchVoice returns the pickable voice named name (any case).
func matchVoice(name string) (string, bool) {
	name = strings.TrimSpace(name)
	for _, v := range pickableVoices {
		if strings.EqualFold(v.Name, name) || strings.EqualFold(v.Label, name) {
			return v.Name, true
		}
	}
	return "", false
}

// voiceSuggestions answers autocomplete for voice: the voices whose label
// contains query (so "หญิง" lists the female ones), at most 25.
func voiceSuggestions(query string) []*discordgo.ApplicationCommandOptionChoice {
	q := strings.ToLower(strings.TrimSpace(query))
	var out []*discordgo.ApplicationCommandOptionChoice
	for _, v := range pickableVoices {
		if q != "" && !strings.Contains(strings.ToLower(v.Label), q) {
			continue
		}
		out = append(out, &discordgo.ApplicationCommandOptionChoice{Name: v.Label, Value: v.Name})
		if len(out) == maxVoiceChoices {
			break
		}
	}
	return out
}

// handleTTSAutocomplete suggests voices for /tts join voice:.
func handleTTSAutocomplete(s *discordgo.Session, i *discordgo.InteractionCreate) {
	var query string
	for _, sub := range i.ApplicationCommandData().Options {
		for _, opt := range sub.Options {
			if opt.Name == "voice" && opt.Focused {
				query = opt.StringValue()
			}
		}
	}
	if err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionApplicationCommandAutocompleteResult,
		Data: &discordgo.InteractionResponseData{Choices: voiceSuggestions(query)},
	}); err != nil {
		log.Printf("tts: voice autocomplete: %v", err)
	}
}

func init() {
	dmPermission := false
	register(Command{
		Definition: &discordgo.ApplicationCommand{
			Name:         "tts",
			Description:  "Read your messages in this text channel aloud in your voice channel",
			DMPermission: &dmPermission,
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionSubCommand,
					Name:        "join",
					Description: "Join the read-aloud session; your messages in this channel are read aloud, then deleted",
					Options:     []*discordgo.ApplicationCommandOption{joinVoiceOption},
				},
				{
					Type:        discordgo.ApplicationCommandOptionSubCommand,
					Name:        "leave",
					Description: "Leave the read-aloud session (the bot leaves voice when the last person leaves)",
				},
				{
					Type:        discordgo.ApplicationCommandOptionSubCommand,
					Name:        "usage",
					Description: "Show this month's Google TTS usage",
				},
			},
		},
		Handler:      handleTTS,
		Autocomplete: handleTTSAutocomplete,
	})
}

func handleTTS(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if i.GuildID == "" || i.Member == nil || i.Member.User == nil {
		ttsRespondEphemeral(s, i, "/tts ใช้ได้เฉพาะในเซิร์ฟเวอร์")
		return
	}
	ctl := ttsController
	if ctl == nil {
		ttsRespondEphemeral(s, i, "บอทนี้ไม่ได้เปิดใช้การอ่านแชทออกเสียง")
		return
	}

	options := i.ApplicationCommandData().Options
	if len(options) == 0 {
		return
	}
	userID := i.Member.User.ID
	switch options[0].Name {
	case "join":
		vs, err := s.State.VoiceState(i.GuildID, userID)
		if err != nil || vs.ChannelID == "" {
			ttsRespondEphemeral(s, i, "เข้าห้องเสียงก่อน แล้วค่อยใช้ /tts join")
			return
		}
		var pickedVoice string
		for _, opt := range options[0].Options {
			if opt.Name == "voice" {
				pickedVoice = opt.StringValue()
			}
		}
		if pickedVoice != "" {
			name, ok := matchVoice(pickedVoice)
			if !ok {
				ttsRespondEphemeral(s, i, "ไม่รู้จักเสียง "+pickedVoice+" — เลือกจากรายการในช่อง voice")
				return
			}
			pickedVoice = name
		}
		// Joining voice can take longer than the 3 s Discord allows for a
		// first response, so acknowledge now and edit the reply later. The
		// public message is the session message in the channel; this reply
		// is only for the caller.
		if err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{Flags: discordgo.MessageFlagsEphemeral},
		}); err != nil {
			log.Printf("tts: failed to defer interaction: %v", err)
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), ttsJoinTimeout)
		defer cancel()
		reply := ttsJoinReply(ctx, ctl, readaloud.JoinRequest{
			GuildID:        i.GuildID,
			UserID:         userID,
			VoiceChannelID: vs.ChannelID,
			TextChannelID:  i.ChannelID,
			Voice:          pickedVoice,
		})
		if _, err := s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Content: &reply}); err != nil {
			log.Printf("tts: failed to edit interaction response: %v", err)
		}
	case "leave":
		ttsRespondEphemeral(s, i, ttsLeaveReply(ctl, i.GuildID, userID))
	case "usage":
		ttsRespondData(s, i, ttsUsageResponse(usageReporter, time.Now()))
	}
}

func ttsJoinReply(ctx context.Context, ctl TTSController, req readaloud.JoinRequest) string {
	res, err := ctl.Join(ctx, req)
	if err != nil {
		var busy *readaloud.BusyError
		switch {
		case errors.As(err, &busy):
			return fmt.Sprintf("ตอนนี้บอทกำลังอ่านออกเสียงอยู่ในห้องเสียง <#%s> และยังมีคนอยู่ — ไปเข้าร่วมที่นั่น หรือรอให้จบก่อน",
				busy.VoiceChannelID)
		case errors.Is(err, readaloud.ErrSessionMessage):
			log.Printf("tts: join guild %s channel %s: %v", req.GuildID, req.VoiceChannelID, err)
			return "บอทส่งข้อความเซสชันในห้องนี้ไม่ได้ — ตรวจสอบว่าบอทมีสิทธิ์ View Channel, Send Messages และ Embed Links ในห้องนี้"
		}
		log.Printf("tts: join guild %s channel %s: %v", req.GuildID, req.VoiceChannelID, err)
		if errors.Is(err, context.DeadlineExceeded) {
			return "เข้าห้องเสียงไม่ทันเวลา — การเชื่อมต่อเสียงอาจช้าหรือล้มเหลว หรือบอทอาจไม่มีสิทธิ์ Connect และ Speak ในห้องนั้น"
		}
		return "บอทเข้าห้องเสียงของคุณไม่ได้ — ตรวจสอบว่าบอทมีสิทธิ์ Connect และ Speak ในห้องนั้น หรือลองใหม่หากการเชื่อมต่อเสียงล้มเหลว"
	}

	suffix := ""
	if res.Voice != "" {
		suffix = " · เสียง " + res.Voice
	}
	switch {
	case res.AlreadyIn:
		return fmt.Sprintf("คุณอยู่ในการอ่านนี้แล้ว — ข้อความของคุณใน <#%s> ถูกอ่านอยู่%s", req.TextChannelID, suffix)
	case res.VoiceChanged && !res.CanManage:
		return fmt.Sprintf("✅ เปลี่ยนเสียงแล้ว — ข้อความของคุณใน <#%s> จะถูกอ่าน (บอทไม่มีสิทธิ์ลบข้อความ)%s", req.TextChannelID, suffix)
	case res.VoiceChanged:
		return fmt.Sprintf("✅ เปลี่ยนเสียงแล้ว — ข้อความของคุณใน <#%s> จะถูกอ่านแล้วลบ%s", req.TextChannelID, suffix)
	case res.Moved && !res.CanManage:
		return fmt.Sprintf("✅ ย้ายแล้ว — จากนี้จะอ่านข้อความของคุณใน <#%s> แทน <#%s> (บอทไม่มีสิทธิ์ลบข้อความ)%s", req.TextChannelID, res.PreviousChannelID, suffix)
	case res.Moved:
		return fmt.Sprintf("✅ ย้ายแล้ว — จากนี้จะอ่านข้อความของคุณใน <#%s> แทน <#%s>%s", req.TextChannelID, res.PreviousChannelID, suffix)
	case !res.CanManage:
		return fmt.Sprintf("✅ เข้าร่วมแล้ว — ข้อความของคุณใน <#%s> จะถูกอ่าน (บอทไม่มีสิทธิ์ลบข้อความ)%s", req.TextChannelID, suffix)
	default:
		return fmt.Sprintf("✅ เข้าร่วมแล้ว — ข้อความของคุณใน <#%s> จะถูกอ่านแล้วลบ%s", req.TextChannelID, suffix)
	}
}

func ttsLeaveReply(ctl TTSController, guildID, userID string) string {
	res, err := ctl.Leave(guildID, userID)
	switch {
	case errors.Is(err, readaloud.ErrNotBound):
		return "ตอนนี้ไม่ได้อ่านออกเสียงในเซิร์ฟเวอร์นี้"
	case errors.Is(err, readaloud.ErrNotParticipant):
		return "คุณไม่ได้อยู่ในการอ่านนี้ — เข้าร่วมด้วย /tts join"
	case err != nil:
		log.Printf("tts: leave guild %s: %v", guildID, err)
		return "จบการอ่านออกเสียงแล้ว แต่ออกจากห้องเสียงไม่สำเร็จ"
	case res.Ended:
		return "ออกแล้ว — จบการอ่าน"
	default:
		return fmt.Sprintf("ออกแล้ว (เหลืออีก %d คน)", res.Participants)
	}
}

func ttsUsageReply(u UsageReporter) string {
	if u == nil {
		return "Usage is not tracked: no Google TTS API key is configured."
	}
	snap := u.Snapshot()
	if snap.Unit == usage.UnitTHBMicro {
		return ttsMoneyUsageReply(snap)
	}
	if snap.Limit <= 0 {
		return fmt.Sprintf("Google TTS usage for %s (US/Pacific month): %s characters. No monthly cap is set.",
			snap.Month, formatCount(snap.Used))
	}
	return fmt.Sprintf("Google TTS usage for %s (US/Pacific month): %s / %s characters (%d%%). %s",
		snap.Month, formatCount(snap.Used), formatCount(snap.Limit), snap.Percent, ttsPausedText(snap))
}

// ttsMoneyUsageReply reports Gemini-TTS spend, which the meter prices in
// micro-baht, plus the audio generated.
func ttsMoneyUsageReply(snap usage.Snapshot) string {
	audio := fmt.Sprintf("Audio generated: %s s.", usage.FormatSeconds(snap.AudioMs))
	if snap.Limit <= 0 {
		return fmt.Sprintf("Google TTS spend for %s (US/Pacific month): %s. No monthly budget is set. %s",
			snap.Month, usage.FormatTHB(snap.Used), audio)
	}
	return fmt.Sprintf("Google TTS spend for %s (US/Pacific month): %s / %s budget (%d%%). %s %s",
		snap.Month, usage.FormatTHB(snap.Used), usage.FormatTHB(snap.Limit), snap.Percent, ttsPausedText(snap), audio)
}

func ttsPausedText(snap usage.Snapshot) string {
	if snap.Paused {
		return "Reading is **paused** until next month."
	}
	return "Reading is active."
}

// formatCount renders n with thousands separators, e.g. 1,000,000.
func formatCount(n int64) string {
	s := strconv.FormatInt(n, 10)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}

func ttsUsageResponse(u UsageReporter, now time.Time) *discordgo.InteractionResponseData {
	if u == nil {
		return &discordgo.InteractionResponseData{
			Content: "Usage is not tracked: no Google TTS API key is configured.",
			Flags:   discordgo.MessageFlagsEphemeral,
		}
	}
	return &discordgo.InteractionResponseData{
		Embeds: []*discordgo.MessageEmbed{usage.FormatUsageEmbed(u.Snapshot(), now)},
		Flags:  discordgo.MessageFlagsEphemeral,
	}
}

func ttsRespondData(s *discordgo.Session, i *discordgo.InteractionCreate, data *discordgo.InteractionResponseData) {
	if err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: data,
	}); err != nil {
		log.Printf("tts: failed to respond to interaction: %v", err)
	}
}

func ttsRespondEphemeral(s *discordgo.Session, i *discordgo.InteractionCreate, content string) {
	ttsRespondData(s, i, &discordgo.InteractionResponseData{
		Content: content,
		Flags:   discordgo.MessageFlagsEphemeral,
	})
}
