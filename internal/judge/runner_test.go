package judge

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/MorrisMorrison/dealsieve/internal/settings"
	"github.com/MorrisMorrison/dealsieve/internal/source"
	"github.com/MorrisMorrison/dealsieve/internal/store"
)

var t0 = time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)

type fakeJudge struct {
	mu    sync.Mutex
	calls []string
	err   func(title string) error
}

func (f *fakeJudge) Judge(_ context.Context, d Deal) (Opinion, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, d.Title)
	if f.err != nil {
		if err := f.err(d.Title); err != nil {
			return Opinion{}, err
		}
	}
	return Opinion{Rating: Meh, Reason: "about " + d.Title}, nil
}

type harness struct {
	st    *store.Store
	judge *fakeJudge
	mu    sync.Mutex
	set   settings.Settings
	now   time.Time
	r     *Runner
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	h := &harness{st: st, judge: &fakeJudge{}, set: settings.Defaults(), now: t0}
	h.set.Judge = settings.Judge{Enabled: true, Model: "example/model", DailyLimit: 200}
	h.r = &Runner{
		Store:    st,
		Settings: func() settings.Settings { h.mu.Lock(); defer h.mu.Unlock(); return h.set },
		New:      func(settings.Judge) Judge { return h.judge },
		Now:      func() time.Time { return h.now },
		Log:      slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	return h
}

// addDeal stores a deal seen at h.now with the given decision.
func (h *harness) addDeal(t *testing.T, id, decision string) {
	t.Helper()
	d := source.Deal{ID: "mydealz:" + id, Source: "mydealz", URL: "https://www.mydealz.de/deals/" + id, Title: id, Merchant: "Example Shop"}
	if _, err := h.st.UpsertDeal(context.Background(), d, source.FeedTrending, id, id, h.now); err != nil {
		t.Fatal(err)
	}
	if err := h.st.SaveVerdict(context.Background(), store.Verdict{DealID: d.ID, Decision: decision, Reasons: []string{}, DecidedAt: h.now}); err != nil {
		t.Fatal(err)
	}
}

func TestTickJudgesPassedDealsOnce(t *testing.T) {
	h := newHarness(t)
	h.addDeal(t, "a", "pass")
	h.addDeal(t, "b", "block")
	ctx := context.Background()
	h.r.Tick(ctx)
	h.r.Tick(ctx)
	if len(h.judge.calls) != 1 || h.judge.calls[0] != "a" {
		t.Fatalf("calls = %v, want [a] once", h.judge.calls)
	}
	o, err := h.st.GetOpinion(ctx, "mydealz:a")
	if err != nil || o.Status != store.OpinionOK || o.Rating != "meh" || o.Reason != "about a" || o.Model != "example/model" {
		t.Fatalf("opinion = %+v, %v", o, err)
	}
}

func TestTickOffDoesNothing(t *testing.T) {
	h := newHarness(t)
	h.set.Judge.Enabled = false
	h.addDeal(t, "a", "pass")
	h.r.Tick(context.Background())
	if len(h.judge.calls) != 0 {
		t.Fatalf("calls = %v", h.judge.calls)
	}
}

func TestTickDailyLimit(t *testing.T) {
	h := newHarness(t)
	h.set.Judge.DailyLimit = 2
	for _, id := range []string{"a", "b", "c"} {
		h.addDeal(t, id, "pass")
	}
	ctx := context.Background()
	h.r.Tick(ctx)
	h.r.Tick(ctx)
	if len(h.judge.calls) != 2 {
		t.Fatalf("calls = %v, want 2 (the limit)", h.judge.calls)
	}
	// A day later the limit has room again (deal c was seen 25 h ago, so
	// re-sight it to keep it a candidate).
	h.now = t0.Add(25 * time.Hour)
	h.addDeal(t, "c", "pass")
	h.r.Tick(ctx)
	if len(h.judge.calls) != 3 {
		t.Fatalf("calls = %v, want 3", h.judge.calls)
	}
}

func TestTickPerTickCap(t *testing.T) {
	h := newHarness(t)
	for i := range perTick + 5 {
		h.addDeal(t, string(rune('a'+i)), "pass")
	}
	h.r.Tick(context.Background())
	if len(h.judge.calls) != perTick {
		t.Fatalf("calls = %d, want %d", len(h.judge.calls), perTick)
	}
}

func (h *harness) calls24h(t *testing.T) int {
	t.Helper()
	st, err := h.r.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return st.Calls24h
}

func (h *harness) tickAt(d time.Duration) {
	h.now = t0.Add(d)
	h.r.Tick(context.Background())
}

func TestTickRetryableErrorPausesWithoutUsingAttempts(t *testing.T) {
	h := newHarness(t)
	h.addDeal(t, "a", "pass")
	h.addDeal(t, "b", "pass")
	h.judge.err = func(string) error { return &HTTPError{Status: 429} }
	ctx := context.Background()
	h.tickAt(0)
	if len(h.judge.calls) != 1 {
		t.Fatalf("calls = %v, want 1 (stop after a 429)", h.judge.calls)
	}
	if _, err := h.st.GetOpinion(ctx, "mydealz:"+h.judge.calls[0]); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("a retryable failure must not store an opinion row: %v", err)
	}
	st, err := h.r.Status(ctx)
	if err != nil || st.LastError != "HTTP 429" || !st.LastErrorAt.Equal(t0) || st.Calls24h != 1 {
		t.Fatalf("status = %+v, %v", st, err)
	}
	h.tickAt(time.Minute)
	h.tickAt(9 * time.Minute)
	if len(h.judge.calls) != 1 {
		t.Fatalf("calls = %v, want no call while paused", h.judge.calls)
	}
	h.tickAt(11 * time.Minute)
	if len(h.judge.calls) != 2 || h.judge.calls[0] != h.judge.calls[1] {
		t.Fatalf("calls = %v, want the same deal tried again after the pause", h.judge.calls)
	}
}

func TestTickBackoffDoublesAndCaps(t *testing.T) {
	h := newHarness(t)
	h.addDeal(t, "a", "pass")
	h.judge.err = func(string) error { return &HTTPError{Status: 503} }
	at := time.Duration(0)
	for i, pause := range []time.Duration{10 * time.Minute, 20 * time.Minute, 40 * time.Minute, time.Hour, time.Hour} {
		h.tickAt(at)
		if len(h.judge.calls) != i+1 {
			t.Fatalf("step %d: calls = %d", i, len(h.judge.calls))
		}
		h.tickAt(at + pause - time.Second)
		if len(h.judge.calls) != i+1 {
			t.Fatalf("step %d: called before the %v pause ended", i, pause)
		}
		at += pause
	}
}

func TestTickSuccessResetsBackoff(t *testing.T) {
	h := newHarness(t)
	h.addDeal(t, "a", "pass")
	h.judge.err = func(string) error { return &HTTPError{Status: 503} }
	h.tickAt(0)
	h.tickAt(11 * time.Minute) // second failure: pause is now 20 min
	h.judge.err = nil
	h.tickAt(32 * time.Minute) // succeeds
	if st, _ := h.r.Status(context.Background()); st.LastError != "" || !st.LastErrorAt.IsZero() {
		t.Fatalf("status = %+v, want the error cleared", st)
	}
	h.addDeal(t, "b", "pass")
	h.judge.err = func(string) error { return &HTTPError{Status: 503} }
	n := len(h.judge.calls)
	h.tickAt(32 * time.Minute) // fails again: the pause starts over at 10 min
	h.tickAt(43 * time.Minute)
	if len(h.judge.calls) != n+2 {
		t.Fatalf("calls = %v, want a retry 11 min after the first new failure", h.judge.calls)
	}
}

func TestWakeClearsPause(t *testing.T) {
	h := newHarness(t)
	h.addDeal(t, "a", "pass")
	h.judge.err = func(string) error { return &HTTPError{Status: 503} }
	h.tickAt(0)
	h.judge.err = nil
	h.tickAt(time.Minute)
	if len(h.judge.calls) != 1 {
		t.Fatalf("calls = %v, want paused", h.judge.calls)
	}
	h.r.Wake()
	h.tickAt(time.Minute)
	if len(h.judge.calls) != 2 {
		t.Fatalf("calls = %v, want a retry right after Wake", h.judge.calls)
	}
}

func TestTickNonRetryableMovesOn(t *testing.T) {
	h := newHarness(t)
	h.addDeal(t, "a", "pass")
	h.addDeal(t, "b", "pass")
	h.judge.err = func(string) error { return ErrBadReply }
	h.tickAt(0)
	if len(h.judge.calls) != 2 {
		t.Fatalf("calls = %v, want both deals tried", h.judge.calls)
	}
	o, err := h.st.GetOpinion(context.Background(), "mydealz:a")
	if err != nil || o.Status != store.OpinionFailed || o.Attempts != 1 {
		t.Fatalf("opinion = %+v, %v", o, err)
	}
	h.tickAt(time.Minute) // no pause after a bad reply, but the deals wait 10 min
	if len(h.judge.calls) != 2 {
		t.Fatalf("calls = %v", h.judge.calls)
	}
}

func TestTickCountsCallsOnlyWhenTheServerWasReached(t *testing.T) {
	h := newHarness(t)
	h.addDeal(t, "a", "pass")
	h.judge.err = func(string) error {
		return fmt.Errorf("judge request: %w", &net.OpError{Op: "dial", Err: errors.New("refused")})
	}
	h.tickAt(0)
	if len(h.judge.calls) != 1 || h.calls24h(t) != 0 {
		t.Fatalf("calls = %v, counted = %d, want 1 call and 0 counted", h.judge.calls, h.calls24h(t))
	}
	h.judge.err = func(string) error { return &HTTPError{Status: 502} }
	h.r.Wake()
	h.tickAt(time.Minute)
	if len(h.judge.calls) != 2 || h.calls24h(t) != 1 {
		t.Fatalf("calls = %v, counted = %d, want an HTTP 502 counted", h.judge.calls, h.calls24h(t))
	}
}

func TestTickGivesUpAfterThreeAttempts(t *testing.T) {
	h := newHarness(t)
	h.addDeal(t, "a", "pass")
	h.judge.err = func(string) error { return ErrBadReply }
	ctx := context.Background()
	for i := range 5 {
		h.now = t0.Add(time.Duration(i) * 11 * time.Minute)
		h.r.Tick(ctx)
	}
	if len(h.judge.calls) != 3 {
		t.Fatalf("calls = %d, want 3", len(h.judge.calls))
	}
}

func TestTickCancelledStoresNothing(t *testing.T) {
	h := newHarness(t)
	h.addDeal(t, "a", "pass")
	ctx, cancel := context.WithCancel(context.Background())
	h.judge.err = func(string) error { cancel(); return context.Canceled }
	h.r.Tick(ctx)
	if _, err := h.st.GetOpinion(context.Background(), "mydealz:a"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("opinion stored after cancel: %v", err)
	}
}

func TestTestJudgesSampleWithoutStoring(t *testing.T) {
	h := newHarness(t)
	var got settings.Judge
	h.r.New = func(j settings.Judge) Judge { got = j; return h.judge }
	res := h.r.Test(context.Background(), settings.Judge{Model: "other/model", DailyLimit: 1})
	if !res.OK || res.Rating != Meh || res.Reason != "about "+SampleDeal.Title || got.Model != "other/model" {
		t.Fatalf("result = %+v, judge = %+v", res, got)
	}
	if n, _ := h.st.JudgeCallsSince(context.Background(), time.Time{}); n != 0 {
		t.Errorf("test call counted: %d", n)
	}
	h.judge.err = func(string) error { return &HTTPError{Status: 401} }
	if res := h.r.Test(context.Background(), settings.Judge{Model: "m", DailyLimit: 1}); res.OK || res.Error != "HTTP 401" {
		t.Fatalf("error result = %+v", res)
	}
}

func (h *harness) setJudge(f func(*settings.Judge)) {
	h.mu.Lock()
	defer h.mu.Unlock()
	f(&h.set.Judge)
}

func TestWakeTicksNow(t *testing.T) {
	h := newHarness(t)
	h.set.Judge.Enabled = false
	h.addDeal(t, "a", "pass")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { h.r.Run(ctx); close(done) }()
	time.Sleep(50 * time.Millisecond) // let Run's first (disabled) tick pass
	h.setJudge(func(j *settings.Judge) { j.Enabled = true })
	h.r.Wake()
	deadline := time.Now().Add(5 * time.Second)
	for {
		h.judge.mu.Lock()
		n := len(h.judge.calls)
		h.judge.mu.Unlock()
		if n == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("Wake didn't make Run tick")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	<-done
}

func TestTickConnectionErrorStopsTick(t *testing.T) {
	h := newHarness(t)
	h.addDeal(t, "a", "pass")
	h.addDeal(t, "b", "pass")
	h.judge.err = func(string) error {
		return fmt.Errorf("judge request: %w", &net.OpError{Op: "dial", Err: errors.New("refused")})
	}
	h.r.Tick(context.Background())
	if len(h.judge.calls) != 1 {
		t.Fatalf("calls = %v, want 1 (stop when the server is unreachable)", h.judge.calls)
	}
	if _, err := h.st.GetOpinion(context.Background(), "mydealz:"+h.judge.calls[0]); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("an unreachable server must not store an opinion row: %v", err)
	}
}

func TestTestRefusesConcurrentRuns(t *testing.T) {
	h := newHarness(t)
	release := make(chan struct{})
	started := make(chan struct{})
	h.r.New = func(settings.Judge) Judge {
		return judgeFunc(func(context.Context, Deal) (Opinion, error) {
			close(started)
			<-release
			return Opinion{Rating: Good, Reason: "ok"}, nil
		})
	}
	done := make(chan TestResult)
	go func() { done <- h.r.Test(context.Background(), settings.Judge{Model: "m", DailyLimit: 1}) }()
	<-started
	if res := h.r.Test(context.Background(), settings.Judge{Model: "m", DailyLimit: 1}); res.OK || res.Error != "a test is already running" {
		t.Fatalf("second test = %+v", res)
	}
	close(release)
	if res := <-done; !res.OK {
		t.Fatalf("first test = %+v", res)
	}
}

type judgeFunc func(context.Context, Deal) (Opinion, error)

func (f judgeFunc) Judge(ctx context.Context, d Deal) (Opinion, error) { return f(ctx, d) }

func TestWakeDuringCallKeepsTheImmediateRetry(t *testing.T) {
	h := newHarness(t)
	h.addDeal(t, "a", "pass")
	// The user saves settings while the call is in flight; the call then fails.
	h.judge.err = func(string) error { h.r.Wake(); return &HTTPError{Status: 503} }
	h.tickAt(0)
	h.judge.err = nil
	h.tickAt(time.Minute)
	if len(h.judge.calls) != 2 {
		t.Fatalf("calls = %v, want a retry right after a save that happened mid-call", h.judge.calls)
	}
	if st, _ := h.r.Status(context.Background()); st.LastError != "" {
		t.Fatalf("status = %+v", st)
	}
}

func TestTickUnreachedNonRetryableErrorKeepsBackoff(t *testing.T) {
	h := newHarness(t)
	h.addDeal(t, "a", "pass")
	h.judge.err = func(string) error { return &HTTPError{Status: 503} }
	h.tickAt(0)
	h.tickAt(11 * time.Minute) // pause is now 20 min
	h.judge.err = func(string) error { return errors.New("odd local failure") }
	h.tickAt(32 * time.Minute) // neither retryable nor a reply: no reset
	h.addDeal(t, "b", "pass")
	h.judge.err = func(string) error { return &HTTPError{Status: 503} }
	h.tickAt(32 * time.Minute) // third outage: 40 min, not 10
	n := len(h.judge.calls)
	h.tickAt(43 * time.Minute)
	if len(h.judge.calls) != n {
		t.Fatalf("calls = %v, want the back-off to keep doubling", h.judge.calls)
	}
}
