package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestADryShellIsDryForEveryLine: a shell entered with --dry-run plans every
// write line, and a line that says --dry-run=false is refused, never run: the
// address it names holds no store, so a line that dialled would be answered
// with the unreachable store instead of the refusal. A batch line, whose dry
// run is its own branch, is held the same way; --dry-run=true and a bare
// line still plan.
func TestADryShellIsDryForEveryLine(t *testing.T) {
	t.Parallel()
	nowhere := filepath.Join(t.TempDir(), "no-store.sock")
	manifest := `'{"schema":1,"table":"demo","epoch":"0","expected_table_revision":"2","operation_id":"create-b1","members":[{"id":"b1","expect":{"absent":true},"create":{"row":"build","col":"ready","score":0}}]}'`
	for _, tc := range []struct {
		name, line string
		refused    bool
	}{
		{"a write line turning the dry run off", "row add demo build --dry-run=false", true},
		{"a view line turning it off", "view set work --tables demo --dry-run=false", true},
		{"a batch line turning it off", "batch --dry-run=false " + manifest, true},
		{"a bare write line", "row add demo build", false},
		{"a write line saying --dry-run", "row add demo build --dry-run", false},
		{"a batch line", "batch " + manifest, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			app := &application{dryRun: true, getenv: func(string) string { return "" }}
			var out, errs bytes.Buffer
			code := app.readCommands(strings.NewReader(tc.line+" --redis "+nowhere+"\n"), &out, &errs, false, false)
			assert.NotContains(t, errs.String(), "unreachable", "the line dialled the store")
			if tc.refused {
				assert.EqualValues(t, 2, code)
				assert.Empty(t, out.String())
				assert.Contains(t, errs.String(), "REFUSED: this shell was entered with --dry-run, so every write line is planned and none is written")
				return
			}
			assert.EqualValues(t, 0, code, "%s", errs.String())
			assert.Contains(t, out.String(), "TABLE DRY-RUN verb=")
			assert.Contains(t, out.String(), "dialled=0 written=0")
		})
	}
}
