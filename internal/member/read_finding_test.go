package member

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A broken read names the defect: at least one finding line names a file, a line or the
// rule the work breaks (docs/SPEC-CARD-CONTRACT.md section 3, the read's finding). A
// broken verdict that names none is no verdict: the member hands the read back as "no
// finding" (read --return), so the sprint asks another reader and the coordinator is not
// asked to judge on nothing. A broken read that names one is reported with every line of
// its finding, never only the first (docs/SPEC-SPRINT.md section 6).
func TestABrokenReadNamesItsDefectOrIsHandedBack(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, verb, want string
		res              Result
	}{
		{"request changes and nothing else is handed back", "return",
			"read --as r --return r1 --reason no finding: the broken read names no file, line or rule: Request changes. --epoch 7",
			Result{Ran: true, Verdict: "broken", Report: "Request changes.", Body: "Request changes."}},
		{"an approval's words under a broken verdict are handed back", "return",
			"read --as r --return r1 --reason no finding: the broken read names no file, line or rule: The diff matches the card. / No code or unrelated file changes were found. --epoch 7",
			Result{Ran: true, Verdict: "broken", Report: "The diff matches the card.", Body: "The diff matches the card.\nNo code or unrelated file changes were found."}},
		{"a finding under its first line is reported in full", "report",
			"read --as r --broken r1 --finding Request changes. / internal/ci/testdata/deleted-tests.txt:12 still names the old file; rename it there too. --epoch 7",
			Result{Ran: true, Verdict: "broken", Report: "Request changes.", Body: "Request changes.\n\n## Findings\n\ninternal/ci/testdata/deleted-tests.txt:12 still names the old file; rename it there too."}},
		{"a finding naming the card's step is a finding", "report",
			"read --as r --broken r1 --finding STEP 2 is not done: git grep of the old names prints three hits. --epoch 7",
			Result{Ran: true, Verdict: "broken", Report: "STEP 2 is not done: git grep of the old names prints three hits."}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := newRig(Config{As: "r", Width: 1, Reader: true})
			p := Packet{Card: "r1", Kind: "read", As: "r", Attempt: 1, Epoch: 7}
			g.s.set("queue", 0, queueJSON(t, 7, reading("r1", &p)))
			_, err := g.tick(t)
			require.NoError(t, err)
			g.r.child("r1").end(tc.res)
			g.s.reset()
			_, err = g.tick(t)
			require.NoError(t, err)
			assert.Equal(t, []string{tc.want}, g.s.lines(tc.verb), "%s lines", tc.verb)
		})
	}
}
