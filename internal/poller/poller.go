// Package poller fetches each source's feeds on their own intervals, backs
// off on failures and keeps per-feed status for the page. The set of sources
// can change while it runs.
package poller

import (
	"context"
	"log/slog"
	"math"
	"math/rand/v2"
	"slices"
	"sync"
	"time"

	"github.com/MorrisMorrison/dealsieve/internal/settings"
	"github.com/MorrisMorrison/dealsieve/internal/source"
)

const maxBackoff = 30 * time.Minute

type Ingester interface {
	Ingest(ctx context.Context, feed source.Feed, deals []source.Deal) error
}

type Status struct {
	Source      string     `json:"source"`
	Label       string     `json:"label"`
	Feed        string     `json:"feed"`
	Interval    string     `json:"interval"`
	LastSuccess *time.Time `json:"last_success"`
	LastAttempt *time.Time `json:"last_attempt"`
	LastError   string     `json:"last_error,omitempty"`
	Failures    int        `json:"consecutive_failures"`
	Stale       bool       `json:"stale"`
}

type loopKey struct {
	source string
	feed   source.Feed
}

type feedState struct {
	started     time.Time // staleness counts from here until the first success
	lastSuccess time.Time
	lastAttempt time.Time
	lastErr     string
	failures    int
}

type Poller struct {
	fetchers  map[string]source.Fetcher
	ingest    Ingester
	intervals map[source.Feed]time.Duration
	feeds     []source.Feed // the keys of intervals, sorted
	log       *slog.Logger
	now       func() time.Time

	mu      sync.Mutex
	order   []string                      // enabled sources, as last set
	running map[string]context.CancelFunc // per source
	state   map[loopKey]*feedState
	ctx     context.Context // Run's; nil before Run
	closed  bool            // Run is waiting for the loops to end
	wg      sync.WaitGroup
}

func New(fetchers map[string]source.Fetcher, ing Ingester, intervals map[source.Feed]time.Duration, log *slog.Logger) *Poller {
	p := &Poller{fetchers: fetchers, ingest: ing, intervals: intervals, log: log, now: time.Now,
		running: map[string]context.CancelFunc{}, state: map[loopKey]*feedState{}}
	for feed := range intervals {
		p.feeds = append(p.feeds, feed)
	}
	slices.Sort(p.feeds)
	return p
}

// SetSources sets the sources to poll, starting and stopping their loops if
// Run is running. Names without a fetcher are logged and skipped.
func (p *Poller) SetSources(names []string) {
	var order []string
	for _, n := range names {
		if p.fetchers[n] == nil {
			p.log.Warn("unknown source", "source", n)
			continue
		}
		if !slices.Contains(order, n) {
			order = append(order, n)
		}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.order = order
	p.reconcile()
}

// reconcile makes state and loops match order; p.mu must be held. A source
// that is turned off loses its state, so turning it on again creates fresh
// *feedState values and a loop of the old generation that is still finishing
// a poll can tell (by pointer) that its state is gone.
func (p *Poller) reconcile() {
	for src, cancel := range p.running {
		if !slices.Contains(p.order, src) {
			cancel()
			delete(p.running, src)
		}
	}
	for key := range p.state {
		if !slices.Contains(p.order, key.source) {
			delete(p.state, key)
		}
	}
	for _, src := range p.order {
		for _, feed := range p.feeds {
			if key := (loopKey{src, feed}); p.state[key] == nil {
				p.state[key] = &feedState{started: p.now()}
			}
		}
		// Once closed, Run is in wg.Wait: no loop may be added.
		if p.ctx == nil || p.closed || p.running[src] != nil {
			continue
		}
		ctx, cancel := context.WithCancel(p.ctx)
		p.running[src] = cancel
		for _, feed := range p.feeds {
			st, interval := p.state[loopKey{src, feed}], p.intervals[feed]
			p.wg.Go(func() { p.loop(ctx, src, feed, interval, st) })
		}
	}
}

// Run polls every feed of the current sources right away and then on its
// interval, follows SetSources, and returns once ctx ended and every loop
// stopped.
func (p *Poller) Run(ctx context.Context) {
	p.mu.Lock()
	p.ctx = ctx
	p.reconcile()
	p.mu.Unlock()
	<-ctx.Done()
	p.mu.Lock()
	p.closed = true
	for src, cancel := range p.running {
		cancel()
		delete(p.running, src)
	}
	p.mu.Unlock()
	p.wg.Wait()
}

func (p *Poller) loop(ctx context.Context, src string, feed source.Feed, interval time.Duration, st *feedState) {
	for {
		failures := p.poll(ctx, src, feed, st)
		timer := time.NewTimer(nextDelay(interval, failures, rand.Float64()))
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

// PollOnce fetches and ingests one feed of an enabled source and returns the
// consecutive failures; 0 if the source isn't (or is no longer) enabled.
func (p *Poller) PollOnce(ctx context.Context, src string, feed source.Feed) int {
	p.mu.Lock()
	st := p.state[loopKey{src, feed}]
	p.mu.Unlock()
	if st == nil {
		return 0
	}
	return p.poll(ctx, src, feed, st)
}

// poll records the result in st only while st is still the source's current
// state: the source may have been turned off (or off and on) meanwhile.
func (p *Poller) poll(ctx context.Context, src string, feed source.Feed, st *feedState) int {
	deals, err := p.fetchers[src].Fetch(ctx, feed)
	if err == nil {
		err = p.ingest.Ingest(ctx, feed, deals)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.state[loopKey{src, feed}] != st {
		return 0
	}
	st.lastAttempt = p.now()
	if err != nil {
		st.failures++
		st.lastErr = err.Error()
		if ctx.Err() == nil {
			p.log.Warn("poll failed", "source", src, "feed", feed, "failures", st.failures, "err", err)
		}
		return st.failures
	}
	st.failures, st.lastErr, st.lastSuccess = 0, "", st.lastAttempt
	p.log.Debug("polled", "source", src, "feed", feed, "deals", len(deals))
	return 0
}

// Status has one row per feed of each enabled source, in the order given to
// SetSources, then by feed.
func (p *Poller) Status() []Status {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := p.now()
	out := make([]Status, 0, len(p.state))
	for _, src := range p.order {
		for _, feed := range p.feeds {
			st := p.state[loopKey{src, feed}]
			if st == nil {
				continue
			}
			interval := p.intervals[feed]
			ref := st.lastSuccess
			if ref.IsZero() {
				ref = st.started
			}
			s := Status{Source: src, Label: settings.SourceLabel(src), Feed: string(feed), Interval: interval.String(),
				LastError: st.lastErr, Failures: st.failures, Stale: now.Sub(ref) > 3*interval}
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
	}
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
