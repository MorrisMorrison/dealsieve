// Package fingerprint turns deal titles into keys that stay the same when a
// merchant re-posts the same kind of offer with different numbers.
package fingerprint

import (
	"slices"
	"strings"
	"unicode"
)

// stopwords are the function words of the sites' languages (German, English,
// French, Spanish, Polish) that carry no product meaning.
var stopwords = map[string]bool{
	// German
	"ab": true, "am": true, "an": true, "auf": true, "aus": true, "bei": true, "bis": true,
	"das": true, "dem": true, "den": true, "der": true, "des": true, "die": true, "ein": true,
	"eine": true, "einen": true, "für": true, "im": true, "in": true, "inkl": true, "ist": true,
	"jetzt": true, "mit": true, "nach": true, "nur": true, "oder": true, "statt": true,
	"und": true, "uvp": true, "vom": true, "von": true, "zu": true, "zum": true, "zur": true,
	// English
	"and": true, "at": true, "for": true, "from": true, "of": true, "on": true, "only": true,
	"or": true, "the": true, "to": true, "with": true, "now": true,
	"by": true, "via": true, "per": true, "off": true,
	// French
	"le": true, "la": true, "les": true, "du": true, "et": true, "pour": true, "avec": true,
	"au": true, "aux": true, "sur": true, "par": true, "chez": true, "dans": true, "une": true,
	// Spanish
	"el": true, "los": true, "las": true, "del": true, "con": true, "por": true, "para": true, "al": true,
	// French and Spanish
	"de": true, "en": true,
	// Polish
	"na": true, "do": true, "dla": true, "od": true, "ze": true, "za": true, "po": true,
	"oraz": true, "lub": true, "przy": true,
}

// units are what follows a number in sizes, counts and specs ("16gb", "2x",
// "3er", "120hz"). Such tokens are dropped like plain numbers, so a re-post
// in another size or pack still matches; other letter+digit tokens are model
// codes ("s24", "rtx5080", "1000xm5") and stay.
var units = map[string]bool{
	"b": true, "kb": true, "mb": true, "gb": true, "tb": true,
	"g": true, "mg": true, "kg": true, "l": true, "ml": true, "cl": true,
	"m": true, "mm": true, "cm": true, "km": true, "zoll": true, "inch": true,
	"w": true, "kw": true, "kwh": true, "v": true, "mah": true, "wh": true,
	"hz": true, "khz": true, "mhz": true, "ghz": true, "k": true, "p": true, "fps": true,
	"x": true, "er": true, "st": true, "stk": true, "tlg": true, "teilig": true, "fach": true,
	"h": true, "min": true, "s": true, "d": true, "tage": true, "jahre": true, "monate": true,
	"eur": true, "euro": true, "ct": true, "uhr": true, "ps": true, "pa": true, "lagig": true,
}

// sizeUnits are also dropped on their own ("gb" in "16 GB"): unlike "uhr" or
// "ps" they are never product words.
var sizeUnits = map[string]bool{
	"kb": true, "mb": true, "gb": true, "tb": true, "mg": true, "kg": true, "ml": true, "cl": true,
	"mm": true, "cm": true, "km": true, "kwh": true, "mah": true,
	"hz": true, "khz": true, "mhz": true, "ghz": true, "fps": true, "lagig": true, "tlg": true, "stk": true,
}

// CleanTitle lowercases a title and drops plain numbers, sizes like "16gb",
// punctuation, one-letter words and stopwords. Model codes like "s24" stay.
func CleanTitle(title string) string {
	fields := Tokens(title)
	words := fields[:0]
	for _, w := range fields {
		if len([]rune(w)) < 2 || stopwords[w] || isQuantity(w) {
			continue
		}
		words = append(words, w)
	}
	return strings.Join(words, " ")
}

// Tokens splits a title into its lowercased runs of letters and digits, the
// words CleanTitle starts from.
func Tokens(title string) []string {
	return strings.FieldsFunc(strings.ToLower(title), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
}

// IsQuantity reports whether CleanTitle drops w as a number or size.
func IsQuantity(w string) bool { return isQuantity(w) }

// IsUnit reports whether w can follow a number as its unit ("gb", "zoll").
func IsUnit(w string) bool { return units[w] || sizeUnits[w] }

// Key is the merchant plus the cleaned title's distinct words in sorted order.
func Key(merchant, cleanTitle string) string {
	m := strings.ToLower(strings.TrimSpace(merchant))
	if m == "" {
		m = "-"
	}
	words := strings.Fields(cleanTitle)
	slices.Sort(words)
	return m + "|" + strings.Join(slices.Compact(words), " ")
}

// isQuantity reports whether w is a number, a size unit on its own ("gb" in
// "16 GB"), a number followed by a unit ("16gb", "2x") or a pack size like
// "4x10".
func isQuantity(w string) bool {
	if sizeUnits[w] {
		return true
	}
	rest := strings.TrimLeftFunc(w, unicode.IsDigit)
	if rest == w {
		return false // starts with a letter: a word or a model code
	}
	if after, ok := strings.CutPrefix(rest, "x"); ok && after != "" && strings.TrimLeftFunc(after, unicode.IsDigit) == "" {
		return true
	}
	return rest == "" || units[rest]
}

// Of returns a deal's cleaned title and its fingerprint key.
func Of(merchant, title string) (clean, key string) {
	clean = CleanTitle(title)
	return clean, Key(merchant, clean)
}

// RekeyOld converts a key made by an older CleanTitle, which dropped every
// token containing a digit and fewer stopwords, to the current rules. Such keys
// have no digits, so only the words the current rules drop change: standalone
// size units and stopwords.
func RekeyOld(key string) string {
	merchant, words, ok := strings.Cut(key, "|")
	if !ok {
		return key
	}
	kept := strings.Fields(words)[:0]
	for _, w := range strings.Fields(words) {
		if !isQuantity(w) && !stopwords[w] {
			kept = append(kept, w)
		}
	}
	return Key(merchant, strings.Join(kept, " "))
}
