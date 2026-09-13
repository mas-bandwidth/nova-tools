package main

import (
	"github.com/mas-bandwidth/nova-tools/internal/update"
	"os"
)

var version string

func main() { os.Exit(update.Main("nova-update", os.Args[1:], version, os.Stdout, os.Stderr)) }
