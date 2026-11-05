// Package readaloud owns the "read chat aloud" state. Each guild has at most
// one session: one voice connection, the users who opted in with /tts join, and
// the player that speaks. Every participant is read in the text channel where
// they ran /tts join (and only there), so a session spans several text
// channels; each of them has one public session card that lists everyone. When
// the session ends the participants' messages are deleted from the channels
// they were read in and the cards become a summary. With Config.DeleteAfter
// set, each message is also deleted a few seconds after it has been read
// aloud. Discord-specific parts are behind the Joiner, Presence and Messenger
// interfaces so the logic can be tested with fakes.
package readaloud

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/Sonty15/SONNIE/internal/tts"
	"github.com/Sonty15/SONNIE/internal/usage"
	"github.com/Sonty15/SONNIE/internal/voice"
)

var (
	// ErrNotBound is returned by Leave when the guild has no active session.
	ErrNotBound = errors.New("readaloud: not reading in this guild")
	// ErrNotParticipant is returned by Leave for a user who is not in the
	// guild's session.
	ErrNotParticipant = errors.New("readaloud: user is not in the session")
	// ErrSessionMessage is returned by Join when the public session message
	// could not be posted. The bot has then left the voice channel again.
	ErrSessionMessage = errors.New("readaloud: cannot post the session message")
)

// BusyError is returned by Join when the guild already has a session in
// another voice channel that still has people in it. The bot has one voice
// connection per guild, so a second voice channel has to wait.
type BusyError struct {
	VoiceChannelID string
}

func (e *BusyError) Error() string {
	return fmt.Sprintf("readaloud: already reading in voice channel %s", e.VoiceChannelID)
}

// Joiner connects to a voice channel.
type Joiner interface {
	Join(ctx context.Context, guildID, channelID string) (voice.Conn, error)
}

// Presence reports voice channel occupancy in a guild.
type Presence interface {
	// HumansInVoice counts the humans (non-bots) in channelID.
	HumansInVoice(guildID, channelID string) int
	// BotVoiceChannel returns the voice channel the bot is currently in, or
	// "" if it is not in a voice channel in guildID.
	BotVoiceChannel(guildID string) string
}

const (
	// editTimeout bounds one session-message edit.
	editTimeout = 10 * time.Second
	// cleanupTimeout bounds the clean-up of one ended session.
	cleanupTimeout = 3 * time.Minute
	// closeGrace is how long Close waits for clean-ups before giving up. A
	// session whose clean-up is cut short keeps its "open" marker, so the next
	// start finishes it.
	closeGrace = 20 * time.Second
)

// Config holds the Manager's dependencies and settings.
type Config struct {
	Synth     tts.Synthesizer
	Encoder   voice.Encoder
	Joiner    Joiner
	Presence  Presence
	Messenger Messenger

	Lang        string
	MaxChars    int
	QueueSize   int
	SpeakAuthor bool
	// DeleteAfter is how long after a participant's message has been read
	// aloud (or skipped) it is deleted. Zero turns this off and leaves only the
	// end-of-session clean-up.
	DeleteAfter time.Duration

	// Now returns the current time; nil means time.Now. For tests.
	Now func() time.Time
	// AfterFunc runs f after d; nil means time.AfterFunc. For tests.
	AfterFunc func(d time.Duration, f func()) Timer
}

// Timer is what Config.AfterFunc returns: a pending call that can be stopped.
type Timer interface {
	Stop() bool
}

// session is one guild's read-aloud session. The fields below mu are guarded
// by Manager.mu; the rest never change after the session starts.
type session struct {
	guildID        string
	voiceChannelID string
	conn           voice.Conn
	player         *voice.Player
	started        time.Time
	// live tracks the per-message deletions that run while the session does.
	live *liveDeletes

	// view is who takes part and where; cards are the posted session messages,
	// one per channel in view.Channels.
	view  sessionView
	cards map[string]card
	// tracked are the messages of participants seen live, for the clean-up's
	// fallback when the history scan fails.
	tracked map[string]trackedMessage
	// pending holds the delayed deletions that have not fired yet, by message
	// ID. Once ended is set (the session is over), none may be added.
	pending map[string]Timer
	ended   bool
}

// card is a session message the bot posted in a text channel.
type card struct {
	messageID string
	// created is when Discord created it: the clean-up of its channel only
	// looks at messages after it.
	created time.Time
}

// trackedMessage is a participant message seen live.
type trackedMessage struct {
	channelID string
	time      time.Time
}

// liveDeletes follows the deletions of one session that run in the
// background: how many are in flight, and how many messages they removed
// (guarded by Manager.mu). The end-of-session clean-up waits for them, so it
// neither races a delete nor misses it in the summary.
type liveDeletes struct {
	wg      sync.WaitGroup
	deleted int
}

// Manager holds at most one session per guild. It is safe for concurrent use.
type Manager struct {
	cfg      Config
	linkWord string

	// joinMu serialises Join, Leave and the end of a session, so two
	// concurrent /tts join calls cannot both connect and edits of the session
	// message reach Discord in the order the state changed. It is held across
	// the (slow) voice join, so mu, which message handling takes, is kept
	// separate.
	joinMu sync.Mutex

	mu        sync.Mutex
	sessions  map[string]*session
	recovered map[string]bool // session messages already taken over

	// bg parents the clean-ups, which outlive the request that ended the
	// session. Close cancels it if they take too long.
	bg     context.Context
	stopBG context.CancelFunc
	wg     sync.WaitGroup

	// limitLogged is the month for which the monthly-limit error was last
	// logged, so it is logged once rather than for every skipped message.
	limitMu     sync.Mutex
	limitLogged string
}

// New creates a Manager.
func New(cfg Config) *Manager {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.AfterFunc == nil {
		cfg.AfterFunc = func(d time.Duration, f func()) Timer { return time.AfterFunc(d, f) }
	}
	bg, stop := context.WithCancel(context.Background())
	return &Manager{
		cfg:       cfg,
		linkWord:  DefaultLinkWord(cfg.Lang),
		sessions:  make(map[string]*session),
		recovered: make(map[string]bool),
		bg:        bg,
		stopBG:    stop,
	}
}

// logLimitOnce handles usage.ErrLimitReached: it logs the first one each month
// and reports true (handled) for all of them, so skipped messages stay quiet.
func (m *Manager) logLimitOnce(err error) bool {
	var le *usage.LimitError
	if !errors.As(err, &le) {
		return false
	}
	m.limitMu.Lock()
	first := m.limitLogged != le.Month
	m.limitLogged = le.Month
	m.limitMu.Unlock()
	if first {
		log.Printf("tts: %v; messages are skipped until next month", le)
	}
	return true
}

// JoinRequest is a user's /tts join.
type JoinRequest struct {
	GuildID string
	UserID  string
	// VoiceChannelID is the voice channel the user is in; TextChannelID the
	// channel they ran the command in, which is where their messages are read.
	VoiceChannelID string
	TextChannelID  string
}

// JoinResult describes what a successful Join did.
type JoinResult struct {
	// Started is true when this call created the session; otherwise the user
	// was added to an existing one.
	Started bool
	// AlreadyIn is true when the user was already read in this text channel;
	// nothing changed.
	AlreadyIn bool
	// Moved is true when the user was a participant already and is now read in
	// another text channel (PreviousChannelID).
	Moved             bool
	PreviousChannelID string
	// Participants is how many users are in the session now.
	Participants int
	// CanManage is false when the bot lacks Manage Messages in the text
	// channel, so participants' messages there will not be deleted.
	CanManage bool
}

// Join adds the user to the guild's session, starting one (joining the voice
// channel) if there is none. The user is read in req.TextChannelID, which gets
// a session card of its own if it has none yet; a participant who joins again
// from another text channel moves there.
//
// It fails with a *BusyError when the guild's session is in another voice
// channel that still has people in it. A session whose voice channel has no
// humans left is ended (with clean-up) and replaced.
func (m *Manager) Join(ctx context.Context, req JoinRequest) (JoinResult, error) {
	m.joinMu.Lock()
	defer m.joinMu.Unlock()

	m.mu.Lock()
	s := m.sessions[req.GuildID]
	m.mu.Unlock()

	if s != nil && s.voiceChannelID == req.VoiceChannelID {
		return m.addParticipant(ctx, s, req)
	}
	if s != nil {
		if m.cfg.Presence == nil || m.cfg.Presence.HumansInVoice(req.GuildID, s.voiceChannelID) > 0 {
			return JoinResult{}, &BusyError{VoiceChannelID: s.voiceChannelID}
		}
		// Nobody is in the old session's voice channel: end it, then start
		// the new one. discordgo keeps one voice connection per guild, so
		// the old one is torn down first.
		if err := m.endLocked(s, "replaced by a session in another voice channel", true); err != nil {
			log.Printf("tts: guild %s: ending the previous session: %v", req.GuildID, err)
		}
	}
	return m.start(ctx, req)
}

// addParticipant adds the user to s, or moves them to req.TextChannelID.
// joinMu is held.
func (m *Manager) addParticipant(ctx context.Context, s *session, req JoinRequest) (JoinResult, error) {
	m.mu.Lock()
	prev, in := s.view.channelOf(req.UserID)
	view := s.view
	_, haveCard := s.cards[req.TextChannelID]
	m.mu.Unlock()

	if in && prev == req.TextChannelID {
		return JoinResult{AlreadyIn: true, Participants: len(view.Members), CanManage: view.manageIn(prev)}, nil
	}

	// A channel without a card gets one; the card is posted before anything
	// is committed, so a failure leaves the session as it was.
	manage := view.manageIn(req.TextChannelID)
	if !haveCard {
		manage = m.cfg.Messenger.CanManageMessages(req.TextChannelID)
	}
	next := view.join(req.UserID, req.TextChannelID, manage, m.cfg.Now())
	var posted *card
	if !haveCard {
		id, created, err := m.cfg.Messenger.Post(ctx, req.TextChannelID, sessionEmbed(next, req.TextChannelID))
		if err != nil {
			return JoinResult{}, fmt.Errorf("%w: %v", ErrSessionMessage, err)
		}
		if created.IsZero() {
			created = m.cfg.Now()
		}
		posted = &card{messageID: id, created: created}
	}

	m.mu.Lock()
	s.view = next
	if posted != nil {
		s.cards[req.TextChannelID] = *posted
	}
	m.mu.Unlock()

	log.Printf("tts: session join guild=%s text=%s user=%s moved_from=%q new_card=%t participants=%d",
		s.guildID, req.TextChannelID, req.UserID, prev, posted != nil, len(next.Members))
	skip := ""
	if posted != nil {
		skip = req.TextChannelID // posted with the current state already
	}
	m.editCards(s, next, skip)
	return JoinResult{Moved: in, PreviousChannelID: prev, Participants: len(next.Members), CanManage: manage}, nil
}

// start connects to the voice channel, posts the first card and registers the
// session. joinMu is held.
func (m *Manager) start(ctx context.Context, req JoinRequest) (JoinResult, error) {
	conn, err := m.cfg.Joiner.Join(ctx, req.GuildID, req.VoiceChannelID)
	if err != nil {
		return JoinResult{}, err
	}

	manage := m.cfg.Messenger.CanManageMessages(req.TextChannelID)
	s := &session{
		guildID:        req.GuildID,
		voiceChannelID: req.VoiceChannelID,
		conn:           conn,
		cards:          make(map[string]card),
		tracked:        make(map[string]trackedMessage),
		pending:        make(map[string]Timer),
		live:           new(liveDeletes),
	}
	// The first render needs a start time before Discord has reported one.
	s.started = m.cfg.Now()
	// The first user's clean-up runs from the start of their card (zero time).
	s.view = sessionView{
		VoiceChannelID: req.VoiceChannelID,
		Started:        s.started,
		PerMessage:     m.cfg.DeleteAfter > 0,
	}.join(req.UserID, req.TextChannelID, manage, time.Time{})
	id, created, err := m.cfg.Messenger.Post(ctx, req.TextChannelID, sessionEmbed(s.view, req.TextChannelID))
	if err != nil {
		if derr := conn.Disconnect(); derr != nil {
			log.Printf("tts: guild %s: leaving voice after a failed session message: %v", req.GuildID, derr)
		}
		return JoinResult{}, fmt.Errorf("%w: %v", ErrSessionMessage, err)
	}
	// The card's creation time is the start of the window whose messages are
	// cleaned up, so prefer what Discord reports to our clock.
	if created.IsZero() {
		created = s.started
	}
	s.started = created
	s.view.Started = created
	s.cards[req.TextChannelID] = card{messageID: id, created: created}

	guildID := req.GuildID
	s.player = voice.NewPlayer(voice.PlayerConfig{
		Synth:     m.cfg.Synth,
		Encoder:   m.cfg.Encoder,
		Conn:      conn,
		Lang:      m.cfg.Lang,
		QueueSize: m.cfg.QueueSize,
		OnError: func(err error) {
			if m.logLimitOnce(err) {
				return
			}
			log.Printf("tts: guild %s: %v", guildID, err)
		},
	})

	m.mu.Lock()
	m.sessions[req.GuildID] = s
	m.mu.Unlock()

	log.Printf("tts: session start guild=%s text=%s voice=%s user=%s message=%s can_manage_messages=%t",
		req.GuildID, req.TextChannelID, req.VoiceChannelID, req.UserID, id, manage)
	return JoinResult{Started: true, Participants: 1, CanManage: manage}, nil
}

// LeaveResult describes what a successful Leave did.
type LeaveResult struct {
	// Ended is true when the user was the last participant, so the session
	// ended and its clean-up started.
	Ended bool
	// Participants is how many users remain.
	Participants int
}

// Leave removes the user from the guild's session. When the last participant
// leaves, the session ends: the bot leaves the voice channel and, in the
// background, deletes the participants' messages and turns the session
// cards into a summary.
//
// It returns ErrNotBound without a session and ErrNotParticipant if the user
// is not in it. An error together with Ended means the session ended but
// leaving the voice channel failed.
func (m *Manager) Leave(guildID, userID string) (LeaveResult, error) {
	m.joinMu.Lock()
	defer m.joinMu.Unlock()

	m.mu.Lock()
	s := m.sessions[guildID]
	if s == nil {
		m.mu.Unlock()
		return LeaveResult{}, ErrNotBound
	}
	if _, ok := s.view.channelOf(userID); !ok {
		m.mu.Unlock()
		return LeaveResult{}, ErrNotParticipant
	}
	s.view = s.view.leave(userID)
	n := len(s.view.Members)
	view := s.view
	m.mu.Unlock()

	log.Printf("tts: session leave guild=%s user=%s participants=%d", guildID, userID, n)
	if n == 0 {
		return LeaveResult{Ended: true}, m.endLocked(s, "last participant left", true)
	}
	m.editCards(s, view, "")
	return LeaveResult{Participants: n}, nil
}

// Bound returns the voice channel of guildID's session, if any.
func (m *Manager) Bound(guildID string) (voiceChannelID string, ok bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.sessions[guildID]
	if s == nil {
		return "", false
	}
	return s.voiceChannelID, true
}

// TextChannel returns the text channel userID is read in, if they take part
// in guildID's session.
func (m *Manager) TextChannel(guildID, userID string) (string, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s := m.sessions[guildID]; s != nil {
		return s.view.channelOf(userID)
	}
	return "", false
}

// TextChannels returns the text channels that have a session card in
// guildID's session, in the order the cards were posted.
func (m *Manager) TextChannels(guildID string) []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.sessions[guildID]
	if s == nil {
		return nil
	}
	ids := make([]string, len(s.view.Channels))
	for i, c := range s.view.Channels {
		ids[i] = c.ID
	}
	return ids
}

// Participants returns the users in guildID's session, in join order.
func (m *Manager) Participants(guildID string) []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s := m.sessions[guildID]; s != nil {
		return s.view.participants()
	}
	return nil
}

// HandleMessage queues msg for speech if its author is a participant and it
// was sent in the text channel that participant joined from, and it survives
// filtering. It reports whether something was queued. A message of anyone who
// has been in the session is remembered for the clean-up if it is in a channel
// they were bound to, whether or not it is spoken; a participant's messages in
// other channels are neither read nor touched, and other people's never are.
//
// A participant's message is also deleted DeleteAfter after the player has
// finished it, or, when it is not going to be spoken (nothing left to say
// after filtering, queue full, synthesis failed, monthly budget reached),
// after the same delay from now, so the channel stays clean either way.
func (m *Manager) HandleMessage(msg Message, names Names) bool {
	m.mu.Lock()
	s := m.sessions[msg.GuildID]
	if s == nil || msg.IsBot || msg.AuthorID == "" || !s.view.hasChannel(msg.ChannelID) {
		m.mu.Unlock()
		return false
	}
	if msg.MessageID != "" && s.view.hasBind(msg.AuthorID, msg.ChannelID) {
		at := msg.Time
		if at.IsZero() {
			at = m.cfg.Now()
		}
		s.tracked[msg.MessageID] = trackedMessage{channelID: msg.ChannelID, time: at}
	}
	readIn, isParticipant := s.view.channelOf(msg.AuthorID)
	speak := isParticipant && readIn == msg.ChannelID
	player := s.player
	m.mu.Unlock()

	if !speak {
		return false
	}
	if !ShouldSpeak(msg) {
		m.scheduleDelete(s, msg.ChannelID, msg.MessageID)
		return false
	}
	text := Normalize(msg.Content, TextOptions{
		MaxChars: m.cfg.MaxChars,
		LinkWord: m.linkWord,
		Names:    names,
	})
	if text == "" {
		m.scheduleDelete(s, msg.ChannelID, msg.MessageID)
		return false
	}
	// Spoken, dropped from a full queue or failed: in every case the player is
	// done with the message, which is when its delay starts. A message cut off
	// by the end of the session is left to the end-of-session clean-up.
	player.EnqueueWith(Utterance(msg.AuthorName, text, m.cfg.SpeakAuthor), func(bool) {
		m.scheduleDelete(s, msg.ChannelID, msg.MessageID)
	})
	return true
}

// scheduleDelete deletes message id in channelID after Config.DeleteAfter, in
// the background. It does nothing when per-message deletion is off, the bot
// could not delete messages in that channel when its card was posted, or the
// session is over (the end-of-session clean-up takes what is left).
func (m *Manager) scheduleDelete(s *session, channelID, id string) {
	if m.cfg.DeleteAfter <= 0 || id == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if !s.view.manageIn(channelID) || s.ended || s.pending[id] != nil {
		return
	}
	// Holding mu until the timer is stored means a fired callback, which
	// takes mu first, always finds it.
	s.pending[id] = m.cfg.AfterFunc(m.cfg.DeleteAfter, func() { m.deleteSpoken(s, channelID, id) })
}

// deleteSpoken is the timer callback of scheduleDelete: one single delete,
// never blocking speech (timers run on their own goroutine). On success the
// message leaves the tracked set and counts towards the summary. On failure
// it is logged once and stays tracked, so the end-of-session clean-up retries.
func (m *Manager) deleteSpoken(s *session, channelID, id string) {
	m.mu.Lock()
	if s.ended || s.pending[id] == nil {
		m.mu.Unlock()
		return
	}
	delete(s.pending, id)
	s.live.wg.Add(1)
	m.mu.Unlock()
	defer s.live.wg.Done()

	ctx, cancel := context.WithTimeout(m.bg, editTimeout)
	defer cancel()
	err := m.cfg.Messenger.DeleteMessage(ctx, channelID, id)
	gone := errors.Is(err, ErrMessageGone)
	if err != nil && !gone {
		log.Printf("tts: guild %s: deleting message %s after it was read: %v; the end-of-session clean-up retries", s.guildID, id, err)
		return
	}
	m.mu.Lock()
	delete(s.tracked, id)
	if !gone { // someone else deleting it first is not our deletion
		s.live.deleted++
	}
	m.mu.Unlock()
}

// HandleVoiceStateUpdate ends the session in guildID if the bot itself was
// disconnected or moved out of the session's voice channel, or if no humans
// remain in that channel. Call it on every voice state change in the guild.
func (m *Manager) HandleVoiceStateUpdate(guildID string) {
	m.mu.Lock()
	s := m.sessions[guildID]
	m.mu.Unlock()
	if s == nil || m.cfg.Presence == nil {
		return
	}

	switch botChannel := m.cfg.Presence.BotVoiceChannel(guildID); {
	case botChannel == "":
		// Already disconnected from voice: end the session and stop the
		// player without calling Disconnect again.
		m.endIfCurrent(s, "bot disconnected from voice", false)
	case botChannel != s.voiceChannelID:
		// Moved to another channel by a moderator: end the session rather
		// than silently rebinding, and leave that channel.
		m.endIfCurrent(s, "bot moved to another voice channel", true)
	case m.cfg.Presence.HumansInVoice(guildID, s.voiceChannelID) == 0:
		m.endIfCurrent(s, "no humans left in voice", true)
	}
}

func (m *Manager) endIfCurrent(s *session, reason string, disconnect bool) {
	m.joinMu.Lock()
	defer m.joinMu.Unlock()

	m.mu.Lock()
	current := m.sessions[s.guildID] == s
	m.mu.Unlock()
	if !current {
		return
	}
	if err := m.endLocked(s, reason, disconnect); err != nil {
		log.Printf("tts: guild %s: auto-leave: %v", s.guildID, err)
	}
}

// endedSession is everything the clean-up of a finished session needs.
type endedSession struct {
	guildID string
	view    sessionView
	// cards are the session messages to clean up after and turn into the
	// summary, in channel order.
	cards   []endedCard
	tracked map[string]trackedMessage
	ended   time.Time
	// endedIn overrides ended for a channel where a newer live session took
	// over (recovery only).
	endedIn map[string]time.Time
	reason  string
	// live is the session's per-message deletions; nil for a recovered one.
	live *liveDeletes
	// src is the live session this was detached from (nil when recovered
	// after a restart); its tracked set is re-read once in-flight deletes
	// are done, so messages they removed are not retried.
	src *session
}

type endedCard struct {
	channelID, messageID string
	created              time.Time
}

// endLocked removes s from its guild, leaves voice and starts the clean-up in
// the background. joinMu is held.
func (m *Manager) endLocked(s *session, reason string, disconnect bool) error {
	m.mu.Lock()
	if m.sessions[s.guildID] == s {
		delete(m.sessions, s.guildID)
	}
	m.mu.Unlock()
	return m.detach(s, reason, disconnect)
}

// detach stops s's player, optionally disconnects, and starts the clean-up.
// s must already be out of m.sessions.
func (m *Manager) detach(s *session, reason string, disconnect bool) error {
	// Cancel the delayed deletions first: from here on nothing is added or
	// fires, and what is left is the clean-up's. A delete already in flight is
	// waited for in finish.
	m.mu.Lock()
	s.ended = true
	for id, t := range s.pending {
		t.Stop()
		delete(s.pending, id)
	}
	m.mu.Unlock()

	s.player.Close()
	var err error
	if disconnect {
		err = s.conn.Disconnect()
	}

	m.mu.Lock()
	e := endedSession{
		guildID: s.guildID,
		view:    s.view.clone(),
		tracked: maps.Clone(s.tracked),
		ended:   m.cfg.Now(),
		reason:  reason,
		live:    s.live,
		src:     s,
	}
	for _, c := range e.view.Channels {
		if cd, ok := s.cards[c.ID]; ok {
			e.cards = append(e.cards, endedCard{channelID: c.ID, messageID: cd.messageID, created: cd.created})
		}
	}
	m.mu.Unlock()

	m.goFinish(e)
	return err
}

func (m *Manager) goFinish(e endedSession) {
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		ctx, cancel := context.WithTimeout(m.bg, cleanupTimeout)
		defer cancel()
		m.finish(ctx, e)
	}()
}

// finish deletes the participants' messages in every channel they were read
// in and turns the session cards into the summary that stays as the log.
func (m *Manager) finish(ctx context.Context, e endedSession) {
	msgr := m.cfg.Messenger
	var res CleanupResult
	// Deletions that fired just before the session ended may still be talking
	// to Discord; let them finish so the scan below does not see them half
	// done and the count includes them.
	var live int
	if e.live != nil {
		e.live.wg.Wait()
		m.mu.Lock()
		live = e.live.deleted
		if e.src != nil {
			e.tracked = maps.Clone(e.src.tracked)
		}
		m.mu.Unlock()
	}
	// Each channel's participants were told whether their messages would be
	// deleted when its card was posted; keep that promise, and never delete
	// without the permission now.
	skipped := 0
	for _, c := range e.cards {
		if !e.view.manageIn(c.channelID) || !msgr.CanManageMessages(c.channelID) {
			skipped++
			continue
		}
		end := e.ended
		if t, ok := e.endedIn[c.channelID]; ok {
			end = t
		}
		tracked := make(map[string]time.Time)
		for id, t := range e.tracked {
			if t.channelID == c.channelID {
				tracked[id] = t.time
			}
		}
		r := cleanupMessages(ctx, msgr, cleanupJob{
			ChannelID:        c.channelID,
			SessionMessageID: c.messageID,
			Started:          c.created,
			Ended:            end,
			Participants:     e.view.bindsIn(c.channelID), // zero time: from the card
			Tracked:          tracked,
		}, m.cfg.Now())
		res.Deleted += r.Deleted
		res.Failed += r.Failed
	}
	res.NoPermission = skipped > 0 && skipped == len(e.cards)

	res.Deleted += live

	// Cut short by shutdown: leave the cards open so the next start finishes
	// the job instead of recording a summary that hides the rest.
	if m.bg.Err() != nil {
		log.Printf("tts: session end guild=%s channels=%s: clean-up interrupted by shutdown (deleted=%d failed=%d); it is finished on the next start",
			e.guildID, e.channelList(), res.Deleted, res.Failed)
		return
	}

	duration := e.ended.Sub(e.view.Started)
	embed := summaryEmbed(e.view, summary{Participants: e.view.everyone(), Duration: duration, Cleanup: res}, e.ended)
	// The clean-up may have used up the context; the summary is what marks
	// the session finished, so it gets its own.
	editCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), editTimeout)
	defer cancel()
	for _, c := range e.cards {
		if err := msgr.Edit(editCtx, c.channelID, c.messageID, embed); err != nil {
			log.Printf("tts: session summary guild=%s channel=%s message=%s: %v", e.guildID, c.channelID, c.messageID, err)
		}
	}
	log.Printf("tts: session end guild=%s channels=%s reason=%q users=%s duration=%s deleted=%d failed=%d no_permission=%t",
		e.guildID, e.channelList(), e.reason, strings.Join(e.view.everyone(), ","),
		duration.Round(time.Second), res.Deleted, res.Failed, res.NoPermission)
}

func (e endedSession) channelList() string {
	ids := make([]string, len(e.cards))
	for i, c := range e.cards {
		ids[i] = c.channelID
	}
	return strings.Join(ids, ",")
}

// editCards brings every session card up to date with view, except the one
// in skipChannel (just posted with it). Failures are logged: the session
// keeps working without a current card. joinMu is held, which keeps edits in
// order.
func (m *Manager) editCards(s *session, view sessionView, skipChannel string) {
	m.mu.Lock()
	cards := maps.Clone(s.cards)
	m.mu.Unlock()
	for _, c := range view.Channels {
		cd, ok := cards[c.ID]
		if !ok || c.ID == skipChannel {
			continue
		}
		ctx, cancel := context.WithTimeout(m.bg, editTimeout)
		if err := m.cfg.Messenger.Edit(ctx, c.ID, cd.messageID, sessionEmbed(view, c.ID)); err != nil {
			log.Printf("tts: guild %s: editing the session message %s in %s: %v", s.guildID, cd.messageID, c.ID, err)
		}
		cancel()
	}
}

// Wait blocks until all background clean-ups started so far have finished.
func (m *Manager) Wait() {
	m.wg.Wait()
}

// Close ends every session on shutdown: it leaves voice and finishes the
// clean-ups, waiting a short while for them. Clean-ups that do not finish
// keep their "open" marker, and the next start completes them.
func (m *Manager) Close() {
	m.joinMu.Lock()
	m.mu.Lock()
	sessions := m.sessions
	m.sessions = make(map[string]*session)
	m.mu.Unlock()
	for guildID, s := range sessions {
		if err := m.detach(s, "bot shutting down", true); err != nil {
			log.Printf("tts: guild %s: close: %v", guildID, err)
		}
	}
	m.joinMu.Unlock()

	done := make(chan struct{})
	go func() {
		m.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(closeGrace):
		log.Printf("tts: clean-up did not finish in %s; unfinished sessions are completed on the next start", closeGrace)
		m.stopBG()
		<-done
	}
	m.stopBG()
}

// RecoverGuild finishes the clean-up of sessions in guildID that a previous
// run of the bot left unfinished: it finds the bot's own session cards that
// still carry an "open" marker, groups the cards of one session (v2 markers
// name the same voice channel; a v1 card is a session of its own), deletes the
// messages of every participant in every channel they were bound to and turns
// all the cards into a summary. Call it once per guild at startup; it does
// nothing for a session that is live in this process.
func (m *Manager) RecoverGuild(ctx context.Context, guildID string) {
	open, err := m.cfg.Messenger.OpenSessions(ctx, guildID)
	if err != nil {
		log.Printf("tts: guild %s: looking for unfinished sessions: %v", guildID, err)
		return
	}
	for _, group := range groupOpenSessions(open) {
		now := m.cfg.Now()
		m.mu.Lock()
		skip := false
		for _, o := range group {
			skip = skip || m.recovered[o.MessageID]
			m.recovered[o.MessageID] = true
		}
		// A session started since the restart that has a card in the same
		// channel owns everything in it from its own card on.
		endedIn := make(map[string]time.Time)
		if live := m.sessions[guildID]; live != nil {
			for _, o := range group {
				if cd, ok := live.cards[o.ChannelID]; ok {
					if cd.messageID == o.MessageID {
						skip = true
					}
					if cd.created.Before(now) {
						endedIn[o.ChannelID] = cd.created
					}
				}
			}
		}
		m.mu.Unlock()
		if skip {
			continue
		}

		e := recoveredSession(guildID, group, now)
		e.endedIn = endedIn
		log.Printf("tts: recovering unfinished session guild=%s channels=%s users=%s",
			guildID, e.channelList(), strings.Join(e.view.everyone(), ","))
		m.goFinish(e)
	}
}

// groupOpenSessions groups the cards found in history into sessions, keeping
// the order in which they were found.
func groupOpenSessions(open []OpenSession) [][]OpenSession {
	var groups [][]OpenSession
	index := make(map[string]int)
	for _, o := range open {
		key := "v1/" + o.ChannelID + "/" + o.MessageID
		if o.Marker.Version >= 2 {
			key = "v2/" + o.Marker.Voice
		}
		i, ok := index[key]
		if !ok {
			i = len(groups)
			index[key] = i
			groups = append(groups, nil)
		}
		groups[i] = append(groups[i], o)
	}
	return groups
}

// recoveredSession rebuilds a lost session from its cards. Every v2 card has
// the whole marker, but one may be out of date (a failed edit), so the markers
// are merged: the union of channels, participants and binds. A v1 marker has
// a single channel and no join times, so everyone is cleaned from the card on.
func recoveredSession(guildID string, cards []OpenSession, ended time.Time) endedSession {
	e := endedSession{guildID: guildID, tracked: map[string]trackedMessage{}, ended: ended, reason: "recovered after restart"}
	v := &e.view
	v.Started = cards[0].Marker.Started
	v.VoiceChannelID = cards[0].Marker.Voice

	addChannel := func(id string, manage bool) {
		if !v.hasChannel(id) {
			v.Channels = append(v.Channels, MarkerChannel{ID: id, Manage: manage})
		}
	}
	addBind := func(b MarkerBind) {
		// Zero (from the card on) is the earliest of all.
		for i, old := range v.Binds {
			if old.User == b.User && old.Channel == b.Channel {
				if !old.Since.IsZero() && (b.Since.IsZero() || b.Since.Before(old.Since)) {
					v.Binds[i].Since = b.Since
				}
				return
			}
		}
		v.Binds = append(v.Binds, b)
	}
	addMember := func(user, channel string) {
		if _, ok := v.channelOf(user); !ok {
			v.Members = append(v.Members, member{User: user, Channel: channel})
		}
	}
	for _, o := range cards {
		mk := o.Marker
		if mk.Version < 2 {
			addChannel(o.ChannelID, mk.Manage)
			for _, u := range mk.Participants {
				addMember(u, o.ChannelID)
			}
			for _, u := range mk.All() {
				addBind(MarkerBind{User: u, Channel: o.ChannelID})
			}
			v.Left = append(v.Left, mk.Left...)
			continue
		}
		for _, c := range mk.Channels {
			addChannel(c.ID, c.Manage)
		}
		for _, u := range mk.Participants {
			addMember(u, cmp.Or(mk.Reading[u], o.ChannelID))
		}
		for _, b := range mk.Binds {
			addBind(b)
		}
		v.Left = append(v.Left, mk.Left...)
	}
	// A user who is in one card's participants but in another's "left" is a
	// participant; duplicates are dropped.
	seen := make(map[string]bool)
	v.Left = slices.DeleteFunc(v.Left, func(id string) bool {
		_, in := v.channelOf(id)
		dup := in || seen[id]
		seen[id] = true
		return dup
	})

	// Discord's creation time of the earliest card, not the host clock in the
	// marker, so clock skew cannot skip early messages.
	var first time.Time
	for _, o := range cards {
		if !o.Time.IsZero() && (first.IsZero() || o.Time.Before(first)) {
			first = o.Time
		}
	}
	if !first.IsZero() {
		v.Started = first
	}
	for _, o := range cards {
		created := o.Time
		if created.IsZero() {
			created = o.Marker.Started
		}
		e.cards = append(e.cards, endedCard{channelID: o.ChannelID, messageID: o.MessageID, created: created})
		// A card without a channel entry (a v2 marker that lost it) must not be
		// cleaned up: its permission at the time is unknown.
		addChannel(o.ChannelID, false)
	}
	// Cards in the order of the channels.
	slices.SortStableFunc(e.cards, func(a, b endedCard) int {
		return slices.IndexFunc(v.Channels, func(c MarkerChannel) bool { return c.ID == a.channelID }) -
			slices.IndexFunc(v.Channels, func(c MarkerChannel) bool { return c.ID == b.channelID })
	})
	return e
}
