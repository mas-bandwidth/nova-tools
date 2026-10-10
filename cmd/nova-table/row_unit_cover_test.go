package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// rowCover tests for row verbs (add, set, hide, show, del) that cover
// refusals and dry-run plans without needing a live store.
func TestNovaTableRowCoverAdd(t *testing.T) {
	t.Parallel()

	// unknown flag (exit 2, ROW-ADD REFUSED naming nova-table help row add)
	t.Run("unknownFlag", func(t *testing.T) {
		t.Parallel()
		code, out, err := runTable("row", "add", "demo", "build", "--unknown")
		assert.EqualValues(t, 2, code)
		assert.Empty(t, out)
		assert.Contains(t, err, "ROW-ADD REFUSED")
		assert.Contains(t, err, "unknown flag")
		assert.Contains(t, err, "nova-table help row add")
	})

	// one positional (usage refusal)
	t.Run("onePositional", func(t *testing.T) {
		t.Parallel()
		code, out, err := runTable("row", "add", "demo")
		assert.EqualValues(t, 2, code)
		assert.Empty(t, out)
		assert.Contains(t, err, "ROW-ADD REFUSED")
		assert.Contains(t, err, "wants a table and a row")
	})

	// batch form with dry-run
	t.Run("batchDryRun", func(t *testing.T) {
		t.Parallel()
		code, out, err := runTable("row", "add", "demo", "a", "b", "c", "--dry-run", "--redis", "")
		assert.EqualValues(t, 0, code)
		assert.Empty(t, err)
		assert.Contains(t, out, "TABLE DRY-RUN verb=row-add")
		assert.Contains(t, out, ` sends="FCALL ns_table_rows_add"`)
		assert.Contains(t, out, "redis=- dialled=0 written=0")
		assert.Equal(t, 1, strings.Count(out, "\n"))
	})

	// batch form with bad key (non-zero exit, nothing on stdout, same text with and without --dry-run)
	t.Run("batchBadKey", func(t *testing.T) {
		t.Parallel()
		code, out, err := runTable("row", "add", "demo", badKey)
		assert.NotEqualValues(t, 0, code)
		assert.Empty(t, out)
		// same text without --dry-run
		code2, out2, err2 := runTable("row", "add", "demo", badKey, "--dry-run", "--redis", "")
		assert.Equal(t, code, code2)
		assert.Empty(t, out2)
		assert.Equal(t, err, err2)
	})

	// malformed binding c2= (exit 2, names the binding)
	t.Run("malformedBinding", func(t *testing.T) {
		t.Parallel()
		code, out, err := runTable("row", "add", "demo", "build", "c2=")
		assert.EqualValues(t, 2, code)
		assert.Empty(t, out)
		assert.Contains(t, err, "ROW-ADD REFUSED")
		assert.Contains(t, err, "c2=")
	})

	// binding with no --owner (exit 2, names --owner)
	t.Run("bindingNoOwner", func(t *testing.T) {
		t.Parallel()
		code, out, err := runTable("row", "add", "demo", "build", "c1=k1")
		assert.EqualValues(t, 2, code)
		assert.Empty(t, out)
		assert.Contains(t, err, "ROW-ADD REFUSED")
		assert.Contains(t, err, "--owner")
	})

	// binding with --owner deploy --label Build --dry-run (exit 0, plan line carries owner=deploy and label=Build)
	t.Run("bindingWithOwnerLabelDryRun", func(t *testing.T) {
		t.Parallel()
		code, out, err := runTable("row", "add", "demo", "build", "c1=k1", "--owner", "deploy", "--label", "Build", "--dry-run", "--redis", "")
		assert.EqualValues(t, 0, code)
		assert.Empty(t, err)
		assert.Contains(t, out, "TABLE DRY-RUN verb=row-add")
		assert.Contains(t, out, "owner=deploy")
		assert.Contains(t, out, "label=Build")
	})

	// with --redis "" and no --dry-run (exit 2, ROW-ADD REFUSED: --redis <addr> is required)
	t.Run("noAddressNoDryRun", func(t *testing.T) {
		t.Parallel()
		code, out, err := runTable("row", "add", "demo", "build", "--redis", "")
		assert.EqualValues(t, 2, code)
		assert.Empty(t, out)
		assert.Contains(t, err, "ROW-ADD REFUSED")
		assert.Contains(t, err, "--redis <addr> is required")
	})
}

func TestNovaTableRowCoverDel(t *testing.T) {
	t.Parallel()

	// one positional refused
	t.Run("onePositional", func(t *testing.T) {
		t.Parallel()
		code, out, err := runTable("row", "del", "demo")
		assert.EqualValues(t, 2, code)
		assert.Empty(t, out)
		assert.Contains(t, err, "ROW-DEL REFUSED")
		assert.Contains(t, err, "wants a table and a row")
	})

	// three positionals refused
	t.Run("threePositionals", func(t *testing.T) {
		t.Parallel()
		code, out, err := runTable("row", "del", "demo", "build", "extra")
		assert.EqualValues(t, 2, code)
		assert.Empty(t, out)
		assert.Contains(t, err, "ROW-DEL REFUSED")
		assert.Contains(t, err, "wants a table and a row")
	})

	// dry-run plan
	t.Run("dryRun", func(t *testing.T) {
		t.Parallel()
		code, out, err := runTable("row", "del", "demo", "build", "--dry-run", "--redis", "")
		assert.EqualValues(t, 0, code)
		assert.Empty(t, err)
		assert.Contains(t, out, "TABLE DRY-RUN verb=row-del")
		assert.Contains(t, out, ` sends="FCALL ns_table_row_del"`)
		assert.Contains(t, out, "redis=- dialled=0 written=0")
	})

	// no-address refusal
	t.Run("noAddress", func(t *testing.T) {
		t.Parallel()
		code, out, err := runTable("row", "del", "demo", "build", "--redis", "")
		assert.EqualValues(t, 2, code)
		assert.Empty(t, out)
		assert.Contains(t, err, "ROW-DEL REFUSED")
		assert.Contains(t, err, "--redis <addr> is required")
	})
}

func TestNovaTableRowCoverSet(t *testing.T) {
	t.Parallel()

	// two positionals refused
	t.Run("twoPositionals", func(t *testing.T) {
		t.Parallel()
		code, out, err := runTable("row", "set", "demo")
		assert.EqualValues(t, 2, code)
		assert.Empty(t, out)
		assert.Contains(t, err, "ROW-SET REFUSED")
		assert.Contains(t, err, "wants a table, a row and at least one <col>=<value>")
	})

	// =v (empty column) refused naming it
	t.Run("emptyColumn", func(t *testing.T) {
		t.Parallel()
		code, out, err := runTable("row", "set", "demo", "build", "=v")
		assert.EqualValues(t, 2, code)
		assert.Empty(t, out)
		assert.Contains(t, err, "ROW-SET REFUSED")
		assert.Contains(t, err, "wants <col>=<value>, not =v")
	})

	// build note=a done=b --dry-run plans
	t.Run("dryRun", func(t *testing.T) {
		t.Parallel()
		code, out, err := runTable("row", "set", "demo", "build", "note=a", "done=b", "--dry-run", "--redis", "")
		assert.EqualValues(t, 0, code)
		assert.Empty(t, err)
		assert.Contains(t, out, "TABLE DRY-RUN verb=row-set")
		assert.Contains(t, out, ` sends="FCALL ns_table_row_set"`)
		assert.Contains(t, out, "redis=- dialled=0 written=0")
	})

	// value over ntable.LimitFieldValueBytes refused before any dial
	t.Run("valueTooLarge", func(t *testing.T) {
		t.Parallel()
		largeValue := strings.Repeat("x", 1024*1024)
		code, out, err := runTable("row", "set", "demo", "build", "col="+largeValue, "--redis", "")
		assert.NotEqualValues(t, 0, code)
		assert.Empty(t, out)
		assert.Contains(t, err, "ROW-SET REFUSED")
		assert.Contains(t, err, "field value bytes")
	})

	// no-address refusal
	t.Run("noAddress", func(t *testing.T) {
		t.Parallel()
		code, out, err := runTable("row", "set", "demo", "build", "col=val", "--redis", "")
		assert.EqualValues(t, 2, code)
		assert.Empty(t, out)
		assert.Contains(t, err, "ROW-SET REFUSED")
		assert.Contains(t, err, "--redis <addr> is required")
	})
}

func TestNovaTableRowCoverHideShow(t *testing.T) {
	t.Parallel()

	// one positional refused (ROW-HIDE)
	t.Run("hideOnePositional", func(t *testing.T) {
		t.Parallel()
		code, out, err := runTable("row", "hide", "demo")
		assert.EqualValues(t, 2, code)
		assert.Empty(t, out)
		assert.Contains(t, err, "ROW-HIDE REFUSED")
		assert.Contains(t, err, "wants a table and at least one row")
	})

	// one positional refused (ROW-SHOW)
	t.Run("showOnePositional", func(t *testing.T) {
		t.Parallel()
		code, out, err := runTable("row", "show", "demo")
		assert.EqualValues(t, 2, code)
		assert.Empty(t, out)
		assert.Contains(t, err, "ROW-SHOW REFUSED")
		assert.Contains(t, err, "wants a table and at least one row")
	})

	// dry-run plan with two rows
	t.Run("dryRun", func(t *testing.T) {
		t.Parallel()
		code, out, err := runTable("row", "hide", "demo", "build", "release", "--dry-run", "--redis", "")
		assert.EqualValues(t, 0, code)
		assert.Empty(t, err)
		assert.Contains(t, out, "TABLE DRY-RUN verb=row-hide")
		assert.Contains(t, out, ` sends="FCALL ns_table_rows_hide"`)
		assert.Contains(t, out, "redis=- dialled=0 written=0")
	})

	// no-address refusal (hide)
	t.Run("hideNoAddress", func(t *testing.T) {
		t.Parallel()
		code, out, err := runTable("row", "hide", "demo", "build", "--redis", "")
		assert.EqualValues(t, 2, code)
		assert.Empty(t, out)
		assert.Contains(t, err, "ROW-HIDE REFUSED")
		assert.Contains(t, err, "--redis <addr> is required")
	})
}
