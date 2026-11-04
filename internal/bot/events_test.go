package bot

import (
	"testing"

	"github.com/bwmarrin/discordgo"

	"github.com/Sonty15/SONNIE/internal/commands"
	"github.com/Sonty15/SONNIE/internal/modal"
)

func TestVoiceChannelOf(t *testing.T) {
	state := discordgo.NewState()
	if err := state.GuildAdd(&discordgo.Guild{
		ID: "g1",
		Channels: []*discordgo.Channel{
			{ID: "100", GuildID: "g1", Type: discordgo.ChannelTypeGuildVoice},
			{ID: "200", GuildID: "g1", Type: discordgo.ChannelTypeGuildStageVoice},
		},
		VoiceStates: []*discordgo.VoiceState{
			{GuildID: "g1", UserID: "in-voice", ChannelID: "100"},
			{GuildID: "g1", UserID: "on-stage", ChannelID: "200"},
			{GuildID: "g1", UserID: "unknown-channel", ChannelID: "300"},
		},
	}); err != nil {
		t.Fatal(err)
	}
	for user, want := range map[string]string{"in-voice": "100", "on-stage": "", "unknown-channel": "", "elsewhere": ""} {
		if got := voiceChannelOf(state, "g1", user); got != want {
			t.Errorf("%s: %q, want %q", user, got, want)
		}
	}
	if got := voiceChannelOf(state, "other-guild", "in-voice"); got != "" {
		t.Errorf("other guild: %q", got)
	}
}

type fakeEventHandler struct {
	submits []modal.Submit
	users   []string
}

func (*fakeEventHandler) HandleCommand(*discordgo.InteractionCreate)      {}
func (*fakeEventHandler) HandleAutocomplete(*discordgo.InteractionCreate) {}
func (*fakeEventHandler) HandleComponent(*discordgo.InteractionCreate)    {}
func (f *fakeEventHandler) HandleModalSubmit(i *discordgo.InteractionCreate, m modal.Submit) {
	f.submits = append(f.submits, m)
	f.users = append(f.users, i.Member.User.ID)
}

func TestHandleModalSubmit_RoutesRawEvents(t *testing.T) {
	fake := &fakeEventHandler{}
	commands.SetEvents(fake)
	t.Cleanup(func() { commands.SetEvents(nil) })

	submit := `{"type": 5, "id": "1", "token": "t", "guild_id": "g1", "member": {"user": {"id": "42"}, "permissions": "0"},
	  "data": {"custom_id": "event:form:1", "components": [{"type": 18, "component": {"type": 4, "custom_id": "name", "value": "x"}}]}}`
	events := []*discordgo.Event{
		{Type: "INTERACTION_CREATE", RawData: []byte(submit)},
		{Type: "MESSAGE_CREATE", RawData: []byte(submit)},                                       // not an interaction
		{Type: "INTERACTION_CREATE", RawData: []byte(`{"type": 2, "data": {"name": "event"}}`)}, // a command
		{Type: "INTERACTION_CREATE", RawData: []byte(`{"type": 5, "data": {"custom_id": "other:1", "components": []}}`)},
		{Type: "INTERACTION_CREATE", RawData: []byte(`{"type": 5, "data": `)}, // broken
	}
	for _, e := range events {
		handleModalSubmit(nil, e)
	}
	if len(fake.submits) != 1 || fake.submits[0].Value("name") != "x" || fake.users[0] != "42" {
		t.Fatalf("submits %+v users %v", fake.submits, fake.users)
	}
}
