// Package config loads dealsieve's YAML config and the settings that come
// from environment variables (paths, listen address, secrets).
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

const MinPollInterval = time.Minute

type Config struct {
	Poll      Poll               `yaml:"poll" json:"poll"`
	Rules     Rules              `yaml:"rules" json:"rules"`
	Watchlist []string           `yaml:"watchlist" json:"watchlist"`
	Notify    []NotifyRule       `yaml:"notify" json:"-"`
	Channels  map[string]Channel `yaml:"channels" json:"-"`
	Env       Env                `yaml:"-" json:"-"`
}

type Poll struct {
	Trending Duration `yaml:"trending" json:"trending"`
	New      Duration `yaml:"new" json:"new"`
}

type Rules struct {
	MinTemperature    int            `yaml:"min_temperature" json:"min_temperature"`
	CategoryOverrides map[string]int `yaml:"category_overrides" json:"category_overrides"`
	MutedCategories   []string       `yaml:"muted_categories" json:"muted_categories"`
	MutedMerchants    []string       `yaml:"muted_merchants" json:"muted_merchants"`
	BlockKeywords     []string       `yaml:"block_keywords" json:"block_keywords"`
	Recurrence        Recurrence     `yaml:"recurrence" json:"recurrence"`
}

type Recurrence struct {
	Window  Duration `yaml:"window" json:"window"`
	MaxSeen int      `yaml:"max_seen" json:"max_seen"`
}

type NotifyRule struct {
	When     When     `yaml:"when"`
	Channels []string `yaml:"channels"`
}

// When is a notify condition: the scalar "watchlist" or {min_temperature: N}.
type When struct {
	Watchlist      bool
	MinTemperature int
}

func (w *When) UnmarshalYAML(n *yaml.Node) error {
	switch n.Kind {
	case yaml.ScalarNode:
		if n.Value != "watchlist" {
			return fmt.Errorf("line %d: unknown condition %q", n.Line, n.Value)
		}
		w.Watchlist = true
		return nil
	case yaml.MappingNode:
		for i := 0; i+1 < len(n.Content); i += 2 {
			k, v := n.Content[i], n.Content[i+1]
			if k.Value != "min_temperature" {
				return fmt.Errorf("line %d: unknown condition key %q", k.Line, k.Value)
			}
			if err := v.Decode(&w.MinTemperature); err != nil {
				return err
			}
		}
		return nil
	}
	return fmt.Errorf("line %d: condition must be \"watchlist\" or a mapping", n.Line)
}

type Channel struct {
	Type     string `yaml:"type"`
	URL      string `yaml:"url"`
	Topic    string `yaml:"topic"`
	Priority string `yaml:"priority"`
	ChatID   string `yaml:"chat_id"`
}

type Env struct {
	DataDir       string
	Listen        string
	LogFormat     string
	TelegramToken string
	NtfyToken     string
	MydealzURL    string
}

func Load(path string, getenv func(string) string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	cfg := defaults()
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	if err := dec.Decode(cfg); err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("parse config %s: %w", path, err)
	}
	cfg.Env = envFrom(getenv)
	if err := cfg.validate(); err != nil {
		return nil, fmt.Errorf("invalid config %s: %w", path, err)
	}
	return cfg, nil
}

func defaults() *Config {
	return &Config{
		Poll:  Poll{Trending: Duration(5 * time.Minute), New: Duration(2 * time.Minute)},
		Rules: Rules{Recurrence: Recurrence{Window: Duration(90 * 24 * time.Hour), MaxSeen: 2}},
	}
}

func envFrom(getenv func(string) string) Env {
	or := func(key, def string) string {
		if v := getenv(key); v != "" {
			return v
		}
		return def
	}
	return Env{
		DataDir:       or("DEALSIEVE_DATA_DIR", "./data"),
		Listen:        or("DEALSIEVE_LISTEN", ":8080"),
		LogFormat:     or("DEALSIEVE_LOG_FORMAT", "text"),
		TelegramToken: getenv("DEALSIEVE_TELEGRAM_TOKEN"),
		NtfyToken:     getenv("DEALSIEVE_NTFY_TOKEN"),
		MydealzURL:    or("DEALSIEVE_MYDEALZ_URL", "https://www.mydealz.de"),
	}
}

var ntfyPriorities = map[string]bool{"": true, "min": true, "low": true, "default": true, "high": true, "urgent": true}

func (c *Config) validate() error {
	var errs []error
	add := func(format string, args ...any) { errs = append(errs, fmt.Errorf(format, args...)) }

	if c.Poll.Trending.D() < MinPollInterval {
		add("poll.trending: must be at least %s", MinPollInterval)
	}
	if c.Poll.New.D() < MinPollInterval {
		add("poll.new: must be at least %s", MinPollInterval)
	}
	if c.Rules.Recurrence.Window.D() <= 0 {
		add("rules.recurrence.window: must be positive")
	}
	if c.Rules.Recurrence.MaxSeen < 1 {
		add("rules.recurrence.max_seen: must be at least 1")
	}
	if f := c.Env.LogFormat; f != "text" && f != "json" {
		add("DEALSIEVE_LOG_FORMAT: must be text or json, got %q", f)
	}
	for name, ch := range c.Channels {
		switch ch.Type {
		case "ntfy":
			if ch.URL == "" || ch.Topic == "" {
				add("channel %q: ntfy needs url and topic", name)
			}
			if !ntfyPriorities[ch.Priority] {
				add("channel %q: unknown priority %q", name, ch.Priority)
			}
		case "telegram":
			if ch.ChatID == "" {
				add("channel %q: telegram needs chat_id", name)
			}
			if c.Env.TelegramToken == "" {
				add("channel %q: set DEALSIEVE_TELEGRAM_TOKEN", name)
			}
		default:
			add("channel %q: unknown type %q", name, ch.Type)
		}
	}
	for i, n := range c.Notify {
		if !n.When.Watchlist && n.When.MinTemperature <= 0 {
			add(`notify[%d]: when must be "watchlist" or {min_temperature: N}`, i)
		}
		if len(n.Channels) == 0 {
			add("notify[%d]: needs at least one channel", i)
		}
		for _, ch := range n.Channels {
			if _, ok := c.Channels[ch]; !ok {
				add("notify[%d]: unknown channel %q", i, ch)
			}
		}
	}
	return errors.Join(errs...)
}
