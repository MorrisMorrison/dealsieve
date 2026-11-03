package poller

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/MorrisMorrison/dealsieve/internal/source"
)

type fakeFetcher struct {
	deals []source.Deal
	err   error
}

func (f *fakeFetcher) Fetch(context.Context, source.Feed) ([]source.Deal, error) {
	return f.deals, f.err
}

type fakeIngester struct {
	calls int
	err   error
}

func (f *fakeIngester) Ingest(context.Context, source.Feed, []source.Deal) error {
	f.calls++
	return f.err
}

func TestNextDelay(t *testing.T) {
	tests := []struct {
		interval time.Duration
		failures int
		r        float64
		want     time.Duration
	}{
		{5 * time.Minute, 0, 0.5, 5 * time.Minute},
		{5 * time.Minute, 1, 0.5, 10 * time.Minute},
		{5 * time.Minute, 2, 0.5, 20 * time.Minute},
		{5 * time.Minute, 10, 0.5, 30 * time.Minute},
		{60 * time.Minute, 3, 0.5, 60 * time.Minute},
		{10 * time.Minute, 0, 0, 9 * time.Minute},
		{10 * time.Minute, 0, 1, 11 * time.Minute},
	}
	for _, tt := range tests {
		if got := nextDelay(tt.interval, tt.failures, tt.r); got != tt.want {
			t.Errorf("nextDelay(%v, %d, %v) = %v, want %v", tt.interval, tt.failures, tt.r, got, tt.want)
		}
	}
}

func newTestPoller(f source.Fetcher, ing Ingester, now *time.Time) *Poller {
	p := New(f, ing, map[source.Feed]time.Duration{source.FeedTrending: 5 * time.Minute, source.FeedNew: 2 * time.Minute}, slog.New(slog.DiscardHandler))
	p.now = func() time.Time { return *now }
	p.started = *now
	return p
}

func TestPollOnceTracksFailuresAndRecovery(t *testing.T) {
	now := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	f := &fakeFetcher{err: errors.New("unexpected status 503")}
	ing := &fakeIngester{}
	p := newTestPoller(f, ing, &now)

	if n := p.PollOnce(context.Background(), source.FeedTrending); n != 1 {
		t.Errorf("failures = %d, want 1", n)
	}
	if n := p.PollOnce(context.Background(), source.FeedTrending); n != 2 {
		t.Errorf("failures = %d, want 2", n)
	}
	if ing.calls != 0 {
		t.Errorf("ingest called %d times on fetch errors", ing.calls)
	}
	st := p.Status()[1] // sorted: new, trending
	if st.Feed != "trending" || st.LastError != "unexpected status 503" || st.Failures != 2 || st.LastSuccess != nil {
		t.Errorf("status = %+v", st)
	}

	f.err = nil
	if n := p.PollOnce(context.Background(), source.FeedTrending); n != 0 {
		t.Errorf("failures after success = %d", n)
	}
	st = p.Status()[1]
	if st.LastError != "" || st.LastSuccess == nil || !st.LastSuccess.Equal(now) || ing.calls != 1 {
		t.Errorf("status after success = %+v (ingest calls %d)", st, ing.calls)
	}
}

func TestIngestErrorCountsAsFailure(t *testing.T) {
	now := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	p := newTestPoller(&fakeFetcher{}, &fakeIngester{err: errors.New("disk full")}, &now)
	if n := p.PollOnce(context.Background(), source.FeedNew); n != 1 {
		t.Errorf("failures = %d, want 1", n)
	}
}

func TestStale(t *testing.T) {
	now := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	p := newTestPoller(&fakeFetcher{}, &fakeIngester{}, &now)

	now = now.Add(10 * time.Minute) // never polled: new (2m) is stale, trending (5m) is not yet
	st := p.Status()
	if !st[0].Stale || st[1].Stale {
		t.Errorf("before any poll: %+v", st)
	}
	p.PollOnce(context.Background(), source.FeedNew)
	now = now.Add(6 * time.Minute) // new: 6m since success (not > 6m); trending: 16m since start (> 15m)
	if st := p.Status(); st[0].Stale || !st[1].Stale {
		t.Errorf("after polling new: %+v", st)
	}
}

func TestRunStopsWithContext(t *testing.T) {
	p := New(&fakeFetcher{}, &fakeIngester{}, map[source.Feed]time.Duration{source.FeedNew: time.Minute}, slog.New(slog.DiscardHandler))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { p.Run(ctx); close(done) }()
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run didn't stop")
	}
}
