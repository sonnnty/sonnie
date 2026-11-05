package readaloud

import (
	"slices"
	"strings"
	"time"

	"github.com/Sonty15/SONNIE/internal/voice"
)

// SessionStatus is a read-only view of one guild's session, for the
// dashboard.
type SessionStatus struct {
	GuildID        string
	VoiceChannelID string
	// TextChannels have a session card, in the order they were posted.
	TextChannels []string
	// Participants are in join order.
	Participants []ParticipantStatus
	Started      time.Time
	// Queue is how many utterances wait to be spoken (not counting the one
	// being spoken).
	Queue int
	// Read is how many messages were read aloud to the end; Deleted how many
	// were deleted after being read (TTS_DELETE_AFTER), this session.
	Read    int
	Deleted int
}

// ParticipantStatus is one participant: where they are read and in which
// voice.
type ParticipantStatus struct {
	UserID    string
	ChannelID string
	Voice     string
}

// Status returns every active session, ordered by guild ID.
func (m *Manager) Status() []SessionStatus {
	m.mu.Lock()
	out := make([]SessionStatus, 0, len(m.sessions))
	players := make([]*voice.Player, 0, len(m.sessions))
	for _, s := range m.sessions {
		st := SessionStatus{
			GuildID:        s.guildID,
			VoiceChannelID: s.voiceChannelID,
			Started:        s.started,
			Read:           s.spoken,
			Deleted:        s.live.deleted,
		}
		for _, c := range s.view.Channels {
			st.TextChannels = append(st.TextChannels, c.ID)
		}
		for _, mb := range s.view.Members {
			st.Participants = append(st.Participants, ParticipantStatus{UserID: mb.User, ChannelID: mb.Channel, Voice: s.view.Voices[mb.User]})
		}
		out = append(out, st)
		players = append(players, s.player)
	}
	m.mu.Unlock()

	// The queue has its own lock; read it without holding mu.
	for i, p := range players {
		if p != nil {
			out[i].Queue = p.Pending()
		}
	}
	slices.SortFunc(out, func(a, b SessionStatus) int { return strings.Compare(a.GuildID, b.GuildID) })
	return out
}
