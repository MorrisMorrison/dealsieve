package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

const fullConfig = `
poll:
  trending: 5m
  new: 2m
rules:
  min_temperature: 100
  category_overrides: { "Elektronik": 60 }
  muted_categories: ["Fashion & Accessoires"]
  muted_merchants: [Example Air]
  block_keywords: [gratisprobe]
  recurrence: { window: 30d, max_seen: 3 }
watchlist: [oled, rtx 5080]
notify:
  - when: watchlist
    channels: [ntfy-high, telegram]
  - when: { min_temperature: 500 }
    channels: [ntfy-high]
channels:
  ntfy-high: { type: ntfy, url: https://ntfy.example.com, topic: deals, priority: high }
  telegram: { type: telegram, chat_id: "123" }
`

func TestLoadFullConfig(t *testing.T) {
	cfg, err := Load(writeConfig(t, fullConfig), env(map[string]string{"DEALSIEVE_TELEGRAM_TOKEN": "tok"}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Poll.Trending.D() != 5*time.Minute || cfg.Poll.New.D() != 2*time.Minute {
		t.Errorf("poll = %+v", cfg.Poll)
	}
	if cfg.Rules.MinTemperature != 100 || cfg.Rules.CategoryOverrides["Elektronik"] != 60 {
		t.Errorf("rules = %+v", cfg.Rules)
	}
	if cfg.Rules.Recurrence.Window.D() != 30*24*time.Hour || cfg.Rules.Recurrence.MaxSeen != 3 {
		t.Errorf("recurrence = %+v", cfg.Rules.Recurrence)
	}
	if cfg.Rules.MutedCategories[0] != "Fashion & Accessoires" {
		t.Errorf("muted categories = %v", cfg.Rules.MutedCategories)
	}
	if !cfg.Notify[0].When.Watchlist || cfg.Notify[1].When.MinTemperature != 500 {
		t.Errorf("notify = %+v", cfg.Notify)
	}
	if cfg.Channels["telegram"].ChatID != "123" || cfg.Channels["ntfy-high"].Priority != "high" {
		t.Errorf("channels = %+v", cfg.Channels)
	}
	want := Env{DataDir: "./data", Listen: ":8080", LogFormat: "text", TelegramToken: "tok", MydealzURL: "https://www.mydealz.de"}
	if cfg.Env != want {
		t.Errorf("env = %+v, want %+v", cfg.Env, want)
	}
}

func TestDefaults(t *testing.T) {
	cfg, err := Load(writeConfig(t, ""), env(nil))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Poll.Trending.D() != 5*time.Minute || cfg.Poll.New.D() != 2*time.Minute {
		t.Errorf("poll = %+v", cfg.Poll)
	}
	if cfg.Rules.Recurrence.Window.D() != 90*24*time.Hour || cfg.Rules.Recurrence.MaxSeen != 2 {
		t.Errorf("recurrence = %+v", cfg.Rules.Recurrence)
	}
}

func TestPartialRecurrenceKeepsDefaultMaxSeen(t *testing.T) {
	cfg, err := Load(writeConfig(t, "rules:\n  recurrence: { window: 30d }\n"), env(nil))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Rules.Recurrence.MaxSeen != 2 {
		t.Errorf("max_seen = %d, want default 2", cfg.Rules.Recurrence.MaxSeen)
	}
}

func TestEnvOverrides(t *testing.T) {
	cfg, err := Load(writeConfig(t, ""), env(map[string]string{
		"DEALSIEVE_DATA_DIR": "/data", "DEALSIEVE_LISTEN": "127.0.0.1:9000", "DEALSIEVE_LOG_FORMAT": "json",
		"DEALSIEVE_NTFY_TOKEN": "nt", "DEALSIEVE_MYDEALZ_URL": "http://127.0.0.1:1234",
	}))
	if err != nil {
		t.Fatal(err)
	}
	want := Env{DataDir: "/data", Listen: "127.0.0.1:9000", LogFormat: "json", NtfyToken: "nt", MydealzURL: "http://127.0.0.1:1234"}
	if cfg.Env != want {
		t.Errorf("env = %+v, want %+v", cfg.Env, want)
	}
}

func TestValidationErrors(t *testing.T) {
	tests := []struct {
		name, body string
		env        map[string]string
		want       string
	}{
		{"unknown key", "rules:\n  min_temp: 5\n", nil, "min_temp"},
		{"interval too short", "poll:\n  trending: 30s\n", nil, "poll.trending"},
		{"bad duration", "poll:\n  new: soon\n", nil, "invalid duration"},
		{"unknown channel type", "channels:\n  x: { type: email }\n", nil, `unknown type "email"`},
		{"ntfy without topic", "channels:\n  x: { type: ntfy, url: https://n.example.com }\n", nil, "needs url and topic"},
		{"bad ntfy priority", "channels:\n  x: { type: ntfy, url: https://n.example.com, topic: t, priority: loud }\n", nil, `unknown priority "loud"`},
		{"telegram without token", "channels:\n  x: { type: telegram, chat_id: \"1\" }\n", nil, "DEALSIEVE_TELEGRAM_TOKEN"},
		{"notify unknown channel", "notify:\n  - when: watchlist\n    channels: [nope]\n", nil, `unknown channel "nope"`},
		{"notify without channels", "notify:\n  - when: watchlist\n", nil, "at least one channel"},
		{"bad condition", "notify:\n  - when: always\n    channels: [x]\nchannels:\n  x: { type: ntfy, url: https://n.example.com, topic: t }\n", nil, `unknown condition "always"`},
		{"bad condition key", "notify:\n  - when: { max_temperature: 5 }\n    channels: [x]\nchannels:\n  x: { type: ntfy, url: https://n.example.com, topic: t }\n", nil, `unknown condition key "max_temperature"`},
		{"bad max_seen", "rules:\n  recurrence: { max_seen: 0 }\n", nil, "max_seen"},
		{"bad log format", "", map[string]string{"DEALSIEVE_LOG_FORMAT": "xml"}, "DEALSIEVE_LOG_FORMAT"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Load(writeConfig(t, tt.body), env(tt.env))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want it to contain %q", err, tt.want)
			}
		})
	}
}

func TestParseDuration(t *testing.T) {
	tests := []struct {
		in      string
		want    time.Duration
		wantErr bool
	}{
		{"90d", 90 * 24 * time.Hour, false},
		{"5m", 5 * time.Minute, false},
		{"1h30m", 90 * time.Minute, false},
		{"xd", 0, true},
		{"-1d", 0, true},
		{"soon", 0, true},
	}
	for _, tt := range tests {
		got, err := ParseDuration(tt.in)
		if (err != nil) != tt.wantErr || got != tt.want {
			t.Errorf("ParseDuration(%q) = %v, %v", tt.in, got, err)
		}
	}
}

func TestDurationJSON(t *testing.T) {
	for d, want := range map[Duration]string{
		Duration(90 * 24 * time.Hour): `"90d"`,
		Duration(5 * time.Minute):     `"5m0s"`,
	} {
		got, err := json.Marshal(d)
		if err != nil || string(got) != want {
			t.Errorf("Marshal(%v) = %s, %v; want %s", time.Duration(d), got, err, want)
		}
	}
}

func TestExampleConfigLoads(t *testing.T) {
	if _, err := Load("../../config.example.yaml", env(nil)); err != nil {
		t.Fatal(err)
	}
}
