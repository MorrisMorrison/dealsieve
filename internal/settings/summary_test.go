package settings

import (
	"slices"
	"testing"
)

func TestSummarize(t *testing.T) {
	prev := valid()
	next := valid()
	next.Watchlist = []string{"oled", "lego"}
	next.Rules.MutedMerchants = []string{"Example Air"}
	next.Rules.MinTemperature = 80
	next.Rules.CategoryOverrides = map[string]int{"Elektronik": 60}
	next.Rules.Recurrence = Recurrence{WindowDays: 60, MaxSeen: 3}
	delete(next.Channels, "tg")
	next.Notify = []NotifyRule{{When: When{Watchlist: true}, Channels: []string{"phone"}}}
	next.Suggestions = Suggestions{MinCount: 4, WindowDays: 30}

	want := []string{
		`Added watchlist entry "lego"`,
		`Added muted shop "Example Air"`,
		`Minimum temperature 100° → 80°`,
		`Added minimum for "Elektronik" (60°)`,
		`Recurring deals: 2× in 90 days → 3× in 60 days`,
		`Removed channel "tg"`,
		`Changed notify rules`,
		`Suggestions: 3 in 30 days → 4 in 30 days`,
	}
	if got := Summarize(prev, next); !slices.Equal(got, want) {
		t.Errorf("Summarize =\n%q\nwant\n%q", got, want)
	}
}

func TestSummarizeDetails(t *testing.T) {
	a := valid()
	b := valid()
	if got := Summarize(a, b); len(got) != 0 {
		t.Errorf("identical settings: %q", got)
	}
	b.Watchlist = []string{"OLED"} // case-only change is not a change
	if got := Summarize(a, b); len(got) != 0 {
		t.Errorf("case-only change: %q", got)
	}
	a.Rules.CategoryOverrides = map[string]int{"Elektronik": 60, "Gaming": 80}
	b.Rules.CategoryOverrides = map[string]int{"Elektronik": 50}
	b.Rules.BlockKeywords = []string{"gratisprobe"}
	a.Rules.MutedCategories = []string{"Reisen"}
	b.Channels["phone"] = Channel{Type: "ntfy", URL: "https://ntfy.example.com", Topic: "other", Priority: "high"}
	want := []string{
		`Removed muted category "Reisen"`,
		`Added block keyword "gratisprobe"`,
		`Minimum for "Elektronik" 60° → 50°`,
		`Removed minimum for "Gaming"`,
		`Changed channel "phone"`,
	}
	if got := Summarize(a, b); !slices.Equal(got, want) {
		t.Errorf("Summarize =\n%q\nwant\n%q", got, want)
	}
}
