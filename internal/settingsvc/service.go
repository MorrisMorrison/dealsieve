// Package settingsvc loads, validates, versions and applies the settings,
// and turns the user's clicks into rule suggestions.
package settingsvc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/MorrisMorrison/dealsieve/internal/settings"
	"github.com/MorrisMorrison/dealsieve/internal/store"
	"github.com/MorrisMorrison/dealsieve/internal/suggest"
)

// Applier puts settings into effect (the pipeline).
type Applier interface {
	ApplySettings(settings.Settings)
	RecomputeRecent(ctx context.Context) (int, error)
}

var (
	ErrConflict      = errors.New("the settings were changed in the meantime")
	ErrNothingToUndo = errors.New("there is no earlier version to go back to")
)

// ValidationError maps field paths to problems.
type ValidationError map[string]string

func (e ValidationError) Error() string { return fmt.Sprintf("%d invalid settings", len(e)) }

type Snapshot struct {
	Version   int64
	UpdatedAt time.Time
	Settings  settings.Settings
}

type Result struct {
	Snapshot
	Rechecked int
	Changed   bool
}

type HistoryEntry struct {
	Version   int64     `json:"version"`
	CreatedAt time.Time `json:"created_at"`
	Summary   []string  `json:"summary"`
}

// Service serialises all settings changes; the current settings are only
// read through Current.
type Service struct {
	Store *store.Store
	Apply Applier
	Now   func() time.Time

	mu  sync.Mutex
	cur Snapshot
}

// Load reads the newest stored settings (saving the defaults on first start)
// and applies them without re-checking deals.
func (s *Service) Load(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, err := s.Store.LatestSettings(ctx)
	if errors.Is(err, store.ErrNotFound) {
		v, err = s.write(ctx, settings.Defaults(), []string{"Initial settings"})
	}
	if err != nil {
		return err
	}
	cur, err := settings.Decode(v.Document)
	if err != nil {
		return fmt.Errorf("stored settings version %d: %w", v.ID, err)
	}
	s.cur = Snapshot{Version: v.ID, UpdatedAt: v.CreatedAt, Settings: cur.Normalize()}
	s.Apply.ApplySettings(s.cur.Settings)
	return nil
}

// Current returns the settings in effect. Treat them as read-only.
func (s *Service) Current() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cur
}

func (s *Service) Save(ctx context.Context, version int64, next settings.Settings) (Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if version != s.cur.Version {
		return Result{}, ErrConflict
	}
	next = next.Normalize()
	if errs := next.Validate(); len(errs) > 0 {
		return Result{}, ValidationError(errs)
	}
	return s.commit(ctx, next, settings.Summarize(s.cur.Settings, next))
}

// Undo saves the version before the current one as a new version.
func (s *Service) Undo(ctx context.Context) (Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	prev, err := s.Store.SettingsBefore(ctx, s.cur.Version)
	if errors.Is(err, store.ErrNotFound) {
		return Result{}, ErrNothingToUndo
	}
	if err != nil {
		return Result{}, err
	}
	latest, err := s.Store.LatestSettings(ctx)
	if err != nil {
		return Result{}, err
	}
	next, err := settings.Decode(prev.Document)
	if err != nil {
		return Result{}, fmt.Errorf("stored settings version %d: %w", prev.ID, err)
	}
	return s.commit(ctx, next.Normalize(), []string{"Undid: " + strings.Join(latest.Summary, "; ")})
}

// Mute adds a shop or category to the muted lists; note is appended to the
// history summary (e.g. " (from a deal)").
func (s *Service) Mute(ctx context.Context, kind, value, note string) (Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	next, err := clone(s.cur.Settings)
	if err != nil {
		return Result{}, err
	}
	switch kind {
	case suggest.KindMuteMerchant:
		next.Rules.MutedMerchants = append(next.Rules.MutedMerchants, value)
	case suggest.KindMuteCategory:
		next.Rules.MutedCategories = append(next.Rules.MutedCategories, value)
	default:
		return Result{}, fmt.Errorf("unknown mute kind %q", kind)
	}
	next = next.Normalize()
	summary := settings.Summarize(s.cur.Settings, next)
	for i := range summary {
		summary[i] += note
	}
	return s.commit(ctx, next, summary)
}

func (s *Service) History(ctx context.Context, limit int) ([]HistoryEntry, error) {
	versions, err := s.Store.SettingsHistory(ctx, limit)
	if err != nil {
		return nil, err
	}
	out := make([]HistoryEntry, 0, len(versions))
	for _, v := range versions {
		out = append(out, HistoryEntry{Version: v.ID, CreatedAt: v.CreatedAt, Summary: v.Summary})
	}
	return out, nil
}

func (s *Service) Suggestions(ctx context.Context) ([]suggest.Suggestion, error) {
	cur := s.Current().Settings
	since := s.Now().Add(-time.Duration(cur.Suggestions.WindowDays) * 24 * time.Hour)
	clicks, err := s.Store.ClickedDealsSince(ctx, since)
	if err != nil {
		return nil, err
	}
	dismissed, err := s.Store.DismissedSuggestions(ctx)
	if err != nil {
		return nil, err
	}
	return suggest.Compute(clicks, cur, dismissed), nil
}

func (s *Service) ApplySuggestion(ctx context.Context, kind, value string) (Result, error) {
	return s.Mute(ctx, kind, value, " (suggested)")
}

func (s *Service) Dismiss(ctx context.Context, kind, value string) error {
	if kind != suggest.KindMuteMerchant && kind != suggest.KindMuteCategory {
		return fmt.Errorf("unknown suggestion kind %q", kind)
	}
	return s.Store.DismissSuggestion(ctx, kind, value, s.Now())
}

// commit stores next as a new version (if anything changed), applies it and
// re-checks recent deals. Callers hold s.mu. The change is saved and applied
// even when the error is a re-check error; the returned Result then reports
// the new version with Changed set and Rechecked zero.
func (s *Service) commit(ctx context.Context, next settings.Settings, summary []string) (Result, error) {
	if len(summary) == 0 {
		return Result{Snapshot: s.cur}, nil
	}
	v, err := s.write(ctx, next, summary)
	if err != nil {
		return Result{}, err
	}
	s.cur = Snapshot{Version: v.ID, UpdatedAt: v.CreatedAt, Settings: next}
	s.Apply.ApplySettings(next)
	// The change is saved; finish re-checking even if the client goes away.
	n, err := s.Apply.RecomputeRecent(context.WithoutCancel(ctx))
	if err != nil {
		return Result{Snapshot: s.cur, Changed: true}, fmt.Errorf("re-check deals: %w", err)
	}
	return Result{Snapshot: s.cur, Rechecked: n, Changed: true}, nil
}

func (s *Service) write(ctx context.Context, next settings.Settings, summary []string) (store.SettingsVersion, error) {
	doc, err := json.Marshal(next)
	if err != nil {
		return store.SettingsVersion{}, err
	}
	return s.Store.SaveSettings(ctx, doc, summary, s.Now())
}

func clone(s settings.Settings) (settings.Settings, error) {
	raw, err := json.Marshal(s)
	if err != nil {
		return settings.Settings{}, err
	}
	return settings.Decode(raw)
}
