package commands

import "testing"

func TestFind_ReturnsRegisteredCommand(t *testing.T) {
	if _, ok := Find("ping"); !ok {
		t.Fatal("expected \"ping\" command to be registered")
	}
}

func TestFind_ReturnsFalseForUnknownCommand(t *testing.T) {
	if _, ok := Find("does-not-exist"); ok {
		t.Fatal("expected unknown command to not be found")
	}
}
