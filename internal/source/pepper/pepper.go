// Package pepper reads deals from the RSS feeds of mydealz and the other sites of the Pepper network, which share one format.
package pepper

import (
	"context"
	"encoding/xml"
	"fmt"
	"html"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/MorrisMorrison/dealsieve/internal/source"
)

const TrendingPath = "/rss/trending"

// Site is one Pepper site. Every site serves its trending feed at
// TrendingPath; NewPath is where its /rss/new redirects to, so dealsieve
// never relies on the redirect.
type Site struct {
	Name    string
	BaseURL string
	NewPath string
}

var sites = []Site{
	{"mydealz", "https://www.mydealz.de", "/rss/alles"},
	{"hotukdeals", "https://www.hotukdeals.com", "/rss/new"},
	{"dealabs", "https://www.dealabs.com", "/rss/tous"},
	{"preisjaeger", "https://www.preisjaeger.at", "/rss/alle"},
	{"chollometro", "https://www.chollometro.com", "/rss/nuevos"},
	{"pepperpl", "https://www.pepper.pl", "/rss/wszystkie"},
}

func Sites() []Site { return slices.Clone(sites) }

func Lookup(name string) (Site, bool) {
	i := slices.IndexFunc(sites, func(s Site) bool { return s.Name == name })
	if i < 0 {
		return Site{}, false
	}
	return sites[i], true
}

var (
	temperaturePrefix = regexp.MustCompile(`^\s*(-?\d+)°\s*-\s*`)
	threadID          = regexp.MustCompile(`-(\d+)/?$`)
)

type rss struct {
	XMLName xml.Name `xml:"rss"`
	Items   []item   `xml:"channel>item"`
}

type item struct {
	Title    string `xml:"title"`
	Link     string `xml:"link"`
	Category string `xml:"category"`
	Merchant struct {
		Name  string `xml:"name,attr"`
		Price string `xml:"price,attr"`
	} `xml:"http://www.pepper.com/rss merchant"`
	Media struct {
		URL string `xml:"url,attr"`
	} `xml:"http://search.yahoo.com/mrss/ content"`
}

// Parse reads a feed. Items that can't be used are reported in the second
// return value and skipped; the error is for a document that isn't a feed.
func Parse(r io.Reader, site string) ([]source.Deal, []error, error) {
	var doc rss
	if err := xml.NewDecoder(r).Decode(&doc); err != nil {
		return nil, nil, fmt.Errorf("decode feed: %w", err)
	}
	var deals []source.Deal
	var itemErrs []error
	for i, it := range doc.Items {
		d, err := toDeal(it, site)
		if err != nil {
			itemErrs = append(itemErrs, fmt.Errorf("item %d: %w", i, err))
			continue
		}
		deals = append(deals, d)
	}
	return deals, itemErrs, nil
}

// validImageURL keeps only http(s) URLs with a host; anything else is dropped.
func validImageURL(raw string) string {
	raw = strings.TrimSpace(raw)
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return ""
	}
	return raw
}

func toDeal(it item, site string) (source.Deal, error) {
	link := strings.TrimSpace(it.Link)
	u, err := url.Parse(link)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return source.Deal{}, fmt.Errorf("bad link %q", link)
	}
	m := threadID.FindStringSubmatch(u.Path)
	if m == nil {
		return source.Deal{}, fmt.Errorf("no thread id in link %q", link)
	}
	// Pepper double-escapes entities in attributes and CDATA is never decoded.
	title := html.UnescapeString(strings.TrimSpace(it.Title))
	var temperature *int
	if tm := temperaturePrefix.FindStringSubmatch(title); tm != nil {
		t, err := strconv.Atoi(tm[1])
		if err != nil {
			return source.Deal{}, fmt.Errorf("bad temperature in %q", title)
		}
		temperature = &t
		title = title[len(tm[0]):]
	}
	if title == "" {
		return source.Deal{}, fmt.Errorf("empty title for %q", link)
	}
	return source.Deal{
		ID:          site + ":" + m[1],
		Source:      site,
		URL:         link,
		Title:       title,
		Merchant:    html.UnescapeString(strings.TrimSpace(it.Merchant.Name)),
		Price:       strings.TrimSpace(it.Merchant.Price),
		Category:    html.UnescapeString(strings.TrimSpace(it.Category)),
		ImageURL:    validImageURL(it.Media.URL),
		Temperature: temperature,
	}, nil
}

type Client struct {
	Site      Site
	BaseURL   string // empty means Site.BaseURL
	UserAgent string
	HTTP      *http.Client
	Log       *slog.Logger
}

// StatusError is a non-200 response, so the poller can back off.
type StatusError struct{ Code int }

func (e *StatusError) Error() string { return fmt.Sprintf("unexpected status %d", e.Code) }

func (c *Client) path(feed source.Feed) (string, error) {
	switch feed {
	case source.FeedTrending:
		return TrendingPath, nil
	case source.FeedNew:
		return c.Site.NewPath, nil
	}
	return "", fmt.Errorf("unknown feed %q", feed)
}

func (c *Client) Fetch(ctx context.Context, feed source.Feed) ([]source.Deal, error) {
	path, err := c.path(feed)
	if err != nil {
		return nil, err
	}
	base := c.BaseURL
	if base == "" {
		base = c.Site.BaseURL
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(base, "/")+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", c.UserAgent)
	req.Header.Set("Accept", "application/rss+xml, application/xml;q=0.9")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, &StatusError{Code: resp.StatusCode}
	}
	deals, itemErrs, err := Parse(io.LimitReader(resp.Body, 5<<20), c.Site.Name)
	if err != nil {
		return nil, err
	}
	for _, e := range itemErrs {
		c.Log.Warn("skipping feed item", "site", c.Site.Name, "feed", feed, "err", e)
	}
	return deals, nil
}
