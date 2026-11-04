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
	callTimeout = 45 * time.Second
	seenWithin  = 24 * time.Hour
	retryAfter  = 10 * time.Minute
	maxAttempts = 3
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
	s := r.Settings().Judge
	if !s.Enabled || s.Model == "" {
		return
	}
	now := r.Now()
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
		if err := r.Store.RecordJudgeCall(ctx, now); err != nil {
			r.Log.Error("record judge call", "err", err)
			return
		}
		callCtx, cancel := context.WithTimeout(ctx, callTimeout)
		op, err := j.Judge(callCtx, Deal{Title: rec.Title, Merchant: rec.Merchant, Price: rec.Price, Category: rec.Category, Temperature: rec.Temperature})
		cancel()
		if ctx.Err() != nil {
			return // shutting down: not the deal's fault
		}
		o := store.OpinionRecord{DealID: rec.ID, Model: s.Model, JudgedAt: now}
		if err != nil {
			o.Status, o.Error = store.OpinionFailed, Reason(err)
			r.setErr(o.Error, now)
			r.Log.Warn("judge failed", "deal", rec.ID, "err", o.Error)
		} else {
			o.Status, o.Rating, o.Reason = store.OpinionOK, string(op.Rating), op.Reason
		}
		if serr := r.Store.SaveOpinion(ctx, o); serr != nil {
			r.Log.Error("save opinion", "deal", rec.ID, "err", serr)
			return
		}
		if err != nil && Retryable(err) {
			return // busy or rate-limited; later ticks retry
		}
	}
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
	callCtx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	op, err := r.New(j).Judge(callCtx, SampleDeal)
	if err != nil {
		return TestResult{Error: Reason(err)}
	}
	return TestResult{OK: true, Rating: op.Rating, Reason: op.Reason}
}

// Wake makes Run tick now instead of at the next minute, e.g. right after
// the judge was turned on. It never blocks.
func (r *Runner) Wake() {
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
