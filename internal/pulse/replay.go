package pulse

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/swarm"
	"github.com/mas-bandwidth/nova-tools/internal/typedrec"
)

// ReplayClass is one results bundle's class in the #2634 part 3 report.
// gateway is a route that died before the model had a turn. card is the
// card's own ABSTAIN or BLOCKED. other is neither, and is not counted as a
// card failure.
type ReplayClass string

const (
	ReplayGateway ReplayClass = "gateway"
	ReplayCard    ReplayClass = "card"
	ReplayOther   ReplayClass = "other"
)

// ReplayRow is one bundle. Path is the bundle directory relative to the
// replay root, slash-separated.
type ReplayRow struct {
	Path  string
	Class ReplayClass
}

// ReplayReport is what a replay counted. The text is counts of bundles, never
// a rate: the sprint's live results are not in this tree, and this report
// does not invent one.
type ReplayReport struct {
	Rows    []ReplayRow
	Gateway int
	Card    int
	Other   int
}

// Text is the report: one count line, then one class line per bundle.
func (r ReplayReport) Text() string {
	var b strings.Builder
	fmt.Fprintf(&b, "REPLAY bundles=%d gateway=%d card=%d other=%d\n", len(r.Rows), r.Gateway, r.Card, r.Other)
	for _, row := range r.Rows {
		fmt.Fprintf(&b, "%s %s\n", row.Class, oneline.Escape(row.Path))
	}
	return b.String()
}

// replayCaptureNames are the files a gateway error was written into. RESULT.md
// is the card's verdict, not this capture: a card may quote an error and still
// have abstained on its own.
var replayCaptureNames = []string{"harness-output.log", "harness.log", "native.log", "exit.json"}

const (
	replayRecordMax = 1 << 20
	replayHeadBytes = 256 << 10
	replayTailBytes = 64 << 10
)

// ReplayBundles classifies every results bundle under root. A bundle is a
// directory that holds a regular usage.tsv, which is the record the sprint
// harvested beside RESULT.md when the card published one.
//
// A gateway death is the provider failure the sprint recorded on that route:
// no positive token count, and a capture swarm.ProviderLaunchFailure accepts
// (the UnknownError body, "Unexpected server error"). A card that published
// ABSTAIN or BLOCKED is a card failure and is not reclassified because a log
// also names an error. DONE is the card's own word that it finished, so it
// is not a gateway death either.
func ReplayBundles(root string) (ReplayReport, error) {
	fi, err := os.Stat(root)
	if err != nil {
		return ReplayReport{}, err
	}
	if !fi.IsDir() {
		return ReplayReport{}, fmt.Errorf("%s is not a directory", root)
	}
	var rows []ReplayRow
	err = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || d.Name() != "usage.tsv" {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		dir := filepath.Dir(path)
		class, err := classifyBundle(dir)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, dir)
		if err != nil {
			return err
		}
		rows = append(rows, ReplayRow{Path: filepath.ToSlash(rel), Class: class})
		return nil
	})
	if err != nil {
		return ReplayReport{}, err
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Path < rows[j].Path })
	var rep ReplayReport
	rep.Rows = rows
	for _, row := range rows {
		switch row.Class {
		case ReplayGateway:
			rep.Gateway++
		case ReplayCard:
			rep.Card++
		default:
			rep.Other++
		}
	}
	return rep, nil
}

func classifyBundle(dir string) (ReplayClass, error) {
	usage, err := readReplayRecord(filepath.Join(dir, "usage.tsv"))
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(usage) == "" {
		return "", fmt.Errorf("%s: usage.tsv is empty", dir)
	}
	verdict, err := bundleVerdict(dir)
	if err != nil {
		return "", err
	}
	switch verdict {
	case "card":
		return ReplayCard, nil
	case "done":
		return ReplayOther, nil
	}
	turn, known, err := bundleHadTurn(usage)
	if err != nil {
		return "", err
	}
	if !known || turn {
		return ReplayOther, nil
	}
	capture, err := bundleCapture(dir)
	if err != nil {
		return "", err
	}
	if _, ok := swarm.ProviderLaunchFailure([]byte(capture)); ok {
		return ReplayGateway, nil
	}
	return ReplayOther, nil
}

// bundleVerdict is "card" for ABSTAIN or BLOCKED, "done" for DONE, and empty
// when the card published no such line. A missing RESULT.md is empty, not an
// error: the sprint's gateway deaths left none.
func bundleVerdict(dir string) (string, error) {
	raw, err := readReplayRecord(filepath.Join(dir, "RESULT.md"))
	if err != nil {
		return "", err
	}
	if raw == "" {
		return "", nil
	}
	switch typedrec.Status([]byte(raw)) {
	case typedrec.StatusAbstain, typedrec.StatusBlocked:
		return "card", nil
	case typedrec.StatusDone:
		return "done", nil
	}
	return "", nil
}

// bundleHadTurn reports whether any usage row recorded a positive token count.
// known is false when the file has no tokens_in or tokens_out column, and a
// bundle whose tokens cannot be read is not called a gateway death.
func bundleHadTurn(raw string) (turn, known bool, err error) {
	raw = strings.ReplaceAll(raw, "\r\n", "\n")
	var header []string
	var rows []string
	for _, line := range strings.Split(raw, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if header == nil {
			header = strings.Split(line, "\t")
			continue
		}
		rows = append(rows, line)
	}
	if header == nil {
		return false, false, nil
	}
	idx := make(map[string]int, len(header))
	for i, h := range header {
		idx[strings.TrimSpace(h)] = i
	}
	_, hasIn := idx["tokens_in"]
	_, hasOut := idx["tokens_out"]
	if !hasIn && !hasOut {
		return false, false, nil
	}
	for _, line := range rows {
		cols := strings.Split(line, "\t")
		for _, name := range []string{"tokens_in", "tokens_out"} {
			i, ok := idx[name]
			if !ok || i >= len(cols) {
				continue
			}
			v := strings.TrimSpace(cols[i])
			if v == "" || v == "-" {
				continue
			}
			n, convErr := strconv.Atoi(v)
			if convErr != nil {
				return false, false, fmt.Errorf("usage %s %q", name, v)
			}
			if n > 0 {
				return true, true, nil
			}
		}
	}
	return false, true, nil
}

func bundleCapture(dir string) (string, error) {
	var b strings.Builder
	for _, name := range replayCaptureNames {
		s, err := readReplayCapture(filepath.Join(dir, name))
		if err != nil {
			return "", err
		}
		if s == "" {
			continue
		}
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(s)
	}
	return b.String(), nil
}

// readReplayRecord reads a whole regular file up to replayRecordMax. A missing
// file and a non-regular file are empty: neither is a record.
func readReplayRecord(path string) (string, error) {
	fi, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	if !fi.Mode().IsRegular() {
		return "", nil
	}
	if fi.Size() > replayRecordMax {
		return "", fmt.Errorf("%s is %d bytes; replay reads at most %d", path, fi.Size(), replayRecordMax)
	}
	return readFileRange(path, 0, fi.Size())
}

// readReplayCapture is the head of a log and, when the file is longer than
// that head, its tail. The provider error is in one of those two places.
func readReplayCapture(path string) (string, error) {
	fi, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	if !fi.Mode().IsRegular() || fi.Size() == 0 {
		return "", nil
	}
	if fi.Size() <= replayHeadBytes+replayTailBytes {
		return readFileRange(path, 0, fi.Size())
	}
	head, err := readFileRange(path, 0, replayHeadBytes)
	if err != nil {
		return "", err
	}
	tail, err := readFileRange(path, fi.Size()-replayTailBytes, replayTailBytes)
	if err != nil {
		return "", err
	}
	if i := strings.IndexByte(tail, '\n'); i >= 0 {
		tail = tail[i+1:]
	}
	return head + "\n" + tail, nil
}

func readFileRange(path string, off, n int64) (string, error) {
	if n == 0 {
		return "", nil
	}
	f, err := os.OpenFile(path, os.O_RDONLY|swarm.ONoFollow, 0)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	defer f.Close()
	if off > 0 {
		if _, err := f.Seek(off, io.SeekStart); err != nil {
			return "", err
		}
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(f, buf); err != nil {
		return "", fmt.Errorf("%s: %w", path, err)
	}
	return string(buf), nil
}
