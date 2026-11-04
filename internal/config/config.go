// Package config reads the settings that come from environment variables:
// paths, the listen address, polling intervals and secrets. Everything the
// user edits lives in the settings package and on the Settings page.
package config

import (
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/MorrisMorrison/dealsieve/internal/source/pepper"
)

const MinPollInterval = time.Minute

type Env struct {
	DataDir       string
	Listen        string
	LogFormat     string
	TelegramToken string
	NtfyToken     string
	NtfyTokenHost string // host[:port] the ntfy token may be sent to; empty = any ntfy server
	PublicURL     string
	JudgeURL      string
	JudgeAPIKey   string
	SourceURLs    map[string]string // site name to feed base URL
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
		JudgeAPIKey:   strings.TrimSpace(getenv("DEALSIEVE_JUDGE_API_KEY")),
		PollTrending:  poll("DEALSIEVE_POLL_TRENDING", 5*time.Minute),
		PollNew:       poll("DEALSIEVE_POLL_NEW", 2*time.Minute),
	}
	e.SourceURLs = make(map[string]string, len(pepper.Sites()))
	for _, s := range pepper.Sites() {
		key := "DEALSIEVE_" + strings.ToUpper(s.Name) + "_URL"
		v := getenv(key)
		if v == "" {
			e.SourceURLs[s.Name] = s.BaseURL
			continue
		}
		if u, err := url.Parse(v); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			errs = append(errs, fmt.Errorf("%s: must be an absolute http(s) URL, got %q", key, v))
			continue
		}
		e.SourceURLs[s.Name] = strings.TrimRight(v, "/")
	}
	if v := strings.ToLower(strings.TrimSpace(getenv("DEALSIEVE_NTFY_TOKEN_HOST"))); v != "" {
		if !validTokenHost(v) {
			errs = append(errs, fmt.Errorf("DEALSIEVE_NTFY_TOKEN_HOST: must be a host or host:port without scheme or path, got %q", v))
		} else {
			e.NtfyTokenHost = v
		}
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

// validTokenHost accepts "host" or "host:port" and nothing else: no scheme,
// path, query, user info or spaces.
func validTokenHost(v string) bool {
	if strings.ContainsAny(v, "/?#@ 	\\") {
		return false
	}
	u, err := url.Parse("http://" + v)
	if err != nil || u.Host != v || u.Hostname() == "" {
		return false
	}
	if p := u.Port(); p != "" || strings.HasSuffix(v, ":") {
		n, err := strconv.Atoi(p)
		return err == nil && n > 0 && n < 65536
	}
	return true
}
