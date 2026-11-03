package mydealz

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/MorrisMorrison/dealsieve/internal/source"
)

func parseFile(t *testing.T, name string) ([]source.Deal, []error) {
	t.Helper()
	f, err := os.Open("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	deals, itemErrs, err := Parse(f)
	if err != nil {
		t.Fatal(err)
	}
	return deals, itemErrs
}

func TestParseTrending(t *testing.T) {
	deals, itemErrs := parseFile(t, "trending.xml")
	if len(deals) != 3 || len(itemErrs) != 1 {
		t.Fatalf("got %d deals, %d item errors (%v); want 3 and 1", len(deals), len(itemErrs), itemErrs)
	}
	d := deals[0]
	if d.ID != "mydealz:1000001" || d.Source != "mydealz" || d.Title != "Example OLED TV 55 Zoll" ||
		d.Merchant != "Example Shop" || d.Price != "899€" || d.Category != "Elektronik" ||
		d.ImageURL != "https://static.example.test/1000001.jpg" ||
		d.URL != "https://www.mydealz.de/deals/example-oled-tv-55-zoll-1000001" {
		t.Errorf("deal 0 = %+v", d)
	}
	if d.Temperature == nil || *d.Temperature != 142 {
		t.Errorf("deal 0 temperature = %v, want 142", d.Temperature)
	}
	if d := deals[1]; d.Merchant != "" || d.Price != "0,01€" || d.ImageURL != "" || d.Category != "Dienstleistungen & Verträge" {
		t.Errorf("deal 1 = %+v", d)
	}
	if d := deals[2]; d.Temperature == nil || *d.Temperature != -15 || d.Title != "Example Air Flash Sale: Flüge ab 19,99€" {
		t.Errorf("deal 2 = %+v (temperature %v)", d, d.Temperature)
	}
}

func TestParseNewHasNoTemperatures(t *testing.T) {
	deals, itemErrs := parseFile(t, "new.xml")
	if len(deals) != 2 || len(itemErrs) != 1 {
		t.Fatalf("got %d deals, %d item errors; want 2 and 1", len(deals), len(itemErrs))
	}
	for _, d := range deals {
		if d.Temperature != nil {
			t.Errorf("%s: temperature = %d, want nil", d.ID, *d.Temperature)
		}
	}
	if deals[0].Title != "Example RTX 5080 Grafikkarte 16GB" {
		t.Errorf("title = %q", deals[0].Title)
	}
}

func TestParseRejectsHTML(t *testing.T) {
	_, _, err := Parse(strings.NewReader(`<!DOCTYPE html><html><head><title>Redirecting</title></head><body></body></html>`))
	if err == nil {
		t.Fatal("want an error for an HTML page")
	}
}

func newClient(url string, hc *http.Client) *Client {
	return &Client{BaseURL: url, UserAgent: "dealsieve-test", HTTP: hc, Log: slog.New(slog.DiscardHandler)}
}

func TestFetchUsesFeedPathAndUserAgent(t *testing.T) {
	var gotPath, gotUA string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotUA = r.URL.Path, r.UserAgent()
		http.ServeFile(w, r, "testdata/trending.xml")
	}))
	defer srv.Close()

	deals, err := newClient(srv.URL, srv.Client()).Fetch(context.Background(), source.FeedTrending)
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/rss/trending" || gotUA != "dealsieve-test" || len(deals) != 3 {
		t.Errorf("path %q, UA %q, %d deals", gotPath, gotUA, len(deals))
	}
}

func TestFetchNewUsesAllesFeed(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		http.ServeFile(w, r, "testdata/new.xml")
	}))
	defer srv.Close()

	if _, err := newClient(srv.URL, srv.Client()).Fetch(context.Background(), source.FeedNew); err != nil {
		t.Fatal(err)
	}
	if gotPath != "/rss/alles" {
		t.Errorf("path = %q, want /rss/alles", gotPath)
	}
}

func TestFetchStatusError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	_, err := newClient(srv.URL, srv.Client()).Fetch(context.Background(), source.FeedTrending)
	var se *StatusError
	if !errors.As(err, &se) || se.Code != http.StatusServiceUnavailable {
		t.Fatalf("err = %v, want StatusError 503", err)
	}
}

func TestFetchUnknownFeed(t *testing.T) {
	if _, err := newClient("http://127.0.0.1:1", http.DefaultClient).Fetch(context.Background(), "hot"); err == nil {
		t.Fatal("want an error for an unknown feed")
	}
}

func TestParseDropsNonHTTPImageURL(t *testing.T) {
	for _, img := range []string{"javascript:alert(1)", "data:image/png;base64,AAAA", "/relative.jpg", ""} {
		feed := `<?xml version="1.0"?><rss xmlns:media="http://search.yahoo.com/mrss/" version="2.0"><channel><item>
<media:content medium="image" url="` + img + `"/>
<title>Example Deal</title>
<link>https://www.mydealz.de/deals/example-deal-1000020</link>
</item></channel></rss>`
		deals, itemErrs, err := Parse(strings.NewReader(feed))
		if err != nil || len(itemErrs) != 0 || len(deals) != 1 {
			t.Fatalf("image %q: deals=%d itemErrs=%v err=%v", img, len(deals), itemErrs, err)
		}
		if deals[0].ImageURL != "" {
			t.Errorf("image %q kept as %q", img, deals[0].ImageURL)
		}
	}
}
