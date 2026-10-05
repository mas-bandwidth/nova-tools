package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

func init() {
	for i := range verbs {
		if verbs[i].name == "where" {
			orig := verbs[i].run
			verbs[i].run = func(a *app, args []string, stdout, stderr io.Writer) int {
				isJSON := false
				for _, arg := range args {
					if arg == "--json" || strings.HasPrefix(arg, "--json=") {
						isJSON = true
						break
					}
				}
				if !isJSON {
					return orig(a, args, stdout, stderr)
				}
				w := &whereJSONWriter{
					a:      a,
					args:   args,
					out:    stdout,
					stderr: stderr,
				}
				code := orig(a, args, w, stderr)
				w.flush()
				return code
			}
		}
	}
}

type whereJSONWriter struct {
	a      *app
	args   []string
	out    io.Writer
	stderr io.Writer
	buf    bytes.Buffer
}

func (w *whereJSONWriter) Write(p []byte) (int, error) {
	w.buf.Write(p)
	for {
		line, err := w.buf.ReadString('\n')
		if err != nil {
			w.buf.WriteString(line)
			break
		}
		trimmed := strings.TrimRight(line, "\r\n")
		if trimmed == "" {
			fmt.Fprintln(w.out)
			continue
		}
		enriched := w.enrich(trimmed)
		fmt.Fprintln(w.out, enriched)
	}
	return len(p), nil
}

func (w *whereJSONWriter) flush() {
	if w.buf.Len() == 0 {
		return
	}
	trimmed := strings.TrimRight(w.buf.String(), "\r\n")
	w.buf.Reset()
	if trimmed == "" {
		return
	}
	enriched := w.enrich(trimmed)
	fmt.Fprintln(w.out, enriched)
}

func (w *whereJSONWriter) enrich(line string) string {
	var raw map[string]any
	if err := json.Unmarshal([]byte(line), &raw); err != nil {
		return line
	}
	if _, ok := raw["tables"]; !ok {
		return line
	}

	fs, c := w.a.verbSetup("where")
	atEpoch := fs.Int64("at-epoch", -1, "the sprint as it was at an earlier epoch (before a clear)")
	_, _ = parse(fs, w.args) // ignored: flags already validated by where

	ctx := context.Background()
	st, err := w.a.storeAtCtx(ctx, *c, *atEpoch)
	if err != nil || st == nil {
		return line
	}

	now := w.a.now()
	if atStr, ok := raw["at"].(string); ok {
		if t, err := time.Parse(time.RFC3339Nano, atStr); err == nil {
			now = t
		} else if t, err := time.Parse(time.RFC3339, atStr); err == nil {
			now = t
		}
	}

	series, err := sprint.LandedSeriesFrom(ctx, st, now)
	if err != nil {
		return line
	}

	raw["landedSeries"] = series
	b, err := json.Marshal(raw)
	if err != nil {
		return line
	}
	return string(b)
}
