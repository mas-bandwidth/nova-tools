package main

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// badIDs are ids that do not name exactly one file or directory of that name
// inside the store, on some platform, once they become a path component
// (sessions/<id>.md, <store>/<id>.md, entries/<id>/, <id>.json).
var badIDs = map[string]string{
	"empty":                    "",
	"dot":                      ".",
	"dot dot":                  "..",
	"only dots":                "...",
	"slash":                    "a/b",
	"backslash":                `a\b`,
	"parent inside":            "a..b",
	"trailing dot":             "s.",
	"trailing space":           "s ",
	"space inside":             "a b",
	"colon":                    "a:b",
	"windows forbidden char":   "a*b",
	"windows device":           "con",
	"windows device any case":  "Nul",
	"windows device with ext":  "aux.txt",
	"windows numbered device":  "COM1",
	"windows numbered printer": "lpt9",
	"control":                  "a\x01b",
}

// snapshot is every path under dir with its mode and bytes: two snapshots are
// equal only when nothing was written, created or removed.
func snapshot(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	require.NoError(t, filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		v := info.Mode().String()
		if d.Type().IsRegular() {
			raw, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			v += " " + string(raw)
		}
		out[path] = v
		return nil
	}))
	return out
}

// Every verb that takes a session or an entry id refuses a bad one in the
// tool's grammar, at exit 2, and writes nothing: the store is the same, path
// for path and byte for byte, before and after.
func TestABadIDIsRefusedByEveryVerbAndWritesNothing(t *testing.T) {
	t.Parallel()
	for name, bad := range badIDs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			c := newRig(t)
			c.ok("open", "--session", "s1", "--publish", "manual")
			c.ok("append", "--session", "s1", "--entry", "e1", "--text", "kept words")
			before := snapshot(t, c.store)
			for _, args := range [][]string{
				{"open", "--session", bad, "--publish", "manual"},
				{"open", "--session", bad, "--publish", "manual", "--dry-run"},
				{"append", "--session", bad, "--entry", "e2", "--text", "w"},
				{"append", "--session", "s1", "--entry", bad, "--text", "w"},
				{"append", "--session", "s1", "--entry", bad, "--text", "w", "--dry-run"},
				{"index", "--session", bad},
				{"receipt", "--session", bad, "--entry", "e1"},
				{"receipt", "--session", "s1", "--entry", bad},
				{"receipt", "--session", "s1", "--entry", bad, "--text"},
			} {
				if bad == "" && args[0] == "index" {
					continue // index --session "" is the documented default: every record
				}
				r := c.run(args[0], args[1:]...)
				assert.Equal(t, 2, r.Code, "%q: %+v", args, r)
				assert.Empty(t, r.Stdout, "%q", args)
				assert.Contains(t, r.Stderr, strings.ToUpper(args[0])+" REFUSED: ", "%q", args)
				assert.Equal(t, before, snapshot(t, c.store), "%q wrote to the store", args)
			}
		})
	}
}

// The round trip a bad id broke: with ordinary ids, an entry appended is the
// entry index lists and receipt finds.
func TestAnAppendedEntryIsTheOneIndexAndReceiptFind(t *testing.T) {
	t.Parallel()
	c := newRig(t)
	for _, id := range []string{"s1", "a.b", ".hidden", "2026-10-02_x"} {
		c.ok("open", "--session", id, "--publish", "manual")
		c.ok("append", "--session", id, "--entry", "e-"+id, "--text", "words of "+id)
		printed(t, c.ok("index", "--session", id), "INDEX OK sessions=", " entries=1", "INDEX ENTRY session="+id+" entry=e-"+id+" ")
		printed(t, c.ok("receipt", "--session", id, "--entry", "e-"+id, "--text"), `text="words of `+id+`"`)
	}
}
