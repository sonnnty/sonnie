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
	ChannelMessageSendComplex(channelID string, data *discordgo.MessageSend, options ...discordgo.RequestOption) (*discordgo.Message, error)
	ChannelMessageEditComplex(m *discordgo.MessageEdit, options ...discordgo.RequestOption) (*discordgo.Message, error)
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
// messages that carries a usage marker. It checks embed footer text first
// (the current format) and falls back to plain content (legacy format).
func (c *discordUsageChannel) FindStatus(context.Context) (string, bool, error) {
	msgs, err := c.api.ChannelMessages(c.channelID, statusScanLimit, "", "", "")
	if err != nil {
		return "", false, fmt.Errorf("read alert channel %s: %w", c.channelID, err)
	}
	botID := c.botID()
	// Discord returns newest first.
	for _, m := range msgs {
		if m == nil || m.Author == nil || botID == "" || m.Author.ID != botID {
			continue
		}
		// Check embeds first: marker in the hidden URL (current format), then
		// in the footer text (older messages).
		for _, e := range m.Embeds {
			if e == nil {
				continue
			}
			if text, ok := usage.MarkerFromURL(e.URL); ok {
				if _, ok := usage.ParseMarker(text); ok {
					c.mu.Lock()
					c.messageID = m.ID
					c.mu.Unlock()
					return text, false, nil
				}
			}
			if e.Footer != nil && e.Footer.Text != "" {
				if _, ok := usage.ParseMarker(e.Footer.Text); ok {
					c.mu.Lock()
					c.messageID = m.ID
					c.mu.Unlock()
					return e.Footer.Text, false, nil
				}
			}
		}
		// Fall back to message content (legacy v1/v2 plain text format).
		if _, ok := usage.ParseMarker(m.Content); ok {
			c.mu.Lock()
			c.messageID = m.ID
			c.mu.Unlock()
			return m.Content, true, nil
		}
	}
	return "", false, nil
}

// SaveStatus edits the status message, or posts a new one if there is none or
// the old one can no longer be edited (for example it was deleted).
// When editing an old plain-text message, it clears the content so only the
// embed remains.
func (c *discordUsageChannel) SaveStatus(_ context.Context, embed *discordgo.MessageEmbed) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.messageID != "" {
		edit := discordgo.NewMessageEdit(c.channelID, c.messageID)
		edit.SetContent("")
		edit.SetEmbeds([]*discordgo.MessageEmbed{embed})
		if _, err := c.api.ChannelMessageEditComplex(edit); err == nil {
			return nil
		}
		c.messageID = ""
	}
	send := &discordgo.MessageSend{
		Embeds: []*discordgo.MessageEmbed{embed},
	}
	m, err := c.api.ChannelMessageSendComplex(c.channelID, send)
	if err != nil {
		return fmt.Errorf("post status message in %s: %w", c.channelID, err)
	}
	c.messageID = m.ID
	return nil
}

func (c *discordUsageChannel) Alert(_ context.Context, content string, embed *discordgo.MessageEmbed) error {
	send := &discordgo.MessageSend{
		Content: content,
		Embeds:  []*discordgo.MessageEmbed{embed},
	}
	if _, err := c.api.ChannelMessageSendComplex(c.channelID, send); err != nil {
		return fmt.Errorf("post alert in %s: %w", c.channelID, err)
	}
	return nil
}
