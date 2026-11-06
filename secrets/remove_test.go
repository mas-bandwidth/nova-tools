package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// seat remove takes the rule and both files out in one commit; a seat this node granted to is refused until its ledger is gone.
func TestSeatRemoveTakesTheRuleAndFiles(t *testing.T) {
	t.Parallel()
	f := newFleet(t)
	require.Equal(t, 0, f.main(f.first()...), f.stderr.String())
	f.seed("bo", map[string]string{"BO_OWN": "bo-sealed-three"})
	assert.Equal(t, 2, f.main("seat", "remove", "bo", "--store", f.store, "--pins", f.pins))
	assert.Contains(t, f.stderr.String(), "SECRETS SEAT REMOVE REFUSED reason=other: this node granted bo values ("+f.pins+"/bo.ledger); delete that ledger and the pin "+f.pins+"/bo.pub first")
	assert.FileExists(t, filepath.Join(f.store, "bo.grants.yaml"))
	assert.Equal(t, 2, f.main("seat", "remove", "bo", "--store", f.store), "the pins are not optional")
	assert.Contains(t, f.stderr.String(), "missing --pins <dir> (or NOVA_SECRETS_PINS)")
	f.environ = append(f.environ, "NOVA_SECRETS_SEAT=ada")
	assert.Equal(t, 2, f.main("seat", "remove", "ada", "--store", f.store, "--pins", f.pins), "the granting seat stays while a ledger remains")
	assert.Contains(t, f.stderr.String(), "SECRETS SEAT REMOVE REFUSED reason=other: ada is this node's own seat, the one that granted 1 seat(s) their values")
	require.NoError(t, os.Remove(filepath.Join(f.pins, "bo.ledger")))
	n := len(f.gitCalls())
	require.Equal(t, 0, f.main("seat", "remove", "bo", "--store", f.store, "--pins", f.pins), f.stderr.String())
	assert.Equal(t, "SECRETS SEAT REMOVE OK seat=bo rule=removed files=2 committed; run: git -C "+f.store+" push\n", f.stdout.String())
	assert.NoFileExists(t, filepath.Join(f.store, "bo.yaml"))
	assert.NoFileExists(t, filepath.Join(f.store, "bo.grants.yaml"))
	assert.Equal(t, []string{"ada"}, seatsOf(f.rules()), "ada's rule is kept")
	assert.Len(t, f.gitCalls(), n+2, "one add, one commit")
	assert.Equal(t, 2, f.main("seat", "remove", "bo", "--store", f.store, "--pins", f.pins))
	assert.Contains(t, f.stderr.String(), "SECRETS SEAT REMOVE REFUSED reason=no-seat: no rule and no file for bo")
	assert.Equal(t, 2, f.main("seat", "remove", "ada", "--store", f.store, "--pub", pubB))
	assert.Contains(t, f.stderr.String(), "unknown flag --pub")
	f.noLeak("bus-sealed-one", "bo-sealed-three")
}

func seatsOf(rules []map[string]string) []string {
	var out []string
	for _, r := range rules {
		if m := ruleSeat.FindStringSubmatch(r["path_regex"]); m != nil {
			out = append(out, m[1])
		}
	}
	return out
}
