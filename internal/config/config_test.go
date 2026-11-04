package config

import (
	"reflect"
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
	want := Env{DataDir: "./data", Listen: ":8080", LogFormat: "text",
		PollTrending: 5 * time.Minute, PollNew: 2 * time.Minute}
	e.SourceURLs = nil
	if !reflect.DeepEqual(e, want) {
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
		PollTrending: 10 * time.Minute, PollNew: time.Minute}
	if e.SourceURLs["mydealz"] != "http://127.0.0.1:1" {
		t.Errorf("mydealz URL = %q", e.SourceURLs["mydealz"])
	}
	e.SourceURLs = nil
	if !reflect.DeepEqual(e, want) {
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

func TestPublicURL(t *testing.T) {
	env := func(v string) func(string) string {
		return func(k string) string {
			if k == "DEALSIEVE_PUBLIC_URL" {
				return v
			}
			return ""
		}
	}
	e, err := FromEnv(env("https://deals.example.com/"))
	if err != nil || e.PublicURL != "https://deals.example.com" {
		t.Errorf("PublicURL = %q, %v", e.PublicURL, err)
	}
	if e, err := FromEnv(env("")); err != nil || e.PublicURL != "" {
		t.Errorf("unset = %q, %v", e.PublicURL, err)
	}
	for _, bad := range []string{"deals.example.com", "ftp://x.example.com", "https://"} {
		if _, err := FromEnv(env(bad)); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestSourceURLs(t *testing.T) {
	env := map[string]string{"DEALSIEVE_HOTUKDEALS_URL": "http://127.0.0.1:9999"}
	e, err := FromEnv(func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	if got := e.SourceURLs["hotukdeals"]; got != "http://127.0.0.1:9999" {
		t.Errorf("hotukdeals = %q", got)
	}
	if got := e.SourceURLs["pepperpl"]; got != "https://www.pepper.pl" {
		t.Errorf("pepperpl default = %q", got)
	}
	if len(e.SourceURLs) != 6 {
		t.Errorf("got %d source URLs, want 6", len(e.SourceURLs))
	}
	env["DEALSIEVE_DEALABS_URL"] = "ftp://example.test"
	if _, err := FromEnv(func(k string) string { return env[k] }); err == nil || !strings.Contains(err.Error(), "DEALSIEVE_DEALABS_URL") {
		t.Errorf("bad URL error = %v", err)
	}
}
