package rules

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/MorrisMorrison/dealsieve/internal/config"
)

func intp(v int) *int { return &v }

func engine() *Engine {
	return New(config.Rules{
		MinTemperature:    100,
		CategoryOverrides: map[string]int{"Elektronik": 60},
		MutedCategories:   []string{"Fashion"},
		MutedMerchants:    []string{"Example Air"},
		BlockKeywords:     []string{"Gratisprobe"},
		Recurrence:        config.Recurrence{Window: config.Duration(90 * 24 * time.Hour), MaxSeen: 2},
	}, []string{"RTX 5080", "oled"})
}

func TestEvaluate(t *testing.T) {
	hot := Deal{Title: "Example Laptop", Merchant: "Shop", Category: "Computer", Fingerprint: "shop|laptop", Temperature: intp(150), InTrending: true}
	with := func(f func(*Deal)) Deal { d := hot; f(&d); return d }
	last := time.Date(2026, 10, 12, 8, 0, 0, 0, time.UTC)

	tests := []struct {
		name        string
		deal        Deal
		facts       Facts
		want        Decision
		wantReason  string // prefix of Reasons[0]; "" means no reasons
		wantWatch   bool
		wantScore   int
		wantReasons []string // the full Reasons slice, checked when set
	}{
		{"hot deal passes", hot, Facts{}, Pass, "", false, 150, []string{"hot: 150° (minimum 100°)", "new offer: not seen in the last 90 days"}},
		{"too cold", with(func(d *Deal) { d.Temperature = intp(80) }), Facts{}, Block, "too cold: 80° < 100°", false, 80, nil},
		{"category override", with(func(d *Deal) { d.Temperature = intp(80); d.Category = "Elektronik" }), Facts{}, Pass, "", false, 80, []string{"hot: 80° (minimum 60°)", "new offer: not seen in the last 90 days"}},
		{"muted category from config, any case", with(func(d *Deal) { d.Category = "fashion" }), Facts{}, Block, "muted category: fashion", false, 150, nil},
		{"muted category from a click", hot, Facts{MutedCategories: map[string]bool{"computer": true}}, Block, "muted category: Computer", false, 150, nil},
		{"muted merchant", with(func(d *Deal) { d.Merchant = "EXAMPLE AIR" }), Facts{}, Block, "muted merchant: EXAMPLE AIR", false, 150, nil},
		{"muted merchant from a click", hot, Facts{MutedMerchants: map[string]bool{"shop": true}}, Block, "muted merchant: Shop", false, 150, nil},
		{"block keyword", with(func(d *Deal) { d.Title = "Gratisprobe Handcreme" }), Facts{}, Block, "block keyword: gratisprobe", false, 150, nil},
		{"recurring", hot, Facts{Recurrence: 2, LastRecurrence: last}, Block, "recurring: seen 2× before in 90 days (last 2026-10-12)", false, 150, nil},
		{"below recurrence limit", hot, Facts{Recurrence: 1, LastRecurrence: last}, Pass, "", false, 150, []string{"hot: 150° (minimum 100°)", "seen 1× before in the last 90 days (limit 2)"}},
		{"like your junk", hot, Facts{JunkFingerprints: map[string]bool{"shop|laptop": true}}, Block, "like a deal you marked as junk", false, 150, nil},
		{"watchlist skips recurrence", with(func(d *Deal) { d.Title = "Example RTX 5080" }), Facts{Recurrence: 5, LastRecurrence: last}, Pass, "watchlist: rtx 5080", true, 1150, []string{"watchlist: rtx 5080", "hot: 150° (minimum 100°)", "seen 5× before in the last 90 days, kept for your watchlist"}},
		{"watchlist still blocked as junk", with(func(d *Deal) { d.Title = "Example RTX 5080" }), Facts{Recurrence: 5, LastRecurrence: last, JunkFingerprints: map[string]bool{"shop|laptop": true}}, Block, "like a deal you marked as junk", true, 1150, []string{"like a deal you marked as junk", "watchlist: rtx 5080"}},
		{"hidden beats watchlist", with(func(d *Deal) { d.Title = "Example RTX 5080" }), Facts{Hidden: true}, Block, "hidden by you", true, 1150, []string{"hidden by you", "watchlist: rtx 5080"}},
		{"watchlist skips temperature", with(func(d *Deal) { d.Title = "Example rtx 5080 Karte"; d.Temperature = intp(5) }), Facts{}, Pass, "watchlist: rtx 5080", true, 1005, []string{"watchlist: rtx 5080", "below the 100° minimum, kept for your watchlist", "new offer: not seen in the last 90 days"}},
		{"watchlist still blocked by keyword", with(func(d *Deal) { d.Title = "Gratisprobe OLED Reiniger" }), Facts{}, Block, "block keyword: gratisprobe", true, 1150, []string{"block keyword: gratisprobe", "watchlist: oled"}},
		{"new-only without watchlist", with(func(d *Deal) { d.Temperature = nil; d.InTrending = false }), Facts{}, Block, "new-only", false, 0, nil},
		{"new-only watchlist hit", with(func(d *Deal) { d.Title = "Example OLED TV"; d.Temperature = nil; d.InTrending = false }), Facts{}, Pass, "watchlist: oled", true, 1000, []string{"watchlist: oled", "new offer: not seen in the last 90 days"}},
		{"unknown temperature in trending", with(func(d *Deal) { d.Temperature = nil }), Facts{}, Pass, "", false, 0, []string{"new offer: not seen in the last 90 days"}},
		{"watchlist hit seen 3 times, below minimum", with(func(d *Deal) { d.Title = "Example OLED TV"; d.Temperature = intp(40) }), Facts{Recurrence: 3, LastRecurrence: last}, Pass, "watchlist: oled", true, 1040, []string{"watchlist: oled", "below the 100° minimum, kept for your watchlist", "seen 3× before in the last 90 days, kept for your watchlist"}},
	}
	e := engine()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := e.Evaluate(tt.deal, tt.facts)
			if got.Decision != tt.want || got.WatchlistHit != tt.wantWatch || got.Score != tt.wantScore {
				t.Errorf("got %+v, want decision %s, watchlist %v, score %d", got, tt.want, tt.wantWatch, tt.wantScore)
			}
			if tt.wantReasons != nil && !slices.Equal(got.Reasons, tt.wantReasons) {
				t.Errorf("reasons = %q, want %q", got.Reasons, tt.wantReasons)
			}
			if tt.wantReason != "" && !(len(got.Reasons) > 0 && strings.HasPrefix(got.Reasons[0], tt.wantReason)) {
				t.Errorf("reasons = %v, want first to start with %q", got.Reasons, tt.wantReason)
			}
		})
	}
}

func TestBlockedWatchlistHitKeepsBothReasons(t *testing.T) {
	got := engine().Evaluate(Deal{Title: "Example RTX 5080", Temperature: intp(150), InTrending: true}, Facts{Hidden: true})
	if !slices.Equal(got.Reasons, []string{"hidden by you", "watchlist: rtx 5080"}) {
		t.Errorf("reasons = %v", got.Reasons)
	}
}

func TestWindow(t *testing.T) {
	if engine().Window() != 90*24*time.Hour {
		t.Errorf("window = %v", engine().Window())
	}
}
