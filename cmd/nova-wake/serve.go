package main

import (
	"io"

	"github.com/mas-bandwidth/nova-tools/internal/wake"
)

func cmdServe(args []string, stdout, stderr io.Writer, clock wake.Clock) int {
	return refused(stderr, "serve is not built yet")
}
