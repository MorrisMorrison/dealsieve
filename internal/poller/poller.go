// Package poller fetches each feed on its own interval, backs off on
// failures and keeps per-feed status for the page.
package poller

import (
	"context"
	"log/slog"
	"math"
	"math/rand/v2"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/MorrisMorrison/dealsieve/internal/source"
)

const maxBackoff = 30 * time.Minute

type Ingester interface {
	Ingest(ctx context.Context, feed source.Feed, deals []source.Deal) error
}

type Status struct {
	Feed        string     `json:"feed"`
	Interval    string     `json:"interval"`
	LastSuccess *time.Time `json:"last_success"`
	LastAttempt *time.Time `json:"last_attempt"`
	LastError   string     `json:"last_error,omitempty"`
	Failures    int        `json:"consecutive_failures"`
	Stale       bool       `json:"stale"`
}

type feedState struct {
	lastSuccess time.Time
	lastAttempt time.Time
	lastErr     string
	failures    int
}

type Poller struct {
	fetcher   source.Fetcher
	ingest    Ingester
	intervals map[source.Feed]time.Duration
	log       *slog.Logger
	now       func() time.Time
	started   time.Time

	mu    sync.Mutex
	state map[source.Feed]*feedState
}

func New(f source.Fetcher, ing Ingester, intervals map[source.Feed]time.Duration, log *slog.Logger) *Poller {
	p := &Poller{fetcher: f, ingest: ing, intervals: intervals, log: log, now: time.Now, state: map[source.Feed]*feedState{}}
	p.started = p.now()
	for feed := range intervals {
		p.state[feed] = &feedState{}
	}
	return p
}

// Run polls every feed right away and then on its interval until ctx ends.
func (p *Poller) Run(ctx context.Context) {
	var wg sync.WaitGroup
	for feed, interval := range p.intervals {
		wg.Go(func() {
			for {
				failures := p.PollOnce(ctx, feed)
				timer := time.NewTimer(nextDelay(interval, failures, rand.Float64()))
				select {
				case <-ctx.Done():
					timer.Stop()
					return
				case <-timer.C:
				}
			}
		})
	}
	wg.Wait()
}

// PollOnce fetches and ingests one feed and returns the consecutive failures.
func (p *Poller) PollOnce(ctx context.Context, feed source.Feed) int {
	deals, err := p.fetcher.Fetch(ctx, feed)
	if err == nil {
		err = p.ingest.Ingest(ctx, feed, deals)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	st := p.state[feed]
	st.lastAttempt = p.now()
	if err != nil {
		st.failures++
		st.lastErr = err.Error()
		if ctx.Err() == nil {
			p.log.Warn("poll failed", "feed", feed, "failures", st.failures, "err", err)
		}
		return st.failures
	}
	st.failures, st.lastErr, st.lastSuccess = 0, "", st.lastAttempt
	p.log.Debug("polled", "feed", feed, "deals", len(deals))
	return 0
}

func (p *Poller) Status() []Status {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := p.now()
	out := make([]Status, 0, len(p.state))
	for feed, st := range p.state {
		interval := p.intervals[feed]
		ref := st.lastSuccess
		if ref.IsZero() {
			ref = p.started
		}
		s := Status{Feed: string(feed), Interval: interval.String(), LastError: st.lastErr, Failures: st.failures,
			Stale: now.Sub(ref) > 3*interval}
		if !st.lastSuccess.IsZero() {
			t := st.lastSuccess
			s.LastSuccess = &t
		}
		if !st.lastAttempt.IsZero() {
			t := st.lastAttempt
			s.LastAttempt = &t
		}
		out = append(out, s)
	}
	slices.SortFunc(out, func(a, b Status) int { return strings.Compare(a.Feed, b.Feed) })
	return out
}

// nextDelay doubles the interval per consecutive failure (capped at 30 min,
// or the interval if that's longer) and adds ±10% jitter; r is in [0, 1].
func nextDelay(interval time.Duration, failures int, r float64) time.Duration {
	limit := max(maxBackoff, interval)
	d := interval
	for range failures {
		if d >= limit {
			break
		}
		d *= 2
	}
	d = min(d, limit)
	return d + time.Duration(math.Round((r*0.2-0.1)*float64(d)))
}
