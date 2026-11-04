package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/MorrisMorrison/dealsieve/internal/notify"
	"github.com/MorrisMorrison/dealsieve/internal/settings"
	"github.com/MorrisMorrison/dealsieve/internal/settingsvc"
	"github.com/MorrisMorrison/dealsieve/internal/source/pepper"
	"github.com/MorrisMorrison/dealsieve/internal/suggest"
)

const (
	settingsBodyLimit = 64 << 10
	recheckWarning    = "Saved, but re-checking recent deals failed; they'll be re-checked on the next poll."
)

func (s *Server) settingsBody(snap settingsvc.Snapshot) map[string]any {
	return map[string]any{
		"version":    snap.Version,
		"updated_at": snap.UpdatedAt,
		"settings":   snap.Settings,
		"warnings":   settings.Warnings(snap.Settings, s.Env.TelegramToken != "", notify.Binding(s.Env)),
		"judge_env":  map[string]any{"url": s.Env.JudgeURL, "key_set": s.Env.JudgeAPIKey != ""},
		"sites":      siteList(),
	}
}

type siteJSON struct {
	Name   string `json:"name"`
	Label  string `json:"label"`
	Domain string `json:"domain"`
}

// siteList names every site the Settings page can switch on; the domain is
// shown next to the label, so it comes from the fixed table, not from env.
func siteList() []siteJSON {
	var out []siteJSON
	for _, site := range pepper.Sites() {
		domain := site.BaseURL
		if u, err := url.Parse(site.BaseURL); err == nil {
			domain = strings.TrimPrefix(u.Hostname(), "www.")
		}
		out = append(out, siteJSON{Name: site.Name, Label: settings.SourceLabel(site.Name), Domain: domain})
	}
	return out
}

func (s *Server) writeResult(w http.ResponseWriter, res settingsvc.Result) {
	writeJSON(w, http.StatusOK, s.resultBody(res))
}

func (s *Server) resultBody(res settingsvc.Result) map[string]any {
	body := s.settingsBody(res.Snapshot)
	body["rechecked"], body["changed"] = res.Rechecked, res.Changed
	return body
}

func (s *Server) getSettings(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.settingsBody(s.Settings.Current()))
}

func (s *Server) putSettings(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Version  int64           `json:"version"`
		Settings json.RawMessage `json:"settings"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, settingsBodyLimit)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	next, err := settings.Decode(req.Settings)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid settings: "+err.Error())
		return
	}
	res, err := s.Settings.Save(r.Context(), req.Version, next)
	s.respondSave(w, res, err)
}

func (s *Server) undoSettings(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Version int64 `json:"version"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	res, err := s.Settings.Undo(r.Context(), req.Version)
	s.respondSave(w, res, err)
}

func (s *Server) respondSave(w http.ResponseWriter, res settingsvc.Result, err error) {
	var verr settingsvc.ValidationError
	switch {
	case err != nil && res.Changed:
		// Saved and applied, but re-checking recent deals failed.
		s.Log.Error("re-check after settings change failed", "err", err)
		body := s.resultBody(res)
		body["warning"] = recheckWarning
		writeJSON(w, http.StatusOK, body)
	case errors.Is(err, settingsvc.ErrConflict):
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error(), "code": CodeStale})
	case errors.Is(err, settingsvc.ErrNothingToUndo):
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error(), "code": CodeNothingToUndo})
	case errors.As(err, &verr):
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"errors": verr})
	case err != nil:
		s.fail(w, err)
	default:
		s.writeResult(w, res)
	}
}

func (s *Server) settingsHistory(w http.ResponseWriter, r *http.Request) {
	entries, err := s.Settings.History(r.Context(), 20)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"history": entries})
}

// testChannel sends a test message through a channel as given in the form,
// before it is saved.
func (s *Server) testChannel(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Channel settings.Channel `json:"channel"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	ch := settings.Settings{Channels: map[string]settings.Channel{"test": req.Channel}}.Normalize().Channels["test"]
	if errs := settings.ValidateChannel("test", ch); len(errs) > 0 {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"errors": errs})
		return
	}
	// Only blocking problems stop a test; the ntfy token binding just decides whether the token is attached.
	if warn := settings.Warnings(settings.Settings{Channels: map[string]settings.Channel{"test": ch}}, s.Env.TelegramToken != "", settings.NtfyBinding{}); warn["test"] != "" {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": warn["test"]})
		return
	}
	err := notify.ForChannel(ch, s.Env, s.HTTP).Send(r.Context(), notify.Message{
		Title: "dealsieve test notification",
		Body:  "If you can read this, the channel works.",
	})
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": notify.Reason(err)})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) suggestions(w http.ResponseWriter, r *http.Request) {
	s.writeSuggestions(w, r, "")
}

func (s *Server) writeSuggestions(w http.ResponseWriter, r *http.Request, warning string) {
	list, err := s.Settings.Suggestions(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	body := map[string]any{"suggestions": list}
	if warning != "" {
		body["warning"] = warning
	}
	writeJSON(w, http.StatusOK, body)
}

type suggestionRequest struct {
	Kind  string `json:"kind"`
	Value string `json:"value"`
}

func decodeSuggestion(w http.ResponseWriter, r *http.Request) (suggestionRequest, bool) {
	var req suggestionRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return req, false
	}
	if (req.Kind != suggest.KindMuteMerchant && req.Kind != suggest.KindMuteCategory) || req.Value == "" {
		writeError(w, http.StatusBadRequest, "kind must be mute_merchant or mute_category, with a value")
		return req, false
	}
	return req, true
}

func (s *Server) applySuggestion(w http.ResponseWriter, r *http.Request) {
	req, ok := decodeSuggestion(w, r)
	if !ok {
		return
	}
	res, err := s.Settings.ApplySuggestion(r.Context(), req.Kind, req.Value)
	warning := ""
	if err != nil {
		if !res.Changed {
			s.fail(w, err)
			return
		}
		s.Log.Error("re-check after settings change failed", "err", err)
		warning = recheckWarning
	}
	s.writeSuggestions(w, r, warning)
}

func (s *Server) dismissSuggestion(w http.ResponseWriter, r *http.Request) {
	req, ok := decodeSuggestion(w, r)
	if !ok {
		return
	}
	if err := s.Settings.Dismiss(r.Context(), req.Kind, req.Value); err != nil {
		s.fail(w, err)
		return
	}
	s.writeSuggestions(w, r, "")
}

func (s *Server) categories(w http.ResponseWriter, r *http.Request) {
	list, err := s.Store.Categories(r.Context(), s.Settings.Current().Settings.EnabledSources())
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"categories": list})
}

// testDigest sends the saved digest to its channels now, without counting
// as the day's digest.
func (s *Server) testDigest(w http.ResponseWriter, r *http.Request) {
	if s.Digest == nil || len(s.Settings.Current().Settings.Digest.Channels) == 0 {
		writeError(w, http.StatusUnprocessableEntity, "Add a channel to the daily digest and save first.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": s.Digest.SendTest(r.Context())})
}

// testJudge asks the model for an opinion on a sample deal using the judge
// settings as given in the form, before they are saved.
func (s *Server) testJudge(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Judge settings.Judge `json:"judge"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	j := req.Judge
	j.Model, j.Instructions = strings.TrimSpace(j.Model), strings.TrimSpace(j.Instructions)
	if errs := settings.ValidateJudge(j, true); len(errs) > 0 {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"errors": errs})
		return
	}
	if s.Judge == nil {
		writeError(w, http.StatusServiceUnavailable, "judge isn't available")
		return
	}
	writeJSON(w, http.StatusOK, s.Judge.Test(r.Context(), j))
}
