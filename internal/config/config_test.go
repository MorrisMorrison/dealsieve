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
	want := Env{DataDir: "./data", Listen: ":8080", LogFormat: "text", JudgeURL: "https://openrouter.ai/api/v1",
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
	want := Env{DataDir: "/data", Listen: "127.0.0.1:9000", LogFormat: "json", TelegramToken: "tok", NtfyToken: "nt", JudgeURL: "https://openrouter.ai/api/v1",
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

func TestJudgeEnv(t *testing.T) {
	e, err := FromEnv(func(string) string { return "" })
	if err != nil || e.JudgeURL != "https://openrouter.ai/api/v1" || e.JudgeAPIKey != "" {
		t.Fatalf("defaults = %q %q %v", e.JudgeURL, e.JudgeAPIKey, err)
	}
	env := map[string]string{"DEALSIEVE_JUDGE_URL": "http://llm.example.test:11434/v1/", "DEALSIEVE_JUDGE_API_KEY": "k"}
	e, err = FromEnv(func(k string) string { return env[k] })
	if err != nil || e.JudgeURL != "http://llm.example.test:11434/v1" || e.JudgeAPIKey != "k" {
		t.Fatalf("set = %q %q %v", e.JudgeURL, e.JudgeAPIKey, err)
	}
	env["DEALSIEVE_JUDGE_API_KEY"] = " k\n"
	if e, err := FromEnv(func(k string) string { return env[k] }); err != nil || e.JudgeAPIKey != "k" {
		t.Fatalf("untrimmed key = %q %v", e.JudgeAPIKey, err)
	}
	env["DEALSIEVE_JUDGE_URL"] = "llm.example.test"
	if _, err := FromEnv(func(k string) string { return env[k] }); err == nil {
		t.Fatal("relative URL accepted")
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

func TestNtfyTokenHost(t *testing.T) {
	for in, want := range map[string]string{"": "", "ntfy.example.com": "ntfy.example.com", " Ntfy.Example.com:8080 ": "ntfy.example.com:8080"} {
		e, err := FromEnv(env(map[string]string{"DEALSIEVE_NTFY_TOKEN_HOST": in}))
		if err != nil || e.NtfyTokenHost != want {
			t.Errorf("%q: host %q, err %v, want %q", in, e.NtfyTokenHost, err, want)
		}
	}
	for _, in := range []string{"https://ntfy.example.com", "ntfy.example.com/path", "ntfy.example.com:abc", "a b", "ntfy.example.com:"} {
		if _, err := FromEnv(env(map[string]string{"DEALSIEVE_NTFY_TOKEN_HOST": in})); err == nil || !strings.Contains(err.Error(), "DEALSIEVE_NTFY_TOKEN_HOST") {
			t.Errorf("%q: err = %v", in, err)
		}
	}
}
