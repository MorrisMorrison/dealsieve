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

// Pipeline serialises Ingest, RecomputeRecent, Prune and ApplySettings with a
// mutex. Ingest sends its notifications after unlocking, so a slow target
// doesn't hold up the others.
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

	// sources holds the enabled sources; nil until settings are applied.
	sources     map[string]bool
	multiSource bool
	// sending holds the deal|channel pairs an Ingest has claimed and is
	// sending outside the lock. Other calls skip them until the result is
	// recorded, so two polls never send the same pair at once.
	sending map[string]bool
}

// pendingSend is a notification decided and claimed under the lock, with the
// notifier and message captured then, and sent after unlocking.
type pendingSend struct {
	key, dealID, channel string
	attempt              int
	notifier             notify.Notifier
	msg                  notify.Message
}

// Ingest stores what a feed returned, re-evaluates those deals and sends
// notifications. Failed notifications from earlier polls are retried first,
// and each (deal, channel) gets at most one attempt per call. The sends
// happen after the lock is released; Ingest returns once they are recorded.
func (p *Pipeline) Ingest(ctx context.Context, feed source.Feed, deals []source.Deal) error {
	now := p.Now()
	sends, err := p.ingest(ctx, feed, deals, now)
	for _, s := range sends {
		p.deliver(ctx, s, now)
	}
	return err
}

// ingest does Ingest's work under the lock and returns the claimed sends,
// also on an error, so the ones decided before it still go out.
func (p *Pipeline) ingest(ctx context.Context, feed source.Feed, deals []source.Deal, now time.Time) ([]pendingSend, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	sends := p.retryPending(ctx)
	facts, err := p.actionFacts(ctx)
	if err != nil {
		return sends, err
	}
	for _, d := range deals {
		if p.sources != nil && !p.sources[d.Source] {
			continue // turned off between ApplySettings and SetSources
		}
		clean, key := fingerprint.Of(d.Merchant, d.Title)
		rec, err := p.Store.UpsertDeal(ctx, d, feed, clean, key, now)
		if err != nil {
			return sends, fmt.Errorf("store deal %s: %w", d.ID, err)
		}
		if err := p.indexProduct(ctx, d); err != nil {
			// Information only; the startup reindex repairs the table.
			p.Log.Warn("index product failed", "deal", d.ID, "err", err)
		}
		res, err := p.decide(ctx, rec, facts, now)
		if err != nil {
			return sends, err
		}
		if res.Decision == rules.Pass {
			for _, ch := range p.channelsFor(rec, res) {
				// A pair retried above (or listed twice) is already claimed,
				// so claim skips it: one attempt per call.
				if s, ok := p.claim(ctx, rec, res.Reasons, ch); ok {
					sends = append(sends, s)
				}
			}
		}
	}
	return sends, nil
}

// ApplySettings swaps the rule engine and notifiers in place; a poll in
// flight finishes with the old ones.
func (p *Pipeline) ApplySettings(s settings.Settings) {
	engine := rules.New(s.Rules, s.Watchlist, s.SourceMinimums())
	notifiers := notify.FromSettings(s.Channels, p.Env, p.HTTP)
	p.mu.Lock()
	defer p.mu.Unlock()
	p.Engine, p.Notify, p.Notifiers = engine, s.Notify, notifiers
	enabled := s.EnabledSources()
	p.sources = make(map[string]bool, len(enabled))
	for _, name := range enabled {
		p.sources[name] = true
	}
	p.multiSource = len(enabled) > 1
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
	n, last, err := p.Store.CountRecurrence(ctx, rec.Source, rec.Fingerprint, rec.ID, rec.FirstSeen, p.Engine.Window())
	if err != nil {
		return rules.Result{}, err
	}
	return p.Engine.Evaluate(rules.Deal{
		Title: rec.Title, Merchant: rec.Merchant, Category: rec.Category, Fingerprint: rec.Fingerprint,
		Source: rec.Source, Temperature: rec.Temperature, InTrending: rec.InTrending,
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

// label names the source in messages, only when several sources are on.
func (p *Pipeline) label(src string) string {
	if !p.multiSource {
		return ""
	}
	return settings.SourceLabel(src)
}

// claim decides whether (deal, channel) gets an attempt now and, if so,
// marks it as being sent and captures the notifier and message. Callers hold
// p.mu; deliver releases the claim.
func (p *Pipeline) claim(ctx context.Context, rec store.DealRecord, reasons []string, channel string) (pendingSend, bool) {
	key := rec.ID + "|" + channel
	if p.sending[key] {
		return pendingSend{}, false // another call (or this one) is sending it
	}
	sent, attempts, err := p.Store.NotificationState(ctx, rec.ID, channel)
	if err != nil {
		p.Log.Error("reading notification state", "deal", rec.ID, "channel", channel, "err", err)
		return pendingSend{}, false
	}
	if sent || attempts >= maxNotifyAttempts {
		return pendingSend{}, false
	}
	n, ok := p.Notifiers[channel]
	if !ok {
		p.Log.Error("no notifier for channel", "channel", channel)
		return pendingSend{}, false
	}
	if p.sending == nil {
		p.sending = map[string]bool{}
	}
	p.sending[key] = true
	return pendingSend{
		key: key, dealID: rec.ID, channel: channel, attempt: attempts + 1, notifier: n,
		msg: notify.DealMessage(p.label(rec.Source), rec.Title, rec.Price, rec.Merchant, rec.Temperature, reasons, rec.URL, rec.ImageURL),
	}, true
}

// deliver sends a claimed notification without the lock, records the attempt
// and only then releases the claim, so the next claim sees the result.
func (p *Pipeline) deliver(ctx context.Context, s pendingSend, now time.Time) {
	defer func() {
		p.mu.Lock()
		delete(p.sending, s.key)
		p.mu.Unlock()
	}()
	sendErr := s.notifier.Send(ctx, s.msg)
	if sendErr != nil {
		if ctx.Err() != nil {
			return // cancelled by shutdown or by the source being turned off mid-poll, not a delivery failure
		}
		p.Log.Warn("notification failed", "deal", s.dealID, "channel", s.channel, "attempt", s.attempt, "err", sendErr)
	}
	// Record even if ctx ends right now: a send that went out must count.
	if err := p.Store.RecordNotification(context.WithoutCancel(ctx), s.dealID, s.channel, sendErr == nil, now); err != nil {
		p.Log.Error("recording notification", "deal", s.dealID, "channel", s.channel, "err", err)
	}
}

// retryPending claims the failed notifications that are due another attempt.
func (p *Pipeline) retryPending(ctx context.Context) []pendingSend {
	var sends []pendingSend
	pending, err := p.Store.PendingNotifications(ctx, maxNotifyAttempts)
	if err != nil {
		p.Log.Error("listing pending notifications", "err", err)
		return nil
	}
	for _, pn := range pending {
		if _, ok := p.Notifiers[pn.Channel]; !ok {
			continue // removed in the settings; nothing to retry through
		}
		rec, err := p.Store.GetDeal(ctx, pn.DealID)
		if err != nil {
			continue
		}
		if p.sources != nil && !p.sources[rec.Source] {
			continue // source turned off; nothing to retry
		}
		v, err := p.Store.GetVerdict(ctx, pn.DealID)
		if err != nil || v.Decision != string(rules.Pass) {
			continue
		}
		if s, ok := p.claim(ctx, rec, v.Reasons, pn.Channel); ok {
			sends = append(sends, s)
		}
	}
	return sends
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
