package web

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image/png"
	"io/fs"
	"strings"
	"testing"
)

func TestAssetsContainPage(t *testing.T) {
	for _, name := range []string{"index.html", "static/css/styles.css", "static/js/main.js", "static/favicon.svg", "settings.html", "static/css/settings.css", "static/js/settings.js"} {
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

func TestInstallableAppAssets(t *testing.T) {
	raw, err := fs.ReadFile(Assets, "static/manifest.webmanifest")
	if err != nil {
		t.Fatal(err)
	}
	var m struct {
		Name      string `json:"name"`
		ShortName string `json:"short_name"`
		StartURL  string `json:"start_url"`
		Scope     string `json:"scope"`
		Display   string `json:"display"`
		Icons     []struct {
			Src     string `json:"src"`
			Sizes   string `json:"sizes"`
			Purpose string `json:"purpose"`
		} `json:"icons"`
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("manifest is not valid JSON: %v", err)
	}
	if m.Name != "dealsieve" || m.ShortName != "dealsieve" || m.StartURL != "/" || m.Scope != "/" || m.Display != "standalone" {
		t.Errorf("manifest basics = %+v", m)
	}
	var any192, any512, maskable bool
	for _, ic := range m.Icons {
		data, err := fs.ReadFile(Assets, strings.TrimPrefix(ic.Src, "/"))
		if err != nil {
			t.Errorf("icon %s: %v", ic.Src, err)
			continue
		}
		cfg, err := png.DecodeConfig(bytes.NewReader(data))
		if err != nil {
			t.Errorf("icon %s is not a PNG: %v", ic.Src, err)
			continue
		}
		if want := ic.Sizes; want != fmt.Sprintf("%dx%d", cfg.Width, cfg.Height) {
			t.Errorf("icon %s declares %s but is %dx%d", ic.Src, want, cfg.Width, cfg.Height)
		}
		switch {
		case ic.Purpose == "maskable" && cfg.Width == 512:
			maskable = true
		case ic.Purpose != "maskable" && cfg.Width == 192:
			any192 = true
		case ic.Purpose != "maskable" && cfg.Width == 512:
			any512 = true
		}
	}
	if !any192 || !any512 || !maskable {
		t.Errorf("manifest icons: 192=%v 512=%v maskable512=%v", any192, any512, maskable)
	}
	data, err := fs.ReadFile(Assets, "static/icons/apple-touch-icon.png")
	if err != nil {
		t.Fatal(err)
	}
	if cfg, err := png.DecodeConfig(bytes.NewReader(data)); err != nil || cfg.Width != 180 || cfg.Height != 180 {
		t.Errorf("apple-touch-icon = %+v, %v; want 180x180 PNG", cfg, err)
	}
}

func TestPagesAreInstallable(t *testing.T) {
	for _, name := range []string{"index.html", "settings.html"} {
		page, err := fs.ReadFile(Assets, name)
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{
			`<link rel="manifest" href="/static/manifest.webmanifest">`,
			`<link rel="apple-touch-icon" href="/static/icons/apple-touch-icon.png">`,
			`<meta name="theme-color" content="#f7f7f8" media="(prefers-color-scheme: light)">`,
			`<meta name="theme-color" content="#0f0f11" media="(prefers-color-scheme: dark)">`,
			`<meta name="mobile-web-app-capable" content="yes">`,
			`<meta name="apple-mobile-web-app-capable" content="yes">`,
			`<meta name="apple-mobile-web-app-title" content="dealsieve">`,
			`<meta name="apple-mobile-web-app-status-bar-style" content="default">`,
			`viewport-fit=cover`,
		} {
			if !strings.Contains(string(page), want) {
				t.Errorf("%s: missing %s", name, want)
			}
		}
	}
}
