package redisq

// The directory fallback: the queue a bench takes today, `queue/` by atomic rename, kept
// beside the Redis streams so an outage of the instance is a latency regression and never
// a lost card. A bench chooses this mode at start (ChooseMode) and then never touches a
// Redis key; a bench that chose Redis never writes here. The two stores are never active
// at once, because a card granted twice -- once by each store -- is a fence no token can
// see across.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// ErrUnsafeStream reports a stream name that escapes the queue root.
var ErrUnsafeStream = errors.New("stream escapes queue root")

// DirQueue is the file half of the queue: one directory per stream, one `<id>.card` file
// per card, and a `taken/` subdirectory the atomic rename moves a card into.
type DirQueue struct {
	Root string
}

// DirStream is the directory one stream's cards live under. The stream name carries the
// colons of `nova:queue:<kind>:<lane>`, which are ordinary characters in a file name.
func (d *DirQueue) DirStream(stream string) string {
	return filepath.Join(d.Root, filepath.FromSlash(stream))
}

// isSafe reports whether DirStream(stream) stays strictly below Root.
func (d *DirQueue) isSafe(stream string) bool {
	if strings.TrimSpace(stream) == "" {
		return false
	}
	dir := d.DirStream(stream)
	rootAbs, err := filepath.Abs(d.Root)
	if err != nil {
		return false
	}
	dirAbs, err := filepath.Abs(dir)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(rootAbs, dirAbs)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false
	}
	return true
}

// Add writes one card into its stream directory. The card is written to a temporary name
// and renamed into place, so a reader never sees a half-written card.
func (d *DirQueue) Add(stream, id string, fields map[string]string) (string, error) {
	if !d.isSafe(stream) {
		return "", ErrUnsafeStream
	}
	dir := d.DirStream(stream)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	var b strings.Builder
	b.WriteString(CardFormatMarker + "\n")
	for _, k := range sortedKeys(fields) {
		fmt.Fprintf(&b, "%s=%s\n", k, escapeValue(fields[k]))
	}
	tmp, err := os.CreateTemp(dir, ".add-*")
	if err != nil {
		return "", err
	}
	if _, err := tmp.WriteString(b.String()); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	final := filepath.Join(dir, id+".card")
	if err := os.Rename(tmp.Name(), final); err != nil {
		_ = os.Remove(tmp.Name())
		return "", err
	}
	return final, nil
}

// Pull takes the oldest card in stream by atomic rename into taken/. The rename is what
// makes two pullers race and only one win: a card is never inferred from a count.
func (d *DirQueue) Pull(stream string) (*Card, error) {
	if !d.isSafe(stream) {
		return nil, ErrUnsafeStream
	}
	dir := d.DirStream(stream)
	taken := filepath.Join(dir, "taken")
	if err := os.MkdirAll(taken, 0o755); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".card") {
			continue
		}
		names = append(names, e.Name())
	}
	sort.Strings(names)
	for _, name := range names {
		src := filepath.Join(dir, name)
		dst := filepath.Join(taken, name)
		if err := os.Rename(src, dst); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue // another puller won this card
			}
			return nil, err
		}
		fields, err := parseCardFile(dst)
		if err != nil {
			return nil, err
		}
		return &Card{ID: strings.TrimSuffix(name, ".card"), Stream: stream, Fields: fields}, nil
	}
	return nil, nil
}

// Ack removes a card a worker has landed, from either the taken/ list or the stream dir.
func (d *DirQueue) Ack(stream, id string) error {
	if !d.isSafe(stream) {
		return ErrUnsafeStream
	}
	dir := d.DirStream(stream)
	for _, p := range []string{
		filepath.Join(dir, "taken", id+".card"),
		filepath.Join(dir, id+".card"),
	} {
		if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

// Reclaim returns a card whose worker stopped renewing: a taken card older than lease is
// renamed back into the stream directory, and the next Pull takes it. It is the file
// fallback's XAUTOCLAIM.
func (d *DirQueue) Reclaim(stream string, lease time.Duration, now time.Time) (bool, error) {
	if !d.isSafe(stream) {
		return false, ErrUnsafeStream
	}
	dir := d.DirStream(stream)
	taken := filepath.Join(dir, "taken")
	entries, err := os.ReadDir(taken)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".card") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			return false, err
		}
		if now.Sub(info.ModTime()) < lease {
			continue
		}
		src := filepath.Join(taken, e.Name())
		dst := filepath.Join(dir, e.Name())
		if err := os.Rename(src, dst); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return false, err
		}
		return true, nil
	}
	return false, nil
}

// PullLanes reads the oldest card across the ordered priority lanes:
// red first, then green, then small, then next. The kind is the
// middle segment of nova:queue:<kind>:<lane>.
func (d *DirQueue) PullLanes(kind string) (*Card, error) {
	for _, lane := range []string{"red", "green", "small", "next"} {
		card, err := d.Pull("nova:queue:" + kind + ":" + lane)
		if err != nil {
			return nil, err
		}
		if card != nil {
			return card, nil
		}
	}
	return nil, nil
}

// PullLanes reads the oldest card from Redis across the ordered priority
// lanes: red, green, small, next. The kind is the middle segment of
// nova:queue:<kind>:<lane>.
func (q *Queue) PullLanes(ctx context.Context, kind, bench string, block time.Duration) (*Card, error) {
	for _, lane := range []string{"red", "green", "small", "next"} {
		card, err := q.Pull(ctx, "nova:queue:"+kind+":"+lane, bench, block)
		if err != nil {
			return nil, err
		}
		if card != nil {
			return card, nil
		}
	}
	return nil, nil
}

// LiveCards is every card from this directory store that is live: waiting in a
// stream directory, or taken by a worker and never acked -- the old store's
// queue and pending list. It is what a restart in the other mode drains before
// the switch, one "<stream>/<id>" per card, sorted.
func (d *DirQueue) LiveCards() ([]string, error) {
	entries, err := os.ReadDir(d.Root)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var live []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		stream := e.Name()
		for _, where := range []string{stream, filepath.Join(stream, "taken")} {
			cards, err := os.ReadDir(filepath.Join(d.Root, where))
			if err != nil {
				if errors.Is(err, os.ErrNotExist) {
					continue
				}
				return nil, err
			}
			for _, c := range cards {
				if c.IsDir() || !strings.HasSuffix(c.Name(), ".card") {
					continue
				}
				live = append(live, stream+"/"+strings.TrimSuffix(c.Name(), ".card"))
			}
		}
	}
	sort.Strings(live)
	return live, nil
}

// ModeRestart is the drain gate of SPEC-STATE's "One mode per bench": a bench
// switching mode refuses to start the new mode while any card from this, the
// old mode's store is still live (LiveCards above). The bench drains its cards
// -- reclaiming each back to a queue that will run it, or completing one whose
// result landed -- and asks again; only when the store holds no live card does
// the gate start the new mode, and the drain is written to log as
// `mode restart: directory drained (<n> reclaimed, <m> completed) -> <new>`,
// so a mode switch is never a silent drop of the work the old mode held. The
// refused call writes nothing: a restart that did not start has no drain to
// record.
func (d *DirQueue) ModeRestart(log io.Writer, reclaimed, completed int, newMode Mode) (bool, error) {
	live, err := d.LiveCards()
	if err != nil {
		return false, err
	}
	if len(live) > 0 {
		return false, nil
	}
	fmt.Fprintf(log, "mode restart: %s drained (%d reclaimed, %d completed) -> %s\n",
		ModeDirectory, reclaimed, completed, newMode)
	return true, nil
}

// CardFormatMarker is the header written at the start of card files using escaped values.
const CardFormatMarker = "# dirq:v1"

func parseCardFile(path string) (map[string]string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	lines := strings.Split(string(raw), "\n")
	isEscapedFormat := false
	if len(lines) > 0 {
		first := strings.TrimSpace(lines[0])
		if first == CardFormatMarker || first == "# format=v1" || first == "# format=escaped" || first == "format=v1" {
			isEscapedFormat = true
		}
	}
	fields := map[string]string{}
	for i, line := range lines {
		if line == "" {
			continue
		}
		if i == 0 && (strings.HasPrefix(line, "# dirq:") || strings.HasPrefix(line, "# format=") || line == "format=v1") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		// The format is decided once, from the line-0 marker, never from a per-value
		// prefix: a caller-provided field value is free to begin with "esc:" or "v1:"
		// as ordinary data, and a prefix check here would silently eat it (stella's
		// HOLD 7 on #2489 at 24d5bd7c). Only escapeValue/unescapeValue -- which never
		// emit or expect those strings -- decide what a value means.
		if isEscapedFormat {
			fields[k] = unescapeValue(v)
		} else {
			fields[k] = v
		}
	}
	return fields, nil
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// escapeValue encodes newlines, carriage returns, and backslashes so each key=value pair
// fits on exactly one line and multiline values (such as card bodies) round-trip intact.
func escapeValue(s string) string {
	if !strings.ContainsAny(s, "\\\n\r") {
		return s
	}
	var b strings.Builder
	b.Grow(len(s) + 16)
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		default:
			b.WriteByte(s[i])
		}
	}
	return b.String()
}

// unescapeValue restores escaped newlines, carriage returns, and backslashes.
func unescapeValue(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) {
			switch s[i+1] {
			case '\\':
				b.WriteByte('\\')
				i++
				continue
			case 'n':
				b.WriteByte('\n')
				i++
				continue
			case 'r':
				b.WriteByte('\r')
				i++
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}
