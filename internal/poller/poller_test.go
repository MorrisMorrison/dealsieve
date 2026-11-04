package poller

import (
	"context"
	"errors"
	"log/slog"
	"sync"
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

type fetcherFunc func(ctx context.Context, feed source.Feed) ([]source.Deal, error)

func (f fetcherFunc) Fetch(ctx context.Context, feed source.Feed) ([]source.Deal, error) {
	return f(ctx, feed)
}

type fakeIngester struct {
	calls int
	err   error
}

func (f *fakeIngester) Ingest(context.Context, source.Feed, []source.Deal) error {
	f.calls++
	return f.err
}

type nopIngester struct{}

func (nopIngester) Ingest(context.Context, source.Feed, []source.Deal) error { return nil }

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
	p := New(map[string]source.Fetcher{"mydealz": f}, ing,
		map[source.Feed]time.Duration{source.FeedTrending: 5 * time.Minute, source.FeedNew: 2 * time.Minute},
		slog.New(slog.DiscardHandler))
	p.now = func() time.Time { return *now }
	p.SetSources([]string{"mydealz"})
	return p
}

func TestPollOnceTracksFailuresAndRecovery(t *testing.T) {
	now := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	f := &fakeFetcher{err: errors.New("unexpected status 503")}
	ing := &fakeIngester{}
	p := newTestPoller(f, ing, &now)

	if n := p.PollOnce(context.Background(), "mydealz", source.FeedTrending); n != 1 {
		t.Errorf("failures = %d, want 1", n)
	}
	if n := p.PollOnce(context.Background(), "mydealz", source.FeedTrending); n != 2 {
		t.Errorf("failures = %d, want 2", n)
	}
	if ing.calls != 0 {
		t.Errorf("ingest called %d times on fetch errors", ing.calls)
	}
	st := p.Status()[1] // sorted: new, trending
	if st.Source != "mydealz" || st.Label != "mydealz" || st.Feed != "trending" ||
		st.LastError != "unexpected status 503" || st.Failures != 2 || st.LastSuccess != nil {
		t.Errorf("status = %+v", st)
	}

	f.err = nil
	if n := p.PollOnce(context.Background(), "mydealz", source.FeedTrending); n != 0 {
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
	if n := p.PollOnce(context.Background(), "mydealz", source.FeedNew); n != 1 {
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
	p.PollOnce(context.Background(), "mydealz", source.FeedNew)
	now = now.Add(6 * time.Minute) // new: 6m since success (not > 6m); trending: 16m since start (> 15m)
	if st := p.Status(); st[0].Stale || !st[1].Stale {
		t.Errorf("after polling new: %+v", st)
	}
}

func TestStatusFollowsSourceOrder(t *testing.T) {
	fetchers := map[string]source.Fetcher{"mydealz": &fakeFetcher{}, "pepperpl": &fakeFetcher{}}
	p := New(fetchers, nopIngester{}, map[source.Feed]time.Duration{source.FeedTrending: time.Hour, source.FeedNew: time.Hour},
		slog.New(slog.DiscardHandler))
	p.SetSources([]string{"pepperpl", "mydealz"})
	var got []string
	for _, s := range p.Status() {
		got = append(got, s.Source+"/"+s.Label+"/"+s.Feed)
	}
	want := []string{"pepperpl/pepper.pl/new", "pepperpl/pepper.pl/trending", "mydealz/mydealz/new", "mydealz/mydealz/trending"}
	if len(got) != len(want) {
		t.Fatalf("status = %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("status = %v, want %v", got, want)
			break
		}
	}
}

func TestPollOnceForTurnedOffSource(t *testing.T) {
	now := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	var p *Poller
	f := fetcherFunc(func(context.Context, source.Feed) ([]source.Deal, error) {
		p.SetSources(nil) // turned off while its poll is in flight
		return nil, errors.New("unexpected status 503")
	})
	p = newTestPoller(f, nopIngester{}, &now)
	if n := p.PollOnce(context.Background(), "mydealz", source.FeedTrending); n != 0 {
		t.Errorf("failures = %d, want 0", n)
	}
	if n := p.PollOnce(context.Background(), "mydealz", source.FeedTrending); n != 0 {
		t.Errorf("failures after turning off = %d, want 0", n)
	}
	if n := p.PollOnce(context.Background(), "example", source.FeedTrending); n != 0 {
		t.Errorf("failures for an unknown source = %d, want 0", n)
	}
	if st := p.Status(); len(st) != 0 {
		t.Errorf("status = %+v", st)
	}
}

func TestSetSourcesStartsAndStopsLoops(t *testing.T) {
	calls := make(chan string, 100)
	fetchers := map[string]source.Fetcher{}
	for _, n := range []string{"mydealz", "hotukdeals"} {
		fetchers[n] = fetcherFunc(func(ctx context.Context, feed source.Feed) ([]source.Deal, error) {
			calls <- n + "/" + string(feed)
			return nil, nil
		})
	}
	p := New(fetchers, nopIngester{}, map[source.Feed]time.Duration{source.FeedTrending: time.Hour}, slog.New(slog.DiscardHandler))
	p.SetSources([]string{"mydealz"})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { p.Run(ctx); close(done) }()
	if got := <-calls; got != "mydealz/trending" {
		t.Fatalf("first poll = %q", got)
	}
	p.SetSources([]string{"hotukdeals", "example"}) // unknown names are skipped
	if got := <-calls; got != "hotukdeals/trending" {
		t.Fatalf("after switch = %q", got)
	}
	st := p.Status()
	if len(st) != 1 || st[0].Source != "hotukdeals" || st[0].Feed != "trending" {
		t.Errorf("status = %+v", st)
	}
	cancel()
	<-done
}

func TestCancelStopsWaitingLoops(t *testing.T) {
	polled := make(chan struct{}, 10)
	f := fetcherFunc(func(context.Context, source.Feed) ([]source.Deal, error) {
		polled <- struct{}{}
		return nil, nil
	})
	p := New(map[string]source.Fetcher{"mydealz": f}, nopIngester{},
		map[source.Feed]time.Duration{source.FeedTrending: time.Hour, source.FeedNew: time.Hour}, slog.New(slog.DiscardHandler))
	p.SetSources([]string{"mydealz"})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { p.Run(ctx); close(done) }()
	<-polled
	<-polled // both loops now wait on hour-long timers
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run didn't stop")
	}
}

// A loop of a source that was turned off and on again may still be finishing
// its last poll; that poll must not land in the new loop's status.
func TestReenabledSourceIgnoresOldLoop(t *testing.T) {
	release := make(chan struct{})
	started := make(chan struct{}, 10)
	var mu sync.Mutex
	n := 0
	f := fetcherFunc(func(context.Context, source.Feed) ([]source.Deal, error) {
		mu.Lock()
		n++
		first := n == 1
		mu.Unlock()
		started <- struct{}{}
		if first {
			<-release // a slow request that ignores cancellation
			return nil, errors.New("old loop")
		}
		return nil, nil
	})
	p := New(map[string]source.Fetcher{"mydealz": f}, nopIngester{},
		map[source.Feed]time.Duration{source.FeedTrending: time.Hour}, slog.New(slog.DiscardHandler))
	p.SetSources([]string{"mydealz"})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { p.Run(ctx); close(done) }()
	<-started // the old loop's poll is in flight
	p.SetSources(nil)
	p.SetSources([]string{"mydealz"})
	<-started // the new loop polls
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(time.Millisecond) {
		if st := p.Status(); len(st) == 1 && st[0].LastSuccess != nil {
			break // the new loop recorded its poll
		}
		if time.Now().After(deadline) {
			t.Fatal("the new loop didn't record its poll")
		}
	}
	close(release)
	cancel()
	<-done
	st := p.Status()
	if len(st) != 1 || st[0].Failures != 0 || st[0].LastError != "" {
		t.Errorf("status = %+v", st)
	}
}

func TestSetSourcesAfterRunEnded(t *testing.T) {
	p := New(map[string]source.Fetcher{"mydealz": &fakeFetcher{}}, nopIngester{},
		map[source.Feed]time.Duration{source.FeedNew: time.Minute}, slog.New(slog.DiscardHandler))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	p.Run(ctx)
	p.SetSources([]string{"mydealz"}) // must not start a loop nobody waits for
}

func TestRunStopsWithContext(t *testing.T) {
	p := New(map[string]source.Fetcher{"mydealz": &fakeFetcher{}}, nopIngester{},
		map[source.Feed]time.Duration{source.FeedNew: time.Minute}, slog.New(slog.DiscardHandler))
	p.SetSources([]string{"mydealz"})
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
