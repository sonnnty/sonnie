package webhook

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/bwmarrin/discordgo"
)

func TestSanitizeEmbeds_DiscordLimits(t *testing.T) {
	var in []*discordgo.MessageEmbed
	for i := 0; i < 12; i++ {
		in = append(in, &discordgo.MessageEmbed{Title: "t"})
	}
	if got := sanitizeEmbeds(in); len(got) != maxEmbeds {
		t.Fatalf("kept %d embeds", len(got))
	}

	long := &discordgo.MessageEmbed{
		Title:       strings.Repeat("ท", 300),
		Description: strings.Repeat("d", 5000),
		Footer:      &discordgo.MessageEmbedFooter{Text: "f"},
	}
	for i := 0; i < 30; i++ {
		long.Fields = append(long.Fields, &discordgo.MessageEmbedField{Name: "n", Value: "v"})
	}
	got := sanitizeEmbeds([]*discordgo.MessageEmbed{long})[0]
	if utf8.RuneCountInString(got.Title) != maxTitle || utf8.RuneCountInString(got.Description) != maxDescription || len(got.Fields) != maxFields {
		t.Fatalf("title %d, description %d, fields %d", utf8.RuneCountInString(got.Title), utf8.RuneCountInString(got.Description), len(got.Fields))
	}
}

func TestSanitizeEmbeds_TotalBudget(t *testing.T) {
	var in []*discordgo.MessageEmbed
	for i := 0; i < 3; i++ {
		in = append(in, &discordgo.MessageEmbed{Description: strings.Repeat("x", 4000)})
	}
	got := sanitizeEmbeds(in)
	total := 0
	for _, e := range got {
		total += utf8.RuneCountInString(e.Description)
	}
	if total > maxEmbedTotal || len(got) != 2 {
		t.Fatalf("total %d over %d embeds", total, len(got))
	}
}

func TestSanitizeEmbeds_DropsUnsafeAndUnknownParts(t *testing.T) {
	in := []*discordgo.MessageEmbed{{
		Title:     "ok",
		URL:       "javascript:alert(1)",
		Type:      discordgo.EmbedTypeVideo,
		Color:     -5,
		Timestamp: "yesterday",
		Image:     &discordgo.MessageEmbedImage{URL: "file:///etc/passwd"},
		Thumbnail: &discordgo.MessageEmbedThumbnail{URL: "https://img.example/t.png"},
		Video:     &discordgo.MessageEmbedVideo{URL: "https://v.example"},
		Provider:  &discordgo.MessageEmbedProvider{Name: "p"},
		Author:    &discordgo.MessageEmbedAuthor{Name: "a", URL: "ftp://x", IconURL: "https://i.example/a.png"},
		Fields:    []*discordgo.MessageEmbedField{{Name: "", Value: "no name"}, nil, {Name: "k", Value: "v"}},
	}, nil}
	got := sanitizeEmbeds(in)
	if len(got) != 1 {
		t.Fatalf("kept %d embeds", len(got))
	}
	e := got[0]
	if e.URL != "" || e.Type != "" || e.Color != 0 || e.Timestamp != "" || e.Image != nil || e.Video != nil || e.Provider != nil {
		t.Fatalf("unsafe parts kept: %+v", e)
	}
	if e.Thumbnail == nil || e.Author.URL != "" || e.Author.IconURL != "https://i.example/a.png" || len(e.Fields) != 1 || e.Fields[0].Name != "k" {
		t.Fatalf("safe parts lost: %+v", e)
	}

	withTime := sanitizeEmbeds([]*discordgo.MessageEmbed{{Title: "t", Timestamp: "2026-11-05T10:00:00Z"}})
	if withTime[0].Timestamp != "2026-11-05T10:00:00Z" {
		t.Fatalf("valid timestamp dropped")
	}
}

func TestTruncate(t *testing.T) {
	if got := truncate("สวัสดีครับ", 4); got != "สวั…" {
		t.Fatalf("got %q", got)
	}
	if got := truncate("abc", 3); got != "abc" {
		t.Fatalf("got %q", got)
	}
	if got := truncate("abc", 0); got != "" {
		t.Fatalf("got %q", got)
	}
}
