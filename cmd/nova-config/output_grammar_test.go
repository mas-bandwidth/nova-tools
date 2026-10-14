package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestOutputCarriesTheOKWordAndQuotesFreeText pins the status line and the
// show and list lines (docs/STANDARD.md section 2, "The status word leads
// every line"): CONFIG STATUS OK, FRIEND OK name=, and a note through
// oneline.Quote rather than a \x20 field escape. A non-zero status carries
// FAILED, never OK.
func TestOutputCarriesTheOKWordAndQuotesFreeText(t *testing.T) {
	t.Parallel()

	h := newHarness()
	h.env["NOVA_PG_DSN"] = dsn
	h.env["NOVA_FRIEND"] = "a1"
	h.env["NOVA_SPRINT_REDIS"] = "127.0.0.1:6379"

	code, out, errs := h.run(t, "status")
	require.Equal(t, 0, code, errs)
	require.Contains(t, out, "CONFIG STATUS OK pg=")

	code, _, errs = h.run(t, "friend", "add", "f1", "--tiers", "flash")
	require.Equal(t, 0, code, errs)
	code, out, errs = h.run(t, "friend", "show", "f1")
	require.Equal(t, 0, code, errs)
	require.Contains(t, out, "FRIEND OK name=f1 ")
	code, out, errs = h.run(t, "friend", "list")
	require.Equal(t, 0, code, errs)
	require.Contains(t, out, "FRIEND OK name=f1 ")

	code, _, errs = h.run(t, "machine", "add", "m1", "--user", "u", "--seat", "s", "--slots", "8", "--width", "4", "--note", "held 1:46 PM")
	require.Equal(t, 0, code, errs)
	code, out, errs = h.run(t, "machine", "show", "m1")
	require.Equal(t, 0, code, errs)
	require.Contains(t, out, "MACHINE OK name=m1 ")
	require.Contains(t, out, `note="held 1:46 PM"`)
	require.NotContains(t, out, `\x20`)
	code, out, errs = h.run(t, "machine", "list")
	require.Equal(t, 0, code, errs)
	require.Contains(t, out, "MACHINE OK name=m1 ")
	require.Contains(t, strings.Split(out, "\n")[0], `note="held 1:46 PM"`)
	require.NotContains(t, strings.Split(out, "\n")[0], `\x20`)

	h.store.version = 0
	code, out, errs = h.run(t, "status")
	require.NotEqual(t, 0, code, errs)
	require.Contains(t, out, "CONFIG STATUS FAILED ")
	require.NotContains(t, out, "CONFIG STATUS OK ")
}
