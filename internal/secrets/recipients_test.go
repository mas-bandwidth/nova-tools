package secrets

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A seat's recipients are one seat key and the declared recovery key, distinct, in its
// rule and in its file's sops metadata alike, and the file's are its rule's. check, the
// store's gate and seat inject each judge a rule and a seat file sealed under it; this
// runs all three over every rule shape crossed with every file shape (eleven by six) and
// holds them to one verdict: accepted exactly when the rule is clean (two valid age keys,
// distinct, one the recovery key), the file is clean and the two name the same keys.
func TestCheckTheGateAndSeatInjectAgreeOnEveryRuleAndFile(t *testing.T) {
	t.Parallel()
	seat, rec, other := gateSeatKey, gateRecoveryKey, gateThirdKey
	shapes := map[string][]string{
		"seat,recovery":          {seat, rec},
		"recovery,seat":          {rec, seat},
		"recovery,recovery":      {rec, rec},
		"seat,seat":              {seat, seat},
		"seat,other":             {seat, other},
		"recovery":               {rec},
		"seat":                   {seat},
		"seat,other,recovery":    {seat, other, rec},
		"seat,recovery,seat":     {seat, rec, seat},
		"seat,recovery,recovery": {seat, rec, rec},
		"notakey,recovery":       {"age1notakey", rec},
	}
	clean := func(xs []string) bool {
		return len(xs) == 2 && xs[0] != xs[1] && slices.Contains(xs, rec) && IsValidAgePublicKey(xs[0]) && IsValidAgePublicKey(xs[1])
	}
	sameSet := func(a, b []string) bool {
		for _, x := range a {
			if !slices.Contains(b, x) {
				return false
			}
		}
		for _, x := range b {
			if !slices.Contains(a, x) {
				return false
			}
		}
		return true
	}
	fileShapes := []string{"seat,recovery", "recovery,seat", "seat,recovery,recovery", "seat,other", "seat,seat", "notakey,recovery"}
	for ruleName, rule := range shapes {
		for _, fileName := range fileShapes {
			file := shapes[fileName]
			t.Run("rule "+ruleName+" file "+fileName, func(t *testing.T) {
				t.Parallel()
				want := clean(rule) && clean(file) && sameSet(rule, file)
				sops := gateSops("  - path_regex: ^rowan\\.yaml$\n    age: " + strings.Join(rule, ",") + "\n")
				sealed := injectTargetFile(file, injectSealedBody)

				// check: the rule (invariant 1) and the file against it (invariant 2)
				store := storeOf(t, map[string]string{".sops.yaml": sops, "rowan.yaml": sealed})
				cfg := &SopsConfig{CreationRules: []CreationRule{{PathRegex: `^rowan\.yaml$`, Recipients: rule}}}
				checkFails := append(CheckInvariant1(store, cfg, rec), CheckInvariant2(store, cfg, []string{"rowan.yaml"})...)
				assert.Equal(t, want, len(checkFails) == 0, "check: %+v", checkFails)

				// the gate, over a pull request that adds the rule and the file
				dir := gateStart(t)
				base := strings.TrimSpace(gateGit(t, dir, "rev-parse", "HEAD"))
				head := gateCommit(t, dir, map[string]string{".sops.yaml": sops, "rowan.yaml": sealed})
				line, code := RunGate(GateInput{StoreDir: dir, Base: base, Head: head})
				assert.Equal(t, want, code == 0, "gate: %s", line)

				// seat inject, on the same store
				_, injectErr := seatInjectTarget(store, "rowan.yaml", rec)
				assert.Equal(t, want, injectErr == nil, "seat inject: %v", injectErr)
			})
		}
	}
}

// seat inject holds the rule to the one judgement as well as the file, so a file sealed
// cleanly under a rule check and the gate refuse is refused here too, and a file with a
// duplicate under a clean rule is refused; either order of the two keys is accepted.
func TestSeatInjectHoldsTheRuleAndTheFileToOneSeatKeyAndTheRecoveryKey(t *testing.T) {
	t.Parallel()
	air, rec := pubAir, pubRecovery
	for _, c := range []struct {
		name       string
		rule, file []string
		want       string
	}{
		{"a clean file under a rule naming the seat key twice", []string{air, rec, air}, []string{air, rec}, "is under a rule that has 3 recipients"},
		{"a clean file under a rule naming the recovery key twice", []string{air, rec, rec}, []string{air, rec}, "is under a rule that has 3 recipients"},
		{"a file with a duplicate under a clean rule", []string{air, rec}, []string{air, rec, rec}, "recipients differ from .sops.yaml"},
		{"seat then recovery", []string{air, rec}, []string{air, rec}, ""},
		{"recovery then seat, the rule in the other order", []string{air, rec}, []string{rec, air}, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			f := newInjectFixture(t)
			mustWrite(t, filepath.Join(f.storeDir, ".sops.yaml"), "creation_rules:\n"+
				"  - path_regex: ^rowan\\.yaml$\n    age: "+pubRowan+","+rec+"\n"+
				"  - path_regex: ^air\\.yaml$\n    age: "+strings.Join(c.rule, ",")+"\n", 0o644)
			mustWrite(t, filepath.Join(f.storeDir, "air.yaml"), injectTargetFile(c.file, injectSealedBody), 0o644)
			_, err := seatInjectTarget(f.storeDir, "air.yaml", rec)
			if c.want == "" {
				assert.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), fmt.Sprintf("seat file air.yaml: %s", c.want))
		})
	}
}

// The rows the review asked for, through the verb: a clean file under a rule that names a
// key twice is refused before anything is decrypted, encrypted or committed, on the dry
// run and the real run alike, with the store's files byte for byte as they were.
func TestSeatInjectRefusesABadRuleBeforeItTouchesAnything(t *testing.T) {
	t.Parallel()
	for name, rule := range map[string][]string{"seat key twice": {pubAir, pubRecovery, pubAir}, "recovery key twice": {pubAir, pubRecovery, pubRecovery}} {
		for _, dryRun := range []bool{true, false} {
			t.Run(fmt.Sprintf("rule with the %s, dry run %v", name, dryRun), func(t *testing.T) {
				t.Parallel()
				f := newInjectFixture(t)
				sops := "creation_rules:\n" +
					"  - path_regex: ^rowan\\.yaml$\n    age: " + pubRowan + "," + pubRecovery + "\n" +
					"  - path_regex: ^air\\.yaml$\n    age: " + strings.Join(rule, ",") + "\n"
				mustWrite(t, filepath.Join(f.storeDir, ".sops.yaml"), sops, 0o644)
				seatFile := injectTargetFile([]string{pubAir, pubRecovery}, injectSealedBody)
				mustWrite(t, filepath.Join(f.storeDir, "air.yaml"), seatFile, 0o644)
				o := f.options("GH_TOKEN", true)
				o.DryRun = dryRun
				line, err := RunSeatInject(o)
				assert.Empty(t, line)
				require.Error(t, err)
				assert.Contains(t, err.Error(), "seat file air.yaml: is under a rule that has 3 recipients")
				assert.Empty(t, readMaybe(t, f.sopsStdin), "a refused inject encrypted something")
				assert.Empty(t, readMaybe(t, f.gitArgs), "a refused inject ran git")
				assert.Empty(t, readMaybe(t, f.ghArgs), "a refused inject ran gh")
				assert.Equal(t, sops, f.read(t, ".sops.yaml"))
				assert.Equal(t, seatFile, f.read(t, "air.yaml"))
			})
		}
	}
}
