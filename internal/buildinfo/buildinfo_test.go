package buildinfo

import "testing"

func TestString_PrefersLinkerVersion(t *testing.T) {
	old := Version
	defer func() { Version = old }()
	Version = "abc1234"
	if got := String(); got != "abc1234" {
		t.Fatalf("got %q", got)
	}
	Version = ""
	if got := String(); got == "" {
		t.Fatal("empty version")
	}
}
