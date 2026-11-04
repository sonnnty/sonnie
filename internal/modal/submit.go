package modal

import (
	"encoding/json"
	"strings"

	"github.com/bwmarrin/discordgo"
)

// Submit is what a modal sent back: its custom ID and the values of its
// inputs by custom ID. A text input has one value (possibly ""); a select
// has the picked values, none when left empty.
type Submit struct {
	CustomID string
	Values   map[string][]string
}

// Value is the first value of the input customID, trimmed; "" when the
// input is missing or empty.
func (s Submit) Value(customID string) string {
	if v := s.Values[customID]; len(v) > 0 {
		return strings.TrimSpace(v[0])
	}
	return ""
}

// submitComponent is any component of a modal submit: a Label wraps one
// component, an action row (the older modal layout) several; inputs have
// a custom ID and a value or values.
type submitComponent struct {
	Type       discordgo.ComponentType `json:"type"`
	CustomID   string                  `json:"custom_id"`
	Value      *string                 `json:"value"`
	Values     []string                `json:"values"`
	Component  *submitComponent        `json:"component"`
	Components []submitComponent       `json:"components"`
}

func (c submitComponent) collect(into map[string][]string) {
	if c.Component != nil {
		c.Component.collect(into)
	}
	for _, child := range c.Components {
		child.collect(into)
	}
	switch {
	case c.CustomID == "":
	case c.Value != nil:
		into[c.CustomID] = []string{*c.Value}
	case c.Type != LabelComponent && c.Type != discordgo.ActionsRowComponent:
		into[c.CustomID] = append([]string{}, c.Values...)
	}
}

// Decode reads an INTERACTION_CREATE gateway payload. ok is false when it
// is not a modal submit. The returned interaction's Data holds only the
// custom ID; the values are in the Submit.
func Decode(raw []byte) (i *discordgo.InteractionCreate, s Submit, ok bool, err error) {
	// plain drops Interaction's UnmarshalJSON, which fails on Labels; the
	// outer Data shadows the interface-typed one.
	type plain discordgo.Interaction
	var v struct {
		plain
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, Submit{}, false, err
	}
	if v.Type != discordgo.InteractionModalSubmit {
		return nil, Submit{}, false, nil
	}
	var data struct {
		CustomID   string            `json:"custom_id"`
		Components []submitComponent `json:"components"`
	}
	if err := json.Unmarshal(v.Data, &data); err != nil {
		return nil, Submit{}, false, err
	}
	s = Submit{CustomID: data.CustomID, Values: map[string][]string{}}
	for _, c := range data.Components {
		c.collect(s.Values)
	}
	in := discordgo.Interaction(v.plain)
	in.Data = discordgo.ModalSubmitInteractionData{CustomID: data.CustomID}
	return &discordgo.InteractionCreate{Interaction: &in}, s, true, nil
}
