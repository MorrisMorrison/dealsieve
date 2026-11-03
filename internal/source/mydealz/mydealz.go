// Package mydealz reads deals from mydealz's public RSS feeds.
package mydealz

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/MorrisMorrison/dealsieve/internal/source"
)

const Name = "mydealz"

// /rss/hot and /rss/new only redirect to these two.
var feedPaths = map[source.Feed]string{
	source.FeedTrending: "/rss/trending",
	source.FeedNew:      "/rss/alles",
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
func Parse(r io.Reader) ([]source.Deal, []error, error) {
	var doc rss
	if err := xml.NewDecoder(r).Decode(&doc); err != nil {
		return nil, nil, fmt.Errorf("decode feed: %w", err)
	}
	var deals []source.Deal
	var itemErrs []error
	for i, it := range doc.Items {
		d, err := toDeal(it)
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

func toDeal(it item) (source.Deal, error) {
	link := strings.TrimSpace(it.Link)
	u, err := url.Parse(link)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return source.Deal{}, fmt.Errorf("bad link %q", link)
	}
	m := threadID.FindStringSubmatch(u.Path)
	if m == nil {
		return source.Deal{}, fmt.Errorf("no thread id in link %q", link)
	}
	title := strings.TrimSpace(it.Title)
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
		ID:          Name + ":" + m[1],
		Source:      Name,
		URL:         link,
		Title:       title,
		Merchant:    strings.TrimSpace(it.Merchant.Name),
		Price:       strings.TrimSpace(it.Merchant.Price),
		Category:    strings.TrimSpace(it.Category),
		ImageURL:    validImageURL(it.Media.URL),
		Temperature: temperature,
	}, nil
}

type Client struct {
	BaseURL   string
	UserAgent string
	HTTP      *http.Client
	Log       *slog.Logger
}

// StatusError is a non-200 response, so the poller can back off.
type StatusError struct{ Code int }

func (e *StatusError) Error() string { return fmt.Sprintf("unexpected status %d", e.Code) }

func (c *Client) Fetch(ctx context.Context, feed source.Feed) ([]source.Deal, error) {
	path, ok := feedPaths[feed]
	if !ok {
		return nil, fmt.Errorf("unknown feed %q", feed)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(c.BaseURL, "/")+path, nil)
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
	deals, itemErrs, err := Parse(io.LimitReader(resp.Body, 5<<20))
	if err != nil {
		return nil, err
	}
	for _, e := range itemErrs {
		c.Log.Warn("skipping feed item", "feed", feed, "err", e)
	}
	return deals, nil
}
