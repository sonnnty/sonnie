package commands

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strconv"
	"time"

	"github.com/bwmarrin/discordgo"

	"github.com/Sonty15/SONNIE/internal/readaloud"
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
}

const ttsJoinTimeout = 15 * time.Second

func init() {
	dmPermission := false
	register(Command{
		Definition: &discordgo.ApplicationCommand{
			Name:         "tts",
			Description:  "Read this text channel aloud in your voice channel",
			DMPermission: &dmPermission,
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionSubCommand,
					Name:        "join",
					Description: "Join the read-aloud session; your messages here are read aloud and deleted when it ends",
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
		Handler: handleTTS,
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

const ttsDeleteNotice = "ข้อความที่คุณพิมพ์ในห้องนี้ระหว่างการอ่านจะถูกลบเมื่อจบ"

func ttsJoinReply(ctx context.Context, ctl TTSController, req readaloud.JoinRequest) string {
	res, err := ctl.Join(ctx, req)
	if err != nil {
		var busy *readaloud.BusyError
		var wrongText *readaloud.TextChannelError
		switch {
		case errors.As(err, &busy):
			return fmt.Sprintf("ตอนนี้บอทกำลังอ่านออกเสียงอยู่ในห้องเสียง <#%s> (ห้องข้อความ <#%s>) และยังมีคนอยู่ — ไปเข้าร่วมที่นั่น หรือรอให้จบก่อน",
				busy.VoiceChannelID, busy.TextChannelID)
		case errors.As(err, &wrongText):
			return fmt.Sprintf("ห้องเสียงนี้กำลังอ่านจากห้องข้อความ <#%s> — ใช้ /tts join ในห้อง <#%s> แทน",
				wrongText.TextChannelID, wrongText.TextChannelID)
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

	var reply string
	switch {
	case res.AlreadyIn:
		return "คุณอยู่ในการอ่านออกเสียงนี้อยู่แล้ว — ออกด้วย /tts leave"
	case res.Started:
		reply = fmt.Sprintf("✅ เริ่มอ่านออกเสียงแล้ว — ข้อความของคุณใน <#%s> จะถูกอ่านในห้อง <#%s>", req.TextChannelID, req.VoiceChannelID)
	default:
		reply = fmt.Sprintf("✅ เข้าร่วมแล้ว — ข้อความของคุณใน <#%s> จะถูกอ่านในห้อง <#%s> (ผู้เข้าร่วม %d คน)",
			req.TextChannelID, req.VoiceChannelID, res.Participants)
	}
	reply += " ออกด้วย /tts leave"
	if res.CanManage {
		return reply + "\n🧹 " + ttsDeleteNotice
	}
	return reply + "\n⚠️ บอทไม่มีสิทธิ์ Manage Messages ในห้องนี้ จึงจะไม่ลบข้อความเมื่อจบ"
}

func ttsLeaveReply(ctl TTSController, guildID, userID string) string {
	res, err := ctl.Leave(guildID, userID)
	switch {
	case errors.Is(err, readaloud.ErrNotBound):
		return "ตอนนี้ไม่ได้อ่านออกเสียงในเซิร์ฟเวอร์นี้"
	case errors.Is(err, readaloud.ErrNotParticipant):
		return "คุณไม่ได้อยู่ในการอ่านออกเสียงนี้ — เข้าร่วมด้วย /tts join"
	case err != nil:
		log.Printf("tts: leave guild %s: %v", guildID, err)
		return "จบการอ่านออกเสียงแล้ว แต่ออกจากห้องเสียงไม่สำเร็จ"
	case res.Ended:
		return "ออกแล้ว — จบการอ่านออกเสียงและบอทออกจากห้องเสียง บอทกำลังลบข้อความของผู้เข้าร่วมแล้วสรุปไว้ในข้อความเซสชัน"
	default:
		return fmt.Sprintf("ออกจากการอ่านออกเสียงแล้ว (ยังเหลือผู้เข้าร่วม %d คน)", res.Participants)
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
