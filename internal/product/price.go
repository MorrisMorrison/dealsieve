// Package product recognises the same product across deals and compares
// their prices. It is pure: no I/O.
package product

import (
	"regexp"
	"strconv"
	"strings"
)

var (
	amount = regexp.MustCompile(`\d[\d.,]*`)
	// spaceThousands is a space used as thousands separator ("1 504,89€",
	// "118 500zł"): a digit, a space and exactly three digits.
	spaceThousands = regexp.MustCompile(`(\d)[ \x{00A0}\x{202F}](\d{3})(\D|$)`)
)

// currencies is checked in order; "€" before "eur" so "Euro" texts still
// match, and symbols before codes.
var currencies = []struct{ marker, code string }{
	{"€", "EUR"}, {"£", "GBP"}, {"zł", "PLN"}, {"$", "USD"},
	{"eur", "EUR"}, {"gbp", "GBP"}, {"pln", "PLN"}, {"usd", "USD"},
}

// ParsePrice reads a price as a source shows it ("1.449€", "£1,299.99",
// "12,99 €") into cents and an ISO currency code. A separator followed by
// one or two digits is the decimal separator; any other is a thousands
// separator. Texts without a known currency or amount, and amounts of zero
// (freebies), are not prices.
func ParsePrice(text string) (cents int64, currency string, ok bool) {
	lower := strings.ToLower(text)
	for _, c := range currencies {
		if strings.Contains(lower, c.marker) {
			currency = c.code
			break
		}
	}
	// Matches don't overlap, so "1 000 000" needs more than one pass.
	for prev := ""; prev != lower; {
		prev, lower = lower, spaceThousands.ReplaceAllString(lower, "$1$2$3")
	}
	num := strings.TrimRight(amount.FindString(lower), ".,")
	if currency == "" || num == "" {
		return 0, "", false
	}
	whole, frac := num, ""
	if i := strings.LastIndexAny(num, ".,"); i >= 0 && len(num)-i-1 <= 2 {
		whole, frac = num[:i], num[i+1:]
	}
	whole = strings.NewReplacer(".", "", ",", "").Replace(whole)
	if len(whole) > 12 {
		return 0, "", false
	}
	if len(frac) == 1 {
		frac += "0"
	}
	units, err := strconv.ParseInt(whole, 10, 64)
	if err != nil {
		return 0, "", false
	}
	cents = units * 100
	if frac != "" {
		f, err := strconv.ParseInt(frac, 10, 64)
		if err != nil {
			return 0, "", false
		}
		cents += f
	}
	if cents <= 0 {
		return 0, "", false
	}
	return cents, currency, true
}
