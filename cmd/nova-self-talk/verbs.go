package main

import (
	"embed"
)

// pages are the two example pages, inside the binary, so a first run needs nothing but it.
//
//go:embed testdata/example-pages/*.md
var pages embed.FS
