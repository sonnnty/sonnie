package readaloud

import "testing"

type fakeNames map[string]string

func (f fakeNames) User(id string) string    { return f["u"+id] }
func (f fakeNames) Role(id string) string    { return f["r"+id] }
func (f fakeNames) Channel(id string) string { return f["c"+id] }

func TestShouldSpeak(t *testing.T) {
	tests := []struct {
		name string
		msg  Message
		want bool
	}{
		{"human with text", Message{Content: "hi"}, true},
		{"bot", Message{Content: "hi", IsBot: true}, false},
		{"empty", Message{Content: ""}, false},
		{"whitespace only", Message{Content: " \n\t "}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ShouldSpeak(tt.msg); got != tt.want {
				t.Fatalf("expected %v, got %v", tt.want, got)
			}
		})
	}
}

func TestNormalize(t *testing.T) {
	names := fakeNames{"u1": "Somchai", "r2": "Mods", "c3": "general"}
	opts := TextOptions{LinkWord: "link", Names: names}

	tests := []struct {
		name string
		in   string
		opts TextOptions
		want string
	}{
		{"plain text", "hello world", opts, "hello world"},
		{"thai text", "สวัสดีครับ", opts, "สวัสดีครับ"},
		{"collapses whitespace", "  a \n\n b\t c ", opts, "a b c"},
		{"url", "see https://example.com/a_b?x=1 now", opts, "see link now"},
		{"suppressed-embed url", "see <https://example.com> now", opts, "see link now"},
		{"url dropped when no link word", "see https://example.com now", TextOptions{}, "see now"},
		{"user mention", "hi <@1>", opts, "hi Somchai"},
		{"nickname mention", "hi <@!1>", opts, "hi Somchai"},
		{"role mention", "ping <@&2>", opts, "ping Mods"},
		{"channel mention", "go to <#3>", opts, "go to general"},
		{"unknown mention dropped", "hi <@999>", opts, "hi"},
		{"mentions dropped without names", "hi <@1> in <#3>", TextOptions{}, "hi in"},
		{"custom emoji dropped", "nice <:pepe:123456> <a:dance:789>", opts, "nice"},
		{"unicode emoji kept", "nice 👍", opts, "nice 👍"},
		{"timestamp dropped", "at <t:1700000000:R> ok", opts, "at ok"},
		{"slash command mention", "use </tts join:42>", opts, "use tts join"},
		{"spoiler dropped", "the end is ||he dies|| lol", opts, "the end is lol"},
		{"code block dropped", "look ```go\nfunc main() {}\n``` ok", opts, "look ok"},
		{"markdown stripped", "**bold** _it_ ~~no~~ `code` __u__", opts, "bold _it_ no code u"},
		{"quote and heading markers", "> quoted\n# Title", opts, "quoted Title"},
		{"only emoji becomes empty", "<:pepe:123>", opts, ""},
		{"max chars", "abcdefghij", TextOptions{MaxChars: 4}, "abcd"},
		{"max chars counts runes", "สวัสดีครับ", TextOptions{MaxChars: 6}, "สวัสดี"},
		// "กี่" is ก + ี (combining) + ่ (combining); cutting after ก
		// would leave a bare consonant, so the whole cluster is dropped.
		{"max chars keeps thai clusters whole", "ab กี่", TextOptions{MaxChars: 4}, "ab"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Normalize(tt.in, tt.opts); got != tt.want {
				t.Fatalf("Normalize(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestUtterance(t *testing.T) {
	tests := []struct {
		author, text string
		speak        bool
		want         string
	}{
		{"Somchai", "สวัสดี", true, "Somchai: สวัสดี"},
		{"Somchai", "สวัสดี", false, "สวัสดี"},
		{"  ", "สวัสดี", true, "สวัสดี"},
	}
	for _, tt := range tests {
		if got := Utterance(tt.author, tt.text, tt.speak); got != tt.want {
			t.Errorf("Utterance(%q, %q, %v) = %q, want %q", tt.author, tt.text, tt.speak, got, tt.want)
		}
	}
}

func TestDefaultLinkWord(t *testing.T) {
	if got := DefaultLinkWord("th"); got != "ลิงก์" {
		t.Errorf("th: got %q", got)
	}
	if got := DefaultLinkWord("en"); got != "link" {
		t.Errorf("en: got %q", got)
	}
}
