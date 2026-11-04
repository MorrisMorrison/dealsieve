// Package config reads the settings that come from environment variables:
// paths, the listen address, polling intervals and secrets. Everything the
// user edits lives in the settings package and on the Settings page.
package config

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
)

const MinPollInterval = time.Minute

type Env struct {
	DataDir       string
	Listen        string
	LogFormat     string
	TelegramToken string
	NtfyToken     string
	PublicURL     string
	JudgeURL      string
	JudgeAPIKey   string
	MydealzURL    string
	PollTrending  time.Duration
	PollNew       time.Duration
}

func FromEnv(getenv func(string) string) (Env, error) {
	or := func(key, def string) string {
		if v := getenv(key); v != "" {
			return v
		}
		return def
	}
	var errs []error
	poll := func(key string, def time.Duration) time.Duration {
		v := getenv(key)
		if v == "" {
			return def
		}
		d, err := time.ParseDuration(v)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", key, err))
			return def
		}
		if d < MinPollInterval {
			errs = append(errs, fmt.Errorf("%s: must be at least %s", key, MinPollInterval))
		}
		return d
	}
	e := Env{
		DataDir:       or("DEALSIEVE_DATA_DIR", "./data"),
		Listen:        or("DEALSIEVE_LISTEN", ":8080"),
		LogFormat:     or("DEALSIEVE_LOG_FORMAT", "text"),
		TelegramToken: getenv("DEALSIEVE_TELEGRAM_TOKEN"),
		NtfyToken:     getenv("DEALSIEVE_NTFY_TOKEN"),
		JudgeAPIKey:   getenv("DEALSIEVE_JUDGE_API_KEY"),
		MydealzURL:    or("DEALSIEVE_MYDEALZ_URL", "https://www.mydealz.de"),
		PollTrending:  poll("DEALSIEVE_POLL_TRENDING", 5*time.Minute),
		PollNew:       poll("DEALSIEVE_POLL_NEW", 2*time.Minute),
	}
	if v := getenv("DEALSIEVE_PUBLIC_URL"); v != "" {
		u, err := url.Parse(v)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			errs = append(errs, fmt.Errorf("DEALSIEVE_PUBLIC_URL: must be an absolute http(s) URL, got %q", v))
		} else {
			e.PublicURL = strings.TrimRight(v, "/")
		}
	}
	judgeURL := or("DEALSIEVE_JUDGE_URL", "https://openrouter.ai/api/v1")
	if u, err := url.Parse(judgeURL); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		errs = append(errs, fmt.Errorf("DEALSIEVE_JUDGE_URL: must be an absolute http(s) URL, got %q", judgeURL))
	} else {
		e.JudgeURL = strings.TrimRight(judgeURL, "/")
	}
	if e.LogFormat != "text" && e.LogFormat != "json" {
		errs = append(errs, fmt.Errorf("DEALSIEVE_LOG_FORMAT: must be text or json, got %q", e.LogFormat))
	}
	return e, errors.Join(errs...)
}
