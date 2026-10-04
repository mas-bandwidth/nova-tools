package main

import (
	"github.com/mas-bandwidth/nova-tools/internal/release"
	"os"
)

var version string

func main() {
	os.Exit(release.Main("nova-release", os.Args[1:], version, os.Stdout, os.Stderr))
}
