package bot

import (
	"fmt"
	"log"
	"time"

	"github.com/bwmarrin/discordgo"
)

// braveNotifier posts the one message when Brave's monthly credit runs out
// (cover.QuotaNotifier), in the event channel (EVENT_CHANNEL_ID, else
// TTS_ALERT_CHANNEL_ID). Without a channel it stays quiet; the cover
// package logs the pause anyway.
type braveNotifier struct {
	s interface {
		ChannelMessageSend(channelID, content string, options ...discordgo.RequestOption) (*discordgo.Message, error)
	}
	channelID string
	// fallback names the library used meanwhile: Unsplash when it has a
	// key, else Openverse (or Pexels, which comes before it).
	fallback string
}

func braveQuotaMessage(fallback string, until time.Time) string {
	return fmt.Sprintf("⚠️ เครดิต Brave Search เดือนนี้หมดแล้ว — ใช้รูปจาก %s แทนจนถึง <t:%d:F>", fallback, until.Unix())
}

// BraveQuotaExhausted sends in the background: it is called from inside a
// cover search, which must not wait for Discord.
func (n braveNotifier) BraveQuotaExhausted(until time.Time) {
	if n.channelID == "" {
		return
	}
	go n.post(until)
}

func (n braveNotifier) post(until time.Time) {
	if _, err := n.s.ChannelMessageSend(n.channelID, braveQuotaMessage(n.fallback, until)); err != nil {
		log.Printf("cover: brave: could not post the quota alert in %s: %v", n.channelID, err)
	}
}
