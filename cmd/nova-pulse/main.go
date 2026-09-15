package main

import (
	"os"

	"github.com/mas-bandwidth/nova-tools/internal/pulse"
)

var version string

func main() { os.Exit(pulse.Main("nova-pulse", os.Args[1:], version, os.Stdout, os.Stderr)) }
