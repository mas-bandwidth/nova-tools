package main

import (
	"fmt"
	"io"
	"strings"
)

type termDrawer struct {
	w          io.Writer
	lastHeight int
	enabled    bool
}

func newTermDrawer(w io.Writer, loop bool, out string) *termDrawer {
	return &termDrawer{
		w:       w,
		enabled: loop && out == "",
	}
}

func (t *termDrawer) init() {
	if t.enabled {
		fmt.Fprint(t.w, "\033[?25l")
	}
}

func (t *termDrawer) draw(body string) error {
	if !t.enabled {
		_, err := io.WriteString(t.w, body)
		return err
	}
	height := strings.Count(body, "\n")
	if t.lastHeight != 0 {
		if height != t.lastHeight {
			fmt.Fprint(t.w, "\033[2J\033[H")
		} else {
			fmt.Fprint(t.w, "\033[H\033[J")
		}
	}
	t.lastHeight = height
	_, err := io.WriteString(t.w, body)
	return err
}

func (t *termDrawer) close() {
	if t.enabled {
		fmt.Fprint(t.w, "\033[?25h")
	}
}
