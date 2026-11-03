package events

import (
	"strings"

	"github.com/bwmarrin/discordgo"
)

// CommandName is the slash command.
const CommandName = "event"

func intPtr(n int) *int { return &n }

// CommandDefinition is /event with its subcommands. Everyone sees it; the
// handler refuses people without the permissions (see canCreate,
// canManage), so the refusal can explain why.
func CommandDefinition() *discordgo.ApplicationCommand {
	dm := false
	eventOpt := func() *discordgo.ApplicationCommandOption {
		return &discordgo.ApplicationCommandOption{
			Type: discordgo.ApplicationCommandOptionString, Name: "event", Description: "อีเวนต์ (พิมพ์เพื่อค้น)",
			Required: true, Autocomplete: true,
		}
	}
	whenDesc := "วันเวลา เช่น ศุกร์นี้ 3 ทุ่ม, พรุ่งนี้ 20:00, 12/11 19:00"
	endDesc := "เวลาจบ หรือระยะเวลา เช่น 23:00, 3 ชม. (ไม่ใส่ = 2 ชั่วโมง)"
	placeOpt := func() *discordgo.ApplicationCommandOption {
		return &discordgo.ApplicationCommandOption{
			Type: discordgo.ApplicationCommandOptionChannel, Name: "place", Description: "ห้องเสียงที่จัด",
			ChannelTypes: []discordgo.ChannelType{discordgo.ChannelTypeGuildVoice},
		}
	}
	str := func(name, desc string, required bool, maxLen int) *discordgo.ApplicationCommandOption {
		return &discordgo.ApplicationCommandOption{
			Type: discordgo.ApplicationCommandOptionString, Name: name, Description: desc, Required: required, MaxLength: maxLen,
		}
	}
	image := &discordgo.ApplicationCommandOption{
		Type: discordgo.ApplicationCommandOptionAttachment, Name: "image", Description: "รูปปก (JPEG, PNG, WebP) — ไม่ใส่ = บอทหาให้",
	}
	return &discordgo.ApplicationCommand{
		Name:         CommandName,
		Description:  "สร้างและจัดการอีเวนต์ของเซิร์ฟเวอร์",
		DMPermission: &dm,
		Options: []*discordgo.ApplicationCommandOption{
			{
				Type: discordgo.ApplicationCommandOptionSubCommand, Name: "create", Description: "สร้างอีเวนต์ พร้อมรูปปกที่บอทหาให้",
				Options: []*discordgo.ApplicationCommandOption{
					str("name", "ชื่ออีเวนต์", true, maxNameLen),
					str("when", whenDesc, true, 100),
					placeOpt(),
					str("location", "สถานที่ (ถ้าไม่ได้จัดในห้องเสียง) หรือลิงก์", false, maxLocationLen),
					str("description", "รายละเอียด", false, maxUserDescription),
					str("end", endDesc, false, 100),
					image,
					str("keywords", "คำค้นรูปปก (ภาษาอังกฤษได้ผลดีสุด)", false, 100),
				},
			},
			{
				Type: discordgo.ApplicationCommandOptionSubCommand, Name: "cover", Description: "เปลี่ยนรูปปก: เลือกจากรูปที่ค้นได้ หรือแนบรูปเอง",
				Options: []*discordgo.ApplicationCommandOption{
					eventOpt(),
					str("keywords", "คำค้นรูปปก (ไม่ใส่ = ใช้ชื่ออีเวนต์)", false, 100),
					{Type: discordgo.ApplicationCommandOptionAttachment, Name: "image", Description: "รูปปกที่จะใช้ (JPEG, PNG, WebP)"},
				},
			},
			{
				Type: discordgo.ApplicationCommandOptionSubCommand, Name: "edit", Description: "แก้ชื่อ เวลา สถานที่ หรือรายละเอียด",
				Options: []*discordgo.ApplicationCommandOption{
					eventOpt(),
					str("name", "ชื่อใหม่", false, maxNameLen),
					str("when", whenDesc, false, 100),
					str("end", endDesc, false, 100),
					placeOpt(),
					str("location", "สถานที่ใหม่ (ย้ายออกจากห้องเสียง)", false, maxLocationLen),
					str("description", "รายละเอียดใหม่", false, maxUserDescription),
				},
			},
			{
				Type: discordgo.ApplicationCommandOptionSubCommand, Name: "cancel", Description: "ยกเลิกอีเวนต์",
				Options: []*discordgo.ApplicationCommandOption{eventOpt()},
			},
			{
				Type: discordgo.ApplicationCommandOptionSubCommand, Name: "list", Description: "อีเวนต์ที่กำลังจะมา",
			},
		},
	}
}

// options are a subcommand's options by name.
type options struct {
	byName   map[string]*discordgo.ApplicationCommandInteractionDataOption
	resolved *discordgo.ApplicationCommandInteractionDataResolved
}

func subcommand(data discordgo.ApplicationCommandInteractionData) (string, options) {
	o := options{byName: map[string]*discordgo.ApplicationCommandInteractionDataOption{}, resolved: data.Resolved}
	if len(data.Options) == 0 {
		return "", o
	}
	sub := data.Options[0]
	for _, opt := range sub.Options {
		o.byName[opt.Name] = opt
	}
	return sub.Name, o
}

func (o options) str(name string) string {
	opt, ok := o.byName[name]
	if !ok {
		return ""
	}
	s, _ := opt.Value.(string)
	return strings.TrimSpace(s)
}

// attachment resolves an attachment option.
func (o options) attachment(name string) *discordgo.MessageAttachment {
	id := o.str(name)
	if id == "" || o.resolved == nil {
		return nil
	}
	return o.resolved.Attachments[id]
}

func (o options) focused() (*discordgo.ApplicationCommandInteractionDataOption, bool) {
	for _, opt := range o.byName {
		if opt.Focused {
			return opt, true
		}
	}
	return nil, false
}

// Permissions. Discord sends the member's resolved permissions in the
// channel with every interaction.

func hasPerm(perms, p int64) bool {
	return perms&discordgo.PermissionAdministrator != 0 || perms&p != 0
}

// canCreate: Manage Events or Create Events.
func canCreate(perms int64) bool {
	return hasPerm(perms, discordgo.PermissionManageEvents) || hasPerm(perms, discordgo.PermissionCreateEvents)
}

// canManage: Manage Events, or the event's creator.
func canManage(perms int64, userID, creatorID string) bool {
	return hasPerm(perms, discordgo.PermissionManageEvents) || (creatorID != "" && creatorID == userID)
}
