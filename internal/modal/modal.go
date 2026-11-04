// Package modal builds Discord modals in the current component model, where
// every field is a Label (type 18) wrapping one input: a text input, a
// string select or a channel select. discordgo v0.29 knows neither Label
// nor select menus in modals, so the components here marshal to Discord's
// JSON themselves, and Decode reads a modal submit from the raw gateway
// event (discordgo fails to unmarshal one that contains Labels).
package modal

import (
	"encoding/json"

	"github.com/bwmarrin/discordgo"
)

// LabelComponent is Discord's Label component type.
const LabelComponent discordgo.ComponentType = 18

// Response is the interaction response that opens a modal.
func Response(customID, title string, fields ...Label) *discordgo.InteractionResponse {
	comps := make([]discordgo.MessageComponent, len(fields))
	for i, f := range fields {
		comps[i] = f
	}
	return &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseModal,
		Data: &discordgo.InteractionResponseData{CustomID: customID, Title: title, Components: comps},
	}
}

// Label is one field of a modal: a title (max 45 characters), an optional
// description (max 100) and the input.
type Label struct {
	Label       string
	Description string
	Component   discordgo.MessageComponent
}

func (Label) Type() discordgo.ComponentType { return LabelComponent }

func (l Label) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Type        discordgo.ComponentType    `json:"type"`
		Label       string                     `json:"label"`
		Description string                     `json:"description,omitempty"`
		Component   discordgo.MessageComponent `json:"component"`
	}{LabelComponent, l.Label, l.Description, l.Component})
}

// TextInput is a text field. Unlike discordgo.TextInput it has no label
// (the Label carries it; the input's own label is deprecated) and always
// sends required, which Discord defaults to true.
type TextInput struct {
	CustomID    string
	Style       discordgo.TextInputStyle
	Placeholder string
	Value       string
	Required    bool
	MaxLength   int
}

func (TextInput) Type() discordgo.ComponentType { return discordgo.TextInputComponent }

func (t TextInput) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Type        discordgo.ComponentType  `json:"type"`
		CustomID    string                   `json:"custom_id"`
		Style       discordgo.TextInputStyle `json:"style"`
		Placeholder string                   `json:"placeholder,omitempty"`
		Value       string                   `json:"value,omitempty"`
		Required    bool                     `json:"required"`
		MaxLength   int                      `json:"max_length,omitempty"`
	}{discordgo.TextInputComponent, t.CustomID, t.Style, t.Placeholder, t.Value, t.Required, t.MaxLength})
}

// Option is a choice of a StringSelect.
type Option struct {
	Label       string `json:"label"`
	Value       string `json:"value"`
	Description string `json:"description,omitempty"`
	Default     bool   `json:"default,omitempty"`
}

// StringSelect picks one of up to 25 options.
type StringSelect struct {
	CustomID    string
	Placeholder string
	Options     []Option
	Required    bool
}

func (StringSelect) Type() discordgo.ComponentType { return discordgo.SelectMenuComponent }

func (s StringSelect) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		selectFields
		Options []Option `json:"options"`
	}{newSelectFields(discordgo.SelectMenuComponent, s.CustomID, s.Placeholder, s.Required), s.Options})
}

// ChannelSelect picks one channel of the given types. Default preselects
// a channel; "" leaves it empty.
type ChannelSelect struct {
	CustomID     string
	Placeholder  string
	ChannelTypes []discordgo.ChannelType
	Default      string
	Required     bool
}

func (ChannelSelect) Type() discordgo.ComponentType { return discordgo.ChannelSelectMenuComponent }

func (c ChannelSelect) MarshalJSON() ([]byte, error) {
	var defaults []discordgo.SelectMenuDefaultValue
	if c.Default != "" {
		defaults = []discordgo.SelectMenuDefaultValue{{ID: c.Default, Type: discordgo.SelectMenuDefaultValueChannel}}
	}
	return json.Marshal(struct {
		selectFields
		ChannelTypes  []discordgo.ChannelType            `json:"channel_types,omitempty"`
		DefaultValues []discordgo.SelectMenuDefaultValue `json:"default_values,omitempty"`
	}{newSelectFields(discordgo.ChannelSelectMenuComponent, c.CustomID, c.Placeholder, c.Required), c.ChannelTypes, defaults})
}

// selectFields are common to single-choice selects. An optional select
// needs min_values 0: Discord refuses min_values 0 on a required one and
// defaults it to 1.
type selectFields struct {
	Type        discordgo.ComponentType `json:"type"`
	CustomID    string                  `json:"custom_id"`
	Placeholder string                  `json:"placeholder,omitempty"`
	MinValues   int                     `json:"min_values"`
	MaxValues   int                     `json:"max_values"`
	Required    bool                    `json:"required"`
}

func newSelectFields(t discordgo.ComponentType, customID, placeholder string, required bool) selectFields {
	f := selectFields{Type: t, CustomID: customID, Placeholder: placeholder, MinValues: 1, MaxValues: 1, Required: required}
	if !required {
		f.MinValues = 0
	}
	return f
}
