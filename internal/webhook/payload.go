package webhook

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/bwmarrin/discordgo"
)

// Discord's message and embed limits, in characters.
const (
	maxContent     = 2000
	maxEmbeds      = 10
	maxEmbedTotal  = 6000
	maxTitle       = 256
	maxDescription = 4096
	maxFields      = 25
	maxFieldName   = 256
	maxFieldValue  = 1024
	maxFooter      = 2048
	maxAuthorName  = 256
	maxURL         = 2048
)

var (
	errUnsupportedType = errors.New("unsupported content type; send application/json or text/plain")
	errEmpty           = errors.New("nothing to post")
)

// payload is every JSON shape accepted: Discord-webhook-like (content,
// embeds) and simple (title, text, color, url).
type payload struct {
	Content string                    `json:"content"`
	Embeds  []*discordgo.MessageEmbed `json:"embeds"`

	Title string          `json:"title"`
	Text  string          `json:"text"`
	Color json.RawMessage `json:"color"`
	URL   string          `json:"url"`
}

// parseMessage turns a request body into the message to post. hook names
// the simple form's embed footer.
func parseMessage(contentType string, body []byte, hook string) (*discordgo.MessageSend, error) {
	mt := ""
	if contentType != "" {
		var err error
		if mt, _, err = mime.ParseMediaType(contentType); err != nil {
			return nil, errUnsupportedType
		}
	}
	var msg *discordgo.MessageSend
	switch {
	case mt == "application/json" || strings.HasSuffix(mt, "+json") ||
		(mt == "" && bytes.HasPrefix(bytes.TrimSpace(body), []byte("{"))):
		var p payload
		if err := json.Unmarshal(body, &p); err != nil {
			return nil, fmt.Errorf("invalid JSON: %v", err)
		}
		var err error
		if msg, err = p.message(hook); err != nil {
			return nil, err
		}
	case mt == "text/plain" || mt == "":
		if !utf8.Valid(body) {
			return nil, errors.New("the body is not UTF-8 text")
		}
		msg = &discordgo.MessageSend{Content: string(body)}
	default:
		return nil, errUnsupportedType
	}

	msg.Content = truncate(strings.TrimSpace(msg.Content), maxContent)
	msg.Embeds = sanitizeEmbeds(msg.Embeds)
	if msg.Content == "" && len(msg.Embeds) == 0 {
		return nil, errEmpty
	}
	// Never ping anyone: @everyone, roles and users stay plain text.
	msg.AllowedMentions = &discordgo.MessageAllowedMentions{Parse: []discordgo.AllowedMentionType{}}
	return msg, nil
}

func (p payload) message(hook string) (*discordgo.MessageSend, error) {
	if p.Content != "" || len(p.Embeds) > 0 {
		return &discordgo.MessageSend{Content: p.Content, Embeds: p.Embeds}, nil
	}
	if p.Title == "" && p.Text == "" {
		return nil, errEmpty
	}
	color, err := parseColor(p.Color)
	if err != nil {
		return nil, err
	}
	return &discordgo.MessageSend{Embeds: []*discordgo.MessageEmbed{{
		Title:       p.Title,
		Description: p.Text,
		URL:         p.URL,
		Color:       color,
		Footer:      &discordgo.MessageEmbedFooter{Text: "webhook · " + hook},
	}}}, nil
}

// parseColor accepts a number (0xRRGGBB as an integer) or a string "#RRGGBB"
// or "RRGGBB".
func parseColor(raw json.RawMessage) (int, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return 0, nil
	}
	var n int
	if err := json.Unmarshal(raw, &n); err == nil {
		if n < 0 || n > 0xFFFFFF {
			return 0, errors.New("color must be between 0 and 0xFFFFFF")
		}
		return n, nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return 0, errors.New(`color must be a number or a string like "#ff8800"`)
	}
	s = strings.TrimPrefix(strings.TrimSpace(s), "#")
	v, err := strconv.ParseUint(s, 16, 32)
	if err != nil || len(s) != 6 {
		return 0, errors.New(`color must be a number or a string like "#ff8800"`)
	}
	return int(v), nil
}

// sanitizeEmbeds keeps at most 10 embeds and only the parts a webhook may
// set, each cut to Discord's limit and all of them together to 6000
// characters. URLs must be http(s). Embeds left empty are dropped.
func sanitizeEmbeds(in []*discordgo.MessageEmbed) []*discordgo.MessageEmbed {
	budget := maxEmbedTotal
	take := func(s string, limit int) string {
		s = truncate(strings.TrimSpace(s), min(limit, budget))
		budget -= utf8.RuneCountInString(s)
		return s
	}
	var out []*discordgo.MessageEmbed
	for _, e := range in {
		if len(out) == maxEmbeds || budget <= 0 {
			break
		}
		if e == nil {
			continue
		}
		clean := &discordgo.MessageEmbed{
			Title:       take(e.Title, maxTitle),
			Description: take(e.Description, maxDescription),
			URL:         safeURL(e.URL),
		}
		if e.Color > 0 && e.Color <= 0xFFFFFF {
			clean.Color = e.Color
		}
		if _, err := time.Parse(time.RFC3339, e.Timestamp); err == nil {
			clean.Timestamp = e.Timestamp
		}
		if e.Author != nil {
			if name := take(e.Author.Name, maxAuthorName); name != "" {
				clean.Author = &discordgo.MessageEmbedAuthor{Name: name, URL: safeURL(e.Author.URL), IconURL: safeURL(e.Author.IconURL)}
			}
		}
		for _, f := range e.Fields {
			if len(clean.Fields) == maxFields || budget <= 0 {
				break
			}
			if f == nil {
				continue
			}
			name, value := take(f.Name, maxFieldName), take(f.Value, maxFieldValue)
			if name == "" || value == "" {
				continue // Discord rejects a field without both
			}
			clean.Fields = append(clean.Fields, &discordgo.MessageEmbedField{Name: name, Value: value, Inline: f.Inline})
		}
		if e.Footer != nil {
			if text := take(e.Footer.Text, maxFooter); text != "" {
				clean.Footer = &discordgo.MessageEmbedFooter{Text: text, IconURL: safeURL(e.Footer.IconURL)}
			}
		}
		if e.Image != nil {
			if u := safeURL(e.Image.URL); u != "" {
				clean.Image = &discordgo.MessageEmbedImage{URL: u}
			}
		}
		if e.Thumbnail != nil {
			if u := safeURL(e.Thumbnail.URL); u != "" {
				clean.Thumbnail = &discordgo.MessageEmbedThumbnail{URL: u}
			}
		}
		if clean.Title == "" && clean.Description == "" && len(clean.Fields) == 0 && clean.Author == nil &&
			clean.Footer == nil && clean.Image == nil && clean.Thumbnail == nil {
			continue
		}
		out = append(out, clean)
	}
	return out
}

// safeURL returns u if it is an absolute http(s) URL Discord accepts, else "".
func safeURL(u string) string {
	u = strings.TrimSpace(u)
	if u == "" || len(u) > maxURL {
		return ""
	}
	parsed, err := url.Parse(u)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return ""
	}
	return u
}

// truncate cuts s to at most n characters, ending in "…" when it was cut.
func truncate(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n-1]) + "…"
}
