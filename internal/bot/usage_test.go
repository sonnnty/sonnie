package bot

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"

	"github.com/Sonty15/SONNIE/internal/usage"
)

type fakeMessageAPI struct {
	history  []*discordgo.Message // newest first
	listErr  error
	editErr  error
	sendErr  error
	sent     []*discordgo.MessageSend
	edits    map[string]*discordgo.MessageEdit
	nextID   int
	gotLimit int
}

func (f *fakeMessageAPI) ChannelMessages(_ string, limit int, _, _, _ string, _ ...discordgo.RequestOption) ([]*discordgo.Message, error) {
	f.gotLimit = limit
	return f.history, f.listErr
}

func (f *fakeMessageAPI) ChannelMessageSendComplex(_ string, data *discordgo.MessageSend, _ ...discordgo.RequestOption) (*discordgo.Message, error) {
	if f.sendErr != nil {
		return nil, f.sendErr
	}
	f.nextID++
	f.sent = append(f.sent, data)
	return &discordgo.Message{ID: string(rune('A' + f.nextID))}, nil
}

func (f *fakeMessageAPI) ChannelMessageEditComplex(m *discordgo.MessageEdit, _ ...discordgo.RequestOption) (*discordgo.Message, error) {
	if f.editErr != nil {
		return nil, f.editErr
	}
	if f.edits == nil {
		f.edits = map[string]*discordgo.MessageEdit{}
	}
	f.edits[m.ID] = m
	return &discordgo.Message{ID: m.ID}, nil
}

func msg(id, author, content string) *discordgo.Message {
	return &discordgo.Message{ID: id, Author: &discordgo.User{ID: author}, Content: content}
}

func msgEmbed(id, author string, embed *discordgo.MessageEmbed) *discordgo.Message {
	return &discordgo.Message{ID: id, Author: &discordgo.User{ID: author}, Embeds: []*discordgo.MessageEmbed{embed}}
}

func newTestChannel(api messageAPI) *discordUsageChannel {
	return &discordUsageChannel{api: api, channelID: "chan", botID: func() string { return "bot" }}
}

func TestDiscordUsageChannel_FindStatusPicksNewestOwnEmbedMarker(t *testing.T) {
	now := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	newerEmbed := usage.FormatStatusEmbed(usage.State{Month: "2026-11", Unit: usage.UnitChars, Value: 20}, 100, now)
	olderEmbed := usage.FormatStatusEmbed(usage.State{Month: "2026-11", Unit: usage.UnitChars, Value: 10}, 100, now)

	api := &fakeMessageAPI{history: []*discordgo.Message{
		msgEmbed("1", "human", newerEmbed), // someone else quoting an embed: ignored
		msg("2", "bot", "⚠️ Google TTS ใช้ไป 50%"),
		msgEmbed("3", "bot", newerEmbed),
		msgEmbed("4", "bot", olderEmbed),
	}}
	c := newTestChannel(api)
	got, isLegacy, err := c.FindStatus(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got != newerEmbed.Footer.Text || isLegacy || api.gotLimit != 100 {
		t.Fatalf("got %q isLegacy=%v (limit %d)", got, isLegacy, api.gotLimit)
	}

	// The next save edits that message in place.
	newEmbed := usage.FormatStatusEmbed(usage.State{Month: "2026-11", Unit: usage.UnitChars, Value: 25}, 100, now)
	if err := c.SaveStatus(context.Background(), newEmbed); err != nil {
		t.Fatal(err)
	}
	edit, ok := api.edits["3"]
	if !ok || edit == nil {
		t.Fatalf("expected edit on message 3, edits=%+v", api.edits)
	}
	if edit.Content == nil || *edit.Content != "" {
		t.Fatalf("expected empty content, got %+v", edit.Content)
	}
	if edit.Embeds == nil || len(*edit.Embeds) != 1 || (*edit.Embeds)[0] != newEmbed {
		t.Fatalf("expected edit embeds to contain newEmbed, got %+v", edit.Embeds)
	}
}

func TestDiscordUsageChannel_FindStatusLegacyMarker(t *testing.T) {
	now := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	legacyContent := usage.FormatStatus(usage.State{Month: "2026-11", Unit: usage.UnitChars, Value: 20}, 100)

	api := &fakeMessageAPI{history: []*discordgo.Message{
		msg("1", "bot", legacyContent),
	}}
	c := newTestChannel(api)
	got, isLegacy, err := c.FindStatus(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got != legacyContent || !isLegacy {
		t.Fatalf("got %q isLegacy=%v, want legacy content with isLegacy=true", got, isLegacy)
	}

	// Migrating edits the legacy message, setting Content to "" and setting the new embed.
	newEmbed := usage.FormatStatusEmbed(usage.State{Month: "2026-11", Unit: usage.UnitChars, Value: 20}, 100, now)
	if err := c.SaveStatus(context.Background(), newEmbed); err != nil {
		t.Fatal(err)
	}
	edit, ok := api.edits["1"]
	if !ok || edit == nil {
		t.Fatalf("expected edit on message 1, edits=%+v", api.edits)
	}
	if edit.Content == nil || *edit.Content != "" {
		t.Fatalf("expected content cleared to empty, got %+v", edit.Content)
	}
	if edit.Embeds == nil || len(*edit.Embeds) != 1 || (*edit.Embeds)[0] != newEmbed {
		t.Fatalf("expected new embed in message edit, got %+v", edit.Embeds)
	}
}

func TestDiscordUsageChannel_FindStatusNoneOrError(t *testing.T) {
	c := newTestChannel(&fakeMessageAPI{history: []*discordgo.Message{msg("1", "human", "hi")}})
	if got, isLegacy, err := c.FindStatus(context.Background()); err != nil || got != "" || isLegacy {
		t.Fatalf("got %q %v %v", got, isLegacy, err)
	}
	c = newTestChannel(&fakeMessageAPI{listErr: errors.New("403")})
	if _, _, err := c.FindStatus(context.Background()); err == nil {
		t.Fatal("expected error")
	}
}

func TestDiscordUsageChannel_SaveStatusCreatesThenEdits(t *testing.T) {
	api := &fakeMessageAPI{}
	c := newTestChannel(api)
	ctx := context.Background()

	now := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	embedOne := usage.FormatStatusEmbed(usage.State{Month: "2026-11", Unit: usage.UnitChars, Value: 1}, 100, now)
	embedTwo := usage.FormatStatusEmbed(usage.State{Month: "2026-11", Unit: usage.UnitChars, Value: 2}, 100, now)

	if err := c.SaveStatus(ctx, embedOne); err != nil {
		t.Fatal(err)
	}
	if err := c.SaveStatus(ctx, embedTwo); err != nil {
		t.Fatal(err)
	}
	if len(api.sent) != 1 || len(api.sent[0].Embeds) != 1 || api.sent[0].Embeds[0] != embedOne {
		t.Fatalf("sent=%+v", api.sent)
	}
	if len(api.edits) != 1 {
		t.Fatalf("edits=%+v", api.edits)
	}
	for _, edit := range api.edits {
		if len(*edit.Embeds) != 1 || (*edit.Embeds)[0] != embedTwo {
			t.Fatalf("edits=%+v", api.edits)
		}
	}
}

func TestDiscordUsageChannel_SaveStatusRecreatesDeletedMessage(t *testing.T) {
	api := &fakeMessageAPI{}
	c := newTestChannel(api)
	ctx := context.Background()

	now := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	embedOne := usage.FormatStatusEmbed(usage.State{Month: "2026-11", Unit: usage.UnitChars, Value: 1}, 100, now)
	embedTwo := usage.FormatStatusEmbed(usage.State{Month: "2026-11", Unit: usage.UnitChars, Value: 2}, 100, now)

	if err := c.SaveStatus(ctx, embedOne); err != nil {
		t.Fatal(err)
	}
	api.editErr = errors.New("404 unknown message")
	if err := c.SaveStatus(ctx, embedTwo); err != nil {
		t.Fatal(err)
	}
	if len(api.sent) != 2 || api.sent[1].Embeds[0] != embedTwo {
		t.Fatalf("sent=%+v", api.sent)
	}
}

func TestDiscordUsageChannel_AlertPostsNewMessage(t *testing.T) {
	api := &fakeMessageAPI{}
	c := newTestChannel(api)
	now := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	embed := usage.FormatAlertEmbed(usage.ModeChars, 50, 50, 100, "2026-11")
	text := "⚠️ Google TTS ใช้ไป 50% ของโควตาเดือนนี้"

	if err := c.Alert(context.Background(), text, embed); err != nil {
		t.Fatal(err)
	}
	if len(api.sent) != 1 || api.sent[0].Content != text || len(api.sent[0].Embeds) != 1 || api.sent[0].Embeds[0] != embed {
		t.Fatalf("sent=%+v", api.sent)
	}
	api.sendErr = errors.New("403")
	if err := c.Alert(context.Background(), text, embed); err != nil {
		_ = now
	} else {
		t.Fatal("expected error")
	}
}
