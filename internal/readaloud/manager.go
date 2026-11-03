// Package readaloud owns the "read chat aloud" state. Each guild has at most
// one session: a text channel bound to a voice connection, the users who opted
// in with /tts join (only their messages are spoken), one public session
// message that lists them, and the player that speaks. When the session ends
// the participants' messages are deleted from the text channel and the session
// message becomes a summary. With Config.DeleteAfter set, each message is also
// deleted a few seconds after it has been read aloud. Discord-specific parts are behind the Joiner,
// Presence and Messenger interfaces so the logic can be tested with fakes.
package readaloud

import (
	"context"
	"errors"
	"fmt"
	"log"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"

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
// another voice channel that still has people in it.
type BusyError struct {
	VoiceChannelID string
	TextChannelID  string
}

func (e *BusyError) Error() string {
	return fmt.Sprintf("readaloud: already reading <#%s> in voice channel %s", e.TextChannelID, e.VoiceChannelID)
}

// TextChannelError is returned by Join when the session in the caller's voice
// channel is bound to a different text channel.
type TextChannelError struct {
	TextChannelID string
}

func (e *TextChannelError) Error() string {
	return fmt.Sprintf("readaloud: this voice channel is bound to text channel %s", e.TextChannelID)
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
	textChannelID  string
	voiceChannelID string
	conn           voice.Conn
	player         *voice.Player
	messageID      string
	started        time.Time
	// manage records whether the bot could delete messages at the start.
	manage bool
	// live tracks the per-message deletions that run while the session does.
	live *liveDeletes

	participants []string             // in the session now, in join order
	left         []string             // joined, then left
	tracked      map[string]time.Time // participants' messages seen live
	// joinedAt is when each user first joined; only their messages from
	// then on are deleted, so chat from before they opted in stays.
	joinedAt map[string]time.Time
	// pending holds the delayed deletions that have not fired yet, by message
	// ID. Once ended is set (the session is over), none may be added.
	pending map[string]Timer
	ended   bool
}

// liveDeletes follows the deletions of one session that run in the
// background: how many are in flight, and how many messages they removed
// (guarded by Manager.mu). The end-of-session clean-up waits for them, so it
// neither races a delete nor misses it in the summary.
type liveDeletes struct {
	wg      sync.WaitGroup
	deleted int
}

func (s *session) isParticipant(userID string) bool {
	return slices.Contains(s.participants, userID)
}

func (s *session) hasTaken(userID string) bool {
	return s.isParticipant(userID) || slices.Contains(s.left, userID)
}

// everyone lists all users who took part, current participants first.
func (s *session) everyone() []string {
	return append(slices.Clone(s.participants), s.left...)
}

func (s *session) view() sessionView {
	return sessionView{
		TextChannelID:  s.textChannelID,
		VoiceChannelID: s.voiceChannelID,
		Started:        s.started,
		Manage:         s.manage,
		Participants:   slices.Clone(s.participants),
		Left:           slices.Clone(s.left),
	}
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
	// channel they ran the command in.
	VoiceChannelID string
	TextChannelID  string
}

// JoinResult describes what a successful Join did.
type JoinResult struct {
	// Started is true when this call created the session and posted the
	// session message; otherwise the user was added to an existing one.
	Started bool
	// AlreadyIn is true when the user was a participant already; nothing
	// changed.
	AlreadyIn bool
	// Participants is how many users are in the session now.
	Participants int
	// CanManage is false when the bot lacks Manage Messages in the text
	// channel, so participants' messages will not be deleted at the end.
	CanManage bool
}

// Join adds the user to the guild's session, starting one (joining the voice
// channel and posting the session message) if there is none.
//
// It fails with a *BusyError when the guild's session is in another voice
// channel that still has people in it, and with a *TextChannelError when the
// session in the user's voice channel is bound to another text channel. A
// session whose voice channel has no humans left is ended (with clean-up)
// and replaced.
func (m *Manager) Join(ctx context.Context, req JoinRequest) (JoinResult, error) {
	m.joinMu.Lock()
	defer m.joinMu.Unlock()

	m.mu.Lock()
	s := m.sessions[req.GuildID]
	m.mu.Unlock()

	if s != nil && s.voiceChannelID == req.VoiceChannelID {
		return m.addParticipant(s, req)
	}
	if s != nil {
		if m.cfg.Presence == nil || m.cfg.Presence.HumansInVoice(req.GuildID, s.voiceChannelID) > 0 {
			return JoinResult{}, &BusyError{VoiceChannelID: s.voiceChannelID, TextChannelID: s.textChannelID}
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

// addParticipant adds the user to s. joinMu is held.
func (m *Manager) addParticipant(s *session, req JoinRequest) (JoinResult, error) {
	if s.textChannelID != req.TextChannelID {
		return JoinResult{}, &TextChannelError{TextChannelID: s.textChannelID}
	}

	m.mu.Lock()
	if s.isParticipant(req.UserID) {
		n := len(s.participants)
		m.mu.Unlock()
		return JoinResult{AlreadyIn: true, Participants: n, CanManage: s.manage}, nil
	}
	s.left = slices.DeleteFunc(s.left, func(id string) bool { return id == req.UserID })
	s.participants = append(s.participants, req.UserID)
	if _, ok := s.joinedAt[req.UserID]; !ok {
		s.joinedAt[req.UserID] = m.cfg.Now()
	}
	n := len(s.participants)
	view := s.view()
	m.mu.Unlock()

	log.Printf("tts: session join guild=%s text=%s user=%s participants=%d", s.guildID, s.textChannelID, req.UserID, n)
	m.editSession(s, sessionEmbed(view))
	return JoinResult{Participants: n, CanManage: s.manage}, nil
}

// start connects to the voice channel, posts the session message and
// registers the session. joinMu is held.
func (m *Manager) start(ctx context.Context, req JoinRequest) (JoinResult, error) {
	conn, err := m.cfg.Joiner.Join(ctx, req.GuildID, req.VoiceChannelID)
	if err != nil {
		return JoinResult{}, err
	}

	manage := m.cfg.Messenger.CanManageMessages(req.TextChannelID)
	s := &session{
		guildID:        req.GuildID,
		textChannelID:  req.TextChannelID,
		voiceChannelID: req.VoiceChannelID,
		conn:           conn,
		manage:         manage,
		participants:   []string{req.UserID},
		tracked:        make(map[string]time.Time),
		joinedAt:       make(map[string]time.Time),
		pending:        make(map[string]Timer),
		live:           new(liveDeletes),
	}
	// The first render needs a start time before Discord has reported one.
	s.started = m.cfg.Now()
	id, created, err := m.cfg.Messenger.Post(ctx, req.TextChannelID, sessionEmbed(s.view()))
	if err != nil {
		if derr := conn.Disconnect(); derr != nil {
			log.Printf("tts: guild %s: leaving voice after a failed session message: %v", req.GuildID, derr)
		}
		return JoinResult{}, fmt.Errorf("%w: %v", ErrSessionMessage, err)
	}
	s.messageID = id
	// The session message's creation time is the start of the window whose
	// messages are cleaned up, so prefer what Discord reports to our clock.
	if !created.IsZero() {
		s.started = created
	}

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
		req.GuildID, req.TextChannelID, req.VoiceChannelID, req.UserID, s.messageID, manage)
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
// message into a summary.
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
	if !s.isParticipant(userID) {
		m.mu.Unlock()
		return LeaveResult{}, ErrNotParticipant
	}
	s.participants = slices.DeleteFunc(s.participants, func(id string) bool { return id == userID })
	s.left = append(s.left, userID)
	n := len(s.participants)
	view := s.view()
	m.mu.Unlock()

	log.Printf("tts: session leave guild=%s user=%s participants=%d", guildID, userID, n)
	if n == 0 {
		return LeaveResult{Ended: true}, m.endLocked(s, "last participant left", true)
	}
	m.editSession(s, sessionEmbed(view))
	return LeaveResult{Participants: n}, nil
}

// Bound returns the text and voice channel of guildID's session, if any.
func (m *Manager) Bound(guildID string) (textChannelID, voiceChannelID string, ok bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.sessions[guildID]
	if s == nil {
		return "", "", false
	}
	return s.textChannelID, s.voiceChannelID, true
}

// Participants returns the users in guildID's session, in join order.
func (m *Manager) Participants(guildID string) []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s := m.sessions[guildID]; s != nil {
		return slices.Clone(s.participants)
	}
	return nil
}

// HandleMessage queues msg for speech if it was sent in a session's text
// channel by a participant and survives filtering. It reports whether
// something was queued. Messages of everyone who has been in the session are
// remembered for the clean-up, whether or not they are spoken; other people's
// messages are not touched.
//
// A participant's message is also deleted DeleteAfter after the player has
// finished it, or, when it is not going to be spoken (nothing left to say
// after filtering, queue full, synthesis failed, monthly budget reached),
// after the same delay from now, so the channel stays clean either way.
func (m *Manager) HandleMessage(msg Message, names Names) bool {
	m.mu.Lock()
	s := m.sessions[msg.GuildID]
	if s == nil || s.textChannelID != msg.ChannelID || msg.IsBot || msg.AuthorID == "" {
		m.mu.Unlock()
		return false
	}
	if msg.MessageID != "" && s.hasTaken(msg.AuthorID) {
		at := msg.Time
		if at.IsZero() {
			at = m.cfg.Now()
		}
		s.tracked[msg.MessageID] = at
	}
	speak := s.isParticipant(msg.AuthorID)
	player := s.player
	m.mu.Unlock()

	if !speak {
		return false
	}
	if !ShouldSpeak(msg) {
		m.scheduleDelete(s, msg.MessageID)
		return false
	}
	text := Normalize(msg.Content, TextOptions{
		MaxChars: m.cfg.MaxChars,
		LinkWord: m.linkWord,
		Names:    names,
	})
	if text == "" {
		m.scheduleDelete(s, msg.MessageID)
		return false
	}
	// Spoken, dropped from a full queue or failed: in every case the player is
	// done with the message, which is when its delay starts. A message cut off
	// by the end of the session is left to the end-of-session clean-up.
	player.EnqueueWith(Utterance(msg.AuthorName, text, m.cfg.SpeakAuthor), func(bool) {
		m.scheduleDelete(s, msg.MessageID)
	})
	return true
}

// scheduleDelete deletes message id after Config.DeleteAfter, in the
// background. It does nothing when per-message deletion is off, the bot could
// not delete messages when the session started, or the session is over (the
// end-of-session clean-up takes what is left).
func (m *Manager) scheduleDelete(s *session, id string) {
	if m.cfg.DeleteAfter <= 0 || !s.manage || id == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if s.ended || s.pending[id] != nil {
		return
	}
	// Holding mu until the timer is stored means a fired callback, which
	// takes mu first, always finds it.
	s.pending[id] = m.cfg.AfterFunc(m.cfg.DeleteAfter, func() { m.deleteSpoken(s, id) })
}

// deleteSpoken is the timer callback of scheduleDelete: one single delete,
// never blocking speech (timers run on their own goroutine). On success the
// message leaves the tracked set and counts towards the summary. On failure
// it is logged once and stays tracked, so the end-of-session clean-up retries.
func (m *Manager) deleteSpoken(s *session, id string) {
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
	err := m.cfg.Messenger.DeleteMessage(ctx, s.textChannelID, id)
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
	guildID   string
	messageID string
	view      sessionView
	everyone  []string
	tracked   map[string]time.Time
	joinedAt  map[string]time.Time // missing users count from the session start
	ended     time.Time
	reason    string
	// live is the session's per-message deletions; nil for a recovered one.
	live *liveDeletes
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
		guildID:   s.guildID,
		messageID: s.messageID,
		view:      s.view(),
		everyone:  s.everyone(),
		tracked:   maps.Clone(s.tracked),
		joinedAt:  maps.Clone(s.joinedAt),
		ended:     m.cfg.Now(),
		reason:    reason,
		live:      s.live,
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

// finish deletes the participants' messages and turns the session message
// into the summary that stays as the log.
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
		m.mu.Unlock()
	}
	// The participants were told whether their messages would be deleted when
	// the session started; keep that promise, and never delete without the
	// permission now.
	if !e.view.Manage || !msgr.CanManageMessages(e.view.TextChannelID) {
		res.NoPermission = true
	} else {
		set := make(map[string]time.Time, len(e.everyone))
		for _, id := range e.everyone {
			set[id] = e.joinedAt[id] // zero: from the session start
		}
		res = cleanupMessages(ctx, msgr, cleanupJob{
			ChannelID:        e.view.TextChannelID,
			SessionMessageID: e.messageID,
			Started:          e.view.Started,
			Ended:            e.ended,
			Participants:     set,
			Tracked:          e.tracked,
		}, m.cfg.Now())
	}

	res.Deleted += live

	// Cut short by shutdown: leave the message open so the next start
	// finishes the job instead of recording a summary that hides the rest.
	if m.bg.Err() != nil {
		log.Printf("tts: session end guild=%s text=%s: clean-up interrupted by shutdown (deleted=%d failed=%d); it is finished on the next start",
			e.guildID, e.view.TextChannelID, res.Deleted, res.Failed)
		return
	}

	duration := e.ended.Sub(e.view.Started)
	embed := summaryEmbed(e.view, summary{Participants: e.everyone, Duration: duration, Cleanup: res}, e.ended)
	// The clean-up may have used up the context; the summary is what marks
	// the session finished, so it gets its own.
	editCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), editTimeout)
	defer cancel()
	if err := m.cfg.Messenger.Edit(editCtx, e.view.TextChannelID, e.messageID, embed); err != nil {
		log.Printf("tts: session summary guild=%s message=%s: %v", e.guildID, e.messageID, err)
	}
	log.Printf("tts: session end guild=%s text=%s reason=%q users=%s duration=%s deleted=%d failed=%d no_permission=%t",
		e.guildID, e.view.TextChannelID, e.reason, strings.Join(e.everyone, ","),
		duration.Round(time.Second), res.Deleted, res.Failed, res.NoPermission)
}

// editSession replaces the session message with embed. Failures are logged:
// the session keeps working without a current message. joinMu is held, which
// keeps edits in order.
func (m *Manager) editSession(s *session, embed *discordgo.MessageEmbed) {
	ctx, cancel := context.WithTimeout(m.bg, editTimeout)
	defer cancel()
	if err := m.cfg.Messenger.Edit(ctx, s.textChannelID, s.messageID, embed); err != nil {
		log.Printf("tts: guild %s: editing the session message %s: %v", s.guildID, s.messageID, err)
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
// run of the bot left unfinished: it finds the bot's own session messages
// that still carry an "open" marker, deletes the participants' messages the
// marker names and turns the message into a summary. Call it once per guild
// at startup; it does nothing for a session that is live in this process.
func (m *Manager) RecoverGuild(ctx context.Context, guildID string) {
	open, err := m.cfg.Messenger.OpenSessions(ctx, guildID)
	if err != nil {
		log.Printf("tts: guild %s: looking for unfinished sessions: %v", guildID, err)
		return
	}
	for _, o := range open {
		m.mu.Lock()
		skip := m.recovered[o.MessageID]
		m.recovered[o.MessageID] = true
		// A session started since the restart in the same channel owns
		// everything from its own start on.
		ended := m.cfg.Now()
		if live := m.sessions[guildID]; live != nil {
			if live.messageID == o.MessageID {
				skip = true
			}
			if live.textChannelID == o.ChannelID && live.started.Before(ended) {
				ended = live.started
			}
		}
		m.mu.Unlock()
		if skip {
			continue
		}

		// Discord's creation time of the session message, not the host
		// clock in the marker, so clock skew cannot skip early messages.
		started := o.Time
		if started.IsZero() {
			started = o.Marker.Started
		}
		log.Printf("tts: recovering unfinished session guild=%s text=%s message=%s users=%s",
			guildID, o.ChannelID, o.MessageID, strings.Join(o.Marker.All(), ","))
		m.goFinish(endedSession{
			guildID:   guildID,
			messageID: o.MessageID,
			view: sessionView{
				TextChannelID:  o.ChannelID,
				VoiceChannelID: o.Marker.Voice,
				Started:        started,
				Manage:         o.Marker.Manage,
				Participants:   o.Marker.Participants,
				Left:           o.Marker.Left,
			},
			everyone: o.Marker.All(),
			tracked:  map[string]time.Time{},
			ended:    ended,
			reason:   "recovered after restart",
		})
	}
}
