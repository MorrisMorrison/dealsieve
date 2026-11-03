package fingerprint

import "testing"

func TestCleanTitle(t *testing.T) {
	tests := map[string]string{
		"Example Air Flash Sale: Flüge ab 19,99€ – z.B. nach Mailand": "example air flash sale flüge mailand",
		"Example RTX 5080 Grafikkarte 16GB":                           "example rtx grafikkarte",
		"The best deal for you, with free shipping":                   "best deal you free shipping",
		"  ": "",
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
