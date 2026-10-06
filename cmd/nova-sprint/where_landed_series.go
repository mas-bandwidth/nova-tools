package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

// where --json carries landedSeries on the verb the dashboard already reads
// (whereJSON runs where --json). The object is folded from the epoch log as
// each JSON frame is written. reads.go is left as it is: this wraps the verb.
func init() {
	for i := range verbs {
		if verbs[i].name != "where" {
			continue
		}
		orig := verbs[i].run
		verbs[i].run = func(a *app, args []string, stdout, stderr io.Writer) int {
			return a.whereWithLandedSeries(orig, args, stdout, stderr)
		}
		break
	}
	verbEffect["where"] = "inspection: reads the sprint table, its rows and the epoch log, writes nothing; " +
		"--json carries landedSeries, cards landed per 10 minutes over the last 24 hours (144 buckets), " +
		"friends and fleet by the worker of the landed attempt (the last <who>:ok of the card's .wN, never the lander; a sentinel's release is not work)"
}

func (a *app) whereWithLandedSeries(orig func(*app, []string, io.Writer, io.Writer) int, args []string, stdout, stderr io.Writer) int {
	if !whereWantsJSON(args) {
		return orig(a, args, stdout, stderr)
	}
	redis, at := whereSeriesArgs(args, a.getenv)
	w := &landedSeriesWriter{w: stdout, load: func() (sprint.LandedSeries, error) {
		return a.landedSeries(redis, at)
	}}
	code := orig(a, args, w, stderr)
	if err := w.finish(); err != nil && code == 0 {
		fmt.Fprintf(stderr, "%s where: landedSeries: %s\n", prog, oneline.Escape(err.Error()))
		return 1
	}
	return code
}

// landedSeries reads the epoch log off the backend directly. The command's
// store() also beats the twin, and where --json is held to a trip budget that
// does not include that beat. The log fold is not a card read.
func (a *app) landedSeries(redis string, at int64) (sprint.LandedSeries, error) {
	var zero sprint.LandedSeries
	b, err := a.backend(context.Background(), redis, sprint.Names{})
	if err != nil {
		return zero, err
	}
	st := &store.Store{B: b, Names: sprint.Names{}, Now: a.now}
	st, err = st.Pinned(context.Background())
	if err != nil {
		return zero, err
	}
	if at >= 0 {
		st = st.At(uint64(at))
	}
	lines, err := st.Log(context.Background())
	if err != nil {
		return zero, err
	}
	return sprint.LandedSeriesOf(lines, a.now()), nil
}

func whereWantsJSON(args []string) bool {
	on := false
	for _, a := range args {
		switch a {
		case "--json", "--json=true", "-json", "-json=true":
			on = true
		case "--json=false", "-json=false":
			on = false
		}
	}
	return on
}

func whereSeriesArgs(args []string, getenv func(string) string) (redis string, at int64) {
	at = -1
	if v, ok := whereFlag(args, "redis"); ok {
		redis = v
	} else {
		redis = firstEnv(getenv, "NOVA_SPRINT_REDIS", "NOVA_REDIS_ADDR", seatLoginAddr)
	}
	if v, ok := whereFlag(args, "at-epoch"); ok {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			at = n
		}
	}
	return redis, at
}

func whereFlag(args []string, name string) (string, bool) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		for _, form := range []string{"--" + name, "-" + name} {
			if a == form {
				if i+1 < len(args) {
					return args[i+1], true
				}
				return "", true
			}
			if v, ok := strings.CutPrefix(a, form+"="); ok {
				return v, true
			}
		}
	}
	return "", false
}

// landedSeriesWriter adds landedSeries to each JSON object as the line
// completes, so a watch is not held to the end. A frame that already carries
// the field is left as it is. A log read that fails after where itself
// succeeded writes no frame missing the field.
type landedSeriesWriter struct {
	w      io.Writer
	buf    []byte
	load   func() (sprint.LandedSeries, error)
	failed error
}

func (w *landedSeriesWriter) Write(p []byte) (int, error) {
	w.buf = append(w.buf, p...)
	if err := w.drain(true); err != nil {
		return len(p), err
	}
	return len(p), nil
}

func (w *landedSeriesWriter) finish() error {
	if err := w.drain(false); err != nil {
		return err
	}
	return w.failed
}

func (w *landedSeriesWriter) drain(linesOnly bool) error {
	for {
		i := bytes.IndexByte(w.buf, '\n')
		if i < 0 {
			break
		}
		line := w.buf[:i]
		w.buf = w.buf[i+1:]
		if err := w.emit(line, true); err != nil {
			return err
		}
	}
	if linesOnly || len(bytes.TrimSpace(w.buf)) == 0 {
		w.buf = nil
		return nil
	}
	line := w.buf
	w.buf = nil
	return w.emit(line, false)
}

func (w *landedSeriesWriter) emit(line []byte, nl bool) error {
	out, err := w.augment(line)
	if err != nil {
		w.failed = err
		return err
	}
	if nl {
		out = append(out, '\n')
	}
	if _, err := w.w.Write(out); err != nil {
		w.failed = err
		return err
	}
	return nil
}

func (w *landedSeriesWriter) augment(line []byte) ([]byte, error) {
	trim := bytes.TrimSpace(line)
	if len(trim) == 0 || trim[0] != '{' {
		return append([]byte(nil), line...), nil
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(trim, &obj); err != nil {
		return append([]byte(nil), line...), nil
	}
	if _, ok := obj["landedSeries"]; ok {
		return append([]byte(nil), trim...), nil
	}
	series, err := w.load()
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(series)
	if err != nil {
		return nil, err
	}
	obj["landedSeries"] = raw
	return json.Marshal(obj)
}
