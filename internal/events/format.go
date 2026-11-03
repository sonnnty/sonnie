package events

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"

	"github.com/Sonty15/SONNIE/internal/cover"
	"github.com/Sonty15/SONNIE/internal/eventtime"
	"github.com/Sonty15/SONNIE/internal/usage"
)

// Embed colours.
const (
	colorEvent  = 0x5865F2 // Discord blurple
	colorOK     = usage.ColorGreen
	colorDanger = usage.ColorRed
)

// Discord's limits for scheduled events.
const (
	maxNameLen        = 100
	maxDescriptionLen = 1000
	maxLocationLen    = 100
	// maxUserDescription leaves room for the credit line.
	maxUserDescription = 750
)

var thaiWeekdayShort = [...]string{"อา.", "จ.", "อ.", "พ.", "พฤ.", "ศ.", "ส."}

var thaiMonthShort = [...]string{"", "ม.ค.", "ก.พ.", "มี.ค.", "เม.ย.", "พ.ค.", "มิ.ย.", "ก.ค.", "ส.ค.", "ก.ย.", "ต.ค.", "พ.ย.", "ธ.ค."}

// ShortTime is a short Thai date and time in Bangkok, e.g. "ศ. 6 พ.ย. 21:00",
// for autocomplete labels, which cannot use Discord timestamps.
func ShortTime(t time.Time) string {
	t = t.In(eventtime.Bangkok)
	return fmt.Sprintf("%s %d %s %s", thaiWeekdayShort[t.Weekday()], t.Day(), thaiMonthShort[t.Month()], t.Format("15:04"))
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return strings.TrimSpace(string(r[:n-1])) + "…"
}

// splitCredit separates the user's description from the cover credit,
// which is the last line and starts with cover.CreditPrefix.
func splitCredit(desc string) (body, credit string) {
	i := strings.LastIndex(desc, cover.CreditPrefix)
	if i < 0 || (i > 0 && desc[i-1] != '\n') || strings.Contains(desc[i:], "\n") {
		return strings.TrimSpace(desc), ""
	}
	return strings.TrimSpace(desc[:i]), desc[i:]
}

// joinCredit puts the credit on the last line, shortening the body so the
// whole fits Discord's 1000 characters.
func joinCredit(body, credit string) string {
	body = strings.TrimSpace(body)
	if credit == "" {
		return truncate(body, maxDescriptionLen)
	}
	credit = truncate(credit, maxDescriptionLen)
	room := maxDescriptionLen - len([]rune(credit)) - 2
	if body == "" || room <= 1 {
		return credit
	}
	return truncate(body, room) + "\n\n" + credit
}

func place(ev *discordgo.GuildScheduledEvent) string {
	switch ev.EntityType {
	case discordgo.GuildScheduledEventEntityTypeVoice, discordgo.GuildScheduledEventEntityTypeStageInstance:
		if ev.ChannelID != "" {
			return "🔊 <#" + ev.ChannelID + ">"
		}
	case discordgo.GuildScheduledEventEntityTypeExternal:
		if ev.EntityMetadata.Location != "" {
			return "📍 " + ev.EntityMetadata.Location
		}
	}
	return "-"
}

func when(ev *discordgo.GuildScheduledEvent) string {
	start := ev.ScheduledStartTime.Unix()
	s := fmt.Sprintf("<t:%d:F> · <t:%d:R>", start, start)
	if ev.ScheduledEndTime != nil {
		style := "t"
		if !sameDay(ev.ScheduledStartTime, *ev.ScheduledEndTime) {
			style = "f"
		}
		s += fmt.Sprintf("\nถึง <t:%d:%s>", ev.ScheduledEndTime.Unix(), style)
	}
	return s
}

func sameDay(a, b time.Time) bool {
	a, b = a.In(eventtime.Bangkok), b.In(eventtime.Bangkok)
	return a.Year() == b.Year() && a.YearDay() == b.YearDay()
}

// EventEmbed shows one event: name linked to it, time, place and cover.
func EventEmbed(ev *discordgo.GuildScheduledEvent) *discordgo.MessageEmbed {
	e := &discordgo.MessageEmbed{
		Title:       truncate("📅 "+ev.Name, 256),
		URL:         Link(ev.GuildID, ev.ID),
		Description: when(ev),
		Color:       colorEvent,
		Fields:      []*discordgo.MessageEmbedField{{Name: "สถานที่", Value: place(ev), Inline: true}},
	}
	if ev.Status == discordgo.GuildScheduledEventStatusActive {
		e.Fields = append(e.Fields, &discordgo.MessageEmbedField{Name: "สถานะ", Value: "🟢 กำลังจัด", Inline: true})
	}
	if url := CoverURL(ev); url != "" {
		e.Image = &discordgo.MessageEmbedImage{URL: url}
	}
	if _, credit := splitCredit(ev.Description); credit != "" {
		e.Footer = &discordgo.MessageEmbedFooter{Text: truncate(credit, 300)}
	}
	return e
}

// eventButtons: change the cover, open the event.
func eventButtons(ev *discordgo.GuildScheduledEvent) []discordgo.MessageComponent {
	return []discordgo.MessageComponent{discordgo.ActionsRow{Components: []discordgo.MessageComponent{
		discordgo.Button{Label: "เปลี่ยนรูป", Emoji: &discordgo.ComponentEmoji{Name: "🔄"}, Style: discordgo.SecondaryButton, CustomID: recoverID(ev.ID)},
		discordgo.Button{Label: "เปิดอีเวนต์", Style: discordgo.LinkButton, URL: Link(ev.GuildID, ev.ID)},
	}}}
}

// listLimit keeps /event list to one compact embed.
const listLimit = 10

// ListEmbed shows the upcoming events, soonest first.
func ListEmbed(evs []*discordgo.GuildScheduledEvent) *discordgo.MessageEmbed {
	e := &discordgo.MessageEmbed{Title: "📅 อีเวนต์ที่กำลังจะมา", Color: colorEvent}
	if len(evs) == 0 {
		e.Description = "ยังไม่มีอีเวนต์ — สร้างด้วย `/event create`"
		return e
	}
	var b strings.Builder
	for i, ev := range evs {
		if i == listLimit {
			fmt.Fprintf(&b, "…และอีก %d อีเวนต์", len(evs)-listLimit)
			break
		}
		start := ev.ScheduledStartTime.Unix()
		status := ""
		if ev.Status == discordgo.GuildScheduledEventStatusActive {
			status = "🟢 "
		}
		fmt.Fprintf(&b, "%s**[%s](%s)**\n<t:%d:f> · <t:%d:R> · %s", status, truncate(ev.Name, 80), Link(ev.GuildID, ev.ID), start, start, place(ev))
		if ev.UserCount > 0 {
			fmt.Fprintf(&b, " · 👥 %d", ev.UserCount)
		}
		b.WriteString("\n\n")
	}
	e.Description = strings.TrimSpace(b.String())
	return e
}

func okEmbed(title string) *discordgo.MessageEmbed {
	return &discordgo.MessageEmbed{Title: title, Color: colorOK}
}

// apiError makes a Discord REST error short enough for a reply.
func apiError(err error) string {
	var rest *discordgo.RESTError
	if errors.As(err, &rest) && rest.Message != nil && rest.Message.Message != "" {
		return truncate(rest.Message.Message, 200)
	}
	return truncate(err.Error(), 200)
}
