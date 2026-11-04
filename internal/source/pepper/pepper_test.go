package pepper

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/MorrisMorrison/dealsieve/internal/settings"
	"github.com/MorrisMorrison/dealsieve/internal/source"
)

func parseFile(t *testing.T, name, site string) ([]source.Deal, []error) {
	t.Helper()
	f, err := os.Open("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	deals, itemErrs, err := Parse(f, site)
	if err != nil {
		t.Fatal(err)
	}
	return deals, itemErrs
}

func TestParseTrending(t *testing.T) {
	deals, itemErrs := parseFile(t, "trending.xml", "mydealz")
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
	deals, itemErrs := parseFile(t, "new.xml", "mydealz")
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
	_, _, err := Parse(strings.NewReader(`<!DOCTYPE html><html><head><title>Redirecting</title></head><body></body></html>`), "mydealz")
	if err == nil {
		t.Fatal("want an error for an HTML page")
	}
}

func newClient(t *testing.T, url string, hc *http.Client) *Client {
	return &Client{Site: mustLookup(t, "mydealz"), BaseURL: url, UserAgent: "dealsieve-test", HTTP: hc, Log: slog.New(slog.DiscardHandler)}
}

func TestFetchUsesFeedPathAndUserAgent(t *testing.T) {
	var gotPath, gotUA string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotUA = r.URL.Path, r.UserAgent()
		http.ServeFile(w, r, "testdata/trending.xml")
	}))
	defer srv.Close()

	deals, err := newClient(t, srv.URL, srv.Client()).Fetch(context.Background(), source.FeedTrending)
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

	if _, err := newClient(t, srv.URL, srv.Client()).Fetch(context.Background(), source.FeedNew); err != nil {
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

	_, err := newClient(t, srv.URL, srv.Client()).Fetch(context.Background(), source.FeedTrending)
	var se *StatusError
	if !errors.As(err, &se) || se.Code != http.StatusServiceUnavailable {
		t.Fatalf("err = %v, want StatusError 503", err)
	}
}

func TestFetchUnknownFeed(t *testing.T) {
	if _, err := newClient(t, "http://127.0.0.1:1", http.DefaultClient).Fetch(context.Background(), "hot"); err == nil {
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
		deals, itemErrs, err := Parse(strings.NewReader(feed), "mydealz")
		if err != nil || len(itemErrs) != 0 || len(deals) != 1 {
			t.Fatalf("image %q: deals=%d itemErrs=%v err=%v", img, len(deals), itemErrs, err)
		}
		if deals[0].ImageURL != "" {
			t.Errorf("image %q kept as %q", img, deals[0].ImageURL)
		}
	}
}

func mustLookup(t *testing.T, name string) Site {
	t.Helper()
	s, ok := Lookup(name)
	if !ok {
		t.Fatalf("no site %q", name)
	}
	return s
}

func TestSitesTable(t *testing.T) {
	want := map[string][2]string{
		"mydealz":     {"https://www.mydealz.de", "/rss/alles"},
		"hotukdeals":  {"https://www.hotukdeals.com", "/rss/new"},
		"dealabs":     {"https://www.dealabs.com", "/rss/tous"},
		"preisjaeger": {"https://www.preisjaeger.at", "/rss/alle"},
		"chollometro": {"https://www.chollometro.com", "/rss/nuevos"},
		"pepperpl":    {"https://www.pepper.pl", "/rss/wszystkie"},
	}
	var names []string
	for _, s := range Sites() {
		names = append(names, s.Name)
		if w := want[s.Name]; w != [2]string{s.BaseURL, s.NewPath} {
			t.Errorf("%s = %+v, want %v", s.Name, s, w)
		}
	}
	if !slices.Equal(names, settings.SourceNames) {
		t.Errorf("site names %v, settings.SourceNames %v", names, settings.SourceNames)
	}
}

func TestParseEverySite(t *testing.T) {
	cases := []struct {
		site, file, firstID, firstPrice string
		temp                            bool
	}{
		{"hotukdeals", "hotukdeals-trending.xml", "hotukdeals:5000001", "£6.79", true},
		{"hotukdeals", "hotukdeals-new.xml", "hotukdeals:5000003", "£40", false},
		{"dealabs", "dealabs-trending.xml", "dealabs:3000001", "1 504,89€", true},
		{"dealabs", "dealabs-new.xml", "dealabs:3000003", "29,90€", false},
		{"preisjaeger", "preisjaeger-trending.xml", "preisjaeger:300001", "41,37€", true},
		{"preisjaeger", "preisjaeger-new.xml", "preisjaeger:300003", "19,99€", false},
		{"chollometro", "chollometro-trending.xml", "chollometro:2000001", "16,80€", true},
		{"chollometro", "chollometro-new.xml", "chollometro:2000003", "24,95€", false},
		{"pepperpl", "pepperpl-trending.xml", "pepperpl:1000001", "309,03zł", true},
		{"pepperpl", "pepperpl-new.xml", "pepperpl:1000003", "89zł", false},
	}
	for _, c := range cases {
		t.Run(c.file, func(t *testing.T) {
			deals, itemErrs := parseFile(t, c.file, c.site)
			if len(itemErrs) != 0 || len(deals) != 2 {
				t.Fatalf("got %d deals, item errors %v; want 2 and none", len(deals), itemErrs)
			}
			d := deals[0]
			if d.ID != c.firstID || d.Source != c.site || d.Price != c.firstPrice || (d.Temperature != nil) != c.temp {
				t.Errorf("deal 0 = %+v (temperature %v)", d, d.Temperature)
			}
		})
	}
}

func TestFetchUsesEachSitesPaths(t *testing.T) {
	for _, site := range Sites() {
		t.Run(site.Name, func(t *testing.T) {
			var paths []string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				paths = append(paths, r.URL.Path)
				if ua := r.Header.Get("User-Agent"); ua != "dealsieve-test" {
					t.Errorf("User-Agent = %q", ua)
				}
				w.Write([]byte(`<?xml version="1.0"?><rss version="2.0"><channel></channel></rss>`))
			}))
			defer srv.Close()
			c := &Client{Site: site, BaseURL: srv.URL, UserAgent: "dealsieve-test", HTTP: srv.Client(), Log: slog.New(slog.DiscardHandler)}
			for _, feed := range []source.Feed{source.FeedTrending, source.FeedNew} {
				if _, err := c.Fetch(context.Background(), feed); err != nil {
					t.Fatal(err)
				}
			}
			if want := []string{TrendingPath, site.NewPath}; !slices.Equal(paths, want) {
				t.Errorf("paths = %v, want %v", paths, want)
			}
		})
	}
}
