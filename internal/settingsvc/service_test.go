package settingsvc

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/MorrisMorrison/dealsieve/internal/settings"
	"github.com/MorrisMorrison/dealsieve/internal/source"
	"github.com/MorrisMorrison/dealsieve/internal/store"
	"github.com/MorrisMorrison/dealsieve/internal/suggest"
)

var ctx = context.Background()

type fakeApplier struct {
	applied   []settings.Settings
	recompute int
}

func (f *fakeApplier) ApplySettings(s settings.Settings) { f.applied = append(f.applied, s) }
func (f *fakeApplier) RecomputeRecent(context.Context) (int, error) {
	f.recompute++
	return 42, nil
}

func newService(t *testing.T) (*Service, *fakeApplier, *store.Store, *time.Time) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "s.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	ap := &fakeApplier{}
	svc := &Service{Store: st, Apply: ap, Now: func() time.Time { return now }}
	if err := svc.Load(ctx); err != nil {
		t.Fatal(err)
	}
	return svc, ap, st, &now
}

func TestLoadCreatesDefaultsAndApplies(t *testing.T) {
	svc, ap, st, _ := newService(t)
	cur := svc.Current()
	if cur.Version == 0 || cur.Settings.Rules.MinTemperature != 100 || len(ap.applied) != 1 {
		t.Fatalf("current = %+v, applied %d", cur, len(ap.applied))
	}
	v, err := st.LatestSettings(ctx)
	if err != nil || !slices.Equal(v.Summary, []string{"Initial settings"}) {
		t.Errorf("stored = %+v, %v", v, err)
	}
	// A second service on the same store loads the stored version instead of new defaults.
	again := &Service{Store: st, Apply: &fakeApplier{}, Now: svc.Now}
	if err := again.Load(ctx); err != nil || again.Current().Version != cur.Version {
		t.Errorf("reload = %+v, %v", again.Current(), err)
	}
}

func TestSave(t *testing.T) {
	svc, ap, _, _ := newService(t)
	v1 := svc.Current().Version
	next := svc.Current().Settings
	next.Watchlist = []string{" oled ", "OLED"}
	res, err := svc.Save(ctx, v1, next)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Changed || res.Rechecked != 42 || res.Version == v1 || !slices.Equal(res.Settings.Watchlist, []string{"oled"}) {
		t.Errorf("result = %+v", res)
	}
	if len(ap.applied) != 2 || ap.recompute != 1 {
		t.Errorf("applied %d, recomputed %d", len(ap.applied), ap.recompute)
	}
	if _, err := svc.Save(ctx, v1, next); !errors.Is(err, ErrConflict) {
		t.Errorf("stale version: %v", err)
	}
	same, err := svc.Save(ctx, res.Version, next)
	if err != nil || same.Changed || same.Version != res.Version || ap.recompute != 1 {
		t.Errorf("unchanged save = %+v, %v (no new version, no recompute)", same, err)
	}
	bad := svc.Current().Settings
	bad.Rules.Recurrence.WindowDays = 0
	var verr ValidationError
	if _, err := svc.Save(ctx, res.Version, bad); !errors.As(err, &verr) || verr["rules.recurrence.window_days"] == "" {
		t.Errorf("invalid save: %v", err)
	}
	hist, err := svc.History(ctx, 10)
	if err != nil || len(hist) != 2 || !slices.Equal(hist[0].Summary, []string{`Added watchlist entry "oled"`}) {
		t.Errorf("history = %+v, %v", hist, err)
	}
}

func TestUndo(t *testing.T) {
	svc, _, _, _ := newService(t)
	if _, err := svc.Undo(ctx); !errors.Is(err, ErrNothingToUndo) {
		t.Fatalf("undo with one version: %v", err)
	}
	next := svc.Current().Settings
	next.Rules.MinTemperature = 80
	if _, err := svc.Save(ctx, svc.Current().Version, next); err != nil {
		t.Fatal(err)
	}
	res, err := svc.Undo(ctx)
	if err != nil || res.Settings.Rules.MinTemperature != 100 || !res.Changed {
		t.Fatalf("undo = %+v, %v", res, err)
	}
	hist, _ := svc.History(ctx, 1)
	if !slices.Equal(hist[0].Summary, []string{"Undid: Minimum temperature 100° → 80°"}) {
		t.Errorf("undo summary = %q", hist[0].Summary)
	}
}

func TestMuteAndSuggestions(t *testing.T) {
	svc, _, st, now := newService(t)
	for i, id := range []string{"1", "2", "3"} {
		d := source.Deal{ID: "mydealz:" + id, Source: "mydealz", URL: "https://www.mydealz.de/deals/x-" + id,
			Title: "Flash Sale " + id, Merchant: "Example Air", Category: "Reisen"}
		if _, err := st.UpsertDeal(ctx, d, source.FeedTrending, "x", "fp"+id, *now); err != nil {
			t.Fatal(err)
		}
		if _, err := st.AddAction(ctx, store.ActionHide, d.ID, now.Add(time.Duration(i)*time.Minute)); err != nil {
			t.Fatal(err)
		}
	}
	got, err := svc.Suggestions(ctx)
	if err != nil || len(got) != 2 {
		t.Fatalf("suggestions = %+v, %v", got, err)
	}
	res, err := svc.ApplySuggestion(ctx, suggest.KindMuteMerchant, "Example Air")
	if err != nil || !slices.Equal(res.Settings.Rules.MutedMerchants, []string{"Example Air"}) {
		t.Fatalf("apply = %+v, %v", res, err)
	}
	hist, _ := svc.History(ctx, 1)
	if !slices.Equal(hist[0].Summary, []string{`Added muted shop "Example Air" (suggested)`}) {
		t.Errorf("summary = %q", hist[0].Summary)
	}
	if err := svc.Dismiss(ctx, suggest.KindMuteCategory, "Reisen"); err != nil {
		t.Fatal(err)
	}
	if got, _ := svc.Suggestions(ctx); len(got) != 0 {
		t.Errorf("after apply + dismiss: %+v", got)
	}
	if _, err := svc.Mute(ctx, "mute_everything", "x", ""); err == nil {
		t.Error("unknown kind accepted")
	}
	if err := svc.Dismiss(ctx, "nope", "x"); err == nil {
		t.Error("unknown dismiss kind accepted")
	}
}

type failingApplier struct{ fakeApplier }

func (f *failingApplier) RecomputeRecent(context.Context) (int, error) {
	return 0, errors.New("pipeline down")
}

func TestRecomputeErrorKeepsSavedChange(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "s.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	ap := &failingApplier{}
	svc := &Service{Store: st, Apply: ap, Now: func() time.Time { return now }}
	if err := svc.Load(ctx); err != nil {
		t.Fatal(err)
	}
	v1 := svc.Current().Version
	next := svc.Current().Settings
	next.Rules.MinTemperature = 80
	res, err := svc.Save(ctx, v1, next)
	if err == nil {
		t.Fatal("expected re-check error")
	}
	if !res.Changed || res.Version == v1 || res.Rechecked != 0 {
		t.Errorf("result = %+v", res)
	}
	if got := svc.Current().Version; got != res.Version {
		t.Errorf("current version %d, want %d", got, res.Version)
	}
	hist, err := svc.History(ctx, 1)
	if err != nil || len(hist) != 1 || hist[0].Version != res.Version {
		t.Errorf("history = %+v, %v", hist, err)
	}
}

type blockingApplier struct {
	fakeApplier
	entered, release chan struct{}
}

func (b *blockingApplier) RecomputeRecent(context.Context) (int, error) {
	close(b.entered)
	<-b.release
	return 0, nil
}

// A slow re-check must not hold up readers such as the settings page's GET.
func TestCurrentDoesNotWaitForASave(t *testing.T) {
	svc, _, _, _ := newService(t)
	ba := &blockingApplier{entered: make(chan struct{}), release: make(chan struct{})}
	svc.Apply = ba
	next := svc.Current().Settings
	next.Rules.MinTemperature = 80
	version := svc.Current().Version
	saved := make(chan error, 1)
	go func() { _, err := svc.Save(ctx, version, next); saved <- err }()
	<-ba.entered

	got := make(chan Snapshot, 1)
	go func() { got <- svc.Current() }()
	select {
	case snap := <-got:
		if snap.Settings.Rules.MinTemperature != 80 {
			t.Errorf("current = %+v, want the new settings", snap)
		}
	case <-time.After(2 * time.Second):
		t.Error("Current blocked behind a running Save")
	}
	close(ba.release)
	if err := <-saved; err != nil {
		t.Fatal(err)
	}
}
