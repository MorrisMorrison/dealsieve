package web

import (
	"io/fs"
	"testing"
)

func TestAssetsContainPage(t *testing.T) {
	for _, name := range []string{"index.html", "static/css/styles.css", "static/js/main.js"} {
		if _, err := fs.Stat(Assets, name); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}
