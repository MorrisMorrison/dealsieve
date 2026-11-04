package fingerprint

import "testing"

func TestCleanTitle(t *testing.T) {
	tests := map[string]string{
		"Example Air Flash Sale: Flüge ab 19,99€ – z.B. nach Mailand": "example air flash sale flüge mailand",
		"Example RTX 5080 Grafikkarte 16GB":                           "example rtx grafikkarte",
		"The best deal for you, with free shipping":                   "best deal you free shipping",
		"  ": "",
		// Model codes stay; sizes, counts and plain numbers go.
		"Example Galaxy S24 128GB":                      "example galaxy s24",
		"Example RTX5080 Gaming PC, 2TB SSD":            "example rtx5080 gaming pc ssd",
		"Example WH-1000XM5 Kopfhörer":                  "example wh 1000xm5 kopfhörer",
		"Example 55\" OLED 4K 120Hz, 2x HDMI, 3er Pack": "example oled hdmi pack",
		"Example Shampoo 250ml 24h":                     "example shampoo",
	}
	for in, want := range tests {
		if got := CleanTitle(in); got != want {
			t.Errorf("CleanTitle(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestKeySameForRepostedOffer(t *testing.T) {
	a := Key("Example Air", CleanTitle("Example Air Flash Sale: Flüge ab 19,99€"))
	b := Key("example air ", CleanTitle("Example Air – Flash Sale, Flüge ab 14,99 €"))
	if a != b {
		t.Errorf("keys differ: %q vs %q", a, b)
	}
	if want := "example air|air example flash flüge sale"; a != want {
		t.Errorf("key = %q, want %q", a, want)
	}
}

func TestKeyIgnoresWordOrderAndDuplicates(t *testing.T) {
	if Key("Shop", "sale flash sale") != Key("Shop", "flash sale") {
		t.Error("word order or duplicates changed the key")
	}
}

func TestKeyDependsOnMerchant(t *testing.T) {
	if Key("Shop A", "oled tv") == Key("Shop B", "oled tv") {
		t.Error("different merchants gave the same key")
	}
	if got := Key("", "oled tv"); got != "-|oled tv" {
		t.Errorf("missing merchant key = %q", got)
	}
}

func TestKeySeparatesModels(t *testing.T) {
	if Key("Shop", CleanTitle("Example Galaxy S24 128GB")) == Key("Shop", CleanTitle("Example Galaxy S25 256GB")) {
		t.Error("different models share a key")
	}
	if Key("Shop", CleanTitle("Example Galaxy S24 128GB")) != Key("Shop", CleanTitle("Example Galaxy S24 256GB")) {
		t.Error("the same model in another size got a different key")
	}
}

func TestUnitWordsStayWithoutANumber(t *testing.T) {
	if got := CleanTitle("Example PS Plus Uhr"); got != "example ps plus uhr" {
		t.Errorf("CleanTitle = %q", got)
	}
}

func TestRekeyOld(t *testing.T) {
	if got := RekeyOld("example shop|gb ssd"); got != "example shop|ssd" {
		t.Errorf("RekeyOld = %q", got)
	}
	if got := RekeyOld("-|oled tv"); got != "-|oled tv" {
		t.Errorf("RekeyOld unchanged key = %q", got)
	}
}

func TestCleanTitleDropsOtherLanguagesStopwords(t *testing.T) {
	cases := map[string]string{
		"Example Plancha de vapor con depósito para el hogar": "example plancha vapor depósito hogar",
		"Example Ordinateur portable pour le jeu avec souris": "example ordinateur portable jeu souris",
		"Example Odkurzacz bezprzewodowy dla domu na raty":    "example odkurzacz bezprzewodowy domu raty",
		"Example Socks by Example Brand, 20% off":             "example socks example brand",
	}
	for in, want := range cases {
		if got := CleanTitle(in); got != want {
			t.Errorf("CleanTitle(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRekeyOldDropsStopwords(t *testing.T) {
	if got := RekeyOld("example shop|de echo la para"); got != "example shop|echo" {
		t.Errorf("RekeyOld = %q", got)
	}
	// Idempotent on a current key.
	k := Key("Example Shop", CleanTitle("Example Echo Dot"))
	if RekeyOld(k) != k {
		t.Errorf("RekeyOld changed a current key %q", k)
	}
}
