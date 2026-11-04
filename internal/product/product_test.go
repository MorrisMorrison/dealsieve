package product

import (
	"slices"
	"testing"
)

func TestOf(t *testing.T) {
	tests := []struct {
		title                         string
		models, sizes, markers, words []string
	}{
		{"Samsung Galaxy S24 128GB Onyx Black", []string{"s24"}, []string{"128gb"}, nil, []string{"black", "galaxy", "onyx", "samsung"}},
		{"Galaxy S24 128 GB", []string{"s24"}, []string{"128gb"}, nil, []string{"galaxy"}},
		{"LG OLED55C47LA 55 Zoll 4K", []string{"oled55c47la"}, []string{"55zoll"}, nil, []string{"lg"}},
		{"Galaxy S24 5G für 549€", []string{"s24"}, nil, nil, []string{"galaxy"}},
		{"Ryzen 7 7800X3D Prozessor", []string{"7800x3d"}, nil, nil, []string{"prozessor", "ryzen"}},
		{"Anker Powerbank 20000mAh 2x USB-C", nil, []string{"20000mah", "2x"}, nil, []string{"anker", "powerbank", "usb"}},
		{"Flüge nach Rom ab 19,99 Euro", nil, nil, nil, []string{"euro", "flüge", "rom"}},
		{"Hue Lampe 3er Pack 12 Monate", nil, []string{"3er"}, nil, []string{"hue", "lampe", "monate", "pack"}},
		{"Galaxy Buds3 + Galaxy S24 Bundle", []string{"buds3", "s24"}, nil, nil, []string{"bundle", "galaxy"}},
		{"Apple iPhone 15 Pro 128GB", []string{"iphone15"}, []string{"128gb"}, []string{"pro"}, []string{"apple"}},
		{"Galaxy S24+ 256GB", []string{"s24"}, []string{"256gb"}, []string{"plus"}, []string{"galaxy"}},
		{"Intel Core i7-14700K", []string{"14700k", "i7"}, nil, nil, []string{"core", "intel"}},
		{"RTX 5080 Grafikkarte", []string{"rtx5080"}, nil, nil, []string{"grafikkarte"}},
		{"Kopfhörer für 49€", nil, nil, nil, []string{"kopfhörer"}},
	}
	for _, tt := range tests {
		got := Of(tt.title)
		if !slices.Equal(got.Models, tt.models) || !slices.Equal(got.Sizes, tt.sizes) || !slices.Equal(got.Markers, tt.markers) || !slices.Equal(got.Words, tt.words) {
			t.Errorf("Of(%q) = %+v; want models %v sizes %v markers %v words %v", tt.title, got, tt.models, tt.sizes, tt.markers, tt.words)
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
		{"Samsung Galaxy S24 Smartphone", "Hülle für Galaxy S24", false}, // accessory
		{"S24", "Galaxy S24 128GB", true},                                // no other words on one side
		{"Anker Powerbank 20000mAh", "Anker Powerbank 20000mAh", false},  // no model code
		{"Samsung Galaxy S24 128GB für 549€", "Samsung Galaxy S24 Hülle", false},
		{"Samsung Galaxy S24", "Samsung Galaxy S24 Ultra", false},
		{"Galaxy S24", "Galaxy S24+", false},
		{"Samsung Galaxy S24 128GB", "Samsung Galaxy S24 128GB refurbished", false},
		{"Sony PS5", "Sony PS5 Pro", false},
		{"Sony PS5", "Sony PS5 Controller", false},
		{"Coque pour Galaxy S24", "Samsung Galaxy S24 pour 549€", false},
		{"Funda de silicona para Galaxy S24", "Samsung Galaxy S24 de 128GB para", false},
		{"LG OLED55C47LA 55 Zoll (139 cm)", "LG OLED55C47LA 55 Zoll", true},
		{"LG OLED55C47LA 55 Zoll 4K 120Hz", "LG OLED55C47LA 55 Zoll", true},
		{"Galaxy S24 128GB 8GB RAM", "Galaxy S24 128GB", true},
		{"LG OLED55C47LA 55 Zoll", "LG OLED55C47LA 65 Zoll", false},
		{"Apple iPhone 15 Pro 128GB", "Apple iPhone 15 Pro 128 GB Titan", true},
		{"iPhone 15 128GB", "iPhone 15 Pro 128GB", false},
		{"iPhone 15", "iPhone 16", false},
		{"Intel Core i7-14700K", "Intel Core i7-14600K", false},
		{"[MediaMarkt] Galaxy S24 128GB", "Samsung Galaxy S24 128GB Bestpreis", true},
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
