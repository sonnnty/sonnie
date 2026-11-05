package readaloud

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"
)

// fakeMessenger is an in-memory Messenger. Messages live in per-channel
// lists in the order they were added, so "after" means later in the list.
type fakeMessenger struct {
	mu sync.Mutex

	botID     string
	canManage bool
	// noManage lists channels where the bot lacks Manage Messages although
	// canManage is set.
	noManage map[string]bool
	clock    func() time.Time

	next     int
	channels map[string][]*fakeMsg
	edits    []fakeEdit
	bulk     [][]string // IDs of each BulkDelete call
	single   []string   // IDs of each DeleteMessage call

	postErr, editErr, scanErr, bulkErr, deleteErr error
	open                                          map[string][]OpenSession // guild -> sessions
	openCalls                                     []string
	onDelete                                      func(id string)
}

type fakeMsg struct {
	id, author string
	time       time.Time
	embed      *discordgo.MessageEmbed
	deleted    bool
}

type fakeEdit struct {
	channelID, messageID string
	embed                *discordgo.MessageEmbed
}

func newFakeMessenger() *fakeMessenger {
	return &fakeMessenger{
		botID:     "bot",
		canManage: true,
		clock:     time.Now,
		channels:  make(map[string][]*fakeMsg),
	}
}

// add appends a message by author and returns its ID.
func (f *fakeMessenger) add(channelID, author string, at time.Time) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.addLocked(channelID, author, at, nil)
}

func (f *fakeMessenger) addLocked(channelID, author string, at time.Time, e *discordgo.MessageEmbed) string {
	f.next++
	id := fmt.Sprintf("m%03d", f.next)
	f.channels[channelID] = append(f.channels[channelID], &fakeMsg{id: id, author: author, time: at, embed: e})
	return id
}

func (f *fakeMessenger) find(id string) *fakeMsg {
	for _, msgs := range f.channels {
		for _, m := range msgs {
			if m.id == id {
				return m
			}
		}
	}
	return nil
}

func (f *fakeMessenger) Post(_ context.Context, channelID string, e *discordgo.MessageEmbed) (string, time.Time, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.postErr != nil {
		return "", time.Time{}, f.postErr
	}
	at := f.clock()
	return f.addLocked(channelID, f.botID, at, e), at, nil
}

func (f *fakeMessenger) Edit(_ context.Context, channelID, messageID string, e *discordgo.MessageEmbed) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.editErr != nil {
		return f.editErr
	}
	m := f.find(messageID)
	if m == nil || m.deleted {
		return ErrMessageGone
	}
	m.embed = e
	f.edits = append(f.edits, fakeEdit{channelID, messageID, e})
	return nil
}

func (f *fakeMessenger) CanManageMessages(channelID string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.canManage && !f.noManage[channelID]
}

func (f *fakeMessenger) MessagesAfter(_ context.Context, channelID, afterID string) ([]ChannelMessage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.scanErr != nil {
		return nil, f.scanErr
	}
	var out []ChannelMessage
	seen := afterID == ""
	for _, m := range f.channels[channelID] {
		if !seen {
			seen = m.id == afterID
			continue
		}
		if !m.deleted {
			out = append(out, ChannelMessage{ID: m.id, AuthorID: m.author, Time: m.time})
		}
	}
	return out, nil
}

func (f *fakeMessenger) DeleteMessage(_ context.Context, channelID, messageID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.single = append(f.single, messageID)
	if f.deleteErr != nil {
		return f.deleteErr
	}
	return f.deleteLocked(channelID, messageID)
}

func (f *fakeMessenger) deleteLocked(channelID, id string) error {
	for _, m := range f.channels[channelID] {
		if m.id == id && !m.deleted {
			m.deleted = true
			if f.onDelete != nil {
				f.onDelete(id)
			}
			return nil
		}
	}
	return ErrMessageGone
}

func (f *fakeMessenger) BulkDelete(_ context.Context, channelID string, ids []string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.bulk = append(f.bulk, append([]string(nil), ids...))
	if f.bulkErr != nil {
		return f.bulkErr
	}
	if len(ids) < 2 || len(ids) > bulkChunk {
		return fmt.Errorf("bulk delete of %d messages", len(ids))
	}
	for _, id := range ids {
		_ = f.deleteLocked(channelID, id)
	}
	return nil
}

func (f *fakeMessenger) OpenSessions(_ context.Context, guildID string) ([]OpenSession, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.openCalls = append(f.openCalls, guildID)
	return f.open[guildID], nil
}

func (f *fakeMessenger) alive(channelID string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var ids []string
	for _, m := range f.channels[channelID] {
		if !m.deleted {
			ids = append(ids, m.id)
		}
	}
	return ids
}

func (f *fakeMessenger) isDeleted(id string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	m := f.find(id)
	return m != nil && m.deleted
}

// embed returns the current embed of a posted message.
func (f *fakeMessenger) embed(id string) *discordgo.MessageEmbed {
	f.mu.Lock()
	defer f.mu.Unlock()
	if m := f.find(id); m != nil {
		return m.embed
	}
	return nil
}

func (f *fakeMessenger) editCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.edits)
}

// embedFor sets the embed of an existing message.
func (f *fakeMessenger) embedFor(id string, e *discordgo.MessageEmbed) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.find(id).embed = e
}
