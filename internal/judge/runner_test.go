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

func TestTickRetryableErrorStopsTick(t *testing.T) {
	h := newHarness(t)
	h.addDeal(t, "a", "pass")
	h.addDeal(t, "b", "pass")
	h.judge.err = func(string) error { return &HTTPError{Status: 429} }
	ctx := context.Background()
	h.r.Tick(ctx)
	if len(h.judge.calls) != 1 {
		t.Fatalf("calls = %v, want 1 (stop after a 429)", h.judge.calls)
	}
	st, err := h.r.Status(ctx)
	if err != nil || st.LastError != "HTTP 429" || !st.LastErrorAt.Equal(t0) || st.Calls24h != 1 {
		t.Fatalf("status = %+v, %v", st, err)
	}
	// Non-retryable errors move on to the next deal.
	h.judge.err = func(string) error { return ErrBadReply }
	h.now = t0.Add(time.Minute)
	h.r.Tick(ctx)
	if len(h.judge.calls) != 2 {
		t.Fatalf("calls = %v, want b judged next (a waits 10 min)", h.judge.calls)
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
}
