// Package eventtime reads the start and end of a Discord event from the
// short Thai or English text people type, such as "ศุกร์นี้ 3 ทุ่ม",
// "พรุ่งนี้ 20:00" or "12/11 19:00". Times are in Asia/Bangkok.
package eventtime

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Bangkok is Asia/Bangkok. Thailand has had no daylight saving time since
// 1920, so a fixed zone is exact and needs no tzdata in the image.
var Bangkok = time.FixedZone("Asia/Bangkok", 7*60*60)

// DefaultDuration is the length of an event when no end is given.
const DefaultDuration = 2 * time.Hour

// maxDuration bounds the end option so a typo like "300 ชม." is caught.
const maxDuration = 31 * 24 * time.Hour

// The error texts are shown to the user as they are.
var (
	ErrEmpty          = errors.New("ยังไม่ได้ระบุเวลา")
	ErrNoTime         = errors.New("ระบุเวลาด้วย เช่น `20:00`, `3 ทุ่ม` หรือ `บ่ายสอง`")
	ErrPast           = errors.New("เวลานี้ผ่านไปแล้ว")
	ErrInvalid        = errors.New("วันหรือเวลาไม่ถูกต้อง")
	ErrEndBeforeStart = errors.New("เวลาจบต้องอยู่หลังเวลาเริ่ม")
	ErrTooLong        = errors.New("อีเวนต์ยาวเกิน 31 วัน")
)

// UnknownError reports text the parser could not read.
type UnknownError struct {
	Text string
}

func (e *UnknownError) Error() string {
	return fmt.Sprintf("อ่าน “%s” ไม่ออก — ลองแบบ `ศุกร์นี้ 3 ทุ่ม`, `พรุ่งนี้ 20:00` หรือ `12/11 19:00`", e.Text)
}

// ParseStart reads the start time of an event. A time without a date is the
// next time it comes (today or tomorrow); a weekday is its next occurrence.
// Times that are not after now are rejected with ErrPast.
func ParseStart(input string, now time.Time) (time.Time, error) {
	now = now.In(Bangkok)
	t, err := parse(input, now, now)
	if err != nil {
		return time.Time{}, err
	}
	if !t.After(now) {
		return time.Time{}, ErrPast
	}
	return t, nil
}

// ParseEnd reads the end of an event that starts at start: either a duration
// ("3 ชม.", "90 นาที", "1h30m") or a time in the same formats as ParseStart.
// A time without a date is the first such time after start.
func ParseEnd(input string, start, now time.Time) (time.Time, error) {
	start = start.In(Bangkok)
	now = now.In(Bangkok)
	if d, ok := parseDuration(input); ok {
		if d <= 0 {
			return time.Time{}, ErrEndBeforeStart
		}
		if d > maxDuration {
			return time.Time{}, ErrTooLong
		}
		return start.Add(d), nil
	}
	t, err := parse(input, now, start)
	if err != nil {
		return time.Time{}, err
	}
	if !t.After(start) {
		return time.Time{}, ErrEndBeforeStart
	}
	if t.Sub(start) > maxDuration {
		return time.Time{}, ErrTooLong
	}
	return t, nil
}

// parsed is what the text says, before it is resolved against a clock.
type parsed struct {
	date    dateSpec
	hasTime bool
	hour    int // 0-24; 24 is midnight at the end of the day
	minute  int
}

type dateKind int

const (
	dateNone dateKind = iota
	dateAbsolute
	dateRelative // offset days from today
	dateWeekday
)

type dateSpec struct {
	kind dateKind

	// dateAbsolute
	year, month, day int
	yearGiven        bool

	// dateRelative
	days    int
	tonight bool

	// dateWeekday
	weekday time.Weekday
	nextWk  bool
}

var thaiDigits = strings.NewReplacer(
	"๐", "0", "๑", "1", "๒", "2", "๓", "3", "๔", "4",
	"๕", "5", "๖", "6", "๗", "7", "๘", "8", "๙", "9",
)

// thaiNumbers turns spelled-out hours into digits. Longer words come first
// so "สิบสอง" is not read as "10" followed by "สอง".
var thaiNumbers = strings.NewReplacer(
	"สิบสอง", "12", "สิบเอ็ด", "11", "สิบ", "10",
	"หนึ่ง", "1", "สอง", "2", "สาม", "3", "สี่", "4", "ห้า", "5",
	"หก", "6", "เจ็ด", "7", "แปด", "8", "เก้า", "9",
)

var spaces = regexp.MustCompile(`\s+`)

func normalize(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = thaiDigits.Replace(s)
	s = thaiNumbers.Replace(s)
	return spaces.ReplaceAllString(s, " ")
}

const thaiMonthAlt = `ม\.ค\.|มกราคม|มกรา|ก\.พ\.|กุมภาพันธ์|กุมภา|มี\.ค\.|มีนาคม|มีนา|เม\.ย\.|เมษายน|เมษา|` +
	`พ\.ค\.|พฤษภาคม|พฤษภา|มิ\.ย\.|มิถุนายน|มิถุนา|ก\.ค\.|กรกฎาคม|กรกฎา|ส\.ค\.|สิงหาคม|สิงหา|` +
	`ก\.ย\.|กันยายน|กันยา|ต\.ค\.|ตุลาคม|ตุลา|พ\.ย\.|พฤศจิกายน|พฤศจิกา|ธ\.ค\.|ธันวาคม|ธันวา`

const englishMonthAlt = `january|february|march|april|may|june|july|august|september|october|november|december|` +
	`jan|feb|mar|apr|jun|jul|aug|sept|sep|oct|nov|dec`

var (
	reISO        = regexp.MustCompile(`(\d{4})-(\d{1,2})-(\d{1,2})`)
	reDMY        = regexp.MustCompile(`\b(\d{1,2})/(\d{1,2})(?:/(\d{4}|\d{2}))?\b`)
	reThaiMonth  = regexp.MustCompile(`(\d{1,2})\s*(` + thaiMonthAlt + `)(?:\s*(\d{4}|[5-9]\d)(?:\s|$))?`)
	reEngMonthDM = regexp.MustCompile(`\b(\d{1,2})\s*(` + englishMonthAlt + `)\.?(?:\s+(\d{4})\b)?`)
	reEngMonthMD = regexp.MustCompile(`\b(` + englishMonthAlt + `)\.?\s+(\d{1,2})\b(?:,?\s+(\d{4})\b)?`)
	reRelative   = regexp.MustCompile(`วันนี้|คืนนี้|พรุ่งนี้|มะรืนนี้|มะรืน|\btoday\b|\btonight\b|\btomorrow\b`)
	reWeekdayTH  = regexp.MustCompile(`(?:วัน)?(จันทร์|อังคาร|พุธ|พฤหัสบดี|พฤหัส|ศุกร์|เสาร์|อาทิตย์)\s*(นี้|หน้า)?`)
	reWeekdayEN  = regexp.MustCompile(`(?:\b(next|this)\s+)?\b(monday|mon|tuesday|tues|tue|wednesday|wed|thursday|thurs|thu|friday|fri|saturday|sat|sunday|sun)\b`)

	reClock   = regexp.MustCompile(`\b(\d{1,2})[:.](\d{2})(?:\s*(am|pm|a\.m\.|p\.m\.))?(?:\s*(?:น\.|นาฬิกา))?`)
	reAmPm    = regexp.MustCompile(`\b(\d{1,2})\s*(am|pm|a\.m\.|p\.m\.)`)
	reTee     = regexp.MustCompile(`ตี\s*(\d{1,2})(\s*ครึ่ง)?`)
	reThum    = regexp.MustCompile(`(\d{1,2})?\s*ทุ่ม(\s*ครึ่ง)?`)
	reBaiN    = regexp.MustCompile(`บ่าย\s*(\d{1,2})\s*(?:โมง)?(\s*ครึ่ง)?`)
	reBaiMong = regexp.MustCompile(`บ่ายโมง(\s*ครึ่ง)?`)
	reMong    = regexp.MustCompile(`(\d{1,2})\s*โมง\s*(เช้า|เย็น|ค่ำ)?(\s*ครึ่ง)?`)
	reNight   = regexp.MustCompile(`เที่ยงคืน|\bmidnight\b`)
	reNoon    = regexp.MustCompile(`เที่ยง(?:วัน)?(\s*ครึ่ง)?|\bnoon\b`)
	reHourNa  = regexp.MustCompile(`\b(\d{1,2})\s*(?:น\.|นาฬิกา)`)

	// reFiller is what may be left over once the date and time are read.
	reFiller = regexp.MustCompile(`วันที่|วัน|เวลา|ตอน|ที่|นาฬิกา|น\.|\bat\b|\bon\b|\bthe\b|[,@]`)
)

var thaiMonths = map[string]int{}

func init() {
	groups := [][]string{
		{"ม.ค.", "มกราคม", "มกรา"}, {"ก.พ.", "กุมภาพันธ์", "กุมภา"}, {"มี.ค.", "มีนาคม", "มีนา"},
		{"เม.ย.", "เมษายน", "เมษา"}, {"พ.ค.", "พฤษภาคม", "พฤษภา"}, {"มิ.ย.", "มิถุนายน", "มิถุนา"},
		{"ก.ค.", "กรกฎาคม", "กรกฎา"}, {"ส.ค.", "สิงหาคม", "สิงหา"}, {"ก.ย.", "กันยายน", "กันยา"},
		{"ต.ค.", "ตุลาคม", "ตุลา"}, {"พ.ย.", "พฤศจิกายน", "พฤศจิกา"}, {"ธ.ค.", "ธันวาคม", "ธันวา"},
	}
	for i, g := range groups {
		for _, name := range g {
			thaiMonths[name] = i + 1
		}
	}
}

func englishMonth(s string) int {
	if len(s) < 3 {
		return 0
	}
	switch s[:3] {
	case "jan":
		return 1
	case "feb":
		return 2
	case "mar":
		return 3
	case "apr":
		return 4
	case "may":
		return 5
	case "jun":
		return 6
	case "jul":
		return 7
	case "aug":
		return 8
	case "sep":
		return 9
	case "oct":
		return 10
	case "nov":
		return 11
	case "dec":
		return 12
	}
	return 0
}

var thaiWeekdays = map[string]time.Weekday{
	"จันทร์": time.Monday, "อังคาร": time.Tuesday, "พุธ": time.Wednesday,
	"พฤหัส": time.Thursday, "พฤหัสบดี": time.Thursday, "ศุกร์": time.Friday,
	"เสาร์": time.Saturday, "อาทิตย์": time.Sunday,
}

func englishWeekday(s string) time.Weekday {
	switch s[:3] {
	case "mon":
		return time.Monday
	case "tue":
		return time.Tuesday
	case "wed":
		return time.Wednesday
	case "thu":
		return time.Thursday
	case "fri":
		return time.Friday
	case "sat":
		return time.Saturday
	}
	return time.Sunday
}

// year reads a year as written: Buddhist Era when above 2400 (2569) or as
// two digits from 50 (69 = 2569), otherwise Common Era (2026, 26).
func year(s string) int {
	n, _ := strconv.Atoi(s)
	switch {
	case len(s) == 2 && n >= 50:
		return n + 2500 - 543
	case len(s) == 2:
		return n + 2000
	case n > 2400:
		return n - 543
	}
	return n
}

func atoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}

// cut removes the first match of re from *s and returns its submatches.
func cut(s *string, re *regexp.Regexp) []string {
	loc := re.FindStringSubmatchIndex(*s)
	if loc == nil {
		return nil
	}
	m := make([]string, len(loc)/2)
	for i := range m {
		if loc[2*i] >= 0 {
			m[i] = (*s)[loc[2*i]:loc[2*i+1]]
		}
	}
	*s = (*s)[:loc[0]] + " " + (*s)[loc[1]:]
	return m
}

func half(s string) int {
	if s != "" {
		return 30
	}
	return 0
}

func readDate(s *string) (dateSpec, bool) {
	if m := cut(s, reISO); m != nil {
		return dateSpec{kind: dateAbsolute, year: atoi(m[1]), month: atoi(m[2]), day: atoi(m[3]), yearGiven: true}, true
	}
	if m := cut(s, reDMY); m != nil {
		d := dateSpec{kind: dateAbsolute, day: atoi(m[1]), month: atoi(m[2])}
		if m[3] != "" {
			d.year, d.yearGiven = year(m[3]), true
		}
		return d, true
	}
	if m := cut(s, reThaiMonth); m != nil {
		d := dateSpec{kind: dateAbsolute, day: atoi(m[1]), month: thaiMonths[m[2]]}
		if m[3] != "" {
			d.year, d.yearGiven = year(m[3]), true
		}
		return d, true
	}
	if m := cut(s, reEngMonthDM); m != nil {
		d := dateSpec{kind: dateAbsolute, day: atoi(m[1]), month: englishMonth(m[2])}
		if m[3] != "" {
			d.year, d.yearGiven = year(m[3]), true
		}
		return d, true
	}
	if m := cut(s, reEngMonthMD); m != nil {
		d := dateSpec{kind: dateAbsolute, day: atoi(m[2]), month: englishMonth(m[1])}
		if m[3] != "" {
			d.year, d.yearGiven = year(m[3]), true
		}
		return d, true
	}
	if m := cut(s, reRelative); m != nil {
		switch m[0] {
		case "วันนี้", "today":
			return dateSpec{kind: dateRelative}, true
		case "คืนนี้", "tonight":
			return dateSpec{kind: dateRelative, tonight: true}, true
		case "พรุ่งนี้", "tomorrow":
			return dateSpec{kind: dateRelative, days: 1}, true
		default: // มะรืน
			return dateSpec{kind: dateRelative, days: 2}, true
		}
	}
	if m := cut(s, reWeekdayTH); m != nil {
		return dateSpec{kind: dateWeekday, weekday: thaiWeekdays[m[1]], nextWk: m[2] == "หน้า"}, true
	}
	if m := cut(s, reWeekdayEN); m != nil {
		return dateSpec{kind: dateWeekday, weekday: englishWeekday(m[2]), nextWk: m[1] == "next"}, true
	}
	return dateSpec{}, false
}

// readTime returns hour (0-24) and minute. ok is false when no time is
// written; err is set when one is written but out of range.
func readTime(s *string) (hour, minute int, ok bool, err error) {
	if m := cut(s, reClock); m != nil {
		h, min := atoi(m[1]), atoi(m[2])
		if min > 59 {
			return 0, 0, true, ErrInvalid
		}
		h, err := ampm(h, m[3])
		return h, min, true, err
	}
	if m := cut(s, reAmPm); m != nil {
		h, err := ampm(atoi(m[1]), m[2])
		return h, 0, true, err
	}
	if m := cut(s, reTee); m != nil {
		n := atoi(m[1])
		if n < 1 || n > 5 {
			return 0, 0, true, ErrInvalid
		}
		return n, half(m[2]), true, nil
	}
	if m := cut(s, reThum); m != nil {
		n := 1
		if m[1] != "" {
			n = atoi(m[1])
		}
		if n < 1 || n > 6 {
			return 0, 0, true, ErrInvalid
		}
		if n == 6 && m[2] != "" {
			return 0, 0, true, ErrInvalid
		}
		return 18 + n, half(m[2]), true, nil
	}
	if m := cut(s, reBaiMong); m != nil {
		return 13, half(m[1]), true, nil
	}
	if m := cut(s, reBaiN); m != nil {
		n := atoi(m[1])
		if n < 1 || n > 5 {
			return 0, 0, true, ErrInvalid
		}
		return 12 + n, half(m[2]), true, nil
	}
	if m := cut(s, reMong); m != nil {
		n := atoi(m[1])
		min := half(m[3])
		switch m[2] {
		case "เช้า":
			// หนึ่งโมงเช้า is 7:00 in the traditional count; 7-11 โมงเช้า is literal.
			switch {
			case n >= 1 && n <= 5:
				return 6 + n, min, true, nil
			case n >= 6 && n <= 11:
				return n, min, true, nil
			}
		case "เย็น", "ค่ำ":
			if n >= 1 && n <= 6 {
				return 12 + n, min, true, nil
			}
		default:
			// Without a part of day, 1-5 โมง is the afternoon (บ่าย is often dropped).
			switch {
			case n >= 1 && n <= 5:
				return 12 + n, min, true, nil
			case n >= 6 && n <= 12:
				return n, min, true, nil
			}
		}
		return 0, 0, true, ErrInvalid
	}
	if m := cut(s, reNight); m != nil {
		return 24, 0, true, nil
	}
	if m := cut(s, reNoon); m != nil {
		return 12, half(m[1]), true, nil
	}
	if m := cut(s, reHourNa); m != nil {
		h := atoi(m[1])
		if h > 23 {
			return 0, 0, true, ErrInvalid
		}
		return h, 0, true, nil
	}
	return 0, 0, false, nil
}

func ampm(h int, suffix string) (int, error) {
	switch suffix {
	case "":
		if h > 23 {
			return 0, ErrInvalid
		}
		return h, nil
	case "am", "a.m.":
		if h < 1 || h > 12 {
			return 0, ErrInvalid
		}
		if h == 12 {
			return 0, nil
		}
		return h, nil
	default:
		if h < 1 || h > 12 {
			return 0, ErrInvalid
		}
		if h == 12 {
			return 12, nil
		}
		return h + 12, nil
	}
}

func read(input string) (parsed, error) {
	s := normalize(input)
	if s == "" {
		return parsed{}, ErrEmpty
	}
	var p parsed
	p.date, _ = readDate(&s)
	h, m, ok, err := readTime(&s)
	if err != nil {
		return parsed{}, err
	}
	p.hasTime, p.hour, p.minute = ok, h, m
	if rest := strings.TrimSpace(reFiller.ReplaceAllString(s, " ")); rest != "" {
		return parsed{}, &UnknownError{Text: spaces.ReplaceAllString(rest, " ")}
	}
	if !p.hasTime {
		return parsed{}, ErrNoTime
	}
	return p, nil
}

// parse resolves the text: dates and weekdays against now, a bare time
// against base (now for a start, the start for an end).
func parse(input string, now, base time.Time) (time.Time, error) {
	p, err := read(input)
	if err != nil {
		return time.Time{}, err
	}
	at := func(y int, m time.Month, d int) time.Time {
		// Hour 24 normalizes to 00:00 the next day.
		return time.Date(y, m, d, p.hour, p.minute, 0, 0, Bangkok)
	}
	switch p.date.kind {
	case dateAbsolute:
		d := p.date
		y := d.year
		if !d.yearGiven {
			y = now.Year()
		}
		day := time.Date(y, time.Month(d.month), d.day, 0, 0, 0, 0, Bangkok)
		if day.Day() != d.day || int(day.Month()) != d.month {
			return time.Time{}, ErrInvalid
		}
		today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, Bangkok)
		if !d.yearGiven && day.Before(today) {
			// "12/1" in November means next January.
			y++
			day = time.Date(y, time.Month(d.month), d.day, 0, 0, 0, 0, Bangkok)
			if day.Day() != d.day {
				return time.Time{}, ErrInvalid // 29/2 in a non-leap year
			}
		}
		return at(y, time.Month(d.month), d.day), nil
	case dateRelative:
		t := at(now.Year(), now.Month(), now.Day()+p.date.days)
		if p.date.tonight && p.hour < 6 {
			// "คืนนี้ตีสอง" is after midnight.
			t = t.AddDate(0, 0, 1)
		}
		return t, nil
	case dateWeekday:
		wd := p.date.weekday
		if p.date.nextWk {
			// The weekday in next week (weeks start on Monday).
			toMonday := 7 - monday0(now.Weekday())
			return at(now.Year(), now.Month(), now.Day()+toMonday+monday0(wd)), nil
		}
		ahead := (int(wd) - int(now.Weekday()) + 7) % 7
		t := at(now.Year(), now.Month(), now.Day()+ahead)
		if !t.After(now) {
			t = t.AddDate(0, 0, 7)
		}
		return t, nil
	}
	t := at(base.Year(), base.Month(), base.Day())
	if !t.After(base) {
		t = t.AddDate(0, 0, 1)
	}
	return t, nil
}

func monday0(d time.Weekday) int {
	return (int(d) + 6) % 7
}

var (
	reDurHour = regexp.MustCompile(`^(\d+(?:\.\d+)?)\s*(?:ชม\.?|ชั่วโมง|ช\.ม\.|hours|hour|hrs|hr|h)\s*(ครึ่ง)?`)
	reDurMin  = regexp.MustCompile(`^(\d+)\s*(?:นาที|minutes|minute|mins|min|m)$`)
)

// parseDuration reads "3 ชม.", "1.5 ชั่วโมง", "2 ชั่วโมงครึ่ง", "90 นาที",
// "1 ชม. 30 นาที", "ครึ่งชั่วโมง" or a Go duration such as "1h30m".
func parseDuration(input string) (time.Duration, bool) {
	s := normalize(input)
	if s == "" {
		return 0, false
	}
	if d, err := time.ParseDuration(strings.ReplaceAll(s, " ", "")); err == nil {
		return d, true
	}
	if s == "ครึ่งชั่วโมง" || s == "ครึ่งชม." {
		return 30 * time.Minute, true
	}
	var total time.Duration
	matched := false
	if m := reDurHour.FindStringSubmatch(s); m != nil {
		h, err := strconv.ParseFloat(m[1], 64)
		if err != nil {
			return 0, false
		}
		total += time.Duration(h * float64(time.Hour))
		if m[2] != "" {
			total += 30 * time.Minute
		}
		s = strings.TrimSpace(s[len(m[0]):])
		matched = true
	}
	if s != "" {
		m := reDurMin.FindStringSubmatch(s)
		if m == nil {
			return 0, false
		}
		total += time.Duration(atoi(m[1])) * time.Minute
		matched = true
	}
	return total, matched
}
