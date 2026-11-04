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

func TestFindComponent_RoutesEventButtons(t *testing.T) {
	c, ok := FindComponent("event:cancel:123")
	if !ok || c.Definition.Name != "event" {
		t.Fatalf("event button routed to %v, %v", c.Definition, ok)
	}
	if _, ok := FindComponent("other:1"); ok {
		t.Fatal("unknown button was routed")
	}
}

func TestEvent_IsRegisteredWithAutocomplete(t *testing.T) {
	c, ok := Find("event")
	if !ok || c.Autocomplete == nil || c.Component == nil {
		t.Fatal("/event is not registered with autocomplete and buttons")
	}
}

func TestFindModal_RoutesEventForms(t *testing.T) {
	c, ok := FindModal("event:form:1")
	if !ok || c.Definition.Name != "event" || c.Modal == nil {
		t.Fatalf("event form routed to %v, %v", c.Definition, ok)
	}
	if _, ok := FindModal("other:form"); ok {
		t.Fatal("unknown modal was routed")
	}
}
