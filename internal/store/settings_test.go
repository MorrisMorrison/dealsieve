package store

import (
	"errors"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/MorrisMorrison/dealsieve/internal/source"
)

func TestSettingsVersions(t *testing.T) {
	s := openTest(t)
	if _, err := s.LatestSettings(ctx); !errors.Is(err, ErrNotFound) {
		t.Fatalf("empty store: %v", err)
	}
	v1, err := s.SaveSettings(ctx, []byte(`{"a":1}`), []string{"Initial settings"}, t0)
	if err != nil {
		t.Fatal(err)
	}
	v2, err := s.SaveSettings(ctx, []byte(`{"a":2}`), []string{"Added x", "Removed y"}, t0.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	latest, err := s.LatestSettings(ctx)
	if err != nil || latest.ID != v2.ID || string(latest.Document) != `{"a":2}` ||
		!slices.Equal(latest.Summary, []string{"Added x", "Removed y"}) || !latest.CreatedAt.Equal(t0.Add(time.Minute)) {
		t.Errorf("latest = %+v, %v", latest, err)
	}
	prev, err := s.SettingsBefore(ctx, v2.ID)
	if err != nil || prev.ID != v1.ID {
		t.Errorf("before v2 = %+v, %v", prev, err)
	}
	if _, err := s.SettingsBefore(ctx, v1.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("before v1: %v", err)
	}
	hist, err := s.SettingsHistory(ctx, 1)
	if err != nil || len(hist) != 1 || hist[0].ID != v2.ID {
		t.Errorf("history = %+v, %v", hist, err)
	}
}

func TestDismissedSuggestions(t *testing.T) {
	s := openTest(t)
	for range 2 {
		if err := s.DismissSuggestion(ctx, ActionMuteMerchant, "Example Air", t0); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.DismissedSuggestions(ctx)
	if err != nil || len(got) != 1 || !got["mute_merchant|example air"] {
		t.Errorf("dismissed = %v, %v", got, err)
	}
}

func TestClickedDealsSince(t *testing.T) {
	s := openTest(t)
	mk := func(id, merchant, category, fp string, at time.Time) {
		d := deal(id, nil)
		d.Merchant, d.Category = merchant, category
		if _, err := s.UpsertDeal(ctx, d, source.FeedTrending, "x", fp, at); err != nil {
			t.Fatal(err)
		}
	}
	mk("1", "Example Air", "Reisen", "air|a", t0)
	mk("2", "Example Air", "Reisen", "air|b", t0)
	mk("3", "Example Air", "Reisen", "air|b", t0.Add(time.Hour)) // newer deal with the junked fingerprint
	mk("4", "Shop", "Elektronik", "shop|c", t0)
	add := func(kind, value string, at time.Time) {
		if _, err := s.AddAction(ctx, kind, value, at); err != nil {
			t.Fatal(err)
		}
	}
	add(ActionHide, "mydealz:1", t0.Add(-40*24*time.Hour)) // outside the window
	add(ActionHide, "mydealz:4", t0.Add(time.Minute))
	add(ActionJunk, "air|b", t0.Add(2*time.Minute))
	add(ActionHide, "mydealz:404", t0.Add(3*time.Minute)) // pruned deal: skipped

	got, err := s.ClickedDealsSince(ctx, t0.Add(-30*24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, c := range got {
		ids = append(ids, c.DealID)
	}
	if !slices.Equal(ids, []string{"mydealz:4", "mydealz:3"}) {
		t.Fatalf("clicked = %+v", got)
	}
	if got[1].Merchant != "Example Air" || got[1].Category != "Reisen" || !got[1].ClickedAt.Equal(t0.Add(2*time.Minute)) {
		t.Errorf("junk row = %+v", got[1])
	}
}

func TestCategories(t *testing.T) {
	s := openTest(t)
	for i, c := range []string{"Reisen", "", "Elektronik", "Reisen"} {
		d := deal(string(rune('a'+i)), nil)
		d.Category = c
		if _, err := s.UpsertDeal(ctx, d, source.FeedTrending, "x", "fp", t0); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.Categories(ctx, nil)
	if err != nil || !slices.Equal(got, []string{"Elektronik", "Reisen"}) {
		t.Errorf("categories = %q, %v", got, err)
	}
}

func TestDeleteActionsByValue(t *testing.T) {
	s := openTest(t)
	s.AddAction(ctx, ActionJunk, "fp", t0)
	s.AddAction(ctx, ActionHide, "fp", t0)
	n, err := s.DeleteActionsByValue(ctx, ActionJunk, "fp")
	if err != nil || n != 1 {
		t.Fatalf("deleted %d, %v", n, err)
	}
	if left, _ := s.Actions(ctx); len(left) != 1 || left[0].Kind != ActionHide {
		t.Errorf("left = %+v", left)
	}
}

func TestMigrationDropsMuteActions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "m.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	s.AddAction(ctx, ActionMuteMerchant, "Example Air", t0)
	s.AddAction(ctx, ActionHide, "mydealz:1", t0)
	// Pretend only migration 001 ran, so 002 runs again on the next open.
	if _, err := s.db.Exec(`ALTER TABLE verdicts DROP COLUMN block_code`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`PRAGMA user_version = 1`); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	left, _ := s.Actions(ctx)
	if len(left) != 1 || left[0].Kind != ActionHide {
		t.Errorf("actions after migration = %+v", left)
	}
}
