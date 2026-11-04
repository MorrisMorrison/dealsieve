package digest

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/MorrisMorrison/dealsieve/internal/config"
	"github.com/MorrisMorrison/dealsieve/internal/notify"
	"github.com/MorrisMorrison/dealsieve/internal/settings"
	"github.com/MorrisMorrison/dealsieve/internal/store"
)

// firstWindow is how far the first digest of a channel reaches back.
const firstWindow = 24 * time.Hour

type TestResult struct {
	Channel string `json:"channel"`
	OK      bool   `json:"ok"`
	Error   string `json:"error,omitempty"`
}

// Runner sends the daily digest. It holds no lock of the pipeline: a slow
// target delays only the digest.
type Runner struct {
	Store    *store.Store
	Settings func() settings.Settings
	Env      config.Env
	HTTP     *http.Client
	// Notifiers builds the channel senders; nil means notify.FromSettings.
	Notifiers func(map[string]settings.Channel) map[string]notify.Notifier
	Now       func() time.Time
	Log       *slog.Logger

	// warned holds the local day each channel was last warned about having
	// no sender. Run is a single goroutine, so no lock.
	warned map[string]string
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
		}
	}
}

func (r *Runner) notifiers(s settings.Settings) map[string]notify.Notifier {
	if r.Notifiers != nil {
		return r.Notifiers(s.Channels)
	}
	return notify.FromSettings(s.Channels, r.Env, r.HTTP)
}

// Tick sends every channel's digest that is due. A successful send records
// the listed deals and moves the cutoff; a failed send stores nothing, so the
// next tick tries again; an empty window moves the cutoff without sending.
func (r *Runner) Tick(ctx context.Context) {
	s := r.Settings()
	if !s.Digest.Enabled {
		return
	}
	now := r.Now()
	senders := r.notifiers(s)
	for _, ch := range s.Digest.Channels {
		last, hasLast, err := r.Store.DigestCutoff(ctx, ch)
		if err != nil {
			r.Log.Error("digest cutoff", "channel", ch, "err", err)
			continue
		}
		if !Due(now, s.Digest, last, hasLast) {
			continue
		}
		msg, ids, err := r.compose(ctx, ch, s, last, hasLast, now)
		if err != nil {
			r.Log.Error("digest deals", "channel", ch, "err", err)
			continue
		}
		if len(ids) == 0 {
			if err := r.Store.SetDigestCutoff(ctx, ch, now); err != nil {
				r.Log.Error("store digest cutoff", "channel", ch, "err", err)
			}
			continue
		}
		n := senders[ch]
		if n == nil {
			r.warnNoSender(ch, now.In(s.Digest.Location()).Format(time.DateOnly))
			continue
		}
		if err := n.Send(ctx, msg); err != nil {
			r.Log.Warn("digest failed, retrying next minute", "channel", ch, "err", notify.Reason(err))
			continue
		}
		r.Log.Info("digest sent", "channel", ch)
		if err := r.Store.CompleteDigest(ctx, ch, ids, now); err != nil {
			r.Log.Error("store digest sent", "channel", ch, "err", err)
		}
	}
}

// warnNoSender logs that the channel can't send at most once per local day:
// the digest stays due, so Tick would otherwise warn every minute.
func (r *Runner) warnNoSender(ch, day string) {
	if r.warned[ch] == day {
		return
	}
	if r.warned == nil {
		r.warned = map[string]string{}
	}
	r.warned[ch] = day
	r.Log.Warn("digest channel can't send", "channel", ch)
}

// SendTest sends each digest channel its digest for the current window
// right away, without moving the window or recording the deals as sent.
func (r *Runner) SendTest(ctx context.Context) []TestResult {
	s := r.Settings()
	now := r.Now()
	senders := r.notifiers(s)
	warnings := settings.Warnings(s, r.Env.TelegramToken != "")
	var out []TestResult
	for _, ch := range s.Digest.Channels {
		res := TestResult{Channel: ch}
		if w := warnings[ch]; w != "" {
			res.Error = w
			out = append(out, res)
			continue
		}
		last, hasLast, err := r.Store.DigestCutoff(ctx, ch)
		var msg notify.Message
		var ids []string
		if err == nil {
			msg, ids, err = r.compose(ctx, ch, s, last, hasLast, now)
		}
		switch {
		case err != nil:
			res.Error = "couldn't read the deals"
			r.Log.Error("digest test", "channel", ch, "err", err)
		case senders[ch] == nil:
			res.Error = "channel can't send"
		default:
			if len(ids) == 0 {
				msg = emptyMessage(windowStart(last, hasLast, now), s.Digest.Location(), r.Env.PublicURL)
			}
			if err := senders[ch].Send(ctx, msg); err != nil {
				res.Error = notify.Reason(err)
			} else {
				res.OK = true
			}
		}
		out = append(out, res)
	}
	return out
}

func windowStart(last time.Time, hasLast bool, now time.Time) time.Time {
	if hasLast {
		return last
	}
	return now.Add(-firstWindow)
}

// compose builds the channel's digest and returns the ids of the deals it
// lists; no ids means there is nothing to send.
func (r *Runner) compose(ctx context.Context, ch string, s settings.Settings, last time.Time, hasLast bool, now time.Time) (notify.Message, []string, error) {
	sources := s.EnabledSources()
	hits, top, err := r.Store.DigestDeals(ctx, ch, sources, windowStart(last, hasLast, now), s.Digest.Top)
	if err != nil {
		return notify.Message{}, nil, err
	}
	if len(hits) == 0 && len(top) == 0 {
		return notify.Message{}, nil, nil
	}
	ids := make([]string, 0, len(hits)+len(top))
	for _, rec := range append(hits, top...) {
		ids = append(ids, rec.ID)
	}
	ops, err := r.Store.Opinions(ctx, ids)
	if err != nil {
		return notify.Message{}, nil, err
	}
	multi := len(sources) > 1
	return Compose(items(hits, ops, multi), items(top, ops, multi), r.Env.PublicURL), ids, nil
}

// items adds the judge's opinion and, only when several sources are on, the
// deal's site.
func items(recs []store.DealRecord, ops map[string]store.OpinionRecord, multi bool) []Item {
	out := make([]Item, len(recs))
	for i, rec := range recs {
		out[i] = Item{Title: rec.Title, URL: rec.URL, Price: rec.Price, Temperature: rec.Temperature}
		if multi {
			out[i].Source = settings.SourceLabel(rec.Source)
		}
		if o, ok := ops[rec.ID]; ok {
			out[i].Opinion = o.Rating + ": " + o.Reason
		}
	}
	return out
}

func emptyMessage(since time.Time, loc *time.Location, publicURL string) notify.Message {
	title := "dealsieve · nothing new since " + since.In(loc).Format("Mon 15:04")
	body := "This is a test digest. The real one only goes out when there are new deals."
	return notify.Message{Title: title, Body: body, ClickURL: publicURL,
		HTML: "<b>" + title + "</b>\n\n" + body}
}
