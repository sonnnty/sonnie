package config

import (
	"reflect"
	"strings"
	"testing"
)

const testSessionKey = "0123456789abcdef0123456789abcdef"

func lookupFrom(m map[string]string) func(string) (string, bool) {
	return func(key string) (string, bool) {
		v, ok := m[key]
		return v, ok
	}
}

func TestLoad_DashboardDefaults(t *testing.T) {
	cfg, err := Load(envFrom(map[string]string{"DISCORD_BOT_TOKEN": "t"}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := DashboardConfig{Addr: ":8080"}
	if !reflect.DeepEqual(cfg.Dashboard, want) {
		t.Fatalf("expected %+v, got %+v", want, cfg.Dashboard)
	}
	if cfg.Dashboard.LoginEnabled() {
		t.Fatal("login must be off without secret, URL and key")
	}
	if cfg.Webhooks != nil {
		t.Fatalf("expected no webhooks, got %+v", cfg.Webhooks)
	}
}

func TestLoad_DashboardFull(t *testing.T) {
	cfg, err := Load(envFrom(map[string]string{
		"DISCORD_BOT_TOKEN":       "t",
		"DASHBOARD_ADDR":          ":9090",
		"DISCORD_CLIENT_ID":       "123456789012345678",
		"DISCORD_CLIENT_SECRET":   "shh",
		"DASHBOARD_PUBLIC_URL":    "http://10.10.5.35:31120/",
		"DASHBOARD_SESSION_KEY":   testSessionKey,
		"DASHBOARD_ALLOWED_USERS": " 111 , 222 ,",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := DashboardConfig{
		Addr:         ":9090",
		ClientID:     "123456789012345678",
		ClientSecret: "shh",
		PublicURL:    "http://10.10.5.35:31120",
		SessionKey:   []byte(testSessionKey),
		AllowedUsers: []string{"111", "222"},
	}
	if !reflect.DeepEqual(cfg.Dashboard, want) {
		t.Fatalf("expected %+v, got %+v", want, cfg.Dashboard)
	}
	if !cfg.Dashboard.LoginEnabled() {
		t.Fatal("login should be enabled")
	}
	if got := cfg.Dashboard.RedirectURI(); got != "http://10.10.5.35:31120/auth/callback" {
		t.Fatalf("redirect URI %q", got)
	}
}

func TestLoadEnv_EmptyAddrTurnsServerOff(t *testing.T) {
	for _, v := range []string{"", "off", "OFF"} {
		cfg, err := LoadEnv(lookupFrom(map[string]string{"DISCORD_BOT_TOKEN": "t", "DASHBOARD_ADDR": v}))
		if err != nil {
			t.Fatalf("%q: unexpected error: %v", v, err)
		}
		if cfg.Dashboard.Addr != "" {
			t.Fatalf("%q: expected the server off, got %q", v, cfg.Dashboard.Addr)
		}
	}
	cfg, err := LoadEnv(lookupFrom(map[string]string{"DISCORD_BOT_TOKEN": "t"}))
	if err != nil || cfg.Dashboard.Addr != ":8080" {
		t.Fatalf("unset DASHBOARD_ADDR: got %q, %v", cfg.Dashboard.Addr, err)
	}
}

func TestLoad_DashboardInvalid(t *testing.T) {
	full := map[string]string{
		"DISCORD_BOT_TOKEN":     "t",
		"DISCORD_CLIENT_SECRET": "shh",
		"DASHBOARD_PUBLIC_URL":  "http://10.0.0.1:31120",
		"DASHBOARD_SESSION_KEY": testSessionKey,
	}
	cases := map[string]map[string]string{
		"short key":       {"DASHBOARD_SESSION_KEY": "too-short"},
		"bad url":         {"DASHBOARD_PUBLIC_URL": "10.0.0.1:31120"},
		"ftp url":         {"DASHBOARD_PUBLIC_URL": "ftp://10.0.0.1"},
		"url with query":  {"DASHBOARD_PUBLIC_URL": "http://10.0.0.1/?a=b"},
		"bad client id":   {"DISCORD_CLIENT_ID": "abc"},
		"bad allowed":     {"DASHBOARD_ALLOWED_USERS": "111,bob"},
		"missing secret":  {"DISCORD_CLIENT_SECRET": ""},
		"missing url":     {"DASHBOARD_PUBLIC_URL": ""},
		"missing session": {"DASHBOARD_SESSION_KEY": ""},
	}
	for name, override := range cases {
		env := map[string]string{}
		for k, v := range full {
			env[k] = v
		}
		for k, v := range override {
			env[k] = v
		}
		_, err := Load(envFrom(env))
		if err == nil {
			t.Errorf("%s: expected an error", name)
			continue
		}
		if strings.Contains(err.Error(), testSessionKey) || strings.Contains(err.Error(), "shh") {
			t.Errorf("%s: error leaks a secret: %v", name, err)
		}
	}
}

func TestLoad_Webhooks(t *testing.T) {
	cfg, err := Load(envFrom(map[string]string{
		"DISCORD_BOT_TOKEN": "t",
		"WEBHOOKS":          " grafana = abcdefghijklmnop@111 , ci-2=tok@en-with-at-sign-x@222,",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []WebhookConfig{
		{Name: "grafana", Token: "abcdefghijklmnop", ChannelID: "111"},
		{Name: "ci-2", Token: "tok@en-with-at-sign-x", ChannelID: "222"},
	}
	if !reflect.DeepEqual(cfg.Webhooks, want) {
		t.Fatalf("expected %+v, got %+v", want, cfg.Webhooks)
	}
}

func TestLoad_WebhooksInvalid(t *testing.T) {
	const tok = "abcdefghijklmnopqrstuvwxyz"
	for _, raw := range []string{
		"grafana",
		"Grafana=" + tok + "@111",
		"graf/ana=" + tok + "@111",
		"=" + tok + "@111",
		"grafana=" + tok,
		"grafana=short@111",
		"grafana=" + tok + "@chan",
		"grafana=" + tok + "@111,grafana=" + tok + "@222",
		"grafana=has space in the token@111",
	} {
		_, err := Load(envFrom(map[string]string{"DISCORD_BOT_TOKEN": "t", "WEBHOOKS": raw}))
		if err == nil {
			t.Errorf("%q: expected an error", raw)
			continue
		}
		if strings.Contains(err.Error(), tok) {
			t.Errorf("%q: error leaks the token: %v", raw, err)
		}
	}
}
