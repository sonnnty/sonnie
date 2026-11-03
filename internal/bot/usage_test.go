package bot

import (
	"context"
	"errors"
	"testing"

	"github.com/bwmarrin/discordgo"

	"github.com/Sonty15/SONNIE/internal/usage"
)

type fakeMessageAPI struct {
	history  []*discordgo.Message // newest first
	listErr  error
	editErr  error
	sendErr  error
	sent     []string
	edits    map[string]string
	nextID   int
	gotLimit int
}

func (f *fakeMessageAPI) ChannelMessages(_ string, limit int, _, _, _ string, _ ...discordgo.RequestOption) ([]*discordgo.Message, error) {
	f.gotLimit = limit
	return f.history, f.listErr
}

func (f *fakeMessageAPI) ChannelMessageSend(_, content string, _ ...discordgo.RequestOption) (*discordgo.Message, error) {
	if f.sendErr != nil {
		return nil, f.sendErr
	}
	f.nextID++
	f.sent = append(f.sent, content)
	return &discordgo.Message{ID: string(rune('A' + f.nextID))}, nil
}

func (f *fakeMessageAPI) ChannelMessageEdit(_, id, content string, _ ...discordgo.RequestOption) (*discordgo.Message, error) {
	if f.editErr != nil {
		return nil, f.editErr
	}
	if f.edits == nil {
		f.edits = map[string]string{}
	}
	f.edits[id] = content
	return &discordgo.Message{ID: id}, nil
}

func msg(id, author, content string) *discordgo.Message {
	return &discordgo.Message{ID: id, Author: &discordgo.User{ID: author}, Content: content}
}

func newTestChannel(api messageAPI) *discordUsageChannel {
	return &discordUsageChannel{api: api, channelID: "chan", botID: func() string { return "bot" }}
}

func TestDiscordUsageChannel_FindStatusPicksNewestOwnMarker(t *testing.T) {
	newer := usage.FormatStatus(usage.State{Month: "2026-11", Unit: usage.UnitChars, Value: 20}, 100)
	older := usage.FormatStatus(usage.State{Month: "2026-11", Unit: usage.UnitChars, Value: 10}, 100)
	api := &fakeMessageAPI{history: []*discordgo.Message{
		msg("1", "human", newer), // someone else quoting a marker: ignored
		msg("2", "bot", "⚠️ Google TTS ใช้ไป 50%"),
		msg("3", "bot", newer),
		msg("4", "bot", older),
	}}
	c := newTestChannel(api)
	got, err := c.FindStatus(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got != newer || api.gotLimit != 100 {
		t.Fatalf("got %q (limit %d)", got, api.gotLimit)
	}
	// The next save edits that message in place.
	if err := c.SaveStatus(context.Background(), "new"); err != nil {
		t.Fatal(err)
	}
	if api.edits["3"] != "new" || len(api.sent) != 0 {
		t.Fatalf("edits=%v sent=%v", api.edits, api.sent)
	}
}

func TestDiscordUsageChannel_FindStatusNoneOrError(t *testing.T) {
	c := newTestChannel(&fakeMessageAPI{history: []*discordgo.Message{msg("1", "human", "hi")}})
	if got, err := c.FindStatus(context.Background()); err != nil || got != "" {
		t.Fatalf("got %q, %v", got, err)
	}
	c = newTestChannel(&fakeMessageAPI{listErr: errors.New("403")})
	if _, err := c.FindStatus(context.Background()); err == nil {
		t.Fatal("expected error")
	}
}

func TestDiscordUsageChannel_SaveStatusCreatesThenEdits(t *testing.T) {
	api := &fakeMessageAPI{}
	c := newTestChannel(api)
	ctx := context.Background()
	if err := c.SaveStatus(ctx, "one"); err != nil {
		t.Fatal(err)
	}
	if err := c.SaveStatus(ctx, "two"); err != nil {
		t.Fatal(err)
	}
	if len(api.sent) != 1 || api.sent[0] != "one" {
		t.Fatalf("sent=%v", api.sent)
	}
	if len(api.edits) != 1 {
		t.Fatalf("edits=%v", api.edits)
	}
	for _, v := range api.edits {
		if v != "two" {
			t.Fatalf("edits=%v", api.edits)
		}
	}
}

func TestDiscordUsageChannel_SaveStatusRecreatesDeletedMessage(t *testing.T) {
	api := &fakeMessageAPI{}
	c := newTestChannel(api)
	ctx := context.Background()
	if err := c.SaveStatus(ctx, "one"); err != nil {
		t.Fatal(err)
	}
	api.editErr = errors.New("404 unknown message")
	if err := c.SaveStatus(ctx, "two"); err != nil {
		t.Fatal(err)
	}
	if len(api.sent) != 2 || api.sent[1] != "two" {
		t.Fatalf("sent=%v", api.sent)
	}
}

func TestDiscordUsageChannel_AlertPostsNewMessage(t *testing.T) {
	api := &fakeMessageAPI{}
	c := newTestChannel(api)
	if err := c.Alert(context.Background(), "hi"); err != nil {
		t.Fatal(err)
	}
	if len(api.sent) != 1 || api.sent[0] != "hi" {
		t.Fatalf("sent=%v", api.sent)
	}
	api.sendErr = errors.New("403")
	if err := c.Alert(context.Background(), "hi"); err == nil {
		t.Fatal("expected error")
	}
}
