package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Card config-list-accepts-max: list --max <n> caps the rows printed while
// reporting the total; a negative or non-integer --max refuses at exit 2.
func TestConfigListMaxFlag(t *testing.T) {
	t.Parallel()
	h := newHarness()
	h.env["NOVA_PG_DSN"] = dsn
	h.machine(t, "m1", "")
	h.machine(t, "m2", "")
	h.machine(t, "m3", "")

	code, out, _ := h.run(t, "machine", "list", "--max", "2")
	require.Equal(t, 0, code, "list --max 2: code %d out %q", code, out)
	assert.Equal(t, 2, strings.Count(out, "MACHINE name="), "list --max 2: out %q, want 2 rows", out)
	assert.Contains(t, out, "rows=3", "list --max 2: out %q, want total rows=3", out)
	assert.Contains(t, out, "shown=2", "list --max 2: out %q, want shown=2", out)

	code, out, _ = h.run(t, "machine", "list", "--max", "2", "--json")
	require.Equal(t, 0, code, "list --max 2 --json: code %d out %q", code, out)
	assert.Contains(t, out, `"rows":3`, "list --max 2 --json: out %q, want total 3", out)
	assert.Contains(t, out, `"shown":2`, "list --max 2 --json: out %q, want shown 2", out)

	code, _, errs := h.run(t, "machine", "list", "--max", "-1")
	assert.Equal(t, 2, code, "list --max -1: code %d, want refusal 2; errs %q", code, errs)
	assert.NotEmpty(t, errs, "list --max -1: want a refusal line")

	code, _, _ = h.run(t, "machine", "list", "--max", "abc")
	assert.Equal(t, 2, code, "list --max abc: code %d, want refusal 2", code)
}
