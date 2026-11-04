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
	n, last, err := s.CountRecurrence(ctx, "mydealz", "shop|x", "mydealz:me", t0, 90*day)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 || !last.Equal(t0.Add(-day)) {
		t.Errorf("count %d, last %v; want 2 and %v", n, last, t0.Add(-day))
	}
	// An older deal keeps its count when re-posts arrive after it.
	if n, _, _ := s.CountRecurrence(ctx, "mydealz", "shop|x", "mydealz:b", t0.Add(-10*day), 90*day); n != 1 {
		t.Errorf("count for b = %d, want 1 (only a)", n)
	}
	if n, _, _ := s.CountRecurrence(ctx, "mydealz", "shop|none", "x", t0, 90*day); n != 0 {
		t.Errorf("count = %d, want 0", n)
	}
}

func TestCountRecurrenceSamePollCountsOnce(t *testing.T) {
	s := openTest(t)
	mustUpsert(t, s, deal("1", nil), source.FeedTrending, "shop|x", t0)
	mustUpsert(t, s, deal("2", nil), source.FeedTrending, "shop|x", t0)
	n1, _, _ := s.CountRecurrence(ctx, "mydealz", "shop|x", "mydealz:1", t0, time.Hour)
	n2, _, _ := s.CountRecurrence(ctx, "mydealz", "shop|x", "mydealz:2", t0, time.Hour)
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
	mustUpsert(t, s, b, source.FeedTrending, "shop|galaxy", t0.Add(time.Hour))
	junk := map[string]time.Time{
		"shop|galaxy":     t0.Add(time.Minute), // clicked on a; b came later
		"shop|galaxy s24": t0,                  // already current: the move above would duplicate it
		"shop|ssd gb":     t0,                  // no deal has it any more
	}
	for v, at := range junk {
		if _, err := s.AddAction(ctx, ActionJunk, v, at); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.AddAction(ctx, ActionHide, "mydealz:a", t0); err != nil {
		t.Fatal(err)
	}
	key := func(merchant, title string) (string, string) {
		return title, strings.ToLower(merchant + "|" + title)
	}
	rekeyOld := func(fp string) string { return strings.TrimSuffix(fp, " gb") }

	n, err := s.Rekey(ctx, key, rekeyOld)
	if err != nil || n != 2 {
		t.Fatalf("Rekey = %d, %v; want 2 deals changed", n, err)
	}
	if rec, _ := s.GetDeal(ctx, "mydealz:a"); rec.Fingerprint != "shop|galaxy s24" || rec.TitleClean != "Galaxy S24" {
		t.Errorf("deal a = %+v", rec)
	}
	if got := junkValues(t, s); !slices.Equal(got, []string{"shop|galaxy s24", "shop|ssd"}) {
		t.Errorf("junk = %v", got)
	}
	if n, err := s.Rekey(ctx, key, rekeyOld); n != 0 || err != nil {
		t.Errorf("second Rekey = %d, %v; want 0", n, err)
	}
	if got := junkValues(t, s); !slices.Equal(got, []string{"shop|galaxy s24", "shop|ssd"}) {
		t.Errorf("junk after second Rekey = %v", got)
	}
}

func junkValues(t *testing.T, s *Store) []string {
	t.Helper()
	list, err := s.Actions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, a := range list {
		if a.Kind == ActionJunk {
			out = append(out, a.Value)
		}
	}
	slices.Sort(out)
	return out
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
		got, err := s.ListDeals(ctx, view, nil, t0.Add(-time.Hour), 100)
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(ids(got), want) {
			t.Errorf("%s = %v, want %v", view, ids(got), want)
		}
	}
	got, _ := s.ListDeals(ctx, ViewBlocked, nil, t0.Add(-time.Hour), 100)
	if v := got[0].Verdict; v.Decision != "block" || v.Score != 50 || !slices.Equal(v.Reasons, []string{"r-mydealz:c"}) {
		t.Errorf("verdict = %+v", v)
	}
	if got, _ := s.ListDeals(ctx, ViewPassed, nil, t0.Add(time.Hour), 100); len(got) != 0 {
		t.Errorf("since filter: got %v", ids(got))
	}
	if _, err := s.ListDeals(ctx, "nope", nil, t0, 10); err == nil {
		t.Error("want an error for an unknown view")
	}

	counts, err := s.CountViews(ctx, nil, t0.Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if want := map[string]int{ViewPassed: 2, ViewWatchlist: 1, ViewBlocked: 1}; !maps.Equal(counts, want) {
		t.Errorf("counts = %v, want %v", counts, want)
	}
	counts, err = s.CountViews(ctx, nil, t0.Add(time.Hour))
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

func TestAddActionIsIdempotent(t *testing.T) {
	s := openTest(t)
	a1, err := s.AddAction(ctx, ActionJunk, "shop|a", t0)
	if err != nil {
		t.Fatal(err)
	}
	a2, err := s.AddAction(ctx, ActionJunk, "shop|a", t0.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if a2.ID != a1.ID || !a2.CreatedAt.Equal(t0) {
		t.Errorf("repeat = %+v, want the first action %+v", a2, a1)
	}
	// Same value under another kind is a different click.
	if _, err := s.AddAction(ctx, ActionHide, "shop|a", t0); err != nil {
		t.Fatal(err)
	}
	if list, _ := s.Actions(ctx); len(list) != 2 {
		t.Errorf("actions = %+v, want 2", list)
	}
	if ok, err := s.DeleteAction(ctx, a1.ID); !ok || err != nil {
		t.Fatalf("delete = %v, %v", ok, err)
	}
	if list, _ := s.Actions(ctx); len(list) != 1 || list[0].Kind != ActionHide {
		t.Errorf("after undo = %+v", list)
	}
}

func TestMigrationDedupesActions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "d.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	// Go back to the schema before migration 006: no unique index, duplicates allowed.
	if _, err := s.db.Exec(`DROP INDEX actions_kind_value`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`PRAGMA user_version = 5`); err != nil {
		t.Fatal(err)
	}
	for _, r := range []struct {
		kind, value string
		at          int64
	}{
		{ActionJunk, "fp", 10}, {ActionJunk, "fp", 20}, {ActionHide, "fp", 30},
		{ActionHide, "mydealz:1", 40}, {ActionHide, "mydealz:1", 50}, {ActionHide, "mydealz:1", 60},
	} {
		if _, err := s.db.Exec(`INSERT INTO actions (kind, value, created_at) VALUES (?, ?, ?)`, r.kind, r.value, r.at); err != nil {
			t.Fatal(err)
		}
	}
	s.Close()
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	list, _ := s.Actions(ctx)
	got := map[string]int64{}
	for _, a := range list {
		got[a.Kind+"/"+a.Value] = a.CreatedAt.Unix()
	}
	want := map[string]int64{"junk/fp": 10, "hide_deal/fp": 30, "hide_deal/mydealz:1": 40}
	if len(list) != 3 || !maps.Equal(got, want) {
		t.Errorf("actions after migration = %+v, want the oldest of each", list)
	}
}

func TestRekeyKeepsActionsUnique(t *testing.T) {
	s := openTest(t)
	// Two junk clicks whose fingerprints both re-key to the same new one.
	mustUpsert(t, s, deal("1", nil), source.FeedTrending, "old-a", t0)
	mustUpsert(t, s, deal("2", nil), source.FeedTrending, "old-b", t0)
	s.AddAction(ctx, ActionJunk, "old-a", t0.Add(time.Minute))
	s.AddAction(ctx, ActionJunk, "old-b", t0.Add(time.Minute))
	if _, err := s.Rekey(ctx, func(m, title string) (string, string) { return title, "same" }, func(string) string { return "same" }); err != nil {
		t.Fatalf("rekey: %v", err)
	}
	if list, _ := s.Actions(ctx); len(list) != 1 || list[0].Value != "same" {
		t.Errorf("actions = %+v, want one junk on the shared key", list)
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

func TestClicks(t *testing.T) {
	s := openTest(t)
	mustUpsert(t, s, deal("a", nil), source.FeedTrending, "shop|alpha", t0)
	older := deal("b", nil)
	older.Title = "Older junk deal"
	mustUpsert(t, s, older, source.FeedTrending, "shop|beta gamma", t0)
	newer := deal("c", nil)
	newer.Title = "Newer junk deal"
	mustUpsert(t, s, newer, source.FeedTrending, "shop|beta gamma", t0.Add(time.Hour))

	mustAdd := func(kind, value string, at time.Time) Action {
		a, err := s.AddAction(ctx, kind, value, at)
		if err != nil {
			t.Fatal(err)
		}
		return a
	}
	mustAdd(ActionHide, "mydealz:a", t0)
	mustAdd(ActionJunk, "shop|beta gamma", t0.Add(time.Minute))
	mustAdd(ActionHide, "mydealz:gone", t0.Add(2*time.Minute))      // pruned deal
	mustAdd(ActionJunk, "shop|nothing left", t0.Add(3*time.Minute)) // no deal with that key
	mustAdd(ActionJunk, "-|bare", t0.Add(4*time.Minute))
	mustAdd(ActionMuteCategory, "Elektronik", t0.Add(5*time.Minute)) // not a click

	got, err := s.Clicks(ctx, 200)
	if err != nil {
		t.Fatal(err)
	}
	type row struct{ kind, label, url string }
	var rows []row
	for _, c := range got {
		rows = append(rows, row{c.Kind, c.Label, c.URL})
	}
	want := []row{
		{ActionJunk, "bare", ""},
		{ActionJunk, "nothing left", ""},
		{ActionHide, "A deal that is no longer stored", ""},
		{ActionJunk, "Newer junk deal", "https://www.mydealz.de/deals/x-c"},
		{ActionHide, "Deal a", "https://www.mydealz.de/deals/x-a"},
	}
	if !slices.Equal(rows, want) {
		t.Errorf("clicks = %+v\nwant     %+v", rows, want)
	}
	if got[0].ID == 0 || !got[0].CreatedAt.Equal(t0.Add(4*time.Minute)) {
		t.Errorf("first click = %+v", got[0])
	}

	limited, err := s.Clicks(ctx, 2)
	if err != nil || len(limited) != 2 || limited[1].Label != "nothing left" {
		t.Errorf("limited = %+v, %v", limited, err)
	}
	empty := openTest(t)
	if none, err := empty.Clicks(ctx, 200); err != nil || len(none) != 0 {
		t.Errorf("empty = %+v, %v", none, err)
	}
}
