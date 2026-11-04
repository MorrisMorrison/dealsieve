// Package pipeline runs a polled feed through storage, the rules and the
// notifiers, and recomputes verdicts when the user's actions change.
package pipeline

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/MorrisMorrison/dealsieve/internal/config"
	"github.com/MorrisMorrison/dealsieve/internal/fingerprint"
	"github.com/MorrisMorrison/dealsieve/internal/notify"
	"github.com/MorrisMorrison/dealsieve/internal/product"
	"github.com/MorrisMorrison/dealsieve/internal/rules"
	"github.com/MorrisMorrison/dealsieve/internal/settings"
	"github.com/MorrisMorrison/dealsieve/internal/source"
	"github.com/MorrisMorrison/dealsieve/internal/store"
)

const (
	recomputeWindow   = 7 * 24 * time.Hour
	retention         = 180 * 24 * time.Hour
	maxNotifyAttempts = 3
)

// Pipeline serialises Ingest, RecomputeRecent and Prune with a mutex.
type Pipeline struct {
	mu        sync.Mutex
	Store     *store.Store
	Engine    *rules.Engine
	Notify    []settings.NotifyRule
	Notifiers map[string]notify.Notifier
	Env       config.Env
	HTTP      *http.Client
	Now       func() time.Time
	Log       *slog.Logger
}

// Ingest stores what a feed returned, re-evaluates those deals and sends
// notifications. Failed notifications from earlier polls are retried first,
// and each (deal, channel) gets at most one attempt per call.
func (p *Pipeline) Ingest(ctx context.Context, feed source.Feed, deals []source.Deal) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := p.Now()
	retried := p.retryPending(ctx, now)
	facts, err := p.actionFacts(ctx)
	if err != nil {
		return err
	}
	for _, d := range deals {
		clean, key := fingerprint.Of(d.Merchant, d.Title)
		rec, err := p.Store.UpsertDeal(ctx, d, feed, clean, key, now)
		if err != nil {
			return fmt.Errorf("store deal %s: %w", d.ID, err)
		}
		if err := p.indexProduct(ctx, d); err != nil {
			// Information only; the startup reindex repairs the table.
			p.Log.Warn("index product failed", "deal", d.ID, "err", err)
		}
		res, err := p.decide(ctx, rec, facts, now)
		if err != nil {
			return err
		}
		if res.Decision == rules.Pass {
			for _, ch := range p.channelsFor(rec, res) {
				if retried[rec.ID+"|"+ch] {
					continue // already attempted this poll; later polls retry
				}
				p.send(ctx, rec, res.Reasons, ch, now)
			}
		}
	}
	return nil
}

// ApplySettings swaps the rule engine and notifiers in place; a poll in
// flight finishes with the old ones.
func (p *Pipeline) ApplySettings(s settings.Settings) {
	engine := rules.New(s.Rules, s.Watchlist)
	notifiers := notify.FromSettings(s.Channels, p.Env, p.HTTP)
	p.mu.Lock()
	defer p.mu.Unlock()
	p.Engine, p.Notify, p.Notifiers = engine, s.Notify, notifiers
}

// RecomputeRecent re-evaluates recent deals, e.g. after an action. It never
// notifies: undoing a mute shouldn't ping about old deals.
func (p *Pipeline) RecomputeRecent(ctx context.Context) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := p.Now()
	facts, err := p.actionFacts(ctx)
	if err != nil {
		return 0, err
	}
	recs, err := p.Store.DealsSeenSince(ctx, now.Add(-recomputeWindow))
	if err != nil {
		return 0, err
	}
	verdicts := make([]store.Verdict, 0, len(recs))
	for _, rec := range recs {
		res, err := p.evaluate(ctx, rec, facts, now)
		if err != nil {
			return 0, err
		}
		verdicts = append(verdicts, verdictOf(rec, res, now))
	}
	return len(verdicts), p.Store.SaveVerdicts(ctx, verdicts)
}

func (p *Pipeline) Prune(ctx context.Context) (int64, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.Store.Prune(ctx, p.Now().Add(-retention))
}

type actionFacts struct {
	hidden, junk map[string]bool
}

func (p *Pipeline) actionFacts(ctx context.Context) (actionFacts, error) {
	actions, err := p.Store.Actions(ctx)
	if err != nil {
		return actionFacts{}, err
	}
	f := actionFacts{hidden: map[string]bool{}, junk: map[string]bool{}}
	for _, a := range actions {
		switch a.Kind {
		case store.ActionHide:
			f.hidden[a.Value] = true
		case store.ActionJunk:
			f.junk[a.Value] = true
		}
	}
	return f, nil
}

// decide evaluates a deal and saves its verdict.
func (p *Pipeline) decide(ctx context.Context, rec store.DealRecord, af actionFacts, now time.Time) (rules.Result, error) {
	res, err := p.evaluate(ctx, rec, af, now)
	if err != nil {
		return rules.Result{}, err
	}
	return res, p.Store.SaveVerdict(ctx, verdictOf(rec, res, now))
}

func verdictOf(rec store.DealRecord, res rules.Result, now time.Time) store.Verdict {
	return store.Verdict{
		DealID: rec.ID, Decision: string(res.Decision), Score: res.Score,
		WatchlistHit: res.WatchlistHit, Reasons: res.Reasons, DecidedAt: now,
	}
}

func (p *Pipeline) evaluate(ctx context.Context, rec store.DealRecord, af actionFacts, now time.Time) (rules.Result, error) {
	n, last, err := p.Store.CountRecurrence(ctx, rec.Fingerprint, rec.ID, rec.FirstSeen, p.Engine.Window())
	if err != nil {
		return rules.Result{}, err
	}
	return p.Engine.Evaluate(rules.Deal{
		Title: rec.Title, Merchant: rec.Merchant, Category: rec.Category, Fingerprint: rec.Fingerprint,
		Temperature: rec.Temperature, InTrending: rec.InTrending,
	}, rules.Facts{
		Hidden:           af.hidden[rec.ID],
		JunkFingerprints: af.junk, Recurrence: n, LastRecurrence: last,
	}), nil
}

func (p *Pipeline) channelsFor(rec store.DealRecord, res rules.Result) []string {
	seen := map[string]bool{}
	var out []string
	for _, n := range p.Notify {
		hot := n.When.MinTemperature > 0 && rec.Temperature != nil && *rec.Temperature >= n.When.MinTemperature
		if !(n.When.Watchlist && res.WatchlistHit) && !hot {
			continue
		}
		for _, ch := range n.Channels {
			if !seen[ch] {
				seen[ch] = true
				out = append(out, ch)
			}
		}
	}
	return out
}

func (p *Pipeline) send(ctx context.Context, rec store.DealRecord, reasons []string, channel string, now time.Time) {
	sent, attempts, err := p.Store.NotificationState(ctx, rec.ID, channel)
	if err != nil {
		p.Log.Error("reading notification state", "deal", rec.ID, "channel", channel, "err", err)
		return
	}
	if sent || attempts >= maxNotifyAttempts {
		return
	}
	n, ok := p.Notifiers[channel]
	if !ok {
		p.Log.Error("no notifier for channel", "channel", channel)
		return
	}
	sendErr := n.Send(ctx, notify.DealMessage(rec.Title, rec.Price, rec.Merchant, rec.Temperature, reasons, rec.URL, rec.ImageURL))
	if sendErr != nil {
		if ctx.Err() != nil {
			return // cancelled by shutdown, not a real delivery failure
		}
		p.Log.Warn("notification failed", "deal", rec.ID, "channel", channel, "attempt", attempts+1, "err", sendErr)
	}
	if err := p.Store.RecordNotification(ctx, rec.ID, channel, sendErr == nil, now); err != nil {
		p.Log.Error("recording notification", "deal", rec.ID, "channel", channel, "err", err)
	}
}

// retryPending returns the deal|channel keys it attempted.
func (p *Pipeline) retryPending(ctx context.Context, now time.Time) map[string]bool {
	attempted := map[string]bool{}
	pending, err := p.Store.PendingNotifications(ctx, maxNotifyAttempts)
	if err != nil {
		p.Log.Error("listing pending notifications", "err", err)
		return attempted
	}
	for _, pn := range pending {
		if _, ok := p.Notifiers[pn.Channel]; !ok {
			continue // removed in the settings; nothing to retry through
		}
		rec, err := p.Store.GetDeal(ctx, pn.DealID)
		if err != nil {
			continue
		}
		v, err := p.Store.GetVerdict(ctx, pn.DealID)
		if err != nil || v.Decision != string(rules.Pass) {
			continue
		}
		attempted[pn.DealID+"|"+pn.Channel] = true
		p.send(ctx, rec, v.Reasons, pn.Channel, now)
	}
	return attempted
}

// indexProduct keeps the deal's price-history index current; a deal whose
// title lost its model code or whose price stopped parsing loses its row.
func (p *Pipeline) indexProduct(ctx context.Context, d source.Deal) error {
	models, currency, cents, ok := product.Index(d.Title, d.Price)
	if !ok {
		return p.Store.DeleteProduct(ctx, d.ID)
	}
	return p.Store.SetProduct(ctx, d.ID, models, currency, cents)
}
