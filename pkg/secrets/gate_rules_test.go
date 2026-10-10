package secrets

import (
	"path/filepath"
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
		wantExtra     string
	}{
		{name: "a rule without the recovery key",
			after:     map[string]string{".sops.yaml": gateSops(gateRule("rowan.yaml", gateSeatKey, gateThirdKey)), "rowan.yaml": gateSealedFile()},
			want:      "GATE FAILED rule=1 check=1 file=.sops.yaml: rule does not contain declared recovery key " + gateRecoveryKey + " (recovery.pub)",
			wantExtra: "is under a rule that does not contain declared recovery key " + gateRecoveryKey + " (recovery.pub)"},
		{name: "a rule naming the recovery key twice, so the seat holds no key",
			after:     map[string]string{".sops.yaml": gateSops(gateRule("rowan.yaml", gateRecoveryKey, gateRecoveryKey)), "rowan.yaml": gateSealedFile()},
			want:      "GATE FAILED rule=1 check=1 file=.sops.yaml: rule has duplicate recipient " + gateRecoveryKey + "; a seat is one seat key and the declared recovery key, distinct",
			wantExtra: "is under a rule that has duplicate recipient " + gateRecoveryKey + "; a seat is one seat key and the declared recovery key, distinct"},
		{name: "a rule naming the seat key twice, so the recovery key is gone",
			after:     map[string]string{".sops.yaml": gateSops(gateRule("rowan.yaml", gateSeatKey, gateSeatKey)), "rowan.yaml": gateSealedFile()},
			want:      "GATE FAILED rule=1 check=1 file=.sops.yaml: rule has duplicate recipient " + gateSeatKey + "; a seat is one seat key and the declared recovery key, distinct",
			wantExtra: "is under a rule that has duplicate recipient " + gateSeatKey + "; a seat is one seat key and the declared recovery key, distinct"},
		{name: "the seat key then the recovery key is approved",
			after: map[string]string{".sops.yaml": gateSops(gateRule("rowan.yaml", gateSeatKey, gateRecoveryKey)), "rowan.yaml": gateSealedFile()},
			want:  "GATE APPROVE files=2 machines=-"},
		{name: "the recovery key then the seat key is approved",
			after: map[string]string{".sops.yaml": gateSops(gateRule("rowan.yaml", gateRecoveryKey, gateSeatKey)), "rowan.yaml": gateSealedFile()},
			want:  "GATE APPROVE files=2 machines=-"},
		{name: "a recovery.pub that declares no key",
			before: map[string]string{"recovery.pub": "not a key\n"},
			after:  map[string]string{".sops.yaml": gateSops(good), "rowan.yaml": gateSealedFile()},
			want:   "GATE FAILED rule=0 check=0 file=recovery.pub: does not declare a single valid age public key"},
		{name: "a path_regex naming no seat file",
			after:     map[string]string{".sops.yaml": gateSops(gateRule("nobody.yaml", gateSeatKey, gateRecoveryKey)), "rowan.yaml": gateSealedFile()},
			want:      "GATE FAILED rule=1 check=1 file=.sops.yaml: path_regex names 0 seat files; expected exactly one",
			wantExtra: "no creation rule in .sops.yaml matches this file"},
		{name: "a path_regex naming two seat files",
			after: map[string]string{".sops.yaml": gateSops("  - path_regex: ^.*\\.yaml$\n    age: " + gateSeatKey + "," + gateRecoveryKey + "\n"), "rowan.yaml": gateSealedFile(), "air.yaml": gateSealedFile()},
			want:  "GATE FAILED rule=1 check=1 file=.sops.yaml: path_regex names 2 seat files; expected exactly one"},
		{name: "a seat file removed",
			before:  map[string]string{".sops.yaml": gateSops(good), "rowan.yaml": gateSealedFile()},
			removed: []string{"rowan.yaml"},
			want:    "GATE FAILED rule=0 check=5 file=rowan.yaml: the seat file is in the store at the base and gone at the head; a seat is never removed here"},
		{name: "a seat file with sealed values and no sops block",
			after: map[string]string{".sops.yaml": gateSops(good), "rowan.yaml": "GH_TOKEN: ENC[AES256_GCM,data:xyz,iv:abc,tag:def,type:str]\n"},
			want:  "GATE FAILED rule=1 check=2 file=rowan.yaml: file is not encrypted (missing sops metadata)"},
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
			switch {
			case strings.HasPrefix(c.want, "GATE APPROVE"):
				wantCode = 0
			case strings.HasPrefix(c.want, "GATE FAILED"):
				wantCode = 1
			}
			assert.Equal(t, wantCode, code, line)
			if c.wantExtra == "" {
				assert.Equal(t, c.want, line)
			} else {
				assert.True(t, strings.HasPrefix(line, c.want), "gate verdict = %q, want primary finding %q", line, c.want)
				assert.Contains(t, line, c.wantExtra, "gate verdict = %q, want all findings", line)
			}
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

// seat inject refuses a seat file sealed to the recovery key twice (a seat that holds no
// key of its own) or to the seat key twice (a file the recovery key cannot open), naming
// the duplicate, and takes the two distinct keys in either order; through the verb, so
// the refusal is in its own grammar and nothing is encrypted or written.
func TestSeatInjectHoldsTheSeatFileToOneSeatKeyAndTheRecoveryKey(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name       string
		recipients []string
		want       string
	}{
		{"recovery key twice", []string{pubRecovery, pubRecovery}, "seat file air.yaml: is under a rule that has duplicate recipient " + pubRecovery + "; a seat is one seat key and the declared recovery key, distinct"},
		{"seat key twice", []string{pubAir, pubAir}, "seat file air.yaml: is under a rule that has duplicate recipient " + pubAir + "; a seat is one seat key and the declared recovery key, distinct"},
		{"seat then recovery", []string{pubAir, pubRecovery}, ""},
		{"recovery then seat", []string{pubRecovery, pubAir}, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			f := newInjectFixture(t)
			mustWrite(t, filepath.Join(f.storeDir, ".sops.yaml"), "creation_rules:\n"+
				"  - path_regex: ^rowan\\.yaml$\n    age: "+pubRowan+","+pubRecovery+"\n"+
				"  - path_regex: ^air\\.yaml$\n    age: "+strings.Join(c.recipients, ",")+"\n", 0o644)
			mustWrite(t, filepath.Join(f.storeDir, "air.yaml"), injectTargetFile(c.recipients, injectSealedBody), 0o644)
			_, err := seatInjectTarget(f.storeDir, "air.yaml", pubRecovery)
			if c.want == "" {
				assert.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), c.want)
			line, runErr := RunSeatInject(f.options("GH_TOKEN", true))
			assert.Empty(t, line)
			require.Error(t, runErr)
			assert.Contains(t, runErr.Error(), c.want)
			assert.Empty(t, readMaybe(t, f.sopsStdin), "a refused inject encrypted something")
		})
	}
}

// seat add writes the new seat's rule as --pub and the recovery key, so it holds that
// rule to the one predicate before anything is written: --pub the recovery key itself
// would be a rule naming the recovery key twice, a seat with no key of its own.
func TestSeatAddRefusesARuleNamingTheRecoveryKeyTwice(t *testing.T) {
	t.Parallel()
	f := newSeatFixture(t)
	before := f.read(t, ".sops.yaml")
	o := f.options(t, "GH_TOKEN")
	o.Pub = pubRecovery
	_, err := RunSeatAdd(o)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "the rule seat add writes for air has duplicate recipient "+pubRecovery+"; a seat is one seat key and the declared recovery key, distinct")
	assert.Equal(t, before, f.read(t, ".sops.yaml"), "a refused seat add touched .sops.yaml")
	assert.NoFileExists(t, filepath.Join(f.storeDir, "air.yaml"))
}

// seat inject encrypts to the file's rule, so it holds the rule and the file's own
// recipients to the same set of keys, both ways: a rule naming the recovery key twice
// beside a file sealed to the seat key and the recovery key is not the file's rule, and
// sealing to it would drop the seat's own key.
func TestSeatInjectRefusesARuleThatIsNotTheFilesOwnRecipients(t *testing.T) {
	t.Parallel()
	f := newInjectFixture(t)
	mustWrite(t, filepath.Join(f.storeDir, ".sops.yaml"), "creation_rules:\n"+
		"  - path_regex: ^rowan\\.yaml$\n    age: "+pubRowan+","+pubRecovery+"\n"+
		"  - path_regex: ^air\\.yaml$\n    age: "+pubRecovery+","+pubRecovery+"\n", 0o644)
	mustWrite(t, filepath.Join(f.storeDir, "air.yaml"), injectTargetFile([]string{pubAir, pubRecovery}, injectSealedBody), 0o644)
	_, err := seatInjectTarget(f.storeDir, "air.yaml", pubRecovery)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "seat file air.yaml: is under a rule that has duplicate recipient "+pubRecovery)
}
