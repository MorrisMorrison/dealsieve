package store

import (
	"context"
	"errors"
	"maps"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/MorrisMorrison/dealsieve/internal/source"
)

var (
	ctx = context.Background()
	t0  = time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
)

func openTest(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func intp(v int) *int { return &v }

func deal(id string, temp *int) source.Deal {
	return source.Deal{
		ID: "mydealz:" + id, Source: "mydealz", URL: "https://www.mydealz.de/deals/x-" + id,
		Title: "Deal " + id, Merchant: "Shop", Price: "9€", Category: "Elektronik", Temperature: temp,
	}
}

func mustUpsert(t *testing.T, s *Store, d source.Deal, feed source.Feed, fp string, at time.Time) DealRecord {
	t.Helper()
	rec, err := s.UpsertDeal(ctx, d, feed, "deal", fp, at)
	if err != nil {
		t.Fatal(err)
	}
	return rec
}

func TestOpenIsRepeatable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	for range 2 {
		s, err := Open(path)
		if err != nil {
			t.Fatal(err)
		}
		s.Close()
	}
}

func TestUpsertMergesFeedsAndKeepsHistory(t *testing.T) {
	s := openTest(t)
	mustUpsert(t, s, deal("1", intp(100)), source.FeedTrending, "fp", t0)
	rec := mustUpsert(t, s, deal("1", nil), source.FeedNew, "fp", t0.Add(5*time.Minute))
	if !rec.InTrending || !rec.InNew {
		t.Errorf("feeds = trending %v, new %v; want both", rec.InTrending, rec.InNew)
	}
	if !rec.FirstSeen.Equal(t0) || !rec.LastSeen.Equal(t0.Add(5*time.Minute)) {
		t.Errorf("first %v, last %v", rec.FirstSeen, rec.LastSeen)
	}
	if rec.Temperature == nil || *rec.Temperature != 100 {
		t.Errorf("temperature = %v, want 100 kept", rec.Temperature)
	}
	mustUpsert(t, s, deal("1", intp(150)), source.FeedTrending, "fp", t0.Add(10*time.Minute))
	temps, err := s.Temperatures(ctx, []string{"mydealz:1", "mydealz:404"})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(temps["mydealz:1"], []int{100, 150}) || len(temps["mydealz:404"]) != 0 {
		t.Errorf("temperatures = %v", temps)
	}
}

func TestGetDealNotFound(t *testing.T) {
	if _, err := openTest(t).GetDeal(ctx, "mydealz:404"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestDealsSeenSince(t *testing.T) {
	s := openTest(t)
	mustUpsert(t, s, deal("old", nil), source.FeedTrending, "a", t0.Add(-10*24*time.Hour))
	mustUpsert(t, s, deal("new", nil), source.FeedTrending, "b", t0)
	recs, err := s.DealsSeenSince(ctx, t0.Add(-7*24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 1 || recs[0].ID != "mydealz:new" {
		t.Errorf("got %v", recs)
	}
}

func TestCountRecurrence(t *testing.T) {
	s := openTest(t)
	day := 24 * time.Hour
	mustUpsert(t, s, deal("a", nil), source.FeedTrending, "shop|x", t0.Add(-100*day))
	mustUpsert(t, s, deal("b", nil), source.FeedTrending, "shop|x", t0.Add(-10*day))
	mustUpsert(t, s, deal("c", nil), source.FeedTrending, "shop|x", t0.Add(-day))
	mustUpsert(t, s, deal("other", nil), source.FeedTrending, "shop|y", t0)
	mustUpsert(t, s, deal("me", nil), source.FeedTrending, "shop|x", t0)
	mustUpsert(t, s, deal("later", nil), source.FeedTrending, "shop|x", t0.Add(day))

	// Only earlier deals count, within the window that ends at this deal.
	n, last, err := s.CountRecurrence(ctx, "shop|x", "mydealz:me", t0, 90*day)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 || !last.Equal(t0.Add(-day)) {
		t.Errorf("count %d, last %v; want 2 and %v", n, last, t0.Add(-day))
	}
	// An older deal keeps its count when re-posts arrive after it.
	if n, _, _ := s.CountRecurrence(ctx, "shop|x", "mydealz:b", t0.Add(-10*day), 90*day); n != 1 {
		t.Errorf("count for b = %d, want 1 (only a)", n)
	}
	if n, _, _ := s.CountRecurrence(ctx, "shop|none", "x", t0, 90*day); n != 0 {
		t.Errorf("count = %d, want 0", n)
	}
}

func TestCountRecurrenceSamePollCountsOnce(t *testing.T) {
	s := openTest(t)
	mustUpsert(t, s, deal("1", nil), source.FeedTrending, "shop|x", t0)
	mustUpsert(t, s, deal("2", nil), source.FeedTrending, "shop|x", t0)
	n1, _, _ := s.CountRecurrence(ctx, "shop|x", "mydealz:1", t0, time.Hour)
	n2, _, _ := s.CountRecurrence(ctx, "shop|x", "mydealz:2", t0, time.Hour)
	if n1+n2 != 1 {
		t.Errorf("counts %d and %d; want one of them to see the other", n1, n2)
	}
}

func TestRekey(t *testing.T) {
	s := openTest(t)
	a := deal("a", nil)
	a.Title = "Galaxy S24"
	b := deal("b", nil)
	b.Title = "Galaxy S25"
	mustUpsert(t, s, a, source.FeedTrending, "shop|galaxy", t0)
	mustUpsert(t, s, b, source.FeedTrending, "shop|galaxy", t0)
	for _, v := range []string{"shop|galaxy", "shop|gone"} {
		if _, err := s.AddAction(ctx, ActionJunk, v, t0); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.AddAction(ctx, ActionHide, "mydealz:a", t0); err != nil {
		t.Fatal(err)
	}
	key := func(merchant, title string) (string, string) {
		return title, strings.ToLower(merchant + "|" + title)
	}

	n, err := s.Rekey(ctx, key)
	if err != nil || n != 2 {
		t.Fatalf("Rekey = %d, %v; want 2 deals changed", n, err)
	}
	if rec, _ := s.GetDeal(ctx, "mydealz:a"); rec.Fingerprint != "shop|galaxy s24" || rec.TitleClean != "Galaxy S24" {
		t.Errorf("deal a = %+v", rec)
	}
	// A junk click on the old key now covers every deal that had it; one
	// whose key no deal has any more stays as it was.
	var junk []string
	list, _ := s.Actions(ctx)
	for _, a := range list {
		if a.Kind == ActionJunk {
			junk = append(junk, a.Value)
		}
	}
	slices.Sort(junk)
	if want := []string{"shop|galaxy s24", "shop|galaxy s25", "shop|gone"}; !slices.Equal(junk, want) {
		t.Errorf("junk = %v, want %v", junk, want)
	}
	if n, err := s.Rekey(ctx, key); n != 0 || err != nil {
		t.Errorf("second Rekey = %d, %v; want 0", n, err)
	}
}

func save(t *testing.T, s *Store, id, decision string, score int, watch bool) {
	t.Helper()
	if err := s.SaveVerdict(ctx, Verdict{DealID: id, Decision: decision, Score: score, WatchlistHit: watch, Reasons: []string{"r-" + id}, DecidedAt: t0}); err != nil {
		t.Fatal(err)
	}
}

func ids(ls []Listed) []string {
	var out []string
	for _, l := range ls {
		out = append(out, l.ID)
	}
	return out
}

func TestListDealsViews(t *testing.T) {
	s := openTest(t)
	mustUpsert(t, s, deal("a", intp(150)), source.FeedTrending, "a", t0) // trending pass
	mustUpsert(t, s, deal("b", nil), source.FeedNew, "b", t0)            // new-only watchlist pass
	mustUpsert(t, s, deal("c", intp(50)), source.FeedTrending, "c", t0)  // trending block
	mustUpsert(t, s, deal("d", nil), source.FeedNew, "d", t0)            // new-only block
	mustUpsert(t, s, deal("e", intp(300)), source.FeedTrending, "e", t0) // trending pass, higher score
	save(t, s, "mydealz:a", "pass", 150, false)
	save(t, s, "mydealz:b", "pass", 1000, true)
	save(t, s, "mydealz:c", "block", 50, false)
	save(t, s, "mydealz:d", "block", 0, false)
	save(t, s, "mydealz:e", "pass", 300, false)

	for view, want := range map[string][]string{
		ViewPassed:    {"mydealz:e", "mydealz:a"},
		ViewWatchlist: {"mydealz:b"},
		ViewBlocked:   {"mydealz:c"},
	} {
		got, err := s.ListDeals(ctx, view, t0.Add(-time.Hour), 100)
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(ids(got), want) {
			t.Errorf("%s = %v, want %v", view, ids(got), want)
		}
	}
	got, _ := s.ListDeals(ctx, ViewBlocked, t0.Add(-time.Hour), 100)
	if v := got[0].Verdict; v.Decision != "block" || v.Score != 50 || !slices.Equal(v.Reasons, []string{"r-mydealz:c"}) {
		t.Errorf("verdict = %+v", v)
	}
	if got, _ := s.ListDeals(ctx, ViewPassed, t0.Add(time.Hour), 100); len(got) != 0 {
		t.Errorf("since filter: got %v", ids(got))
	}
	if _, err := s.ListDeals(ctx, "nope", t0, 10); err == nil {
		t.Error("want an error for an unknown view")
	}

	counts, err := s.CountViews(ctx, t0.Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if want := map[string]int{ViewPassed: 2, ViewWatchlist: 1, ViewBlocked: 1}; !maps.Equal(counts, want) {
		t.Errorf("counts = %v, want %v", counts, want)
	}
	counts, err = s.CountViews(ctx, t0.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if want := map[string]int{ViewPassed: 0, ViewWatchlist: 0, ViewBlocked: 0}; !maps.Equal(counts, want) {
		t.Errorf("counts after the cut-off = %v, want %v", counts, want)
	}
}

func TestGetVerdict(t *testing.T) {
	s := openTest(t)
	mustUpsert(t, s, deal("a", nil), source.FeedTrending, "a", t0)
	if _, err := s.GetVerdict(ctx, "mydealz:a"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
	save(t, s, "mydealz:a", "pass", 7, true)
	v, err := s.GetVerdict(ctx, "mydealz:a")
	if err != nil || v.Score != 7 || !v.WatchlistHit || !v.DecidedAt.Equal(t0) {
		t.Fatalf("verdict = %+v, %v", v, err)
	}
}

func TestActions(t *testing.T) {
	s := openTest(t)
	a1, err := s.AddAction(ctx, ActionHide, "mydealz:1", t0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddAction(ctx, ActionMuteCategory, "Reisen", t0.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	list, err := s.Actions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].Kind != ActionMuteCategory || list[1].ID != a1.ID || !list[1].CreatedAt.Equal(t0) {
		t.Errorf("actions = %+v", list)
	}
	if ok, err := s.DeleteAction(ctx, a1.ID); !ok || err != nil {
		t.Errorf("delete = %v, %v", ok, err)
	}
	if ok, _ := s.DeleteAction(ctx, a1.ID); ok {
		t.Error("second delete reported found")
	}
	if !ValidActionKind(ActionJunk) || ValidActionKind("explode") {
		t.Error("ValidActionKind is wrong")
	}
}

func TestNotifications(t *testing.T) {
	s := openTest(t)
	mustUpsert(t, s, deal("1", nil), source.FeedTrending, "a", t0)
	if err := s.RecordNotification(ctx, "mydealz:1", "push", false, t0); err != nil {
		t.Fatal(err)
	}
	if sent, attempts, _ := s.NotificationState(ctx, "mydealz:1", "push"); sent || attempts != 1 {
		t.Errorf("after failure: sent %v, attempts %d", sent, attempts)
	}
	pending, err := s.PendingNotifications(ctx, 3)
	if err != nil || len(pending) != 1 || pending[0] != (PendingNotification{"mydealz:1", "push"}) {
		t.Errorf("pending = %v, %v", pending, err)
	}
	if err := s.RecordNotification(ctx, "mydealz:1", "push", true, t0); err != nil {
		t.Fatal(err)
	}
	if sent, attempts, _ := s.NotificationState(ctx, "mydealz:1", "push"); !sent || attempts != 2 {
		t.Errorf("after success: sent %v, attempts %d", sent, attempts)
	}
	if pending, _ := s.PendingNotifications(ctx, 3); len(pending) != 0 {
		t.Errorf("pending after success = %v", pending)
	}
	if sent, attempts, err := s.NotificationState(ctx, "mydealz:1", "other"); sent || attempts != 0 || err != nil {
		t.Errorf("unknown channel: %v %d %v", sent, attempts, err)
	}
}

func TestPruneCascades(t *testing.T) {
	s := openTest(t)
	mustUpsert(t, s, deal("old", intp(10)), source.FeedTrending, "a", t0.Add(-200*24*time.Hour))
	mustUpsert(t, s, deal("new", intp(10)), source.FeedTrending, "b", t0)
	save(t, s, "mydealz:old", "pass", 10, false)
	n, err := s.Prune(ctx, t0.Add(-180*24*time.Hour))
	if err != nil || n != 1 {
		t.Fatalf("pruned %d, %v; want 1", n, err)
	}
	if _, err := s.GetDeal(ctx, "mydealz:old"); !errors.Is(err, ErrNotFound) {
		t.Errorf("old deal still there: %v", err)
	}
	if _, err := s.GetVerdict(ctx, "mydealz:old"); !errors.Is(err, ErrNotFound) {
		t.Errorf("old verdict still there: %v", err)
	}
	temps, _ := s.Temperatures(ctx, []string{"mydealz:old"})
	if len(temps["mydealz:old"]) != 0 {
		t.Errorf("old snapshots still there: %v", temps)
	}
}

func TestSaveVerdicts(t *testing.T) {
	s := openTest(t)
	mustUpsert(t, s, deal("a", intp(10)), source.FeedTrending, "fa", t0)
	mustUpsert(t, s, deal("b", intp(10)), source.FeedTrending, "fb", t0)
	vs := []Verdict{
		{DealID: "mydealz:a", Decision: "pass", Score: 5, WatchlistHit: true, Reasons: []string{"x"}, DecidedAt: t0},
		{DealID: "mydealz:b", Decision: "block", Score: 1, DecidedAt: t0},
	}
	if err := s.SaveVerdicts(ctx, vs); err != nil {
		t.Fatal(err)
	}
	vs[1].Decision = "pass"
	if err := s.SaveVerdicts(ctx, vs[1:]); err != nil {
		t.Fatal(err)
	}
	a, err := s.GetVerdict(ctx, "mydealz:a")
	if err != nil || a.Decision != "pass" || !a.WatchlistHit || !slices.Equal(a.Reasons, []string{"x"}) {
		t.Errorf("a = %+v, %v", a, err)
	}
	b, err := s.GetVerdict(ctx, "mydealz:b")
	if err != nil || b.Decision != "pass" {
		t.Errorf("b = %+v, %v", b, err)
	}
	if err := s.SaveVerdicts(ctx, nil); err != nil {
		t.Errorf("empty: %v", err)
	}
}
