package pipeline

import (
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MorrisMorrison/dealsieve/internal/config"
	"github.com/MorrisMorrison/dealsieve/internal/fingerprint"
	"github.com/MorrisMorrison/dealsieve/internal/notify"
	"github.com/MorrisMorrison/dealsieve/internal/rules"
	"github.com/MorrisMorrison/dealsieve/internal/source"
	"github.com/MorrisMorrison/dealsieve/internal/store"
)

var ctx = context.Background()

type fakeNotifier struct {
	mu   sync.Mutex
	sent []notify.Message
	fail bool
}

func (f *fakeNotifier) Send(_ context.Context, m notify.Message) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail {
		return errors.New("boom")
	}
	f.sent = append(f.sent, m)
	return nil
}

func (f *fakeNotifier) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.sent)
}

var baseRules = config.Rules{
	MinTemperature: 100,
	BlockKeywords:  []string{"gratisprobe"},
	Recurrence:     config.Recurrence{Window: config.Duration(90 * 24 * time.Hour), MaxSeen: 2},
}

type harness struct {
	p    *Pipeline
	st   *store.Store
	now  *time.Time
	push *fakeNotifier
}

func newHarness(t *testing.T, when config.When) *harness {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "p.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	now := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	push := &fakeNotifier{}
	p := &Pipeline{
		Store:     st,
		Engine:    rules.New(baseRules, []string{"oled"}),
		Notify:    []config.NotifyRule{{When: when, Channels: []string{"push"}}},
		Notifiers: map[string]notify.Notifier{"push": push},
		Now:       func() time.Time { return now },
		Log:       slog.New(slog.DiscardHandler),
	}
	return &harness{p: p, st: st, now: &now, push: push}
}

func intp(v int) *int { return &v }

func deal(id, title string, temp *int) source.Deal {
	return source.Deal{ID: "mydealz:" + id, Source: "mydealz", URL: "https://www.mydealz.de/deals/x-" + id,
		Title: title, Merchant: "Example Shop", Price: "9€", Category: "Elektronik", Temperature: temp}
}

func (h *harness) ingest(t *testing.T, feed source.Feed, deals ...source.Deal) {
	t.Helper()
	if err := h.p.Ingest(ctx, feed, deals); err != nil {
		t.Fatal(err)
	}
}

func (h *harness) verdict(t *testing.T, id string) store.Verdict {
	t.Helper()
	v, err := h.st.GetVerdict(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestWatchlistDealIsNotifiedOnce(t *testing.T) {
	h := newHarness(t, config.When{Watchlist: true})
	d := deal("1", "Example OLED TV", intp(150))
	h.ingest(t, source.FeedTrending, d)
	h.ingest(t, source.FeedTrending, d)

	if v := h.verdict(t, "mydealz:1"); v.Decision != "pass" || !v.WatchlistHit {
		t.Errorf("verdict = %+v", v)
	}
	if h.push.count() != 1 {
		t.Fatalf("sent %d notifications, want 1", h.push.count())
	}
	if m := h.push.sent[0]; m.Title != "Example OLED TV" || !strings.Contains(m.Body, "watchlist: oled") {
		t.Errorf("message = %+v", m)
	}
}

func TestNewFeedWatchlistHitIsNotified(t *testing.T) {
	h := newHarness(t, config.When{Watchlist: true})
	h.ingest(t, source.FeedNew, deal("1", "Example OLED TV", nil), deal("2", "Example Kabel", nil))
	if h.push.count() != 1 {
		t.Errorf("sent %d, want 1", h.push.count())
	}
	if v := h.verdict(t, "mydealz:2"); v.Decision != "block" {
		t.Errorf("non-watchlist new-only deal = %+v, want block", v)
	}
}

func TestBlockedDealIsNotNotified(t *testing.T) {
	h := newHarness(t, config.When{Watchlist: true})
	h.ingest(t, source.FeedTrending, deal("1", "Gratisprobe OLED Reiniger", intp(150)))
	if h.push.count() != 0 {
		t.Errorf("sent %d, want 0", h.push.count())
	}
}

func TestFailedNotificationIsRetriedThreeTimesAtMost(t *testing.T) {
	h := newHarness(t, config.When{Watchlist: true})
	h.push.fail = true
	h.ingest(t, source.FeedTrending, deal("1", "Example OLED TV", intp(150))) // attempt 1
	h.ingest(t, source.FeedTrending)                                          // attempt 2 (retry)
	h.ingest(t, source.FeedTrending)                                          // attempt 3 (retry)
	h.push.fail = false
	h.ingest(t, source.FeedTrending) // no attempts left

	sent, attempts, err := h.st.NotificationState(ctx, "mydealz:1", "push")
	if err != nil || sent || attempts != 3 || h.push.count() != 0 {
		t.Errorf("sent %v, attempts %d, delivered %d, err %v", sent, attempts, h.push.count(), err)
	}
}

func TestFailedNotificationSucceedsOnRetry(t *testing.T) {
	h := newHarness(t, config.When{Watchlist: true})
	h.push.fail = true
	h.ingest(t, source.FeedTrending, deal("1", "Example OLED TV", intp(150)))
	h.push.fail = false
	h.ingest(t, source.FeedTrending)
	if sent, _, _ := h.st.NotificationState(ctx, "mydealz:1", "push"); !sent || h.push.count() != 1 {
		t.Errorf("sent %v, delivered %d", sent, h.push.count())
	}
}

func TestTemperatureNotifyFiresWhenDealHeatsUp(t *testing.T) {
	h := newHarness(t, config.When{MinTemperature: 500})
	h.ingest(t, source.FeedTrending, deal("1", "Example Laptop", intp(300)))
	if h.push.count() != 0 {
		t.Fatalf("sent %d at 300°", h.push.count())
	}
	h.ingest(t, source.FeedTrending, deal("1", "Example Laptop", intp(520)))
	if h.push.count() != 1 {
		t.Errorf("sent %d at 520°, want 1", h.push.count())
	}
}

func TestRecurringDealIsBlocked(t *testing.T) {
	h := newHarness(t, config.When{Watchlist: true})
	title := "Example Air Flash Sale: Flüge ab 19,99€"
	for i, id := range []string{"1", "2", "3"} {
		*h.now = time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC).Add(time.Duration(i) * 20 * 24 * time.Hour)
		h.ingest(t, source.FeedTrending, deal(id, title, intp(150)))
	}
	if v := h.verdict(t, "mydealz:2"); v.Decision != "pass" {
		t.Errorf("second occurrence = %+v, want pass", v)
	}
	if v := h.verdict(t, "mydealz:3"); v.Decision != "block" || !strings.HasPrefix(v.Reasons[0], "recurring: seen 2× before") {
		t.Errorf("third occurrence = %+v, want recurring block", v)
	}
}

func TestRecomputeAppliesActions(t *testing.T) {
	h := newHarness(t, config.When{Watchlist: true})
	h.ingest(t, source.FeedTrending, deal("1", "Example Laptop", intp(150)), deal("2", "Example Monitor", intp(150)))

	if _, err := h.st.AddAction(ctx, store.ActionHide, "mydealz:1", *h.now); err != nil {
		t.Fatal(err)
	}
	fp := fingerprint.Key("Example Shop", fingerprint.CleanTitle("Example Monitor"))
	if _, err := h.st.AddAction(ctx, store.ActionJunk, fp, *h.now); err != nil {
		t.Fatal(err)
	}
	if err := h.p.RecomputeRecent(ctx); err != nil {
		t.Fatal(err)
	}
	if v := h.verdict(t, "mydealz:1"); v.Decision != "block" || v.Reasons[0] != "hidden by you" {
		t.Errorf("hidden deal = %+v", v)
	}
	if v := h.verdict(t, "mydealz:2"); v.Decision != "block" || v.Reasons[0] != "like a deal you marked as junk" {
		t.Errorf("junk deal = %+v", v)
	}
}

func TestMuteCategoryActionIsCaseInsensitive(t *testing.T) {
	h := newHarness(t, config.When{Watchlist: true})
	h.ingest(t, source.FeedTrending, deal("1", "Example Laptop", intp(150)))
	if _, err := h.st.AddAction(ctx, store.ActionMuteCategory, "ELEKTRONIK", *h.now); err != nil {
		t.Fatal(err)
	}
	if err := h.p.RecomputeRecent(ctx); err != nil {
		t.Fatal(err)
	}
	if v := h.verdict(t, "mydealz:1"); v.Decision != "block" {
		t.Errorf("verdict = %+v, want block", v)
	}
}

func TestPrune(t *testing.T) {
	h := newHarness(t, config.When{Watchlist: true})
	h.ingest(t, source.FeedTrending, deal("1", "Example Laptop", intp(150)))
	*h.now = h.now.Add(181 * 24 * time.Hour)
	if n, err := h.p.Prune(ctx); err != nil || n != 1 {
		t.Errorf("pruned %d, %v; want 1", n, err)
	}
}

func TestFailingSendUsesOneAttemptPerIngest(t *testing.T) {
	h := newHarness(t, config.When{Watchlist: true})
	h.push.fail = true
	d := deal("1", "Example OLED TV", intp(150))
	for want := 1; want <= 3; want++ {
		h.ingest(t, source.FeedTrending, d)
		if _, attempts, _ := h.st.NotificationState(ctx, "mydealz:1", "push"); attempts != want {
			t.Fatalf("after ingest %d: attempts = %d, want %d", want, attempts, want)
		}
	}
	h.ingest(t, source.FeedTrending, d)
	if _, attempts, _ := h.st.NotificationState(ctx, "mydealz:1", "push"); attempts != 3 {
		t.Errorf("after fourth ingest: attempts = %d, want 3", attempts)
	}
	h.push.fail = false
	h.ingest(t, source.FeedTrending, d)
	if h.push.count() != 0 {
		t.Errorf("delivered %d after attempts were used up, want 0", h.push.count())
	}
}

func TestRecomputeNeverNotifies(t *testing.T) {
	h := newHarness(t, config.When{Watchlist: true})
	h.push.fail = true
	h.ingest(t, source.FeedTrending, deal("1", "Example OLED TV", intp(150)))
	h.push.fail = false
	if err := h.p.RecomputeRecent(ctx); err != nil {
		t.Fatal(err)
	}
	sent, attempts, _ := h.st.NotificationState(ctx, "mydealz:1", "push")
	if sent || attempts != 1 || h.push.count() != 0 {
		t.Errorf("sent %v, attempts %d, delivered %d; want false, 1, 0", sent, attempts, h.push.count())
	}
}

type slowNotifier struct {
	mu    sync.Mutex
	count int
}

func (s *slowNotifier) Send(_ context.Context, _ notify.Message) error {
	time.Sleep(100 * time.Millisecond)
	s.mu.Lock()
	s.count++
	s.mu.Unlock()
	return nil
}

func TestConcurrentFeedsSendOnce(t *testing.T) {
	h := newHarness(t, config.When{Watchlist: true})
	slow := &slowNotifier{}
	h.p.Notifiers["push"] = slow
	d := deal("1", "Example OLED TV", intp(150))
	var wg sync.WaitGroup
	for _, feed := range []source.Feed{source.FeedTrending, source.FeedNew} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := h.p.Ingest(ctx, feed, []source.Deal{d}); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if slow.count != 1 {
		t.Errorf("sent %d, want 1", slow.count)
	}
}
