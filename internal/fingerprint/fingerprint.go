// Package fingerprint turns deal titles into keys that stay the same when a
// merchant re-posts the same kind of offer with different numbers.
package fingerprint

import (
	"slices"
	"strings"
	"unicode"
)

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
}

// CleanTitle lowercases a title and drops numbers, punctuation, one-letter
// words and stopwords.
func CleanTitle(title string) string {
	fields := strings.FieldsFunc(strings.ToLower(title), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	words := fields[:0]
	for _, w := range fields {
		if len([]rune(w)) < 2 || stopwords[w] || strings.ContainsFunc(w, unicode.IsDigit) {
			continue
		}
		words = append(words, w)
	}
	return strings.Join(words, " ")
}

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
