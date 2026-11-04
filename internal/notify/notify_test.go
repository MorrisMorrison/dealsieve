package notify

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MorrisMorrison/dealsieve/internal/config"
	"github.com/MorrisMorrison/dealsieve/internal/settings"
)

var ctx = context.Background()

func intp(v int) *int { return &v }

func TestDealMessage(t *testing.T) {
	m := DealMessage("Example OLED TV", "899€", "Example Shop", intp(142), []string{"watchlist: oled"}, "https://d.example.test/1", "https://i.example.test/1.jpg")
	want := Message{Title: "Example OLED TV", Body: "899€ · 142° · Example Shop\nwatchlist: oled", ClickURL: "https://d.example.test/1", ImageURL: "https://i.example.test/1.jpg"}
	if m != want {
		t.Errorf("got %+v, want %+v", m, want)
	}
	if m := DealMessage("T", "", "", nil, nil, "u", ""); m.Body != "" {
		t.Errorf("empty body = %q", m.Body)
	}
	// Reasons contain commas themselves, so each gets its own line.
	m = DealMessage("T", "9€", "", nil, []string{"watchlist: oled", "below the 100° minimum, kept for your watchlist"}, "u", "")
	if want := "9€\nwatchlist: oled\nbelow the 100° minimum, kept for your watchlist"; m.Body != want {
		t.Errorf("body = %q, want %q", m.Body, want)
	}
}

func TestNtfySend(t *testing.T) {
	var got map[string]any
	var auth, path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth, path = r.Header.Get("Authorization"), r.URL.Path
		json.NewDecoder(r.Body).Decode(&got)
	}))
	defer srv.Close()

	n := &Ntfy{BaseURL: srv.URL, Topic: "deals", Priority: "high", Token: "tk", HTTP: srv.Client()}
	if err := n.Send(ctx, Message{Title: "Döner", Body: "b", ClickURL: "https://d.example.test", ImageURL: "https://i.example.test/x.jpg"}); err != nil {
		t.Fatal(err)
	}
	if path != "/" || auth != "Bearer tk" {
		t.Errorf("path %q, auth %q", path, auth)
	}
	if got["topic"] != "deals" || got["title"] != "Döner" || got["message"] != "b" || got["priority"] != float64(4) ||
		got["click"] != "https://d.example.test" || got["attach"] != "https://i.example.test/x.jpg" {
		t.Errorf("payload = %v", got)
	}
}

func TestNtfyOmitsEmptyAttachAndAuth(t *testing.T) {
	var got map[string]any
	var auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		json.NewDecoder(r.Body).Decode(&got)
	}))
	defer srv.Close()

	if err := (&Ntfy{BaseURL: srv.URL, Topic: "deals", HTTP: srv.Client()}).Send(ctx, Message{Title: "t"}); err != nil {
		t.Fatal(err)
	}
	if _, ok := got["attach"]; ok || auth != "" || got["priority"] != float64(3) {
		t.Errorf("payload = %v, auth %q", got, auth)
	}
}

func TestNtfyErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusInternalServerError)
	}))
	defer srv.Close()
	if err := (&Ntfy{BaseURL: srv.URL, Topic: "t", HTTP: srv.Client()}).Send(ctx, Message{}); err == nil {
		t.Fatal("want an error")
	}
}

type telegramCall struct {
	path    string
	payload map[string]string
}

func telegramServer(t *testing.T, photoStatus int) (*httptest.Server, *[]telegramCall) {
	t.Helper()
	var calls []telegramCall
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var p map[string]string
		json.NewDecoder(r.Body).Decode(&p)
		calls = append(calls, telegramCall{r.URL.Path, p})
		if photoStatus != 0 && strings.HasSuffix(r.URL.Path, "/sendPhoto") {
			http.Error(w, `{"ok":false}`, photoStatus)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

func TestTelegramSendsPhoto(t *testing.T) {
	srv, calls := telegramServer(t, 0)
	tg := &Telegram{APIBase: srv.URL, Token: "TOKEN", ChatID: "42", HTTP: srv.Client()}
	if err := tg.Send(ctx, Message{Title: "Deal", Body: "9€", ClickURL: "https://d.example.test", ImageURL: "https://i.example.test/x.jpg"}); err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 1 {
		t.Fatalf("calls = %v", *calls)
	}
	c := (*calls)[0]
	if c.path != "/botTOKEN/sendPhoto" || c.payload["chat_id"] != "42" || c.payload["photo"] != "https://i.example.test/x.jpg" ||
		c.payload["caption"] != "Deal\n9€\nhttps://d.example.test" {
		t.Errorf("call = %+v", c)
	}
}

func TestTelegramFallsBackToText(t *testing.T) {
	srv, calls := telegramServer(t, http.StatusBadRequest)
	tg := &Telegram{APIBase: srv.URL, Token: "TOKEN", ChatID: "42", HTTP: srv.Client()}
	if err := tg.Send(ctx, Message{Title: "Deal", ImageURL: "https://i.example.test/x.jpg"}); err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 2 || (*calls)[1].path != "/botTOKEN/sendMessage" || (*calls)[1].payload["text"] != "Deal\n" {
		t.Errorf("calls = %+v", *calls)
	}
}

func TestTelegramDoesNotFallBackOnServerError(t *testing.T) {
	srv, calls := telegramServer(t, http.StatusInternalServerError)
	tg := &Telegram{APIBase: srv.URL, Token: "TOKEN", ChatID: "42", HTTP: srv.Client()}
	err := tg.Send(ctx, Message{Title: "Deal", ImageURL: "https://i.example.test/x.jpg"})
	if err == nil {
		t.Fatal("want the sendPhoto error")
	}
	if len(*calls) != 1 || (*calls)[0].path != "/botTOKEN/sendPhoto" {
		t.Errorf("calls = %+v, want only sendPhoto", *calls)
	}
}

func TestTelegramErrorHidesToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	srv.Close() // connection refused
	tg := &Telegram{APIBase: srv.URL, Token: "SECRET123", ChatID: "42", HTTP: http.DefaultClient}
	err := tg.Send(ctx, Message{Title: "x"})
	if err == nil || strings.Contains(err.Error(), "SECRET123") {
		t.Fatalf("err = %v; want an error without the token", err)
	}
}

func TestFromSettings(t *testing.T) {
	got := FromSettings(map[string]settings.Channel{
		"a": {Type: "ntfy", URL: "https://n.example.com", Topic: "t", Priority: "low"},
		"b": {Type: "telegram", ChatID: "1"},
	}, config.Env{TelegramToken: "tok", NtfyToken: "nt"}, http.DefaultClient)
	if n, ok := got["a"].(*Ntfy); !ok || n.Token != "nt" || n.Priority != "low" {
		t.Errorf("a = %#v", got["a"])
	}
	if tg, ok := got["b"].(*Telegram); !ok || tg.Token != "tok" || tg.APIBase != "https://api.telegram.org" {
		t.Errorf("b = %#v", got["b"])
	}
}

func TestForChannelUnknownType(t *testing.T) {
	if ForChannel(settings.Channel{Type: "email"}, config.Env{}, nil) != nil {
		t.Error("unknown type")
	}
}

func TestTelegramSendsHTML(t *testing.T) {
	srv, calls := telegramServer(t, 0)
	tg := &Telegram{APIBase: srv.URL, Token: "TOKEN", ChatID: "42", HTTP: srv.Client()}
	html := `<b>dealsieve · 1 new deal</b>` + "\n" + `<a href="https://d.example.test">A &amp; B</a>`
	if err := tg.Send(ctx, Message{Title: "plain", Body: "plain", ClickURL: "https://x.example.test", HTML: html}); err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 1 {
		t.Fatalf("calls = %+v", *calls)
	}
	c := (*calls)[0]
	if c.path != "/botTOKEN/sendMessage" || c.payload["text"] != html || c.payload["parse_mode"] != "HTML" {
		t.Errorf("call = %+v", c)
	}
}

func TestNtfyIgnoresHTML(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&got)
	}))
	defer srv.Close()
	n := &Ntfy{BaseURL: srv.URL, Topic: "deals", HTTP: srv.Client()}
	if err := n.Send(ctx, Message{Title: "T", Body: "plain body", HTML: "<b>x</b>"}); err != nil {
		t.Fatal(err)
	}
	if got["message"] != "plain body" {
		t.Errorf("ntfy got %v", got)
	}
}
