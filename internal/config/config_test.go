package config

import (
	"strings"
	"testing"
	"time"
)

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func TestDefaults(t *testing.T) {
	e, err := FromEnv(env(nil))
	if err != nil {
		t.Fatal(err)
	}
	want := Env{DataDir: "./data", Listen: ":8080", LogFormat: "text", MydealzURL: "https://www.mydealz.de",
		PollTrending: 5 * time.Minute, PollNew: 2 * time.Minute}
	if e != want {
		t.Errorf("env = %+v, want %+v", e, want)
	}
}

func TestOverrides(t *testing.T) {
	e, err := FromEnv(env(map[string]string{
		"DEALSIEVE_DATA_DIR": "/data", "DEALSIEVE_LISTEN": "127.0.0.1:9000", "DEALSIEVE_LOG_FORMAT": "json",
		"DEALSIEVE_TELEGRAM_TOKEN": "tok", "DEALSIEVE_NTFY_TOKEN": "nt", "DEALSIEVE_MYDEALZ_URL": "http://127.0.0.1:1",
		"DEALSIEVE_POLL_TRENDING": "10m", "DEALSIEVE_POLL_NEW": "1m",
	}))
	if err != nil {
		t.Fatal(err)
	}
	want := Env{DataDir: "/data", Listen: "127.0.0.1:9000", LogFormat: "json", TelegramToken: "tok", NtfyToken: "nt",
		MydealzURL: "http://127.0.0.1:1", PollTrending: 10 * time.Minute, PollNew: time.Minute}
	if e != want {
		t.Errorf("env = %+v, want %+v", e, want)
	}
}

func TestErrors(t *testing.T) {
	for in, want := range map[string]string{
		"DEALSIEVE_POLL_TRENDING=30s": "DEALSIEVE_POLL_TRENDING: must be at least 1m0s",
		"DEALSIEVE_POLL_NEW=soon":     "DEALSIEVE_POLL_NEW",
		"DEALSIEVE_LOG_FORMAT=xml":    "DEALSIEVE_LOG_FORMAT",
	} {
		k, v, _ := strings.Cut(in, "=")
		_, err := FromEnv(env(map[string]string{k: v}))
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: err = %v, want it to contain %q", in, err, want)
		}
	}
}
