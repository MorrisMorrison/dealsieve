package product

import (
	"regexp"
	"slices"
	"strings"
	"unicode"

	"github.com/MorrisMorrison/dealsieve/internal/fingerprint"
)

// Info is what Same compares, each list sorted and distinct: a title's model
// codes ("s24", "7800x3d", "iphone15"), sizes ("128gb", "55zoll"),
// distinguishing words (variants, accessories, condition: "pro", "hülle",
// "refurbished") and the remaining cleaned words.
type Info struct {
	Models, Sizes, Markers, Words []string
}

// notSizes look like sizes but name a feature (network, resolution).
var notSizes = map[string]bool{"3g": true, "4g": true, "5g": true, "4k": true, "8k": true}

// notSizeUnits follow numbers that are prices or times, not product sizes.
var notSizeUnits = map[string]bool{
	"eur": true, "euro": true, "ct": true, "uhr": true, "h": true, "min": true,
	"s": true, "d": true, "tage": true, "jahre": true, "monate": true,
}

// markers tell variants, accessories and conditions of a product apart; two
// titles are only the same product when they carry the same ones.
var markers = wordSet(
	"pro max ultra plus mini lite slim fe se air xl",
	"hülle case cover coque funda etui panzerglas schutzglas schutzfolie folie displayschutz controller kabel cable adapter ladegerät charger netzteil halterung tasche bag bumper armband strap",
	"refurbished generalüberholt gebraucht used renewed reconditionné reacondicionado odnowiony ware demo vorführgerät occasion",
)

// fillers are function and marketing words that say nothing about the product.
var fillers = wordSet(
	"pour de la le les des du avec et en sur un une au aux",
	"para con el los las por y del al una",
	"za na do od dla ze",
	"il di per con gli",
	"bestpreis tiefstpreis angebot deal deals preis neu new günstig cheap versandkostenfrei kostenlos gratis",
)

var (
	bracketed = regexp.MustCompile(`\[[^\]]*\]`)
	plusAfter = regexp.MustCompile(`([\p{L}\d])\+`)
	// Prices and percentages in a title ("£799.99", "999€", "19,99 Euro",
	// "20%") are dropped so they never form a spaced code. Symbol-first
	// amounts go first, so "iPhone 15 £799" keeps its 15.
	symbolAmounts = regexp.MustCompile(`(?:€|£|\$)\s*\d[\d.,]*`)
	amounts       = regexp.MustCompile(`(?i)\d[\d.,]*\s*(?:€|£|\$|zł|%|\b(?:eur|euro|gbp|pln|usd)\b)`)
)

// Of splits a title into model codes, sizes, markers and other words. Text in
// square brackets and prices ("999€", "£12") are dropped, and "S24+" reads
// "s24 plus". A number followed by
// a unit word is one size ("16 GB" → "16gb"); a number with 4+ digits and "k"
// is a model code ("14700k"). Only if the title has no model code, a word
// directly followed by a short number is one ("iPhone 15" → "iphone15").
func Of(title string) Info {
	title = bracketed.ReplaceAllString(title, " ")
	title = amounts.ReplaceAllString(symbolAmounts.ReplaceAllString(title, " "), " ")
	title = plusAfter.ReplaceAllString(title, "$1 plus")
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
			num, unit := splitSize(w)
			if unit == "k" && len(num) >= 4 {
				models[w] = true
			} else if unit != "" && !notSizeUnits[unit] {
				sizes[w] = true
			}
		case strings.ContainsFunc(w, unicode.IsDigit) && strings.ContainsFunc(w, unicode.IsLetter):
			models[w] = true
		}
	}
	consumed := map[string]bool{}
	if len(models) == 0 {
		for i := 0; i+1 < len(tokens); i++ {
			w, n := tokens[i], tokens[i+1]
			if !isSpacedCodeWord(w) || !isNumber(n) || len(n) > 5 {
				continue
			}
			if i+2 < len(tokens) && fingerprint.IsUnit(tokens[i+2]) {
				continue
			}
			models[w+n] = true
			consumed[w] = true
			i++
		}
	}
	found, words := map[string]bool{}, map[string]bool{}
	for _, w := range strings.Fields(fingerprint.CleanTitle(title)) {
		switch {
		case models[w] || joined[w] || consumed[w] || fillers[w]:
		case markers[w]:
			found[w] = true
		default:
			words[w] = true
		}
	}
	return Info{Models: sortedKeys(models), Sizes: sortedKeys(sizes), Markers: sortedKeys(found), Words: sortedKeys(words)}
}

// Same reports whether two titles name the same product: identical non-empty
// model codes, identical markers, no size unit both name with no value in
// common, and at least 60% of the smaller set of other words shared.
func Same(a, b Info) bool {
	if len(a.Models) == 0 || !slices.Equal(a.Models, b.Models) || !slices.Equal(a.Markers, b.Markers) {
		return false
	}
	if sizesConflict(a.Sizes, b.Sizes) {
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

// sizesConflict is true when some unit appears on both sides and the two
// sides share no value for it; units named on one side only are ignored.
func sizesConflict(a, b []string) bool {
	values := func(sizes []string) map[string]map[string]bool {
		m := map[string]map[string]bool{}
		for _, s := range sizes {
			num, unit := splitSize(s)
			if m[unit] == nil {
				m[unit] = map[string]bool{}
			}
			m[unit][num] = true
		}
		return m
	}
	va, vb := values(a), values(b)
	for unit, nums := range va {
		other, ok := vb[unit]
		if !ok {
			continue
		}
		common := false
		for n := range nums {
			common = common || other[n]
		}
		if !common {
			return true
		}
	}
	return false
}

// isSpacedCodeWord reports whether w can be the letters of a spaced model code
// ("iphone" in "iPhone 15"): letters only, 2+ of them, not a filler or stopword.
func isSpacedCodeWord(w string) bool {
	return len([]rune(w)) >= 2 && !strings.ContainsFunc(w, unicode.IsDigit) &&
		!fillers[w] && fingerprint.CleanTitle(w) != ""
}

func splitSize(s string) (num, unit string) {
	unit = strings.TrimLeftFunc(s, unicode.IsDigit)
	return s[:len(s)-len(unit)], unit
}

func wordSet(lists ...string) map[string]bool {
	m := map[string]bool{}
	for _, l := range lists {
		for _, w := range strings.Fields(l) {
			m[w] = true
		}
	}
	return m
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
