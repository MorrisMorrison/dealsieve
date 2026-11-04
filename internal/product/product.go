package product

import (
	"slices"
	"strings"
	"unicode"

	"github.com/MorrisMorrison/dealsieve/internal/fingerprint"
)

// Info is what Same compares: a title's model codes ("s24", "7800x3d"),
// sizes ("128gb", "55zoll") and remaining cleaned words, each sorted and
// distinct.
type Info struct {
	Models, Sizes, Words []string
}

// notSizes look like sizes but name a feature (network, resolution).
var notSizes = map[string]bool{"3g": true, "4g": true, "5g": true, "4k": true, "8k": true}

// notSizeUnits follow numbers that are prices or times, not product sizes.
var notSizeUnits = map[string]bool{
	"eur": true, "euro": true, "ct": true, "uhr": true, "h": true, "min": true,
	"s": true, "d": true, "tage": true, "jahre": true, "monate": true,
}

// Of splits a title into model codes, sizes and other words. A number
// followed by a unit word is one size ("16 GB" → "16gb").
func Of(title string) Info {
	tokens := fingerprint.Tokens(title)
	models, sizes, joined := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for i := 0; i < len(tokens); i++ {
		w := tokens[i]
		switch {
		case notSizes[w]:
		case isNumber(w):
			if i+1 < len(tokens) && fingerprint.IsUnit(tokens[i+1]) && !notSizeUnits[tokens[i+1]] {
				sizes[w+tokens[i+1]] = true
				joined[tokens[i+1]] = true
				i++
			}
		case fingerprint.IsQuantity(w):
			if unit := strings.TrimLeftFunc(w, unicode.IsDigit); unit != w && !notSizeUnits[unit] {
				sizes[w] = true
			}
		case strings.ContainsFunc(w, unicode.IsDigit) && strings.ContainsFunc(w, unicode.IsLetter):
			models[w] = true
		}
	}
	words := map[string]bool{}
	for _, w := range strings.Fields(fingerprint.CleanTitle(title)) {
		if !models[w] && !joined[w] {
			words[w] = true
		}
	}
	return Info{Models: sortedKeys(models), Sizes: sortedKeys(sizes), Words: sortedKeys(words)}
}

// Same reports whether two titles name the same product: identical model
// codes, the same sizes when both name any, and at least 60% of the smaller
// set of other words shared.
func Same(a, b Info) bool {
	if len(a.Models) == 0 || !slices.Equal(a.Models, b.Models) {
		return false
	}
	if len(a.Sizes) > 0 && len(b.Sizes) > 0 && !slices.Equal(a.Sizes, b.Sizes) {
		return false
	}
	if len(a.Words) == 0 || len(b.Words) == 0 {
		return true
	}
	shared := 0
	for _, w := range a.Words {
		if _, ok := slices.BinarySearch(b.Words, w); ok {
			shared++
		}
	}
	return shared*5 >= min(len(a.Words), len(b.Words))*3
}

// Index is what the store keeps per deal to find candidates: the model codes
// joined by spaces, and the parsed price. ok is false for deals without a
// model code or a price; they have no price history.
func Index(title, price string) (models, currency string, cents int64, ok bool) {
	info := Of(title)
	cents, currency, ok = ParsePrice(price)
	if !ok || len(info.Models) == 0 {
		return "", "", 0, false
	}
	return strings.Join(info.Models, " "), currency, cents, true
}

func isNumber(w string) bool {
	return w != "" && strings.TrimLeftFunc(w, unicode.IsDigit) == ""
}

func sortedKeys(m map[string]bool) []string {
	if len(m) == 0 {
		return nil
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}
