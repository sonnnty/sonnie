// Package events manages the guild's Discord Scheduled Events for /event:
// creating and editing them with a cover found on the internet, reminding
// interested users, and starting and ending them on time. It never joins
// voice or reads chat.
package events

import (
	"fmt"

	"github.com/bwmarrin/discordgo"
)

// API is the part of *discordgo.Session the events feature uses.
type API interface {
	GuildScheduledEvents(guildID string, userCount bool, options ...discordgo.RequestOption) ([]*discordgo.GuildScheduledEvent, error)
	GuildScheduledEvent(guildID, eventID string, userCount bool, options ...discordgo.RequestOption) (*discordgo.GuildScheduledEvent, error)
	GuildScheduledEventCreate(guildID string, event *discordgo.GuildScheduledEventParams, options ...discordgo.RequestOption) (*discordgo.GuildScheduledEvent, error)
	GuildScheduledEventEdit(guildID, eventID string, event *discordgo.GuildScheduledEventParams, options ...discordgo.RequestOption) (*discordgo.GuildScheduledEvent, error)
	GuildScheduledEventUsers(guildID, eventID string, limit int, withMember bool, beforeID, afterID string, options ...discordgo.RequestOption) ([]*discordgo.GuildScheduledEventUser, error)
	ChannelMessageSendComplex(channelID string, data *discordgo.MessageSend, options ...discordgo.RequestOption) (*discordgo.Message, error)

	InteractionRespond(interaction *discordgo.Interaction, resp *discordgo.InteractionResponse, options ...discordgo.RequestOption) error
	InteractionResponseEdit(interaction *discordgo.Interaction, newresp *discordgo.WebhookEdit, options ...discordgo.RequestOption) (*discordgo.Message, error)
}

var _ API = (*discordgo.Session)(nil)

// Link opens the event in Discord; Discord also unfurls it into a card.
func Link(guildID, eventID string) string {
	return fmt.Sprintf("https://discord.com/events/%s/%s", guildID, eventID)
}

// CoverURL is the event's cover on Discord's CDN, or "" without one.
func CoverURL(ev *discordgo.GuildScheduledEvent) string {
	if ev == nil || ev.Image == "" {
		return ""
	}
	return fmt.Sprintf("https://cdn.discordapp.com/guild-events/%s/%s.png?size=1024", ev.ID, ev.Image)
}

func upcoming(ev *discordgo.GuildScheduledEvent) bool {
	return ev.Status == discordgo.GuildScheduledEventStatusScheduled || ev.Status == discordgo.GuildScheduledEventStatusActive
}
