package product

import (
	"slices"
	"testing"
)

func TestOf(t *testing.T) {
	tests := []struct {
		title                string
		models, sizes, words []string
	}{
		{"Samsung Galaxy S24 128GB Onyx Black", []string{"s24"}, []string{"128gb"}, []string{"black", "galaxy", "onyx", "samsung"}},
		{"Galaxy S24 128 GB", []string{"s24"}, []string{"128gb"}, []string{"galaxy"}},
		{"LG OLED55C47LA 55 Zoll 4K", []string{"oled55c47la"}, []string{"55zoll"}, []string{"lg"}},
		{"Galaxy S24 5G für 549€", []string{"s24"}, nil, []string{"galaxy"}},
		{"Ryzen 7 7800X3D Prozessor", []string{"7800x3d"}, nil, []string{"prozessor", "ryzen"}},
		{"Anker Powerbank 20000mAh 2x USB-C", nil, []string{"20000mah", "2x"}, []string{"anker", "powerbank", "usb"}},
		{"Flüge nach Rom ab 19,99 Euro", nil, nil, []string{"euro", "flüge", "rom"}},
		{"Hue Lampe 3er Pack 12 Monate", nil, []string{"3er"}, []string{"hue", "lampe", "monate", "pack"}},
		{"Galaxy Buds3 + Galaxy S24 Bundle", []string{"buds3", "s24"}, nil, []string{"bundle", "galaxy"}},
	}
	for _, tt := range tests {
		got := Of(tt.title)
		if !slices.Equal(got.Models, tt.models) || !slices.Equal(got.Sizes, tt.sizes) || !slices.Equal(got.Words, tt.words) {
			t.Errorf("Of(%q) = %+v; want models %v sizes %v words %v", tt.title, got, tt.models, tt.sizes, tt.words)
		}
	}
}

func TestSame(t *testing.T) {
	phone := "Samsung Galaxy S24 128GB Onyx Black"
	tests := []struct {
		a, b string
		want bool
	}{
		{phone, "Galaxy S24 128 GB", true},
		{phone, "Samsung Galaxy S24 Smartphone", true}, // one names no size
		{phone, "Galaxy S24 256GB", false},             // both name sizes, differently
		{phone, "Galaxy S25 128GB", false},             // other model
		{phone, "Galaxy Buds3 + Galaxy S24 Bundle", false},
		{"Samsung Galaxy S24 Smartphone", "Hülle für Galaxy S24", false}, // accessory: 1 of 2 words shared
		{"S24", "Galaxy S24 128GB", true},                                // no other words on one side
		{"Anker Powerbank 20000mAh", "Anker Powerbank 20000mAh", false},  // no model code
	}
	for _, tt := range tests {
		if got := Same(Of(tt.a), Of(tt.b)); got != tt.want {
			t.Errorf("Same(%q, %q) = %v, want %v", tt.a, tt.b, got, tt.want)
		}
		if got := Same(Of(tt.b), Of(tt.a)); got != tt.want {
			t.Errorf("Same(%q, %q) = %v, want %v (symmetry)", tt.b, tt.a, got, tt.want)
		}
	}
}

func TestIndex(t *testing.T) {
	tests := []struct {
		title, price     string
		models, currency string
		cents            int64
		ok               bool
	}{
		{"Galaxy S24 128GB", "549€", "s24", "EUR", 54900, true},
		{"Galaxy Buds3 + Galaxy S24", "£199", "buds3 s24", "GBP", 19900, true},
		{"Powerbank 20000mAh", "19€", "", "", 0, false},
		{"Galaxy S24", "Gratis", "", "", 0, false},
	}
	for _, tt := range tests {
		models, currency, cents, ok := Index(tt.title, tt.price)
		if models != tt.models || currency != tt.currency || cents != tt.cents || ok != tt.ok {
			t.Errorf("Index(%q, %q) = %q, %q, %d, %v", tt.title, tt.price, models, currency, cents, ok)
		}
	}
}
