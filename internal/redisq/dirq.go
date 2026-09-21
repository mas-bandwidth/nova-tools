package redisq

// The directory fallback: the queue a bench takes today, `queue/` by atomic rename, kept
// beside the Redis streams so an outage of the instance is a latency regression and never
// a lost card. A bench chooses this mode at start (ChooseMode) and then never touches a
// Redis key; a bench that chose Redis never writes here. The two stores are never active
// at once, because a card granted twice -- once by each store -- is a fence no token can
// see across.

import (
	"errors"
	"fmt"
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

func parseCardFile(path string) (map[string]string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	fields := map[string]string{}
	for _, line := range strings.Split(string(raw), "\n") {
		if line == "" {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		fields[k] = unescapeValue(v)
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
