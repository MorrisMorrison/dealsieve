package web

import (
	"io/fs"
	"strings"
	"testing"
)

func TestAssetsContainPage(t *testing.T) {
	for _, name := range []string{"index.html", "static/css/styles.css", "static/js/main.js", "static/favicon.svg"} {
		if _, err := fs.Stat(Assets, name); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestPageLinksFavicon(t *testing.T) {
	page, err := fs.ReadFile(Assets, "index.html")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(page), `<link rel="icon" href="/static/favicon.svg" type="image/svg+xml">`) {
		t.Error("index.html doesn't link /static/favicon.svg as its icon")
	}
}
