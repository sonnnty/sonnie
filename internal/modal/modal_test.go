package modal

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/bwmarrin/discordgo"
)

func toMap(t *testing.T, v any) map[string]any {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestResponseJSON(t *testing.T) {
	resp := Response("x:form", "Title",
		Label{Label: "Name", Description: "d", Component: TextInput{CustomID: "name", Style: discordgo.TextInputShort, Required: true, MaxLength: 100}},
		Label{Label: "Notes", Component: TextInput{CustomID: "notes", Style: discordgo.TextInputParagraph, Value: "v", Placeholder: "p"}},
		Label{Label: "Day", Component: StringSelect{CustomID: "day", Required: true, Options: []Option{{Label: "A", Value: "a", Default: true}, {Label: "B", Value: "b"}}}},
		Label{Label: "Room", Component: ChannelSelect{CustomID: "room", ChannelTypes: []discordgo.ChannelType{discordgo.ChannelTypeGuildVoice}, Default: "42"}},
		Label{Label: "Empty", Component: ChannelSelect{CustomID: "room2"}},
	)
	m := toMap(t, resp)
	if m["type"] != float64(9) {
		t.Fatalf("response type %v", m["type"])
	}
	data := m["data"].(map[string]any)
	if data["custom_id"] != "x:form" || data["title"] != "Title" {
		t.Fatalf("data %v", data)
	}
	comps := data["components"].([]any)
	want := []map[string]any{
		{"type": 18.0, "label": "Name", "description": "d", "component": map[string]any{
			"type": 4.0, "custom_id": "name", "style": 1.0, "required": true, "max_length": 100.0}},
		{"type": 18.0, "label": "Notes", "component": map[string]any{
			"type": 4.0, "custom_id": "notes", "style": 2.0, "required": false, "value": "v", "placeholder": "p"}},
		{"type": 18.0, "label": "Day", "component": map[string]any{
			"type": 3.0, "custom_id": "day", "min_values": 1.0, "max_values": 1.0, "required": true,
			"options": []any{map[string]any{"label": "A", "value": "a", "default": true}, map[string]any{"label": "B", "value": "b"}}}},
		{"type": 18.0, "label": "Room", "component": map[string]any{
			"type": 8.0, "custom_id": "room", "min_values": 0.0, "max_values": 1.0, "required": false,
			"channel_types": []any{2.0}, "default_values": []any{map[string]any{"id": "42", "type": "channel"}}}},
		{"type": 18.0, "label": "Empty", "component": map[string]any{
			"type": 8.0, "custom_id": "room2", "min_values": 0.0, "max_values": 1.0, "required": false}},
	}
	if len(comps) != len(want) {
		t.Fatalf("%d components", len(comps))
	}
	for i := range want {
		if !reflect.DeepEqual(comps[i], any(want[i])) {
			t.Errorf("component %d:\n got %v\nwant %v", i, comps[i], want[i])
		}
	}
}

// A modal submit as Discord sends it: Labels around the inputs, plus an
// action row as older modals use.
const submitJSON = `{
  "id": "111", "application_id": "222", "type": 5, "token": "tok", "guild_id": "333", "channel_id": "444",
  "member": {"user": {"id": "42", "username": "a"}, "permissions": "8589934592"},
  "data": {
    "custom_id": "event:form:1",
    "components": [
      {"type": 18, "id": 1, "component": {"type": 4, "id": 2, "custom_id": "name", "value": " เตะบอล "}},
      {"type": 18, "id": 3, "component": {"type": 3, "id": 4, "custom_id": "day", "values": ["2026-11-06"]}},
      {"type": 18, "id": 5, "component": {"type": 8, "id": 6, "custom_id": "place", "values": []}},
      {"type": 18, "id": 7, "component": {"type": 4, "id": 8, "custom_id": "description", "value": ""}},
      {"type": 1, "components": [{"type": 4, "custom_id": "old", "value": "row"}]}
    ],
    "resolved": {}
  }
}`

func TestDecode(t *testing.T) {
	// The reason Decode exists: discordgo cannot read this payload.
	var dg discordgo.InteractionCreate
	if err := json.Unmarshal([]byte(submitJSON), &dg); err == nil {
		t.Log("discordgo now decodes Labels; Decode is still correct but may no longer be needed")
	}

	i, s, ok, err := Decode([]byte(submitJSON))
	if err != nil || !ok {
		t.Fatalf("ok %v err %v", ok, err)
	}
	if i.ID != "111" || i.Token != "tok" || i.AppID != "222" || i.GuildID != "333" || i.Type != discordgo.InteractionModalSubmit {
		t.Fatalf("interaction %+v", i.Interaction)
	}
	if i.Member == nil || i.Member.User.ID != "42" || i.Member.Permissions != 8589934592 {
		t.Fatalf("member %+v", i.Member)
	}
	if i.ModalSubmitData().CustomID != "event:form:1" || s.CustomID != "event:form:1" {
		t.Fatalf("custom ID %q %q", i.ModalSubmitData().CustomID, s.CustomID)
	}
	want := map[string][]string{"name": {" เตะบอล "}, "day": {"2026-11-06"}, "place": {}, "description": {""}, "old": {"row"}}
	if !reflect.DeepEqual(s.Values, want) {
		t.Fatalf("values %v", s.Values)
	}
	if s.Value("name") != "เตะบอล" || s.Value("place") != "" || s.Value("missing") != "" {
		t.Fatal("Value")
	}
}

func TestDecode_NotAModal(t *testing.T) {
	_, _, ok, err := Decode([]byte(`{"type": 2, "data": {"name": "event"}}`))
	if ok || err != nil {
		t.Fatalf("command: ok %v err %v", ok, err)
	}
	if _, _, ok, err := Decode([]byte(`{`)); ok || err == nil {
		t.Fatalf("broken JSON: ok %v err %v", ok, err)
	}
}
