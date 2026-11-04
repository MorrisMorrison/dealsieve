package judge

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func reply(content string) string {
	b, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": content}}}})
	return string(b)
}

func TestOpenAIJudge(t *testing.T) {
	var gotPath, gotAuth, gotUA, gotTitle string
	var body struct {
		Model     string    `json:"model"`
		Messages  []Message `json:"messages"`
		MaxTokens int       `json:"max_tokens"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotAuth, gotUA, gotTitle = r.URL.Path, r.Header.Get("Authorization"), r.Header.Get("User-Agent"), r.Header.Get("X-Title")
		json.NewDecoder(r.Body).Decode(&body)
		io.WriteString(w, reply(`{"rating":"bad","reason":"Bag fees make it about €90."}`))
	}))
	defer srv.Close()

	o := &OpenAI{BaseURL: srv.URL + "/api/v1", APIKey: "test-key", Model: "example/model", UserAgent: "dealsieve/test", HTTP: srv.Client()}
	op, err := o.Judge(context.Background(), Deal{Title: "Example Air Flash Sale"})
	if err != nil {
		t.Fatal(err)
	}
	if op != (Opinion{Bad, "Bag fees make it about €90."}) {
		t.Errorf("opinion = %+v", op)
	}
	if gotPath != "/api/v1/chat/completions" || gotAuth != "Bearer test-key" || gotUA != "dealsieve/test" || gotTitle != "dealsieve" {
		t.Errorf("path=%q auth=%q ua=%q title=%q", gotPath, gotAuth, gotUA, gotTitle)
	}
	if body.Model != "example/model" || len(body.Messages) != 2 || body.MaxTokens != 400 {
		t.Errorf("body = %+v", body)
	}
}

func TestOpenAINoKeyNoAuthHeader(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if h := r.Header.Get("Authorization"); h != "" {
			t.Errorf("Authorization = %q, want none", h)
		}
		io.WriteString(w, reply(`{"rating":"good","reason":"ok"}`))
	}))
	defer srv.Close()
	o := &OpenAI{BaseURL: srv.URL, Model: "m", HTTP: srv.Client()}
	if _, err := o.Judge(context.Background(), Deal{Title: "x"}); err != nil {
		t.Fatal(err)
	}
}

func TestOpenAIErrors(t *testing.T) {
	tests := []struct {
		name      string
		status    int
		body      string
		retryable bool
		reason    string
	}{
		{"unauthorized", 401, `{"error":{"message":"bad key sk-secret"}}`, false, "HTTP 401"},
		{"rate limited", 429, `slow down`, true, "HTTP 429"},
		{"server error", 502, `oops`, true, "HTTP 502"},
		{"garbage", 200, `not json`, false, "reply wasn't a valid opinion"},
		{"no choices", 200, `{"choices":[]}`, false, "no reply"},
		{"bad opinion", 200, reply("I like it"), false, "reply wasn't a valid opinion"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.status)
				io.WriteString(w, tt.body)
			}))
			defer srv.Close()
			o := &OpenAI{BaseURL: srv.URL, APIKey: "sk-secret", Model: "m", HTTP: srv.Client()}
			_, err := o.Judge(context.Background(), Deal{Title: "x"})
			if err == nil {
				t.Fatal("want an error")
			}
			if Retryable(err) != tt.retryable {
				t.Errorf("Retryable = %v, want %v", Retryable(err), tt.retryable)
			}
			if got := Reason(err); got != tt.reason {
				t.Errorf("Reason = %q, want %q", got, tt.reason)
			}
			if strings.Contains(err.Error(), "sk-secret") || strings.Contains(Reason(err), "sk-secret") {
				t.Error("error leaks the key or the response body")
			}
		})
	}
}

func TestOpenAITimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The server only notices the client hanging up once the body is read.
		io.Copy(io.Discard, r.Body)
		<-r.Context().Done()
	}))
	defer srv.Close()
	o := &OpenAI{BaseURL: srv.URL, Model: "m", HTTP: &http.Client{Timeout: 50 * time.Millisecond}}
	_, err := o.Judge(context.Background(), Deal{Title: "x"})
	if err == nil || Reason(err) != "timed out" || !Retryable(err) {
		t.Fatalf("err = %v, reason %q, retryable %v", err, Reason(err), Retryable(err))
	}
}

func TestReasonUnreachable(t *testing.T) {
	o := &OpenAI{BaseURL: "http://127.0.0.1:1", Model: "m", HTTP: &http.Client{Timeout: time.Second}}
	_, err := o.Judge(context.Background(), Deal{Title: "x"})
	if Reason(err) != "couldn't reach the server" {
		t.Fatalf("Reason = %q (%v)", Reason(err), err)
	}
	if Reason(ErrDisabled) != "judge is off" {
		t.Errorf("Reason(ErrDisabled) = %q", Reason(ErrDisabled))
	}
}
