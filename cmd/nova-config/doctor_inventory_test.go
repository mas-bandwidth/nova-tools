package main

import (
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/doctor"
	"github.com/stretchr/testify/require"
)

// TestDoctorReadsMachinesFromThePrintedList pins the doctor inventory parse
// against the line this binary prints (docs/SPEC-CONFIG.md, "Lines";
// docs/STANDARD.md section 2, "The status word leads every line"). A parser
// still looking for `MACHINE name=` reads zero machines from that stdout.
func TestDoctorReadsMachinesFromThePrintedList(t *testing.T) {
	t.Parallel()

	h := newHarness()
	h.env["NOVA_PG_DSN"] = dsn
	h.env["NOVA_FRIEND"] = "a1"
	code, _, errs := h.run(t, "machine", "add", "m1", "--user", "u", "--seat", "s", "--slots", "8", "--width", "4")
	require.Equal(t, 0, code, errs)
	code, out, errs := h.run(t, "machine", "list")
	require.Equal(t, 0, code, errs)
	names := doctor.MachineNames(out)
	require.NotEmpty(t, names, "printed list read as zero machines: %q", out)
	require.Equal(t, []string{"m1"}, names)
}
