package voice

import (
	"context"

	"github.com/bwmarrin/discordgo"
)

// DiscordJoiner joins voice channels through a discordgo session.
//
// WARNING: discordgo v0.29.0 (the newest release) only implements the
// xsalsa20_poly1305 transport encryption, which Discord has retired, and
// does not implement DAVE end-to-end encryption, which Discord requires for
// voice since March 2026. Joining may succeed, but audio sent through this
// adapter is not expected to be heard. See README, "Read chat aloud".
type DiscordJoiner struct {
	Session *discordgo.Session
}

// Join connects to a voice channel. The bot joins self-deafened because it
// never listens.
func (j DiscordJoiner) Join(ctx context.Context, guildID, channelID string) (Conn, error) {
	// ChannelVoiceJoin has its own internal timeout and does not take a
	// context; ctx is accepted for interface symmetry.
	vc, err := j.Session.ChannelVoiceJoin(guildID, channelID, false, true)
	if err != nil {
		return nil, err
	}
	return discordConn{vc: vc}, nil
}

type discordConn struct {
	vc *discordgo.VoiceConnection
}

func (c discordConn) Speaking(speaking bool) error {
	return c.vc.Speaking(speaking)
}

func (c discordConn) SendOpus(ctx context.Context, frame []byte) error {
	select {
	case c.vc.OpusSend <- frame:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (c discordConn) Disconnect() error {
	return c.vc.Disconnect()
}
