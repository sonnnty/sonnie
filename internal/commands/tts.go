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
	Join(ctx context.Context, guildID, voiceChannelID, textChannelID string) error
	Leave(guildID string) error
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
					Description: "Join your voice channel and read this channel aloud",
				},
				{
					Type:        discordgo.ApplicationCommandOptionSubCommand,
					Name:        "leave",
					Description: "Stop reading and leave the voice channel",
				},
				{
					Type:        discordgo.ApplicationCommandOptionSubCommand,
					Name:        "usage",
					Description: "Show this month's Google TTS character usage",
				},
			},
		},
		Handler: handleTTS,
	})
}

func handleTTS(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if i.GuildID == "" || i.Member == nil || i.Member.User == nil {
		ttsRespond(s, i, "/tts only works in a server.")
		return
	}
	ctl := ttsController
	if ctl == nil {
		ttsRespond(s, i, "Reading chat aloud is not enabled on this bot.")
		return
	}

	options := i.ApplicationCommandData().Options
	if len(options) == 0 {
		return
	}
	switch options[0].Name {
	case "join":
		vs, err := s.State.VoiceState(i.GuildID, i.Member.User.ID)
		if err != nil || vs.ChannelID == "" {
			ttsRespond(s, i, "Join a voice channel first, then run /tts join.")
			return
		}
		// Joining voice can take longer than the 3 s Discord allows for a
		// first response, so acknowledge now and edit the reply later.
		if err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
		}); err != nil {
			log.Printf("tts: failed to defer interaction: %v", err)
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), ttsJoinTimeout)
		defer cancel()
		reply := ttsJoinReply(ctx, ctl, i.GuildID, vs.ChannelID, i.ChannelID)
		if _, err := s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Content: &reply}); err != nil {
			log.Printf("tts: failed to edit interaction response: %v", err)
		}
	case "leave":
		ttsRespond(s, i, ttsLeaveReply(ctl, i.GuildID))
	case "usage":
		ttsRespondEphemeral(s, i, ttsUsageReply(usageReporter))
	}
}

func ttsJoinReply(ctx context.Context, ctl TTSController, guildID, voiceChannelID, textChannelID string) string {
	if err := ctl.Join(ctx, guildID, voiceChannelID, textChannelID); err != nil {
		log.Printf("tts: join guild %s channel %s: %v", guildID, voiceChannelID, err)
		if errors.Is(err, context.DeadlineExceeded) {
			return "Joining your voice channel timed out — the voice connection may have been slow or failed, or I may be missing the Connect and Speak permissions there."
		}
		return "I couldn't join your voice channel. Check that I have the Connect and Speak permissions there, or try again if the voice connection failed."
	}
	return fmt.Sprintf("Reading <#%s> aloud in <#%s>. Use /tts leave to stop.", textChannelID, voiceChannelID)
}

func ttsLeaveReply(ctl TTSController, guildID string) string {
	err := ctl.Leave(guildID)
	switch {
	case errors.Is(err, readaloud.ErrNotBound):
		return "I'm not reading any channel in this server."
	case err != nil:
		log.Printf("tts: leave guild %s: %v", guildID, err)
		return "Stopped reading, but leaving the voice channel failed."
	default:
		return "Stopped reading and left the voice channel."
	}
}

func ttsUsageReply(u UsageReporter) string {
	if u == nil {
		return "Usage is not tracked: no Google TTS API key is configured."
	}
	snap := u.Snapshot()
	if snap.Limit <= 0 {
		return fmt.Sprintf("Google TTS usage for %s (US/Pacific month): %s characters. No monthly cap is set.",
			snap.Month, formatCount(snap.Used))
	}
	status := "Reading is active."
	if snap.Paused {
		status = "Reading is **paused** until next month."
	}
	return fmt.Sprintf("Google TTS usage for %s (US/Pacific month): %s / %s characters (%d%%). %s",
		snap.Month, formatCount(snap.Used), formatCount(snap.Limit), snap.Percent, status)
}

// formatCount renders n with thousands separators, e.g. 1,000,000.
func formatCount(n int64) string {
	s := strconv.FormatInt(n, 10)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}

func ttsRespondEphemeral(s *discordgo.Session, i *discordgo.InteractionCreate, content string) {
	if err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{Content: content, Flags: discordgo.MessageFlagsEphemeral},
	}); err != nil {
		log.Printf("tts: failed to respond to interaction: %v", err)
	}
}

func ttsRespond(s *discordgo.Session, i *discordgo.InteractionCreate, content string) {
	if err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{Content: content},
	}); err != nil {
		log.Printf("tts: failed to respond to interaction: %v", err)
	}
}
