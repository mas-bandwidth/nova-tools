package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A pinned card's life on the twin, read back: add refuses model lines the deal
// could not read; the pin is dealt with its model, budget and deadline; its take,
// ended by the provider, leaves the card withdrawn for the deal (one ATTEMPT
// line on card <id>, no failed-work judgment); routes shows the pinned model as a row
// of its own, its provider failure counted, and its --json speaks the repository's
// lowercase field names; the member's queue carries its fleet row's width.
func TestAPinnedCardOnTheTwinIsReadBackByCardAndRoutes(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	file := filepath.Join(dir, "sprint.twin")
	run := func(line string) (int, string, string) {
		t.Helper()
		return twinProcess(t, file, line)
	}
	pin := writeBrief(t, "s1: the work (s1) tier: pro\nmodel: x/y\ntokens: 5000\ndeadline: 600")
	bad := filepath.Join(dir, "bad.md")
	require.NoError(t, os.WriteFile(bad, []byte("s1: the work (s1) tier: pro\nmodel: nope\n\nThe task.\n"), 0o644))
	for _, line := range []string{
		"nova-sprint init --readers reader-a,reader-b --members m1:2",
		"nova-sprint add --stream s1 --count 1 --brief-file " + pin,
		"nova-sprint add --stream s4 --count 1",
		"nova-sprint start",
		"nova-sprint tick",
		"nova-sprint tick",
		"nova-sprint take --as m1 --epoch 0",
		"nova-sprint finish --as m1 s1-1.w1@1 --epoch 0 --failed --report 'provider failure: 529' --usage 'wall=3.00s budget=10/5000'",
		// the machine runs: the finish's change of the primary, its cost record with it,
		// waits in the work table's queue for the next tick's pump
		"nova-sprint stop",
	} {
		code, o, e := run(line)
		require.Equal(t, 0, code, "%s\n%s%s", line, o, e)
	}
	code, _, e := run("nova-sprint add --stream s2 --count 1 --brief-file " + bad)
	assert.Equal(t, 2, code)
	assert.Contains(t, e, "model: nope is not <provider>/<model>")

	_, story, _ := run("nova-sprint card s1-1")
	assert.Contains(t, story, "ATTEMPT 1 card=s1-1.w1 gen=2 route=pin model=x/y tier=- member=m1")
	// the take's cost record is the take's own (below): the withdrawn card holds none
	assert.Contains(t, story, "usage=- end=withdrawn")
	assert.Contains(t, story, "ATTEMPT 1 card=s1-1.w1 take=1 route=pin model=x/y member=m1 finished=", "the failed take keeps its own record")
	// the take's wait and run are read on the clock between two verbs of this test: either
	// may be a second on a loaded machine, so the record is pinned around them
	assert.Contains(t, story, "usage=wall=3.00s budget=10/5000 wait=")
	assert.Contains(t, story, "unpriced=no-tokens cost=none end=provider failure: 529")
	assert.Contains(t, story, "COST kind=work card=s1-1.w1 attempt=1 take=1 who=m1 route=pin model=x/y tier=- end=provider-failure input=-", "the failed take is a consumer of the card")
	assert.NotContains(t, story, "came back failed", "a provider failure is never the card's")
	_, plain, _ := run("nova-sprint card s4-1")
	assert.Contains(t, plain, "ATTEMPT 1 card=s4-1.w1 gen=1 route=- model=-", "a card on a twin with no route runs on the member's override")

	_, routes, _ := run("nova-sprint routes")
	assert.Contains(t, routes, "ROUTE pin:x/y model=x/y pinned attempts=1 ok=0 failed=1 provider_failures=1 mean_wall=")
	assert.Contains(t, routes, " rested_until=-\n", "a route the machine does not rest (rule 3)")
	assert.Contains(t, routes, "ROUTES OK routes=1")
	_, js, _ := run("nova-sprint routes --json")
	assert.NotContains(t, js, `"rested_until"`, "only while it rests")
	assert.Contains(t, js, `"route":{"name":"pin:x/y","tier":"","provider":"x","model":"y"`)
	assert.Contains(t, js, `"pinned":true`)
	_, q, _ := run("nova-sprint queue --as m1 --json")
	assert.Contains(t, q, `"width":2`)
}
