package bot

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/bwmarrin/discordgo"

	"github.com/Sonty15/SONNIE/internal/readaloud"
)

const (
	// pageSize is Discord's maximum page for message history and the
	// bulk-delete limit.
	pageSize = 100
	// maxScanPages caps a history scan after a session message: 10,000
	// messages. Beyond that the clean-up falls back to the messages it saw
	// live.
	maxScanPages = 100
	// recoveryPages is how many pages of each channel's recent history are
	// searched for unfinished session messages on startup.
	recoveryPages = 3
)

// sessionAPI is the part of *discordgo.Session the session messenger needs.
type sessionAPI interface {
	ChannelMessages(channelID string, limit int, beforeID, afterID, aroundID string, options ...discordgo.RequestOption) ([]*discordgo.Message, error)
	ChannelMessageSendComplex(channelID string, data *discordgo.MessageSend, options ...discordgo.RequestOption) (*discordgo.Message, error)
	ChannelMessageEditComplex(m *discordgo.MessageEdit, options ...discordgo.RequestOption) (*discordgo.Message, error)
	ChannelMessageDelete(channelID, messageID string, options ...discordgo.RequestOption) error
	ChannelMessagesBulkDelete(channelID string, messageIDs []string, options ...discordgo.RequestOption) error
	UserChannelPermissions(userID, channelID string, fetchOptions ...discordgo.RequestOption) (int64, error)
}

// discordMessenger implements readaloud.Messenger on Discord. It needs View
// Channel, Send Messages, Embed Links and Read Message History in the text
// channel, plus Manage Messages to delete other people's messages.
type discordMessenger struct {
	api sessionAPI
	// botID returns the bot's own user ID; it is only known once the session
	// is open.
	botID func() string
	// channels lists the IDs of a guild's channels that can hold a session
	// (text and voice chat channels).
	channels func(guildID string) []string
}

var _ readaloud.Messenger = (*discordMessenger)(nil)

func newDiscordMessenger(s *discordgo.Session) *discordMessenger {
	return &discordMessenger{
		api: s,
		botID: func() string {
			if s.State != nil && s.State.User != nil {
				return s.State.User.ID
			}
			return ""
		},
		channels: func(guildID string) []string {
			guild, err := s.State.Guild(guildID)
			if err != nil {
				return nil
			}
			s.State.RLock()
			defer s.State.RUnlock()
			var ids []string
			for _, ch := range guild.Channels {
				switch ch.Type {
				case discordgo.ChannelTypeGuildText, discordgo.ChannelTypeGuildNews,
					discordgo.ChannelTypeGuildVoice, discordgo.ChannelTypeGuildStageVoice:
					ids = append(ids, ch.ID)
				}
			}
			return ids
		},
	}
}

// messageTime is when a message was created. Discord decides bulk-delete
// eligibility from the snowflake, so that wins over the timestamp field.
func messageTime(m *discordgo.Message) time.Time {
	if t, err := discordgo.SnowflakeTimestamp(m.ID); err == nil {
		return t
	}
	return m.Timestamp
}

// isUserMessage is true for messages a person typed: not joins, pins, thread
// starters or command responses, which cannot or should not be deleted.
func isUserMessage(m *discordgo.Message) bool {
	return m != nil && m.Author != nil &&
		(m.Type == discordgo.MessageTypeDefault || m.Type == discordgo.MessageTypeReply)
}

func isUnknownMessage(err error) bool {
	var re *discordgo.RESTError
	if !errors.As(err, &re) {
		return false
	}
	return (re.Message != nil && re.Message.Code == discordgo.ErrCodeUnknownMessage) ||
		(re.Response != nil && re.Response.StatusCode == http.StatusNotFound)
}

func (d *discordMessenger) Post(ctx context.Context, channelID string, embed *discordgo.MessageEmbed) (string, time.Time, error) {
	m, err := d.api.ChannelMessageSendComplex(channelID, &discordgo.MessageSend{
		Embeds: []*discordgo.MessageEmbed{embed},
		// Never ping: the embed only names people.
		AllowedMentions: &discordgo.MessageAllowedMentions{},
	}, discordgo.WithContext(ctx))
	if err != nil {
		return "", time.Time{}, fmt.Errorf("post session message in %s: %w", channelID, err)
	}
	return m.ID, messageTime(m), nil
}

func (d *discordMessenger) Edit(ctx context.Context, channelID, messageID string, embed *discordgo.MessageEmbed) error {
	edit := discordgo.NewMessageEdit(channelID, messageID)
	edit.SetContent("")
	edit.SetEmbeds([]*discordgo.MessageEmbed{embed})
	if _, err := d.api.ChannelMessageEditComplex(edit, discordgo.WithContext(ctx)); err != nil {
		if isUnknownMessage(err) {
			return fmt.Errorf("%w: %v", readaloud.ErrMessageGone, err)
		}
		return fmt.Errorf("edit session message %s in %s: %w", messageID, channelID, err)
	}
	return nil
}

func (d *discordMessenger) CanManageMessages(channelID string) bool {
	id := d.botID()
	if id == "" {
		return false
	}
	perms, err := d.api.UserChannelPermissions(id, channelID)
	if err != nil {
		return false
	}
	return perms&(discordgo.PermissionManageMessages|discordgo.PermissionAdministrator) != 0
}

// snowflakeLess orders message IDs by creation. IDs that are not numbers
// compare as strings.
func snowflakeLess(a, b string) bool {
	x, errX := strconv.ParseUint(a, 10, 64)
	y, errY := strconv.ParseUint(b, 10, 64)
	if errX != nil || errY != nil {
		return a < b
	}
	return x < y
}

// MessagesAfter pages through the channel after afterID. Discord returns each
// page newest first, so the next page starts after the newest ID of this one.
// On an error the messages read so far are returned with it.
func (d *discordMessenger) MessagesAfter(ctx context.Context, channelID, afterID string) ([]readaloud.ChannelMessage, error) {
	var out []readaloud.ChannelMessage
	after := afterID
	for page := 0; page < maxScanPages; page++ {
		msgs, err := d.api.ChannelMessages(channelID, pageSize, "", after, "", discordgo.WithContext(ctx))
		if err != nil {
			return out, fmt.Errorf("read history of %s: %w", channelID, err)
		}
		if len(msgs) == 0 {
			return out, nil
		}
		sort.SliceStable(msgs, func(i, j int) bool { return snowflakeLess(msgs[i].ID, msgs[j].ID) })
		for _, m := range msgs {
			if isUserMessage(m) {
				out = append(out, readaloud.ChannelMessage{ID: m.ID, AuthorID: m.Author.ID, Time: messageTime(m)})
			}
		}
		after = msgs[len(msgs)-1].ID
		if len(msgs) < pageSize {
			return out, nil
		}
	}
	return out, fmt.Errorf("history of %s has more than %d messages after the session message", channelID, maxScanPages*pageSize)
}

func (d *discordMessenger) DeleteMessage(ctx context.Context, channelID, messageID string) error {
	if err := d.api.ChannelMessageDelete(channelID, messageID, discordgo.WithContext(ctx)); err != nil {
		if isUnknownMessage(err) {
			return fmt.Errorf("%w: %v", readaloud.ErrMessageGone, err)
		}
		return err
	}
	return nil
}

func (d *discordMessenger) BulkDelete(ctx context.Context, channelID string, messageIDs []string) error {
	return d.api.ChannelMessagesBulkDelete(channelID, messageIDs, discordgo.WithContext(ctx))
}

// OpenSessions searches the recent history of each channel in the guild that
// the bot can read for its own session messages whose marker says "open".
// A channel that cannot be read is skipped.
func (d *discordMessenger) OpenSessions(ctx context.Context, guildID string) ([]readaloud.OpenSession, error) {
	botID := d.botID()
	if botID == "" {
		return nil, errors.New("bot user is not known yet")
	}
	var found []readaloud.OpenSession
	for _, channelID := range d.channels(guildID) {
		if ctx.Err() != nil {
			return found, ctx.Err()
		}
		perms, err := d.api.UserChannelPermissions(botID, channelID)
		const need = discordgo.PermissionViewChannel | discordgo.PermissionReadMessageHistory
		if err != nil || perms&discordgo.PermissionAdministrator == 0 && perms&need != need {
			continue
		}
		before := ""
		for page := 0; page < recoveryPages; page++ {
			msgs, err := d.api.ChannelMessages(channelID, pageSize, before, "", "", discordgo.WithContext(ctx))
			if err != nil || len(msgs) == 0 {
				break
			}
			for _, m := range msgs {
				if m == nil || m.Author == nil || m.Author.ID != botID {
					continue
				}
				for _, e := range m.Embeds {
					if mk, ok := readaloud.ParseSessionMarkerFromEmbed(e); ok && mk.Open {
						found = append(found, readaloud.OpenSession{
							GuildID: guildID, ChannelID: channelID, MessageID: m.ID, Time: messageTime(m), Marker: mk,
						})
						break
					}
				}
			}
			if len(msgs) < pageSize {
				break
			}
			before = msgs[len(msgs)-1].ID
		}
	}
	return found, nil
}
