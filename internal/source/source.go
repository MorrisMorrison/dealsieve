// Package source defines what a deal source delivers.
package source

import "context"

type Feed string

const (
	// FeedTrending feeds the page.
	FeedTrending Feed = "trending"
	// FeedNew carries every new deal; it's only used for watchlist matching.
	FeedNew Feed = "new"
)

type Deal struct {
	ID          string // "<source>:<id at the source>"
	Source      string
	URL         string
	Title       string // without the temperature prefix
	Merchant    string
	Price       string // as the source shows it, e.g. "19,99€"
	Category    string
	ImageURL    string
	Temperature *int // nil when the feed doesn't carry one
}

type Fetcher interface {
	Fetch(ctx context.Context, feed Feed) ([]Deal, error)
}
