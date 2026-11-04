// Package api serves the JSON API and the embedded page.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/MorrisMorrison/dealsieve/internal/config"
	"github.com/MorrisMorrison/dealsieve/internal/poller"
	"github.com/MorrisMorrison/dealsieve/internal/settingsvc"
	"github.com/MorrisMorrison/dealsieve/internal/store"
)

const (
	listWindow = 72 * time.Hour
	listLimit  = 200
)

type Recomputer interface {
	RecomputeRecent(ctx context.Context) (int, error)
}

type Server struct {
	Store     *store.Store
	Recompute Recomputer
	Status    func() []poller.Status
	Settings  *settingsvc.Service
	Env       config.Env
	HTTP      *http.Client
	Assets    fs.FS
	Log       *slog.Logger
	Now       func() time.Time
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/deals", s.listDeals)
	mux.HandleFunc("POST /api/actions", s.addAction)
	mux.HandleFunc("POST /api/actions/undo", s.undoAction)
	mux.HandleFunc("DELETE /api/actions/{id}", s.deleteAction)
	mux.HandleFunc("GET /api/settings", s.getSettings)
	mux.HandleFunc("PUT /api/settings", s.putSettings)
	mux.HandleFunc("POST /api/settings/undo", s.undoSettings)
	mux.HandleFunc("GET /api/settings/history", s.settingsHistory)
	mux.HandleFunc("POST /api/settings/test", s.testChannel)
	mux.HandleFunc("GET /api/suggestions", s.suggestions)
	mux.HandleFunc("POST /api/suggestions/apply", s.applySuggestion)
	mux.HandleFunc("POST /api/suggestions/dismiss", s.dismissSuggestion)
	mux.HandleFunc("GET /api/categories", s.categories)
	mux.HandleFunc("GET /api/status", s.status)
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) })
	mux.Handle("GET /", http.FileServerFS(s.Assets))
	return securityHeaders(http.NewCrossOriginProtection().Handler(mux))
}

// securityHeaders sets the headers every response should carry.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}

type dealJSON struct {
	ID           string    `json:"id"`
	URL          string    `json:"url"`
	Title        string    `json:"title"`
	Merchant     string    `json:"merchant"`
	Price        string    `json:"price"`
	Category     string    `json:"category"`
	ImageURL     string    `json:"image_url"`
	Temperature  *int      `json:"temperature"`
	Temperatures []int     `json:"temperatures"`
	FirstSeen    time.Time `json:"first_seen"`
	LastSeen     time.Time `json:"last_seen"`
	InTrending   bool      `json:"in_trending"`
	InNew        bool      `json:"in_new"`
	Decision     string    `json:"decision"`
	Score        int       `json:"score"`
	WatchlistHit bool      `json:"watchlist_hit"`
	Reasons      []string  `json:"reasons"`
}

func (s *Server) listDeals(w http.ResponseWriter, r *http.Request) {
	view := r.URL.Query().Get("view")
	if view == "" {
		view = store.ViewPassed
	}
	if !store.ValidView(view) {
		writeError(w, http.StatusBadRequest, "unknown view")
		return
	}
	listed, err := s.Store.ListDeals(r.Context(), view, s.Now().Add(-listWindow), listLimit)
	if err != nil {
		s.fail(w, err)
		return
	}
	counts, err := s.Store.CountViews(r.Context(), s.Now().Add(-listWindow))
	if err != nil {
		s.fail(w, err)
		return
	}
	ids := make([]string, len(listed))
	for i, l := range listed {
		ids[i] = l.ID
	}
	temps, err := s.Store.Temperatures(r.Context(), ids)
	if err != nil {
		s.fail(w, err)
		return
	}
	out := make([]dealJSON, 0, len(listed))
	for _, l := range listed {
		t := temps[l.ID]
		if t == nil {
			t = []int{}
		}
		reasons := l.Verdict.Reasons
		if reasons == nil {
			reasons = []string{}
		}
		out = append(out, dealJSON{
			ID: l.ID, URL: l.URL, Title: l.Title, Merchant: l.Merchant, Price: l.Price, Category: l.Category,
			ImageURL: l.ImageURL, Temperature: l.Temperature, Temperatures: t, FirstSeen: l.FirstSeen,
			LastSeen: l.LastSeen, InTrending: l.InTrending, InNew: l.InNew, Decision: l.Verdict.Decision,
			Score: l.Verdict.Score, WatchlistHit: l.Verdict.WatchlistHit, Reasons: reasons,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"deals": out, "counts": counts})
}

func (s *Server) addAction(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Kind   string `json:"kind"`
		DealID string `json:"deal_id"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if !store.ValidActionKind(req.Kind) {
		writeError(w, http.StatusBadRequest, "unknown action kind")
		return
	}
	deal, err := s.Store.GetDeal(r.Context(), req.DealID)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "deal not found")
		return
	}
	if err != nil {
		s.fail(w, err)
		return
	}
	value := map[string]string{
		store.ActionHide:         deal.ID,
		store.ActionJunk:         deal.Fingerprint,
		store.ActionMuteCategory: deal.Category,
		store.ActionMuteMerchant: deal.Merchant,
	}[req.Kind]
	if value == "" {
		writeError(w, http.StatusBadRequest, "this deal has nothing to "+req.Kind)
		return
	}
	if req.Kind == store.ActionMuteCategory || req.Kind == store.ActionMuteMerchant {
		res, err := s.Settings.Mute(r.Context(), req.Kind, value, " (from a deal)")
		body := map[string]any{"kind": req.Kind, "value": value, "version": res.Version}
		if err != nil {
			if !res.Changed {
				s.fail(w, err)
				return
			}
			s.Log.Error("re-check after settings change failed", "err", err)
			body["warning"] = recheckWarning
		}
		writeJSON(w, http.StatusCreated, body)
		return
	}
	a, err := s.Store.AddAction(r.Context(), req.Kind, value, s.Now())
	if err != nil {
		s.fail(w, err)
		return
	}
	if _, err := s.Recompute.RecomputeRecent(r.Context()); err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, a)
}

func (s *Server) deleteAction(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid action id")
		return
	}
	found, err := s.Store.DeleteAction(r.Context(), id)
	if err != nil {
		s.fail(w, err)
		return
	}
	if !found {
		writeError(w, http.StatusNotFound, "action not found")
		return
	}
	if _, err := s.Recompute.RecomputeRecent(r.Context()); err != nil {
		s.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) status(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"feeds": s.Status()})
}

func (s *Server) fail(w http.ResponseWriter, err error) {
	s.Log.Error("request failed", "err", err)
	writeError(w, http.StatusInternalServerError, "internal error")
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// undoAction removes a deal's Hide, or the Junk mark on its fingerprint.
func (s *Server) undoAction(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Kind   string `json:"kind"`
		DealID string `json:"deal_id"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if req.Kind != store.ActionHide && req.Kind != store.ActionJunk {
		writeError(w, http.StatusBadRequest, "only hide_deal and junk can be undone here")
		return
	}
	deal, err := s.Store.GetDeal(r.Context(), req.DealID)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "deal not found")
		return
	}
	if err != nil {
		s.fail(w, err)
		return
	}
	value := deal.ID
	if req.Kind == store.ActionJunk {
		value = deal.Fingerprint
	}
	if _, err := s.Store.DeleteActionsByValue(r.Context(), req.Kind, value); err != nil {
		s.fail(w, err)
		return
	}
	if _, err := s.Recompute.RecomputeRecent(r.Context()); err != nil {
		s.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
