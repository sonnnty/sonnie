package bot

import (
	"context"
	"fmt"
	"sync"

	"github.com/bwmarrin/discordgo"

	"github.com/Sonty15/SONNIE/internal/usage"
)

// statusScanLimit is how many recent messages are searched for the status
// message on startup (Discord's maximum page size).
const statusScanLimit = 100

// messageAPI is the part of *discordgo.Session the usage channel needs.
type messageAPI interface {
	ChannelMessages(channelID string, limit int, beforeID, afterID, aroundID string, options ...discordgo.RequestOption) ([]*discordgo.Message, error)
	ChannelMessageSend(channelID, content string, options ...discordgo.RequestOption) (*discordgo.Message, error)
	ChannelMessageEdit(channelID, messageID, content string, options ...discordgo.RequestOption) (*discordgo.Message, error)
}

// discordUsageChannel implements usage.Channel on a Discord text channel. It
// needs View Channel, Send Messages and Read Message History there.
type discordUsageChannel struct {
	api       messageAPI
	channelID string
	// botID returns the bot's own user ID; it is only known once the session
	// is open.
	botID func() string

	mu        sync.Mutex
	messageID string // the status message, once found or created
}

var _ usage.Channel = (*discordUsageChannel)(nil)

func newDiscordUsageChannel(s *discordgo.Session, channelID string) *discordUsageChannel {
	return &discordUsageChannel{
		api:       s,
		channelID: channelID,
		botID: func() string {
			if s.State != nil && s.State.User != nil {
				return s.State.User.ID
			}
			return ""
		},
	}
}

// FindStatus returns the newest message authored by the bot in the last 100
// messages that carries a usage marker.
func (c *discordUsageChannel) FindStatus(context.Context) (string, error) {
	msgs, err := c.api.ChannelMessages(c.channelID, statusScanLimit, "", "", "")
	if err != nil {
		return "", fmt.Errorf("read alert channel %s: %w", c.channelID, err)
	}
	botID := c.botID()
	// Discord returns newest first.
	for _, m := range msgs {
		if m == nil || m.Author == nil || botID == "" || m.Author.ID != botID {
			continue
		}
		if _, ok := usage.ParseMarker(m.Content); !ok {
			continue
		}
		c.mu.Lock()
		c.messageID = m.ID
		c.mu.Unlock()
		return m.Content, nil
	}
	return "", nil
}

// SaveStatus edits the status message, or posts a new one if there is none or
// the old one can no longer be edited (for example it was deleted).
func (c *discordUsageChannel) SaveStatus(_ context.Context, content string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.messageID != "" {
		if _, err := c.api.ChannelMessageEdit(c.channelID, c.messageID, content); err == nil {
			return nil
		}
		c.messageID = ""
	}
	m, err := c.api.ChannelMessageSend(c.channelID, content)
	if err != nil {
		return fmt.Errorf("post status message in %s: %w", c.channelID, err)
	}
	c.messageID = m.ID
	return nil
}

func (c *discordUsageChannel) Alert(_ context.Context, content string) error {
	if _, err := c.api.ChannelMessageSend(c.channelID, content); err != nil {
		return fmt.Errorf("post alert in %s: %w", c.channelID, err)
	}
	return nil
}
