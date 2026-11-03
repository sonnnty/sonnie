package readaloud

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"

	"github.com/Sonty15/SONNIE/internal/usage"
)

// ErrMessageGone is what Messenger.DeleteMessage returns for a message that no
// longer exists. Cleanup treats it as already done.
var ErrMessageGone = errors.New("readaloud: message no longer exists")

// ChannelMessage is the Discord-independent view of a message in the bound
// text channel, as returned by a history scan.
type ChannelMessage struct {
	ID       string
	AuthorID string
	// Time is when the message was created. Discord decides whether a message
	// may be bulk-deleted from its snowflake, so implementations should use
	// that.
	Time time.Time
}

// OpenSession is an unfinished session message found in channel history: the
// bot posted it, but never replaced it with the end-of-session summary.
type OpenSession struct {
	GuildID   string
	ChannelID string
	MessageID string
	// Time is when the session message was created.
	Time   time.Time
	Marker SessionMarker
}

// Messenger is the Discord side of a session: the public session message and
// the clean-up of the text channel. Implementations are used from several
// goroutines.
type Messenger interface {
	// Post sends embed as a new message and returns its ID and creation time.
	Post(ctx context.Context, channelID string, embed *discordgo.MessageEmbed) (messageID string, created time.Time, err error)
	// Edit replaces the content of a message the bot posted with embed.
	Edit(ctx context.Context, channelID, messageID string, embed *discordgo.MessageEmbed) error
	// CanManageMessages reports whether the bot may delete other people's
	// messages in channelID.
	CanManageMessages(channelID string) bool
	// MessagesAfter lists every user-written message in channelID newer than
	// afterID, oldest first.
	MessagesAfter(ctx context.Context, channelID, afterID string) ([]ChannelMessage, error)
	// DeleteMessage deletes one message. It returns ErrMessageGone when the
	// message does not exist any more.
	DeleteMessage(ctx context.Context, channelID, messageID string) error
	// BulkDelete deletes 2 to 100 messages at once, all younger than 14 days.
	BulkDelete(ctx context.Context, channelID string, messageIDs []string) error
	// OpenSessions returns the bot's own unfinished session messages in
	// recent history of the guild's channels, newest first within a channel.
	OpenSessions(ctx context.Context, guildID string) ([]OpenSession, error)
}

// Session embed colours. Summaries use Discord's neutral grey.
const (
	colorActive  = usage.ColorGreen
	colorWarning = usage.ColorOrange
	colorEnded   = 0x95A5A6
)

const (
	sessionMarkerPrefix = "sonnie-tts-session v1"

	// maxListedMentions caps the participants named in one embed, so a very
	// busy session cannot overflow the 4096-character description.
	maxListedMentions = 40
	// maxMarkerLen keeps the whole footer under Discord's 2048-character cap.
	maxMarkerLen = 1500
)

// Session states in the marker.
const (
	stateOpen  = "open"
	stateEnded = "ended"
)

// SessionMarker is the machine-readable part of the session message. It lets
// a restarted bot finish the clean-up of a session it lost.
type SessionMarker struct {
	// Open is false once the message has become the end-of-session summary.
	Open    bool
	Started time.Time
	Voice   string
	// Manage records whether the bot could delete messages when the session
	// started, which is what the participants were told.
	Manage bool
	// Participants are the users in the session now; Left are users who joined
	// and left again. Both count as participants for clean-up.
	Participants []string
	Left         []string
}

// All returns everyone who took part: current participants, then those who left.
func (m SessionMarker) All() []string {
	return append(append([]string(nil), m.Participants...), m.Left...)
}

// FormatSessionMarker renders m as one line.
func FormatSessionMarker(m SessionMarker) string {
	state := stateEnded
	if m.Open {
		state = stateOpen
	}
	manage := "0"
	if m.Manage {
		manage = "1"
	}
	line := fmt.Sprintf("%s state=%s started=%d voice=%s manage=%s participants=%s left=%s",
		sessionMarkerPrefix, state, m.Started.Unix(), m.Voice, manage,
		strings.Join(m.Participants, ","), strings.Join(m.Left, ","))
	// Only the oldest "left" users can be lost, and only for sessions with
	// dozens of participants; their clean-up still works while the bot runs.
	if len(line) > maxMarkerLen && len(m.Left) > 0 {
		m.Left = m.Left[1:]
		return FormatSessionMarker(m)
	}
	return line
}

// ParseSessionMarker finds the marker line in text (an embed footer) and
// parses it. It reports false when there is no valid marker.
func ParseSessionMarker(text string) (SessionMarker, bool) {
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, sessionMarkerPrefix+" ") {
			continue
		}
		var (
			m                    SessionMarker
			haveState, haveStart bool
		)
		for _, f := range strings.Fields(strings.TrimPrefix(line, sessionMarkerPrefix)) {
			key, value, ok := strings.Cut(f, "=")
			if !ok {
				continue
			}
			switch key {
			case "state":
				switch value {
				case stateOpen:
					m.Open = true
				case stateEnded:
				default:
					return SessionMarker{}, false
				}
				haveState = true
			case "started":
				n, err := strconv.ParseInt(value, 10, 64)
				if err != nil || n <= 0 {
					return SessionMarker{}, false
				}
				m.Started, haveStart = time.Unix(n, 0), true
			case "voice":
				m.Voice = value
			case "manage":
				m.Manage = value == "1"
			case "participants":
				m.Participants = splitIDs(value)
			case "left":
				m.Left = splitIDs(value)
			}
		}
		if haveState && haveStart {
			return m, true
		}
	}
	return SessionMarker{}, false
}

// ParseSessionMarkerFromEmbed reads the marker from an embed's footer.
func ParseSessionMarkerFromEmbed(e *discordgo.MessageEmbed) (SessionMarker, bool) {
	if e == nil || e.Footer == nil {
		return SessionMarker{}, false
	}
	return ParseSessionMarker(e.Footer.Text)
}

func splitIDs(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, ",")
}

// sessionView is what the session message shows.
type sessionView struct {
	TextChannelID  string
	VoiceChannelID string
	Started        time.Time
	Manage         bool
	Participants   []string
	Left           []string
}

func (v sessionView) marker(open bool) SessionMarker {
	return SessionMarker{
		Open:         open,
		Started:      v.Started,
		Voice:        v.VoiceChannelID,
		Manage:       v.Manage,
		Participants: v.Participants,
		Left:         v.Left,
	}
}

func footer(m SessionMarker) *discordgo.MessageEmbedFooter {
	return &discordgo.MessageEmbedFooter{
		Text: usage.StatusFooterHuman + "\n" + FormatSessionMarker(m),
	}
}

// mentions renders ids as Discord mentions joined by sep. Mentions inside an
// embed show the name but never notify anyone.
func mentions(ids []string, sep string) string {
	shown := ids
	if len(shown) > maxListedMentions {
		shown = shown[:maxListedMentions]
	}
	parts := make([]string, len(shown))
	for i, id := range shown {
		parts[i] = "<@" + id + ">"
	}
	s := strings.Join(parts, sep)
	if extra := len(ids) - len(shown); extra > 0 {
		s += fmt.Sprintf("%sและอีก %d คน", sep, extra)
	}
	return s
}

// sessionEmbed builds the live session message: who takes part, where, and
// how to join or leave.
func sessionEmbed(v sessionView) *discordgo.MessageEmbed {
	desc := fmt.Sprintf("**ผู้เข้าร่วม (%d)**\n%s", len(v.Participants), mentions(v.Participants, " "))
	if len(v.Participants) == 0 {
		desc = "**ผู้เข้าร่วม (0)**\n-"
	}
	color := colorActive
	cleanup := "🧹 ข้อความของผู้เข้าร่วมในห้องนี้จะถูกลบเมื่อจบ"
	if !v.Manage {
		color = colorWarning
		cleanup = "⚠️ บอทไม่มีสิทธิ์ Manage Messages ในห้องนี้ จึงจะไม่ลบข้อความเมื่อจบ"
	}
	return &discordgo.MessageEmbed{
		Title:       "🔊 กำลังอ่านออกเสียง",
		Description: desc,
		Color:       color,
		Fields: []*discordgo.MessageEmbedField{
			{Name: "ห้องข้อความ", Value: "<#" + v.TextChannelID + ">", Inline: true},
			{Name: "ห้องเสียง", Value: "<#" + v.VoiceChannelID + ">", Inline: true},
			{Name: "เริ่มเมื่อ", Value: fmt.Sprintf("<t:%d:R>", v.Started.Unix()), Inline: true},
			{
				Name: "วิธีใช้",
				Value: "เข้าร่วม: `/tts join` (ต้องอยู่ในห้องเสียงนี้)\n" +
					"ออก: `/tts leave`\n" +
					"อ่านเฉพาะข้อความของผู้เข้าร่วม\n" + cleanup,
			},
		},
		Footer:    footer(v.marker(true)),
		Timestamp: v.Started.UTC().Format(time.RFC3339),
	}
}

// summary is the outcome of a session's clean-up, shown in the log message.
type summary struct {
	Participants []string
	Duration     time.Duration
	// Cleanup is what deleting the messages achieved.
	Cleanup CleanupResult
}

// summaryText is the one line the session message becomes when the session
// ends, e.g. "📝 จบการอ่านออกเสียง · ผู้เข้าร่วม: @a, @b · 25 นาที · ลบข้อความแล้ว 42 ข้อความ".
func summaryText(s summary) string {
	people := "-"
	if len(s.Participants) > 0 {
		people = mentions(s.Participants, ", ")
	}
	parts := []string{
		"📝 จบการอ่านออกเสียง",
		"ผู้เข้าร่วม: " + people,
		formatSessionDuration(s.Duration),
	}
	switch c := s.Cleanup; {
	case c.NoPermission:
		parts = append(parts, "ไม่ได้ลบข้อความ (บอทไม่มีสิทธิ์ Manage Messages)")
	case c.Failed > 0:
		parts = append(parts, fmt.Sprintf("ลบข้อความแล้ว %d ข้อความ (ลบไม่ได้ %d)", c.Deleted, c.Failed))
	default:
		parts = append(parts, fmt.Sprintf("ลบข้อความแล้ว %d ข้อความ", c.Deleted))
	}
	return strings.Join(parts, " · ")
}

// summaryEmbed is the session message after the session ended. It keeps the
// "ended" marker so a restarted bot does not clean it up again.
func summaryEmbed(v sessionView, s summary, ended time.Time) *discordgo.MessageEmbed {
	// Everyone who took part is in the marker only for open sessions; an
	// ended one never needs it again.
	m := v.marker(false)
	m.Participants, m.Left = nil, nil
	return &discordgo.MessageEmbed{
		Description: summaryText(s),
		Color:       colorEnded,
		Footer:      footer(m),
		Timestamp:   ended.UTC().Format(time.RFC3339),
	}
}

// formatSessionDuration renders d in Thai, rounded to whole minutes.
func formatSessionDuration(d time.Duration) string {
	min := int((d + 30*time.Second) / time.Minute)
	switch {
	case min < 1:
		return "ไม่ถึง 1 นาที"
	case min < 60:
		return fmt.Sprintf("%d นาที", min)
	case min%60 == 0:
		return fmt.Sprintf("%d ชั่วโมง", min/60)
	default:
		return fmt.Sprintf("%d ชั่วโมง %d นาที", min/60, min%60)
	}
}
