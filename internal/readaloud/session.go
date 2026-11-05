package readaloud

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"maps"
	"math/rand/v2"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"

	"github.com/Sonty15/SONNIE/internal/tts"
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
	// sessionMarkerPrefix starts the marker line; a version token ("v1", "v2")
	// follows. v1 (one text channel) is still parsed, v2 is what is written.
	sessionMarkerPrefix = "sonnie-tts-session"

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

// MarkerChannel is a text channel that has a session card.
type MarkerChannel struct {
	ID string
	// Manage records whether the bot could delete messages in the channel when
	// its card was posted, which is what the participants there were told.
	Manage bool
}

// MarkerBind records that User was bound to Channel: their messages there
// are deleted when the session ends, from Since on. A zero Since means from
// the start of the channel's card.
type MarkerBind struct {
	User, Channel string
	Since         time.Time
}

// SessionMarker is the machine-readable part of the session cards. It lets a
// restarted bot finish the clean-up of a session it lost. Every card of a
// session carries the same marker.
type SessionMarker struct {
	// Version is 1 or 2 as parsed; Format always writes 2.
	Version int
	// Open is false once the message has become the end-of-session summary.
	Open    bool
	Started time.Time
	Voice   string
	// Manage is the v1 single-channel flag; v2 has one per channel in Channels.
	Manage bool
	// Participants are the users in the session now; Left are users who joined
	// and left again. Both count as participants for clean-up.
	Participants []string
	Left         []string
	// The fields below are v2 only. Reading is the text channel each current
	// participant is read in, Channels the text channels with a card, and
	// Binds every (user, channel, since) that clean-up covers.
	Reading  map[string]string
	Channels []MarkerChannel
	Binds    []MarkerBind
}

// All returns everyone who took part: current participants, then those who left.
func (m SessionMarker) All() []string {
	return append(append([]string(nil), m.Participants...), m.Left...)
}

// FormatSessionMarker renders m as one v2 line. Channels are written once and
// referred to by position, and join times as base-36 seconds after the start,
// which keeps a busy session inside the footer limit.
func FormatSessionMarker(m SessionMarker) string {
	state := stateEnded
	if m.Open {
		state = stateOpen
	}
	idx := make(map[string]int, len(m.Channels))
	cards := make([]string, len(m.Channels))
	for i, c := range m.Channels {
		idx[c.ID] = i
		manage := "0"
		if c.Manage {
			manage = "1"
		}
		cards[i] = c.ID + ":" + manage
	}
	people := make([]string, len(m.Participants))
	for i, u := range m.Participants {
		people[i] = u
		if n, ok := idx[m.Reading[u]]; ok {
			people[i] += ":" + strconv.Itoa(n)
		}
	}
	var binds []string
	for _, b := range m.Binds {
		n, ok := idx[b.Channel]
		if !ok {
			continue
		}
		since := "-"
		if off := b.Since.Sub(m.Started); !b.Since.IsZero() && off >= 0 {
			since = strconv.FormatInt(int64(off/time.Second), 36)
		}
		binds = append(binds, b.User+":"+strconv.Itoa(n)+":"+since)
	}
	line := fmt.Sprintf("%s v2 state=%s started=%d voice=%s cards=%s participants=%s left=%s binds=%s",
		sessionMarkerPrefix, state, m.Started.Unix(), m.Voice,
		strings.Join(cards, ","), strings.Join(people, ","), strings.Join(m.Left, ","), strings.Join(binds, ","))
	// Only the oldest "left" users can be lost, and only for sessions with
	// dozens of participants; their clean-up still works while the bot runs.
	if len(line) > maxMarkerLen && len(m.Left) > 0 {
		gone := m.Left[0]
		m.Left = m.Left[1:]
		m.Binds = slices.DeleteFunc(slices.Clone(m.Binds), func(b MarkerBind) bool { return b.User == gone })
		return FormatSessionMarker(m)
	}
	return line
}

// ParseSessionMarker finds the marker line in text (an embed footer) and
// parses it, in the v1 (single channel) or v2 format. It reports false when
// there is no valid marker.
func ParseSessionMarker(text string) (SessionMarker, bool) {
	for _, line := range strings.Split(text, "\n") {
		rest, ok := strings.CutPrefix(strings.TrimSpace(line), sessionMarkerPrefix+" ")
		if !ok {
			continue
		}
		fields := strings.Fields(rest)
		if len(fields) == 0 || fields[0] != "v1" && fields[0] != "v2" {
			continue
		}
		var (
			m                    = SessionMarker{Version: int(fields[0][1] - '0')}
			haveState, haveStart bool
			cards, people, binds string
		)
		for _, f := range fields[1:] {
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
				people = value
			case "left":
				m.Left = splitIDs(value)
			case "cards":
				cards = value
			case "binds":
				binds = value
			}
		}
		if !haveState || !haveStart {
			continue
		}
		if m.Version == 1 {
			m.Participants = splitIDs(people)
			return m, true
		}
		for _, c := range splitIDs(cards) {
			id, manage, _ := strings.Cut(c, ":")
			m.Channels = append(m.Channels, MarkerChannel{ID: id, Manage: manage == "1"})
		}
		channelAt := func(s string) (string, bool) {
			n, err := strconv.Atoi(s)
			if err != nil || n < 0 || n >= len(m.Channels) {
				return "", false
			}
			return m.Channels[n].ID, true
		}
		for _, p := range splitIDs(people) {
			user, at, hasChannel := strings.Cut(p, ":")
			m.Participants = append(m.Participants, user)
			if ch, ok := channelAt(at); hasChannel && ok {
				if m.Reading == nil {
					m.Reading = make(map[string]string)
				}
				m.Reading[user] = ch
			}
		}
		for _, b := range splitIDs(binds) {
			parts := strings.Split(b, ":")
			if len(parts) != 3 {
				continue
			}
			ch, ok := channelAt(parts[1])
			if !ok {
				continue
			}
			bind := MarkerBind{User: parts[0], Channel: ch}
			if parts[2] != "-" {
				off, err := strconv.ParseInt(parts[2], 36, 64)
				if err != nil || off < 0 {
					continue
				}
				bind.Since = m.Started.Add(time.Duration(off) * time.Second)
			}
			m.Binds = append(m.Binds, bind)
		}
		return m, true
	}
	return SessionMarker{}, false
}

// markerURLPrefix starts the embed URL that carries the marker. An embed
// without a title never shows its URL, so the marker stays out of sight while
// Discord keeps it (checked live: kept on send and edit, 2027 characters).
const markerURLPrefix = "https://sonnie.invalid/tts#"

// markerURL hides m in an embed URL, base64url-encoded.
func markerURL(m SessionMarker) string {
	return markerURLPrefix + base64.RawURLEncoding.EncodeToString([]byte(FormatSessionMarker(m)))
}

// ParseSessionMarkerFromEmbed reads the marker from an embed: from its hidden
// URL, or from the footer of cards posted before the marker moved there.
func ParseSessionMarkerFromEmbed(e *discordgo.MessageEmbed) (SessionMarker, bool) {
	if e == nil {
		return SessionMarker{}, false
	}
	if enc, ok := strings.CutPrefix(e.URL, markerURLPrefix); ok {
		if raw, err := base64.RawURLEncoding.DecodeString(enc); err == nil {
			if m, ok := ParseSessionMarker(string(raw)); ok {
				return m, true
			}
		}
	}
	if e.Footer == nil {
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

// member is a current participant and the text channel they are read in.
type member struct{ User, Channel string }

// sessionView is the shareable state of a session: who takes part, where each
// of them is read, and which text channels have a card. It is a value: the
// methods that change it return a changed copy, so a join can be previewed
// (to post a card) before it is committed.
type sessionView struct {
	VoiceChannelID string
	Started        time.Time
	// PerMessage is true when each message is deleted soon after it is read
	// (TTS_DELETE_AFTER), not only when the session ends.
	PerMessage bool
	// Channels are the text channels that have (or had) participants, in the
	// order their cards were posted.
	Channels []MarkerChannel
	// Members are the participants now, in join order; Left joined and left.
	Members []member
	Left    []string
	// Binds are the clean-up records: one per (user, channel) the user was
	// ever read in, with the time they first joined there.
	Binds []MarkerBind
	// Voices maps every user who has joined this session (including those who
	// later left) to their assigned speaker voice.
	Voices map[string]string
	// VoiceRound is the 1-based round number of the pool handout.
	VoiceRound int
	// RoundUsed records which voices have been handed out in VoiceRound.
	RoundUsed map[string]bool
}

func (v sessionView) clone() sessionView {
	v.Channels = slices.Clone(v.Channels)
	v.Members = slices.Clone(v.Members)
	v.Left = slices.Clone(v.Left)
	v.Binds = slices.Clone(v.Binds)
	v.Voices = maps.Clone(v.Voices)
	v.RoundUsed = maps.Clone(v.RoundUsed)
	return v
}

// voiceOf returns the speaker voice assigned to user in this session, if any.
func (v sessionView) voiceOf(user string) string {
	return v.Voices[user]
}

// assignVoice ensures user has a voice for this session, picking from pool when
// they have none yet or when picked is non-empty and differs from their current
// voice. It returns the updated view, the user's voice, and whether a new
// assignment was made.
func (v sessionView) assignVoice(user, picked string, pool []string, randIntn func(int) int) (next sessionView, voice string, assigned bool) {
	v = v.clone()
	picked = strings.TrimSpace(picked)
	current := v.Voices[user]
	if current != "" && (picked == "" || picked == current) {
		return v, current, false
	}
	if len(pool) == 0 {
		pool = tts.DefaultVoicePool
	}
	if randIntn == nil {
		randIntn = rand.IntN
	}
	unused := unusedVoices(pool, v.RoundUsed)
	if v.VoiceRound == 0 || len(unused) == 0 {
		v.VoiceRound++
		v.RoundUsed = make(map[string]bool)
		unused = pool
	}
	if v.RoundUsed == nil {
		v.RoundUsed = make(map[string]bool)
	}
	if picked != "" {
		voice = picked
	} else {
		voice = unused[randIntn(len(unused))]
	}
	v.RoundUsed[voice] = true
	if v.Voices == nil {
		v.Voices = make(map[string]string)
	}
	v.Voices[user] = voice
	return v, voice, true
}

func unusedVoices(pool []string, used map[string]bool) []string {
	var out []string
	for _, voice := range pool {
		if !used[voice] {
			out = append(out, voice)
		}
	}
	return out
}

// channelOf is the text channel user is read in, if they are a participant.
func (v sessionView) channelOf(user string) (string, bool) {
	for _, m := range v.Members {
		if m.User == user {
			return m.Channel, true
		}
	}
	return "", false
}

// hasBind reports whether user was ever bound to channel in this session.
func (v sessionView) hasBind(user, channel string) bool {
	return slices.ContainsFunc(v.Binds, func(b MarkerBind) bool { return b.User == user && b.Channel == channel })
}

// manageIn reports whether the bot could delete messages in channel when its
// card was posted.
func (v sessionView) manageIn(channel string) bool {
	for _, c := range v.Channels {
		if c.ID == channel {
			return c.Manage
		}
	}
	return false
}

func (v sessionView) hasChannel(channel string) bool {
	return slices.ContainsFunc(v.Channels, func(c MarkerChannel) bool { return c.ID == channel })
}

func (v sessionView) participants() []string {
	ids := make([]string, len(v.Members))
	for i, m := range v.Members {
		ids[i] = m.User
	}
	return ids
}

// everyone lists all users who took part, current participants first.
func (v sessionView) everyone() []string {
	return append(v.participants(), v.Left...)
}

// bindsIn maps each user bound to channel to when they first joined there.
func (v sessionView) bindsIn(channel string) map[string]time.Time {
	out := make(map[string]time.Time)
	for _, b := range v.Binds {
		if b.Channel == channel {
			out[b.User] = b.Since
		}
	}
	return out
}

// join returns v with user reading in channel from now on. A first join in a
// channel makes the channel known (manage is what the bot may do there) and
// records since for clean-up; a user who comes back keeps their earliest time.
func (v sessionView) join(user, channel string, manage bool, since time.Time) sessionView {
	v = v.clone()
	if !v.hasChannel(channel) {
		v.Channels = append(v.Channels, MarkerChannel{ID: channel, Manage: manage})
	}
	if i := slices.IndexFunc(v.Members, func(m member) bool { return m.User == user }); i >= 0 {
		v.Members[i].Channel = channel
	} else {
		v.Members = append(v.Members, member{User: user, Channel: channel})
		v.Left = slices.DeleteFunc(v.Left, func(id string) bool { return id == user })
	}
	if !v.hasBind(user, channel) {
		v.Binds = append(v.Binds, MarkerBind{User: user, Channel: channel, Since: since})
	}
	return v
}

// leave returns v with user moved from the participants to those who left. Their
// binds stay: their messages in those channels are still cleaned up.
func (v sessionView) leave(user string) sessionView {
	v = v.clone()
	v.Members = slices.DeleteFunc(v.Members, func(m member) bool { return m.User == user })
	v.Left = append(v.Left, user)
	return v
}

func (v sessionView) marker(open bool) SessionMarker {
	m := SessionMarker{
		Version:      2,
		Open:         open,
		Started:      v.Started,
		Voice:        v.VoiceChannelID,
		Participants: v.participants(),
		Left:         slices.Clone(v.Left),
		Channels:     slices.Clone(v.Channels),
		Binds:        slices.Clone(v.Binds),
	}
	for _, mb := range v.Members {
		if m.Reading == nil {
			m.Reading = make(map[string]string)
		}
		m.Reading[mb.User] = mb.Channel
	}
	return m
}

// footer is the small text of a session message. The marker is not in it but
// in the embed's hidden URL (markerURL).
func footer(human string) *discordgo.MessageEmbedFooter {
	return &discordgo.MessageEmbedFooter{Text: human}
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

// participantLines lists the participants grouped by the channel they are read
// in, one compact line per channel, e.g. "<@a> 🎙️Puck, <@b> 🎙️Orus · <#general>".
func participantLines(v sessionView) string {
	if len(v.Members) == 0 {
		return "-"
	}
	var lines []string
	shown := 0
	for _, c := range v.Channels {
		var parts []string
		hasVoice := false
		for _, m := range v.Members {
			if m.Channel == c.ID && shown < maxListedMentions {
				voice := v.Voices[m.User]
				if voice != "" {
					hasVoice = true
				}
				parts = append(parts, formatParticipant(m.User, voice))
				shown++
			}
		}
		if len(parts) > 0 {
			sep := " "
			if hasVoice {
				sep = ", "
			}
			lines = append(lines, strings.Join(parts, sep)+" · <#"+c.ID+">")
		}
	}
	if extra := len(v.Members) - shown; extra > 0 {
		lines = append(lines, fmt.Sprintf("และอีก %d คน", extra))
	}
	return strings.Join(lines, "\n")
}

func formatParticipant(user, voice string) string {
	if voice == "" {
		return "<@" + user + ">"
	}
	return "<@" + user + "> 🎙️" + voice
}

// sessionEmbed builds the live card shown in channelID, kept short: who takes
// part and in which channel they are read, the voice channel, since when, and
// how to join or leave in the footer. Every card shows the same people; only
// the deletion note depends on what the bot may do in channelID.
func sessionEmbed(v sessionView, channelID string) *discordgo.MessageEmbed {
	color := colorActive
	deletion := "ลบข้อความเมื่อจบ"
	if v.PerMessage {
		deletion = "ลบข้อความหลังอ่าน"
	}
	if !v.manageIn(channelID) {
		color = colorWarning
		deletion = "⚠️ ไม่มีสิทธิ์ลบข้อความ"
	}
	return &discordgo.MessageEmbed{
		URL: markerURL(v.marker(true)),
		Description: fmt.Sprintf("### 🔈 <#%s> · <t:%d:R>\n👥 %s",
			v.VoiceChannelID, v.Started.Unix(), participantLines(v)),
		Color:     color,
		Footer:    footer("/tts join เพื่อให้อ่าน · /tts leave เพื่อออก · " + deletion),
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
// ends, e.g. "📝 จบการอ่าน · @a @b · 25 นาที · ลบ 42 ข้อความ".
func summaryText(s summary) string {
	people := "-"
	if len(s.Participants) > 0 {
		people = mentions(s.Participants, " ")
	}
	parts := []string{"📝 จบการอ่าน", people, formatSessionDuration(s.Duration)}
	switch c := s.Cleanup; {
	case c.NoPermission:
		parts = append(parts, "ไม่ได้ลบข้อความ (ไม่มีสิทธิ์)")
	case c.Failed > 0:
		parts = append(parts, fmt.Sprintf("ลบ %d ข้อความ (ลบไม่ได้ %d)", c.Deleted, c.Failed))
	default:
		parts = append(parts, fmt.Sprintf("ลบ %d ข้อความ", c.Deleted))
	}
	return strings.Join(parts, " · ")
}

// summaryEmbed is the session message after the session ended. It keeps the
// "ended" marker so a restarted bot does not clean it up again.
func summaryEmbed(v sessionView, s summary, ended time.Time) *discordgo.MessageEmbed {
	// Everyone who took part is in the marker only for open sessions; an
	// ended one never needs it again.
	m := SessionMarker{Open: false, Started: v.Started, Voice: v.VoiceChannelID}
	return &discordgo.MessageEmbed{
		URL:         markerURL(m),
		Description: summaryText(s),
		Color:       colorEnded,
		Footer:      footer(usage.StatusFooterHuman),
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
