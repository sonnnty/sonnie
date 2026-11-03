package eventtime

import (
	"errors"
	"testing"
	"time"
)

// now is Tuesday 3 November 2026, 18:00 in Bangkok.
var now = time.Date(2026, 11, 3, 18, 0, 0, 0, Bangkok)

func bkk(month time.Month, day, hour, min int) time.Time {
	return time.Date(2026, month, day, hour, min, 0, 0, Bangkok)
}

func TestParseStart(t *testing.T) {
	tests := []struct {
		in   string
		want time.Time
	}{
		// The examples from the spec.
		{"ศุกร์นี้ 3 ทุ่ม", bkk(11, 6, 21, 0)},
		{"พรุ่งนี้ 20:00", bkk(11, 4, 20, 0)},
		{"วันเสาร์ 14.30", bkk(11, 7, 14, 30)},
		{"12/11 19:00", bkk(11, 12, 19, 0)},
		{"2026-11-20 19:00", bkk(11, 20, 19, 0)},
		{"บ่ายสอง", bkk(11, 4, 14, 0)}, // 14:00 has passed today
		{"สองทุ่มครึ่ง", bkk(11, 3, 20, 30)},

		// ทุ่ม = 18 + n.
		{"3 ทุ่ม", bkk(11, 3, 21, 0)},
		{"3ทุ่ม", bkk(11, 3, 21, 0)},
		{"ทุ่ม", bkk(11, 3, 19, 0)},
		{"หนึ่งทุ่ม", bkk(11, 3, 19, 0)},
		{"ห้าทุ่ม", bkk(11, 3, 23, 0)},
		{"ห้าทุ่มครึ่ง", bkk(11, 3, 23, 30)},
		{"หกทุ่ม", bkk(11, 4, 0, 0)},

		// บ่าย n = 12 + n.
		{"บ่ายโมง", bkk(11, 4, 13, 0)},
		{"บ่าย 3", bkk(11, 4, 15, 0)},
		{"บ่าย 3 โมงครึ่ง", bkk(11, 4, 15, 30)},
		{"บ่ายสามโมง", bkk(11, 4, 15, 0)},

		// โมงเช้า, โมงเย็น and bare โมง.
		{"เจ็ดโมงเช้า", bkk(11, 4, 7, 0)},
		{"สองโมงเช้า", bkk(11, 4, 8, 0)},
		{"สิบโมงเช้า", bkk(11, 4, 10, 0)},
		{"สิบเอ็ดโมงครึ่ง", bkk(11, 4, 11, 30)},
		{"ห้าโมงเย็น", bkk(11, 4, 17, 0)},
		{"หกโมงเย็น", bkk(11, 4, 18, 0)},  // exactly now: not in the future
		{"หกโมงครึ่ง", bkk(11, 4, 6, 30)}, // bare 6-12 โมง is the morning
		{"สามโมง", bkk(11, 4, 15, 0)},
		{"เก้าโมง", bkk(11, 4, 9, 0)},

		// เที่ยง, เที่ยงคืน, ตี n.
		{"เที่ยง", bkk(11, 4, 12, 0)},
		{"เที่ยงวัน", bkk(11, 4, 12, 0)},
		{"เที่ยงครึ่ง", bkk(11, 4, 12, 30)},
		{"เที่ยงคืน", bkk(11, 4, 0, 0)},
		{"พรุ่งนี้ เที่ยงคืน", bkk(11, 5, 0, 0)},
		{"ตีสอง", bkk(11, 4, 2, 0)},
		{"ตี 5 ครึ่ง", bkk(11, 4, 5, 30)},
		{"คืนนี้ ตีสอง", bkk(11, 4, 2, 0)},
		{"คืนนี้ 3 ทุ่ม", bkk(11, 3, 21, 0)},

		// Clock forms and Thai digits.
		{"20:00", bkk(11, 3, 20, 0)},
		{"20.30 น.", bkk(11, 3, 20, 30)},
		{"20 น.", bkk(11, 3, 20, 0)},
		{"20 นาฬิกา", bkk(11, 3, 20, 0)},
		{"๒๐:๐๐", bkk(11, 3, 20, 0)},
		{"9:15", bkk(11, 4, 9, 15)},
		{"  พรุ่งนี้   20:00  ", bkk(11, 4, 20, 0)},

		// Relative days and weekdays.
		{"วันนี้ 19:00", bkk(11, 3, 19, 0)},
		{"มะรืนนี้ 10:00", bkk(11, 5, 10, 0)},
		{"มะรืน 10:00", bkk(11, 5, 10, 0)},
		{"วันศุกร์ 3 ทุ่ม", bkk(11, 6, 21, 0)},
		{"ศุกร์หน้า 3 ทุ่ม", bkk(11, 13, 21, 0)},
		{"วันพฤหัสบดี 19:00", bkk(11, 5, 19, 0)},
		{"พฤหัส 19:00", bkk(11, 5, 19, 0)},
		{"อังคาร 20:00", bkk(11, 3, 20, 0)},  // today, still ahead
		{"อังคาร 17:00", bkk(11, 10, 17, 0)}, // today has passed: next week
		{"จันทร์หน้า 20:00", bkk(11, 9, 20, 0)},
		{"อาทิตย์หน้า บ่ายสาม", bkk(11, 15, 15, 0)},
		{"วันอาทิตย์ 10:00", bkk(11, 8, 10, 0)},

		// Dates.
		{"20 พ.ย. 19:00", bkk(11, 20, 19, 0)},
		{"วันที่ 20 พ.ย. เวลา 19:00 น.", bkk(11, 20, 19, 0)},
		{"20 พฤศจิกายน 2569 19:00", bkk(11, 20, 19, 0)},
		{"20 พ.ย. 69 3 ทุ่ม", bkk(11, 20, 21, 0)},
		{"1 ธ.ค. บ่ายโมง", bkk(12, 1, 13, 0)},
		{"12/11/69 19:00", bkk(11, 12, 19, 0)},
		{"12/11/2026 19:00", bkk(11, 12, 19, 0)},
		{"12/11/2569 19:00", bkk(11, 12, 19, 0)},
		{"19:00 12/11", bkk(11, 12, 19, 0)},
		{"5/1 10:00", time.Date(2027, 1, 5, 10, 0, 0, 0, Bangkok)}, // January is next year
		{"3/11 20:00", bkk(11, 3, 20, 0)},
		{"12/11 เที่ยงคืน", bkk(11, 13, 0, 0)},

		// English.
		{"today 19:00", bkk(11, 3, 19, 0)},
		{"tonight 9pm", bkk(11, 3, 21, 0)},
		{"tomorrow 8pm", bkk(11, 4, 20, 0)},
		{"Tomorrow at 8:30 PM", bkk(11, 4, 20, 30)},
		{"fri 9pm", bkk(11, 6, 21, 0)},
		{"Friday 21:00", bkk(11, 6, 21, 0)},
		{"next friday 21:00", bkk(11, 13, 21, 0)},
		{"nov 20 7:30pm", bkk(11, 20, 19, 30)},
		{"20 nov 19:00", bkk(11, 20, 19, 0)},
		{"tomorrow noon", bkk(11, 4, 12, 0)},
		{"tomorrow 12am", bkk(11, 4, 0, 0)},
		{"midnight", bkk(11, 4, 0, 0)},
	}
	for _, tt := range tests {
		got, err := ParseStart(tt.in, now)
		if err != nil {
			t.Errorf("ParseStart(%q): %v", tt.in, err)
			continue
		}
		if !got.Equal(tt.want) {
			t.Errorf("ParseStart(%q) = %s, want %s", tt.in, got.Format(time.RFC3339), tt.want.Format(time.RFC3339))
		}
		if got.Location() != Bangkok {
			t.Errorf("ParseStart(%q) is in %s, want Asia/Bangkok", tt.in, got.Location())
		}
	}
}

func TestParseStart_UsesBangkokWhateverTheClockZone(t *testing.T) {
	utc := now.UTC() // 11:00 UTC is 18:00 in Bangkok
	got, err := ParseStart("20:00", utc)
	if err != nil {
		t.Fatal(err)
	}
	if want := bkk(11, 3, 20, 0); !got.Equal(want) {
		t.Fatalf("got %s, want %s", got, want)
	}
}

func TestParseStart_Errors(t *testing.T) {
	tests := []struct {
		in   string
		want error
	}{
		{"", ErrEmpty},
		{"   ", ErrEmpty},
		{"ศุกร์นี้", ErrNoTime},
		{"พรุ่งนี้", ErrNoTime},
		{"12/11", ErrNoTime},
		{"วันนี้ 10:00", ErrPast},
		{"วันนี้ 18:00", ErrPast},
		{"2026-11-01 10:00", ErrPast},
		{"3/11 17:00", ErrPast}, // today without a year is not moved to next year
		{"1/11/2026 19:00", ErrPast},
		{"25:00", ErrInvalid},
		{"19:75", ErrInvalid},
		{"31/11 19:00", ErrInvalid},
		{"29/2 10:00", ErrInvalid},
		{"2026-13-01 10:00", ErrInvalid},
		{"แปดทุ่ม", ErrInvalid},
		{"ตี 7", ErrInvalid},
		{"บ่าย 7", ErrInvalid},
		{"แปดโมงเย็น", ErrInvalid},
		{"13pm", ErrInvalid},
	}
	for _, tt := range tests {
		_, err := ParseStart(tt.in, now)
		if !errors.Is(err, tt.want) {
			t.Errorf("ParseStart(%q) error = %v, want %v", tt.in, err, tt.want)
		}
	}
}

func TestParseStart_UnknownText(t *testing.T) {
	for _, in := range []string{"blah", "พรุ่งนี้ ประชุม 20:00", "ศุกร์ 3 ทุ่ม ไม่รู้", "20:00 20:00"} {
		_, err := ParseStart(in, now)
		var unk *UnknownError
		if !errors.As(err, &unk) {
			t.Errorf("ParseStart(%q) error = %v, want UnknownError", in, err)
			continue
		}
		if unk.Text == "" || unk.Error() == "" {
			t.Errorf("ParseStart(%q): empty UnknownError", in)
		}
	}
}

func TestParseEnd(t *testing.T) {
	start := bkk(11, 6, 21, 0)
	tests := []struct {
		in   string
		want time.Time
	}{
		{"3 ชม.", bkk(11, 7, 0, 0)},
		{"3ชม", bkk(11, 7, 0, 0)},
		{"สามชั่วโมง", bkk(11, 7, 0, 0)},
		{"1.5 ชั่วโมง", bkk(11, 6, 22, 30)},
		{"2 ชั่วโมงครึ่ง", bkk(11, 6, 23, 30)},
		{"90 นาที", bkk(11, 6, 22, 30)},
		{"1 ชม. 30 นาที", bkk(11, 6, 22, 30)},
		{"ครึ่งชั่วโมง", bkk(11, 6, 21, 30)},
		{"1h30m", bkk(11, 6, 22, 30)},
		{"2h", bkk(11, 6, 23, 0)},
		{"2 hours", bkk(11, 6, 23, 0)},
		{"45 min", bkk(11, 6, 21, 45)},
		{"23:00", bkk(11, 6, 23, 0)},
		{"ห้าทุ่มครึ่ง", bkk(11, 6, 23, 30)},
		{"ตีหนึ่ง", bkk(11, 7, 1, 0)},
		{"เที่ยงคืน", bkk(11, 7, 0, 0)},
		{"เสาร์ 10:00", bkk(11, 7, 10, 0)},
		{"2026-11-07 02:00", bkk(11, 7, 2, 0)},
	}
	for _, tt := range tests {
		got, err := ParseEnd(tt.in, start, now)
		if err != nil {
			t.Errorf("ParseEnd(%q): %v", tt.in, err)
			continue
		}
		if !got.Equal(tt.want) {
			t.Errorf("ParseEnd(%q) = %s, want %s", tt.in, got.Format(time.RFC3339), tt.want.Format(time.RFC3339))
		}
	}
}

func TestParseEnd_Errors(t *testing.T) {
	start := bkk(11, 6, 21, 0)
	tests := []struct {
		in   string
		want error
	}{
		{"", ErrEmpty},
		{"0", ErrEndBeforeStart},
		{"0 นาที", ErrEndBeforeStart},
		{"2026-11-06 20:00", ErrEndBeforeStart},
		{"2026-11-06 21:00", ErrEndBeforeStart},
		{"800 ชม.", ErrTooLong},
		{"2027-01-01 10:00", ErrTooLong},
		{"25:00", ErrInvalid},
	}
	for _, tt := range tests {
		_, err := ParseEnd(tt.in, start, now)
		if !errors.Is(err, tt.want) {
			t.Errorf("ParseEnd(%q) error = %v, want %v", tt.in, err, tt.want)
		}
	}
	var unk *UnknownError
	if _, err := ParseEnd("นานๆ", start, now); !errors.As(err, &unk) {
		t.Errorf("ParseEnd(นานๆ) error = %v, want UnknownError", err)
	}
}
