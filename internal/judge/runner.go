package judge

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/MorrisMorrison/dealsieve/internal/settings"
	"github.com/MorrisMorrison/dealsieve/internal/store"
)

const (
	// perTick bounds one minute's work so a backlog drains gradually.
	perTick     = 20
	callTimeout = 60 * time.Second
	seenWithin  = 24 * time.Hour
	retryAfter  = 10 * time.Minute
	maxAttempts = 3
	// An unreachable or busy server pauses the runner for minBackoff, doubling
	// per consecutive failure up to maxBackoff, without costing the deals
	// attempts.
	minBackoff  = 10 * time.Minute
	maxBackoff  = time.Hour
	limitWindow = 24 * time.Hour
)

// SampleDeal is what the Settings page's test button asks about.
var SampleDeal = Deal{
	Title: "Example Air Flash Sale: Flüge ab 19,99€", Merchant: "Example Air",
	Price: "19,99€", Category: "Reisen", Temperature: func() *int { t := 120; return &t }(),
}

// Runner judges passed deals in the background. It holds no pipeline lock:
// a slow model delays only the opinions.
type Runner struct {
	Store    *store.Store
	Settings func() settings.Settings
	New      func(settings.Judge) Judge
	Now      func() time.Time
	Log      *slog.Logger

	mu          sync.Mutex
	wake        chan struct{}
	lastErr     string
	lastErrAt   time.Time
	limitLogged time.Time
	pausedUntil time.Time
	backoff     time.Duration
	wakeGen     uint64 // bumped by Wake; a call started before it can't undo it

	testMu sync.Mutex // one Settings-page test at a time
}

type Status struct {
	Calls24h    int       `json:"calls_24h"`
	LastError   string    `json:"last_error,omitempty"`
	LastErrorAt time.Time `json:"last_error_at,omitzero"`
}

type TestResult struct {
	OK     bool   `json:"ok"`
	Rating Rating `json:"rating,omitempty"`
	Reason string `json:"reason,omitempty"`
	Error  string `json:"error,omitempty"`
}

func (r *Runner) Run(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		r.Tick(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-r.wakeCh():
		}
	}
}

func (r *Runner) Tick(ctx context.Context) {
	gen := r.wakeGeneration()
	s := r.Settings().Judge
	if !s.Enabled || s.Model == "" {
		return
	}
	now := r.Now()
	if r.paused(now) {
		return
	}
	if _, err := r.Store.PruneJudgeCalls(ctx, now.Add(-2*limitWindow)); err != nil {
		r.Log.Error("prune judge calls", "err", err)
	}
	used, err := r.Store.JudgeCallsSince(ctx, now.Add(-limitWindow))
	if err != nil {
		r.Log.Error("count judge calls", "err", err)
		return
	}
	budget := min(s.DailyLimit-used, perTick)
	if budget <= 0 {
		r.logLimit(now, s.DailyLimit)
		return
	}
	cands, err := r.Store.JudgeCandidates(ctx, now.Add(-seenWithin), now.Add(-retryAfter), maxAttempts, budget)
	if err != nil {
		r.Log.Error("judge candidates", "err", err)
		return
	}
	j := r.New(s)
	for _, rec := range cands {
		if ctx.Err() != nil {
			return
		}
		callCtx, cancel := context.WithTimeout(ctx, callTimeout)
		op, err := j.Judge(callCtx, Deal{Title: rec.Title, Merchant: rec.Merchant, Price: rec.Price, Category: rec.Category, Temperature: rec.Temperature})
		cancel()
		if ctx.Err() != nil {
			return // shutting down: not the deal's fault
		}
		// Counted after the call so an unreachable server doesn't eat the limit.
		if reachedServer(err) {
			if rerr := r.Store.RecordJudgeCall(ctx, now); rerr != nil {
				r.Log.Error("record judge call", "err", rerr)
				return
			}
		}
		if err != nil && Retryable(err) {
			// Busy, rate-limited or down: keep the deal a candidate and pause.
			msg := Reason(err)
			r.setErr(msg, now)
			pause := r.startPause(now, gen)
			r.Log.Warn("judge unavailable", "deal", rec.ID, "err", msg, "pause", pause)
			return
		}
		if err == nil || reachedServer(err) {
			r.resetBackoff()
		}
		o := store.OpinionRecord{DealID: rec.ID, Model: s.Model, JudgedAt: now}
		if err != nil {
			o.Status, o.Error = store.OpinionFailed, Reason(err)
			r.setErr(o.Error, now)
			r.Log.Warn("judge failed", "deal", rec.ID, "err", o.Error)
		} else {
			o.Status, o.Rating, o.Reason = store.OpinionOK, string(op.Rating), op.Reason
			r.setErr("", time.Time{})
		}
		if serr := r.Store.SaveOpinion(ctx, o); serr != nil {
			r.Log.Error("save opinion", "deal", rec.ID, "err", serr)
			return
		}
	}
}

func (r *Runner) paused(now time.Time) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return now.Before(r.pausedUntil)
}

func (r *Runner) wakeGeneration() uint64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.wakeGen
}

// startPause doubles the back-off (10 min up to 1 h) and returns the pause.
// If Wake ran since the tick began (gen), the failed call used the old
// settings, so it must not undo the wake: no pause, and 0 is returned.
func (r *Runner) startPause(now time.Time, gen uint64) time.Duration {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.wakeGen != gen {
		return 0
	}
	if r.backoff == 0 {
		r.backoff = minBackoff
	} else {
		r.backoff = min(r.backoff*2, maxBackoff)
	}
	r.pausedUntil = now.Add(r.backoff)
	return r.backoff
}

func (r *Runner) resetBackoff() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.backoff, r.pausedUntil = 0, time.Time{}
}

// logLimit logs the reached limit once per window instead of every minute.
func (r *Runner) logLimit(now time.Time, limit int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if now.Sub(r.limitLogged) < limitWindow {
		return
	}
	r.limitLogged = now
	r.Log.Info("judge daily limit reached", "limit", limit)
}

func (r *Runner) setErr(msg string, at time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lastErr, r.lastErrAt = msg, at
}

func (r *Runner) Status(ctx context.Context) (Status, error) {
	n, err := r.Store.JudgeCallsSince(ctx, r.Now().Add(-limitWindow))
	if err != nil {
		return Status{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return Status{Calls24h: n, LastError: r.lastErr, LastErrorAt: r.lastErrAt}, nil
}

// Test asks the model about SampleDeal with unsaved settings. It doesn't
// count towards the daily limit and stores nothing.
func (r *Runner) Test(ctx context.Context, j settings.Judge) TestResult {
	if !r.testMu.TryLock() {
		return TestResult{Error: "a test is already running"}
	}
	defer r.testMu.Unlock()
	callCtx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	op, err := r.New(j).Judge(callCtx, SampleDeal)
	if err != nil {
		return TestResult{Error: Reason(err)}
	}
	return TestResult{OK: true, Rating: op.Rating, Reason: op.Reason}
}

// Wake makes Run tick now instead of at the next minute, e.g. right after
// the judge was turned on, and ends a back-off pause so that fixing the model
// or URL is retried at once. It never blocks.
func (r *Runner) Wake() {
	r.mu.Lock()
	r.wakeGen++
	r.backoff, r.pausedUntil = 0, time.Time{}
	r.mu.Unlock()
	select {
	case r.wakeCh() <- struct{}{}:
	default:
	}
}

func (r *Runner) wakeCh() chan struct{} {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.wake == nil {
		r.wake = make(chan struct{}, 1)
	}
	return r.wake
}
