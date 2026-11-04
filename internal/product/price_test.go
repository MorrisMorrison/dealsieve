package product

import "testing"

func TestParsePrice(t *testing.T) {
	tests := []struct {
		in       string
		cents    int64
		currency string
		ok       bool
	}{
		{"9€", 900, "EUR", true},
		{"1.449€", 144900, "EUR", true},
		{"0,01€", 1, "EUR", true},
		{"12,99 €", 1299, "EUR", true},
		{"12,5€", 1250, "EUR", true},
		{"1.000.000€", 100000000, "EUR", true},
		{"ab 5€", 500, "EUR", true},
		{"EUR 15", 1500, "EUR", true},
		{"15 Euro", 1500, "EUR", true},
		{"£12.99", 1299, "GBP", true},
		{"£1,299.99", 129999, "GBP", true},
		{"GBP 3", 300, "GBP", true},
		{"$5", 500, "USD", true},
		{"19,99 zł", 1999, "PLN", true},
		{"1.006€", 100600, "EUR", true}, // three digits after the separator: thousands
		{"£6.79", 679, "GBP", true},
		{"309,03zł", 30903, "PLN", true},
		{"1 504,89€", 150489, "EUR", true},
		{"118 500zł", 11850000, "PLN", true},
		{"1 000 000€", 100000000, "EUR", true},
		{"1 299,00 €", 129900, "EUR", true}, // no-break space
		{"Gratis", 0, "", false},
		{"0€", 0, "", false},
		{"0,00€", 0, "", false},
		{"", 0, "", false},
		{"15", 0, "", false},
		{"€", 0, "", false},
		{"99999999999999€", 0, "", false},
	}
	for _, tt := range tests {
		cents, currency, ok := ParsePrice(tt.in)
		if cents != tt.cents || currency != tt.currency || ok != tt.ok {
			t.Errorf("ParsePrice(%q) = %d, %q, %v; want %d, %q, %v", tt.in, cents, currency, ok, tt.cents, tt.currency, tt.ok)
		}
	}
}
