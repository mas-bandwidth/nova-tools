package secrets

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Every refusal of the store's gate, each pinned by the reason it gives, so a gate that
// stops refusing one shape for that shape's own reason is red even where a later check
// would still have refused the pull request for another.
func TestGateRefusesEachShapeForItsOwnReason(t *testing.T) {
	t.Parallel()
	good := gateRule("rowan.yaml", gateSeatKey, gateRecoveryKey)
	cases := []struct {
		name string
		// before is committed first and is the base; after is the pull request's head.
		before, after map[string]string
		removed       []string
		want          string
	}{
		{name: "a rule without the recovery key",
			after: map[string]string{".sops.yaml": gateSops(gateRule("rowan.yaml", gateSeatKey, gateThirdKey)), "rowan.yaml": gateSealedFile()},
			want:  "GATE REFUSE rule=1 file=.sops.yaml: rule does not contain declared recovery key " + gateRecoveryKey},
		{name: "a rule naming the recovery key twice, so the seat holds no key",
			after: map[string]string{".sops.yaml": gateSops(gateRule("rowan.yaml", gateRecoveryKey, gateRecoveryKey)), "rowan.yaml": gateSealedFile()},
			want:  "GATE REFUSE rule=1 file=.sops.yaml: rule has duplicate recipient " + gateRecoveryKey + "; a rule names one seat key and the declared recovery key, distinct"},
		{name: "a rule naming the seat key twice, so the recovery key is gone",
			after: map[string]string{".sops.yaml": gateSops(gateRule("rowan.yaml", gateSeatKey, gateSeatKey)), "rowan.yaml": gateSealedFile()},
			want:  "GATE REFUSE rule=1 file=.sops.yaml: rule has duplicate recipient " + gateSeatKey + "; a rule names one seat key and the declared recovery key, distinct"},
		{name: "the seat key then the recovery key is approved",
			after: map[string]string{".sops.yaml": gateSops(gateRule("rowan.yaml", gateSeatKey, gateRecoveryKey)), "rowan.yaml": gateSealedFile()},
			want:  "GATE APPROVE files=2 machines=-"},
		{name: "the recovery key then the seat key is approved",
			after: map[string]string{".sops.yaml": gateSops(gateRule("rowan.yaml", gateRecoveryKey, gateSeatKey)), "rowan.yaml": gateSealedFile()},
			want:  "GATE APPROVE files=2 machines=-"},
		{name: "a recovery.pub that declares no key",
			before: map[string]string{"recovery.pub": "not a key\n"},
			after:  map[string]string{".sops.yaml": gateSops(good), "rowan.yaml": gateSealedFile()},
			want:   "GATE REFUSE rule=0 file=recovery.pub: does not declare a single valid age public key"},
		{name: "a path_regex naming no seat file",
			after: map[string]string{".sops.yaml": gateSops(gateRule("nobody.yaml", gateSeatKey, gateRecoveryKey)), "rowan.yaml": gateSealedFile()},
			want:  "GATE REFUSE rule=1 file=.sops.yaml: path_regex names 0 seat files; expected exactly one"},
		{name: "a path_regex naming two seat files",
			after: map[string]string{".sops.yaml": gateSops("  - path_regex: ^.*\\.yaml$\n    age: " + gateSeatKey + "," + gateRecoveryKey + "\n"), "rowan.yaml": gateSealedFile(), "air.yaml": gateSealedFile()},
			want:  "GATE REFUSE rule=1 file=.sops.yaml: path_regex names 2 seat files; expected exactly one"},
		{name: "a seat file removed",
			before:  map[string]string{".sops.yaml": gateSops(good), "rowan.yaml": gateSealedFile()},
			removed: []string{"rowan.yaml"},
			want:    "GATE REFUSE rule=0 file=rowan.yaml: the seat file is in the store at the base and gone at the head; a seat is never removed here"},
		{name: "a seat file with sealed values and no sops block",
			after: map[string]string{".sops.yaml": gateSops(good), "rowan.yaml": "GH_TOKEN: ENC[AES256_GCM,data:xyz,iv:abc,tag:def,type:str]\n"},
			want:  "GATE REFUSE rule=1 file=rowan.yaml: file is not encrypted (missing sops metadata)"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			dir := gateStart(t)
			if c.before != nil {
				gateCommit(t, dir, c.before)
			}
			base := strings.TrimSpace(gateGit(t, dir, "rev-parse", "HEAD"))
			head := base
			if c.after != nil {
				head = gateCommit(t, dir, c.after)
			}
			if c.removed != nil {
				head = gateRemove(t, dir, c.removed...)
			}
			line, code := RunGate(GateInput{StoreDir: dir, Base: base, Head: head})
			wantCode := 2
			if strings.HasPrefix(c.want, "GATE APPROVE") {
				wantCode = 0
			}
			assert.Equal(t, wantCode, code, line)
			assert.Equal(t, c.want, line)
		})
	}
}

// The second wall: whatever reaches gateResolveCommit, a ref shaped like an option is
// refused there too, before git is asked, so RunGate's own check is not the only one.
func TestGateResolveCommitRefusesAnOptionShapedRef(t *testing.T) {
	t.Parallel()
	dir := gateStart(t)
	for _, ref := range []string{"--output=/tmp/x", "-R", "--"} {
		_, err := gateResolveCommit(dir, "--head", ref)
		require.Error(t, err, ref)
		assert.Contains(t, err.Error(), `begins with "-", the shape of an option, not a git ref`, ref)
	}
	sha, err := gateResolveCommit(dir, "--head", "HEAD")
	require.NoError(t, err)
	assert.Len(t, sha, 40)
}

// The first wall: both refs are held to their shape before git is asked anything, so an
// option-shaped --head is refused as one even where --base would fail git first (here, a
// directory that is no repository).
func TestGateRefusesAnOptionShapedRefBeforeAskingGit(t *testing.T) {
	t.Parallel()
	line, code := RunGate(GateInput{StoreDir: t.TempDir(), Base: "HEAD", Head: "-R"})
	assert.Equal(t, 2, code, line)
	assert.Contains(t, line, `SECRETS GATE REFUSED: --head -R begins with "-", the shape of an option, not a git ref`)
}

// check and the gate judge a rule's recipients with one predicate, so they give the
// same verdict on every shape: approved exactly when the rule names one seat key and
// the declared recovery key, distinct, in either order.
func TestCheckAndTheGateGiveOneVerdictOnARulesRecipients(t *testing.T) {
	t.Parallel()
	seat, rec, other := gateSeatKey, gateRecoveryKey, gateThirdKey
	for _, c := range []struct {
		name       string
		recipients []string
		ok         bool
	}{
		{"seat then recovery", []string{seat, rec}, true},
		{"recovery then seat", []string{rec, seat}, true},
		{"recovery twice", []string{rec, rec}, false},
		{"seat twice", []string{seat, seat}, false},
		{"seat and a stranger", []string{seat, other}, false},
		{"recovery alone", []string{rec}, false},
		{"seat alone", []string{seat}, false},
		{"three distinct", []string{seat, other, rec}, false},
		{"seat, recovery, seat", []string{seat, rec, seat}, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			rule := CreationRule{PathRegex: `^rowan\.yaml$`, Recipients: c.recipients}
			checkFails := CheckInvariant1("store", &SopsConfig{CreationRules: []CreationRule{rule}}, rec)
			assert.Equal(t, c.ok, len(checkFails) == 0, "check: %+v", checkFails)

			dir := gateStart(t)
			base := strings.TrimSpace(gateGit(t, dir, "rev-parse", "HEAD"))
			head := gateCommit(t, dir, map[string]string{
				".sops.yaml": gateSops("  - path_regex: ^rowan\\.yaml$\n    age: " + strings.Join(c.recipients, ",") + "\n"),
				"rowan.yaml": gateSealedFile(),
			})
			line, code := RunGate(GateInput{StoreDir: dir, Base: base, Head: head})
			assert.Equal(t, c.ok, code == 0, "gate: %s", line)
			if !c.ok && len(checkFails) == 1 {
				// one predicate, one reason: the gate's refusal carries check's words
				reason := strings.TrimPrefix(checkFails[0].Reason, `rule for ^rowan\.yaml$ `)
				assert.Equal(t, "GATE REFUSE rule=1 file=.sops.yaml: rule "+reason, line)
			}
		})
	}
}
