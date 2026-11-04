package events

import (
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"

	"github.com/Sonty15/SONNIE/internal/eventtime"
	"github.com/Sonty15/SONNIE/internal/modal"
)

// The /event create form. Its custom ID carries a version, so a form that
// was opened before a deploy changed the fields is refused, not misread.
const (
	formPrefix = CustomIDPrefix + "form:"
	formID     = formPrefix + "1"
	formTitle  = "สร้าง Event"

	fieldName        = "name"
	fieldDay         = "day"
	fieldTime        = "time"
	fieldPlace       = "place"
	fieldDescription = "description"

	// formDays is today and the next 24 days: Discord allows 25 options.
	formDays        = 25
	formDefaultTime = "20:00"
	formMaxTimeLen  = 50
	formMaxDescLen  = 1000
	// formLocation is where an event without a voice channel takes place.
	formLocation = "Discord"
)

var (
	errBadForm = errors.New("ฟอร์มนี้ใช้ไม่ได้")
	errOldForm = errors.New("ฟอร์มนี้เก่าแล้ว (บอทเพิ่งอัปเดต) — เปิดใหม่ด้วย `/event create`")
)

// checkFormID accepts the current form's custom ID only.
func checkFormID(id string) error {
	switch {
	case id == formID:
		return nil
	case strings.HasPrefix(id, formPrefix) && len(id) <= 100:
		return errOldForm
	}
	return errBadForm
}

// VoiceChannelFunc returns the voice channel userID is in, or "".
type VoiceChannelFunc func(guildID, userID string) string

// SetVoiceChannel lets the form preselect the caller's voice channel.
// Without it the channel field starts empty.
func (h *Handler) SetVoiceChannel(f VoiceChannelFunc) {
	h.voiceChannel = f
}

var thaiWeekday = [...]string{"อาทิตย์", "จันทร์", "อังคาร", "พุธ", "พฤหัส", "ศุกร์", "เสาร์"}

// startOfDay is midnight of t's date in Bangkok.
func startOfDay(t time.Time) time.Time {
	t = t.In(eventtime.Bangkok)
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, eventtime.Bangkok)
}

// dayLabel names day for the form: "วันนี้ (พุธ 5 พ.ย.)",
// "พรุ่งนี้ (พฤหัส 6 พ.ย.)", "ศุกร์ 7 พ.ย.".
func dayLabel(day, now time.Time) string {
	day = startOfDay(day)
	date := fmt.Sprintf("%s %d %s", thaiWeekday[day.Weekday()], day.Day(), thaiMonthShort[day.Month()])
	today := startOfDay(now)
	switch {
	case day.Equal(today):
		return "วันนี้ (" + date + ")"
	case day.Equal(today.AddDate(0, 0, 1)):
		return "พรุ่งนี้ (" + date + ")"
	}
	return date
}

const dayValueLayout = "2006-01-02"

// dayOptions are today and the next days, today preselected. The value is
// the date, so a form submitted after midnight still means the day shown.
func dayOptions(now time.Time) []modal.Option {
	today := startOfDay(now)
	opts := make([]modal.Option, formDays)
	for n := range opts {
		d := today.AddDate(0, 0, n)
		opts[n] = modal.Option{Label: dayLabel(d, now), Value: d.Format(dayValueLayout), Default: n == 0}
	}
	return opts
}

// createForm is the modal for /event create. voiceChannelID, when set, is
// preselected.
func createForm(now time.Time, voiceChannelID string) *discordgo.InteractionResponse {
	if !isSnowflake(voiceChannelID) {
		voiceChannelID = ""
	}
	return modal.Response(formID, formTitle,
		modal.Label{Label: "ชื่อ event", Component: modal.TextInput{
			CustomID: fieldName, Style: discordgo.TextInputShort, Required: true, MaxLength: maxNameLen,
			Placeholder: "เช่น เตะบอลวันศุกร์",
		}},
		modal.Label{Label: "วัน", Component: modal.StringSelect{
			CustomID: fieldDay, Required: true, Options: dayOptions(now),
		}},
		modal.Label{Label: "เวลา", Description: "เวลาเริ่ม (Asia/Bangkok) อีเวนต์ยาว 2 ชั่วโมง", Component: modal.TextInput{
			CustomID: fieldTime, Style: discordgo.TextInputShort, Required: true, MaxLength: formMaxTimeLen,
			Value: formDefaultTime, Placeholder: "20:00 / 3 ทุ่ม / บ่าย 2",
		}},
		modal.Label{Label: "ห้องเสียง", Description: "ไม่เลือก = จัดนอกห้องเสียง (สถานที่ “Discord”)", Component: modal.ChannelSelect{
			CustomID: fieldPlace, ChannelTypes: []discordgo.ChannelType{discordgo.ChannelTypeGuildVoice},
			Default: voiceChannelID, Placeholder: "เลือกห้องเสียง",
		}},
		modal.Label{Label: "รายละเอียด", Component: modal.TextInput{
			CustomID: fieldDescription, Style: discordgo.TextInputParagraph, MaxLength: formMaxDescLen,
		}},
	)
}

func (h *Handler) openForm(i *discordgo.InteractionCreate, who caller) {
	voice := ""
	if h.voiceChannel != nil {
		voice = h.voiceChannel(i.GuildID, who.userID)
	}
	if err := h.api.InteractionRespond(i.Interaction, createForm(h.now(), voice)); err != nil {
		log.Printf("events: open the create form: %v", err)
	}
}

const msgFormExternal = "📍 ไม่ได้เลือกห้องเสียง — สร้างเป็นอีเวนต์นอกห้องเสียง สถานที่ `Discord` (เปลี่ยนได้ด้วย `/event edit location:`)"

// parseForm validates a submitted create form. The error is shown to the
// user.
func parseForm(s modal.Submit, now time.Time) (createRequest, error) {
	name := s.Value(fieldName)
	if name == "" {
		return createRequest{}, badInput(fieldName, name, errors.New("ใส่ชื่ออีเวนต์ด้วย"))
	}
	dayValue := s.Value(fieldDay)
	day, err := time.ParseInLocation(dayValueLayout, dayValue, eventtime.Bangkok)
	if err != nil || day.After(startOfDay(now).AddDate(0, 0, formDays-1)) {
		return createRequest{}, badInput(fieldDay, dayValue, errors.New("วันที่เลือกใช้ไม่ได้ — เปิดฟอร์มใหม่ด้วย `/event create`"))
	}
	typed := s.Value(fieldTime)
	start, err := eventtime.ParseTimeOn(typed, day, now)
	if err != nil {
		return createRequest{}, badInput(fieldTime, typed, formTimeError(typed, day, now, err))
	}
	end := start.Add(eventtime.DefaultDuration)
	req := createRequest{
		params: discordgo.GuildScheduledEventParams{
			Name:               truncate(name, maxNameLen),
			ScheduledStartTime: &start,
			ScheduledEndTime:   &end,
			PrivacyLevel:       discordgo.GuildScheduledEventPrivacyLevelGuildOnly,
		},
		// joinCredit shortens it if the cover credit needs the room.
		body: s.Value(fieldDescription),
	}
	channelID := s.Value(fieldPlace)
	if channelID != "" && !isSnowflake(channelID) {
		return createRequest{}, badInput(fieldPlace, channelID, errBadForm)
	}
	if channelID == "" {
		req.note = msgFormExternal
		return req, setPlace(&req.params, "", formLocation)
	}
	return req, setPlace(&req.params, channelID, "")
}

// formTimeError explains a time the form could not use, repeating what
// was typed.
func formTimeError(typed string, day, now time.Time, err error) error {
	var unknown *eventtime.UnknownError
	switch {
	case errors.Is(err, eventtime.ErrPast):
		return fmt.Errorf("⏰ %s เวลา “%s” ผ่านไปแล้ว — ใส่เวลาที่ยังไม่ถึง หรือเลือกวันอื่น", dayLabel(day, now), typed)
	case errors.As(err, &unknown):
		return fmt.Errorf("อ่านเวลา “%s” ไม่ออก — ลองแบบ `20:00`, `3 ทุ่ม` หรือ `บ่าย 2`", typed)
	}
	return fmt.Errorf("เวลา “%s”: %w", typed, err)
}

// HandleModalSubmit answers a submitted /event form.
func (h *Handler) HandleModalSubmit(i *discordgo.InteractionCreate, s modal.Submit) {
	who, ok := callerOf(i)
	if !ok {
		h.ephemeral(i, msgGuildOnly)
		return
	}
	if err := checkFormID(s.CustomID); err != nil {
		log.Printf("events: form %q from user %s refused: %v", truncate(s.CustomID, maxLoggedValue), who.userID, err)
		h.ephemeral(i, err.Error())
		return
	}
	// The permission may have been taken away while the form was open.
	if !canCreate(who.perms) {
		h.ephemeral(i, msgNoCreate)
		return
	}
	req, err := parseForm(s, h.now())
	if err != nil {
		h.reject(i, who, "create form", err)
		return
	}
	h.publish(i, who, req)
}
