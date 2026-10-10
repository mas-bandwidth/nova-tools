package main

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

func init() {
	verbClasses["waiting"] = classRead
}

func (a *app) cmdWaiting(args []string, stdout, stderr io.Writer) int {
	fs, c := a.verbSetup("waiting")
	stream := fs.String("stream", "", "the waiting cards of one stream (default: every stream)")
	s, code := a.workSnapshot("waiting", fs, args, c, stderr)
	if code != 0 {
		return code
	}
	view := sprint.ClassifyWaiting(s, *stream)
	if c.json {
		b, _ := json.Marshal(view)
		fmt.Fprintln(stdout, string(b))
		return 0
	}
	for _, card := range view.Cards {
		fmt.Fprintf(stdout, "WAITING %s stream=%s reason=%s head=%s length=%d\n", oneline.Escape(card.ID), oneline.Escape(card.Stream), oneline.Escape(card.Reason), oneline.Escape(card.Head), card.Length)
	}
	fmt.Fprintf(stdout, "WAITING OK cards=%d\n", len(view.Cards))
	return 0
}
