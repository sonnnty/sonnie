package voice

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/gateway"
	dvoice "github.com/disgoorg/disgo/voice"
	"github.com/disgoorg/snowflake/v2"
	davesession "github.com/thomas-vilte/dave-go/session"
)

var (
	// ErrConnClosed is returned when sending on a disconnected Conn.
	ErrConnClosed = errors.New("voice: connection closed")
	// ErrE2EENotReady is returned when the DAVE end-to-end encryption
	// handshake does not finish in time.
	ErrE2EENotReady = errors.New("voice: end-to-end encryption not ready")
	// errNotConnected is returned by Join before the session has a user.
	errNotConnected = errors.New("voice: discord session not connected")
)

const (
	frameDuration = 20 * time.Millisecond
	// readyTimeout bounds how long a frame waits for the DAVE handshake,
	// which runs right after joining and whenever someone joins or leaves.
	readyTimeout = 10 * time.Second
	// maxLag is how far behind schedule sending may fall before the pacing
	// clock restarts instead of bursting frames to catch up.
	maxLag = 3 * frameDuration
	// silenceFrames is how many silence frames end each utterance, so the
	// listener's Opus decoder does not interpolate past the last frame.
	silenceFrames = 5

	speakingTimeout = 5 * time.Second
	closeTimeout    = 5 * time.Second
)

// DAVEJoiner joins voice channels with disgo's voice client: voice gateway
// v8, AEAD transport encryption and DAVE end-to-end encryption through
// dave-go, all without cgo. The main gateway stays discordgo's: the joiner
// sends voice state updates (op 4) through the session and feeds the
// session's voice events to disgo. See docs/voice-transport.md.
//
// Create it with NewDAVEJoiner before the session is opened.
type DAVEJoiner struct {
	session *discordgo.Session

	mu sync.Mutex
	// manager is created on the first Join, because it needs the bot's
	// user ID, which is only known once the session is open.
	manager dvoice.Manager
}

// NewDAVEJoiner returns a DAVEJoiner and registers its voice event
// handlers on s.
func NewDAVEJoiner(s *discordgo.Session) *DAVEJoiner {
	j := &DAVEJoiner{session: s}
	s.AddHandler(j.onVoiceStateUpdate)
	s.AddHandler(j.onVoiceServerUpdate)
	return j
}

// Join connects to a voice channel and returns once the transport is set
// up; the DAVE handshake may still be running, which SendOpus waits for.
// The bot joins self-deafened because it never listens.
func (j *DAVEJoiner) Join(ctx context.Context, guildID, channelID string) (Conn, error) {
	gID, err := snowflake.Parse(guildID)
	if err != nil {
		return nil, fmt.Errorf("voice: guild ID: %w", err)
	}
	cID, err := snowflake.Parse(channelID)
	if err != nil {
		return nil, fmt.Errorf("voice: channel ID: %w", err)
	}
	m, err := j.getManager()
	if err != nil {
		return nil, err
	}

	vc := m.CreateConn(gID)
	if err := vc.Open(ctx, cID, false, true); err != nil {
		// Leave again and drop the half-open conn so the next Join starts
		// fresh.
		closeCtx, cancel := context.WithTimeout(context.Background(), closeTimeout)
		defer cancel()
		vc.Close(closeCtx)
		return nil, fmt.Errorf("voice: join: %w", err)
	}
	return newDAVEConn(vc), nil
}

func (j *DAVEJoiner) getManager() (dvoice.Manager, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.manager != nil {
		return j.manager, nil
	}
	if j.session.State == nil || j.session.State.User == nil {
		return nil, errNotConnected
	}
	userID, err := snowflake.Parse(j.session.State.User.ID)
	if err != nil {
		return nil, fmt.Errorf("voice: user ID: %w", err)
	}
	j.manager = dvoice.NewManager(j.updateVoiceState, userID,
		dvoice.WithDaveSessionCreateFunc(davesession.CreateFunc()),
	)
	return j.manager, nil
}

func (j *DAVEJoiner) currentManager() dvoice.Manager {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.manager
}

// updateVoiceState sends op 4 on the main gateway; a nil channelID leaves.
func (j *DAVEJoiner) updateVoiceState(_ context.Context, guildID snowflake.ID, channelID *snowflake.ID, selfMute, selfDeaf bool) error {
	cID := ""
	if channelID != nil {
		cID = channelID.String()
	}
	return j.session.ChannelVoiceJoinManual(guildID.String(), cID, selfMute, selfDeaf)
}

func (j *DAVEJoiner) onVoiceStateUpdate(_ *discordgo.Session, v *discordgo.VoiceStateUpdate) {
	m := j.currentManager()
	if m == nil || v.VoiceState == nil {
		return
	}
	if ev, ok := voiceStateEvent(v.VoiceState); ok {
		m.HandleVoiceStateUpdate(ev)
	}
}

func (j *DAVEJoiner) onVoiceServerUpdate(_ *discordgo.Session, v *discordgo.VoiceServerUpdate) {
	m := j.currentManager()
	if m == nil {
		return
	}
	if ev, ok := voiceServerEvent(v); ok {
		m.HandleVoiceServerUpdate(ev)
	}
}

// voiceStateEvent converts a discordgo voice state to the disgo event. It
// fills only the fields disgo's voice client reads.
func voiceStateEvent(vs *discordgo.VoiceState) (gateway.EventVoiceStateUpdate, bool) {
	guildID, err := snowflake.Parse(vs.GuildID)
	if err != nil {
		return gateway.EventVoiceStateUpdate{}, false
	}
	userID, err := snowflake.Parse(vs.UserID)
	if err != nil {
		return gateway.EventVoiceStateUpdate{}, false
	}
	ev := gateway.EventVoiceStateUpdate{VoiceState: discord.VoiceState{
		GuildID:   guildID,
		UserID:    userID,
		SessionID: vs.SessionID,
		SelfMute:  vs.SelfMute,
		SelfDeaf:  vs.SelfDeaf,
	}}
	// discordgo turns a null channel (left voice) into "".
	if vs.ChannelID != "" {
		channelID, err := snowflake.Parse(vs.ChannelID)
		if err != nil {
			return gateway.EventVoiceStateUpdate{}, false
		}
		ev.ChannelID = &channelID
	}
	return ev, true
}

// voiceServerEvent converts a discordgo voice server update to the disgo
// event.
func voiceServerEvent(v *discordgo.VoiceServerUpdate) (gateway.EventVoiceServerUpdate, bool) {
	guildID, err := snowflake.Parse(v.GuildID)
	if err != nil {
		return gateway.EventVoiceServerUpdate{}, false
	}
	ev := gateway.EventVoiceServerUpdate{Token: v.Token, GuildID: guildID}
	// A null endpoint means the voice server went away; disgo ignores it.
	if v.Endpoint != "" {
		endpoint := v.Endpoint
		ev.Endpoint = &endpoint
	}
	return ev, true
}

// daveConn adapts a disgo voice connection to Conn. It paces frames itself
// instead of using disgo's AudioSender: that sender runs a goroutine that
// is not stopped when the connection closes while idle.
//
// SendOpus and Speaking must not be called concurrently, which Player
// never does; Disconnect may be called at any time.
type daveConn struct {
	vc           dvoice.Conn
	readyTimeout time.Duration

	// next is when the next frame is due.
	next time.Time
	// talking is set once a frame has been sent since the last
	// Speaking(false), so trailing silence is only sent after audio.
	talking bool

	closed    chan struct{}
	closeOnce sync.Once
}

func newDAVEConn(vc dvoice.Conn) *daveConn {
	return &daveConn{
		vc:           vc,
		readyTimeout: readyTimeout,
		closed:       make(chan struct{}),
	}
}

func (c *daveConn) Speaking(speaking bool) error {
	ctx, cancel := context.WithTimeout(context.Background(), speakingTimeout)
	defer cancel()
	if speaking {
		return c.vc.SetSpeaking(ctx, dvoice.SpeakingFlagMicrophone)
	}

	var silenceErr error
	if c.talking {
		c.talking = false
		for range silenceFrames {
			if silenceErr = c.send(ctx, dvoice.SilenceAudioFrame); silenceErr != nil {
				break
			}
		}
	}
	// Clear the indicator even if the silence could not be sent.
	return errors.Join(silenceErr, c.vc.SetSpeaking(ctx, dvoice.SpeakingFlagNone))
}

// SendOpus waits for the frame's 20 ms slot and for the DAVE session to be
// ready, then encrypts and sends it.
func (c *daveConn) SendOpus(ctx context.Context, frame []byte) error {
	if err := c.send(ctx, frame); err != nil {
		return err
	}
	c.talking = true
	return nil
}

func (c *daveConn) send(ctx context.Context, frame []byte) error {
	if err := c.waitReady(ctx); err != nil {
		return err
	}
	if err := c.waitSlot(ctx); err != nil {
		return err
	}
	if _, err := c.vc.UDP().Write(frame); err != nil {
		return fmt.Errorf("voice: write: %w", err)
	}
	return nil
}

// waitReady blocks until the DAVE session can encrypt. Frames sent before
// that would go out unencrypted and be dropped by every listener.
func (c *daveConn) waitReady(ctx context.Context) error {
	select {
	case <-c.closed:
		return ErrConnClosed
	default:
	}
	if c.vc.DAVE().Ready() {
		return nil
	}

	deadline := time.NewTimer(c.readyTimeout)
	defer deadline.Stop()
	poll := time.NewTicker(frameDuration)
	defer poll.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-c.closed:
			return ErrConnClosed
		case <-deadline.C:
			return ErrE2EENotReady
		case <-poll.C:
			if c.vc.DAVE().Ready() {
				return nil
			}
		}
	}
}

// waitSlot blocks until the next frame is due, keeping a steady 20 ms
// rhythm across calls.
func (c *daveConn) waitSlot(ctx context.Context) error {
	now := time.Now()
	if c.next.Before(now.Add(-maxLag)) {
		c.next = now
	}
	wait := c.next.Sub(now)
	c.next = c.next.Add(frameDuration)
	if wait <= 0 {
		return nil
	}

	t := time.NewTimer(wait)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-c.closed:
		return ErrConnClosed
	}
}

// Disconnect leaves the voice channel and releases the connection, including
// its DAVE session. It is safe to call more than once.
func (c *daveConn) Disconnect() error {
	c.closeOnce.Do(func() {
		close(c.closed)
		ctx, cancel := context.WithTimeout(context.Background(), closeTimeout)
		defer cancel()
		c.vc.Close(ctx)
	})
	return nil
}
