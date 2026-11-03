// Package web holds the page and its static files, embedded into the binary.
package web

import "embed"

//go:embed index.html static
var Assets embed.FS
