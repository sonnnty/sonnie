package readaloud

import (
	"regexp"
	"strings"
	"unicode"
)

// Message is the Discord-independent view of a chat message.
type Message struct {
	GuildID    string
	ChannelID  string
	AuthorName string
	Content    string
	// IsBot is true for bots, webhooks and SONNIE itself.
	IsBot bool
}

// Names resolves mention IDs to readable names. Methods return "" when the
// name is unknown, in which case the mention is dropped.
type Names interface {
	User(id string) string
	Role(id string) string
	Channel(id string) string
}

// TextOptions controls Normalize.
type TextOptions struct {
	// MaxChars caps the result length in characters (runes). Zero means
	// no cap.
	MaxChars int
	// LinkWord replaces each URL. Empty drops URLs entirely.
	LinkWord string
	// Names resolves mentions. Nil drops all mentions.
	Names Names
}

// ShouldSpeak reports whether a message is worth normalising at all.
func ShouldSpeak(m Message) bool {
	return !m.IsBot && strings.TrimSpace(m.Content) != ""
}

// DefaultLinkWord returns the word spoken in place of a URL for lang.
func DefaultLinkWord(lang string) string {
	switch strings.ToLower(lang) {
	case "th", "th-th":
		return "ลิงก์"
	default:
		return "link"
	}
}

var (
	codeBlockRe   = regexp.MustCompile("(?s)```.*?```")
	spoilerRe     = regexp.MustCompile(`(?s)\|\|.*?\|\|`)
	urlRe         = regexp.MustCompile(`<?https?://[^\s>]+>?`)
	customEmojiRe = regexp.MustCompile(`<a?:\w+:\d+>`)
	timestampRe   = regexp.MustCompile(`<t:-?\d+(?::[tTdDfFR])?>`)
	slashCmdRe    = regexp.MustCompile(`</([\w\- ]+):\d+>`)
	userRe        = regexp.MustCompile(`<@!?(\d+)>`)
	roleRe        = regexp.MustCompile(`<@&(\d+)>`)
	channelRe     = regexp.MustCompile(`<#(\d+)>`)
	linePrefixRe  = regexp.MustCompile(`(?m)^\s*(?:>{1,3}|#{1,3}|-#)\s+`)
	markdownRe    = regexp.MustCompile("[*~`|]+|__")
)

// Normalize turns raw Discord message content into text suitable for
// speech. It is pure: the same input always gives the same output.
//
// Code blocks and spoilers are dropped (code is noise when spoken, and
// spoilers should stay hidden), URLs become opts.LinkWord, mentions become
// names or are dropped, custom emoji and timestamps are dropped, markdown
// markers are stripped, whitespace is collapsed and the result is capped at
// opts.MaxChars characters.
func Normalize(content string, opts TextOptions) string {
	s := codeBlockRe.ReplaceAllString(content, " ")
	s = spoilerRe.ReplaceAllString(s, " ")
	s = urlRe.ReplaceAllLiteralString(s, " "+opts.LinkWord+" ")
	s = customEmojiRe.ReplaceAllString(s, " ")
	s = timestampRe.ReplaceAllString(s, " ")
	s = slashCmdRe.ReplaceAllString(s, " $1 ")
	s = replaceMentions(s, roleRe, opts.Names, Names.Role)
	s = replaceMentions(s, userRe, opts.Names, Names.User)
	s = replaceMentions(s, channelRe, opts.Names, Names.Channel)
	s = linePrefixRe.ReplaceAllString(s, "")
	s = markdownRe.ReplaceAllString(s, "")
	s = strings.Join(strings.Fields(s), " ")
	return truncate(s, opts.MaxChars)
}

func replaceMentions(s string, re *regexp.Regexp, names Names, lookup func(Names, string) string) string {
	return re.ReplaceAllStringFunc(s, func(m string) string {
		if names == nil {
			return " "
		}
		id := re.FindStringSubmatch(m)[1]
		return " " + lookup(names, id) + " "
	})
}

// truncate caps s at max runes without splitting a base character from its
// combining marks (Thai vowels and tone marks are combining marks).
func truncate(s string, max int) string {
	if max <= 0 {
		return s
	}
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	cut := max
	for cut > 0 && unicode.Is(unicode.Mn, runes[cut]) {
		cut--
	}
	return strings.TrimSpace(string(runes[:cut]))
}

// Utterance builds the final text to speak, optionally prefixed with the
// author's name.
func Utterance(author, text string, speakAuthor bool) string {
	author = strings.TrimSpace(author)
	if !speakAuthor || author == "" {
		return text
	}
	return author + ": " + text
}
