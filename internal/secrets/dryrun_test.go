package secrets

import (
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// `--dry-run` on seal, seat inject and place (a cold rater's first fix: "no dry-run on
// seal or place"). Every test here holds the same three facts: the plan the dry run
// prints is the plan the real run then takes, the dry run changed nothing on disk and
// ran no write, and no value reached a line. Fixtures and fakes only; no real store,
// key or secret is read.

// treeOf is every file under dir, path to bytes: the before and after a dry run is
// compared by.
func treeOf(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		if d.IsDir() {
			out[rel+"/"] = ""
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		out[rel] = string(b)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func sameTree(t *testing.T, what string, before, after map[string]string) {
	t.Helper()
	if len(before) != len(after) {
		t.Errorf("%s changed the file set: %d entries before, %d after", what, len(before), len(after))
	}
	for k, v := range before {
		if got, ok := after[k]; !ok || got != v {
			t.Errorf("%s changed %s", what, k)
		}
	}
}

// planField is the value of key= on the first line of lines that carries it.
func planField(t *testing.T, lines, key string) string {
	t.Helper()
	for _, l := range strings.Split(lines, "\n") {
		for _, tok := range strings.Fields(l) {
			if v, ok := strings.CutPrefix(tok, key+"="); ok {
				return v
			}
		}
	}
	t.Fatalf("no %s= on any line of:\n%s", key, lines)
	return ""
}

// quotedField is the Go-quoted value after key= (a commit message or a title).
func quotedField(t *testing.T, lines, key string) string {
	t.Helper()
	_, rest, ok := strings.Cut(lines, key+`="`)
	if !ok {
		t.Fatalf("no %s=\"...\" in:\n%s", key, lines)
	}
	val, _, _ := strings.Cut(rest, `"`)
	return val
}

type untouchable struct{ t *testing.T }

func (u untouchable) Read([]byte) (int, error) {
	u.t.Error("the dry run read the value; a dry run takes no stdin")
	return 0, io.EOF
}

// TestSealDryRunPlansWhatTheRealRunThenTakes: the dry run's branch, commit message, pull
// request title and recipients are the ones the real run then hands to git, gh and the
// store's rule; the dry run itself changed no file and ran no git write, gh call or
// sops encrypt.
func TestSealDryRunPlansWhatTheRealRunThenTakes(t *testing.T) {
	t.Parallel()
	skipPOSIXFakesOnWindows(t)

	f := newSealFixture(t, "OTHER: keepme\nTARGET: oldvalue\n")
	before := treeOf(t, f.storeDir)

	dry := f.options(t, "TARGET", "", false)
	dry.Stdin = untouchable{t}
	dry.DryRun = true
	plan, err := RunSeal(dry)
	if err != nil {
		t.Fatalf("RunSeal --dry-run: %v", err)
	}

	sameTree(t, "seal --dry-run", before, treeOf(t, f.storeDir))
	if strings.Contains(plan, "oldvalue") || strings.Contains(plan, "keepme") {
		t.Errorf("a value reached the plan:\n%s", plan)
	}
	if got := readMaybe(t, f.sopsStdin); got != "" {
		t.Errorf("the dry run encrypted; sops stdin held:\n%s", got)
	}
	for _, arg := range strings.Split(readMaybe(t, f.sopsArgs), "\n") {
		if arg == "-e" {
			t.Errorf("the dry run ran a sops encrypt:\n%s", readMaybe(t, f.sopsArgs))
		}
	}
	for _, write := range []string{"checkout", "add", "commit", "push", "pull"} {
		for _, arg := range strings.Split(readMaybe(t, f.gitArgs), "\n") {
			if arg == write {
				t.Errorf("the dry run ran git %s:\n%s", write, readMaybe(t, f.gitArgs))
			}
		}
	}
	if got := readMaybe(t, f.ghArgs); got != "" {
		t.Errorf("the dry run called gh:\n%s", got)
	}

	want := []string{
		"SECRETS SEAL PLAN write=" + oneLineField(filepath.Join(f.storeDir, "rowan.yaml")) + " action=replace name=TARGET seat=rowan recipients=age1abc",
		"SECRETS SEAL PLAN git store=",
		"SECRETS SEAL PLAN push remote=origin branch=seal/rowan-TARGET-20260917-120000 pr title=",
		"SECRETS SEAL DRY-RUN OK name=TARGET seat=rowan nothing written",
	}
	lines := strings.Split(plan, "\n")
	if len(lines) != len(want) {
		t.Fatalf("plan has %d lines, want %d:\n%s", len(lines), len(want), plan)
	}
	for i, w := range want {
		if !strings.HasPrefix(lines[i], w) {
			t.Errorf("plan line %d:\n got %s\nwant prefix %s", i, lines[i], w)
		}
	}

	// The real run, from the same fixture: what it hands to git and gh is the plan.
	if _, err := RunSeal(f.options(t, "TARGET", "newvalue\n", false)); err != nil {
		t.Fatalf("RunSeal: %v", err)
	}
	git := readMaybe(t, f.gitArgs)
	branch, msg := planField(t, plan, "branch"), quotedField(t, plan, "commit")
	for _, w := range []string{"checkout\n-b\n" + branch + "\n", "commit\n-m\n" + msg + "\n"} {
		if !strings.Contains(git, w) {
			t.Errorf("the real run's git calls do not hold the planned %q:\n%s", w, git)
		}
	}
	gh := readMaybe(t, f.ghArgs)
	if w := "pr\ncreate\n--head\n" + branch + "\n--title\n" + quotedField(t, plan, "title") + "\n"; !strings.Contains(gh, w) {
		t.Errorf("the real run's pull request is not the planned one %q:\n%s", w, gh)
	}
}

func oneLineField(s string) string { return strings.ReplaceAll(s, " ", `\x20`) }

// TestSealDryRunNoPRPlansNoPushAndAddsAName: --no-pr is in the plan as no push and no
// gh; a name the seat does not hold is an add.
func TestSealDryRunNoPRPlansNoPushAndAddsAName(t *testing.T) {
	t.Parallel()
	skipPOSIXFakesOnWindows(t)

	f := newSealFixture(t, "OTHER: keepme\n")
	o := f.options(t, "TARGET", "", true)
	o.DryRun = true
	plan, err := RunSeal(o)
	if err != nil {
		t.Fatalf("RunSeal --dry-run --no-pr: %v", err)
	}
	if !strings.Contains(plan, "action=add name=TARGET") {
		t.Errorf("a new name is not planned as an add:\n%s", plan)
	}
	if !strings.Contains(plan, "PLAN push none (--no-pr): no push, no gh call") || strings.Contains(plan, "pr title=") {
		t.Errorf("--no-pr is not planned as no push and no pull request:\n%s", plan)
	}
}

// TestSealDryRunRefusesWhereTheRealRunRefuses: a store whose rule matches no seat file, a
// store not on a branch, and a bad name refuse at the dry run as they do for real, and
// leave the store as it was.
func TestSealDryRunRefusesWhereTheRealRunRefuses(t *testing.T) {
	t.Parallel()
	skipPOSIXFakesOnWindows(t)

	f := newSealFixture(t, "TARGET: old\n")
	before := treeOf(t, f.storeDir)

	noRule := f.options(t, "TARGET", "", true)
	noRule.DryRun = true
	noRule.AsName = "stranger"
	if _, err := RunSeal(noRule); err == nil || !strings.Contains(err.Error(), "no rule matching stranger.yaml") {
		t.Errorf("a seat with no rule was not refused by name: %v", err)
	}

	badName := f.options(t, "not-a-name", "", true)
	badName.DryRun = true
	if _, err := RunSeal(badName); err == nil || !strings.Contains(err.Error(), "invalid key name") {
		t.Errorf("a bad key name was not refused: %v", err)
	}

	detached := f.options(t, "TARGET", "", true)
	detached.DryRun = true
	detached.GitPath = f.writeScript(t, "git-detached", "if [ \"$1\" = \"rev-parse\" ]; then echo HEAD; fi\nexit 0\n")
	if _, err := RunSeal(detached); err == nil || !strings.Contains(err.Error(), "not on a branch") {
		t.Errorf("a store off a branch was not refused: %v", err)
	}

	sameTree(t, "a refused seal --dry-run", before, treeOf(t, f.storeDir))
}

// TestSeatInjectDryRunPlansWhatTheRealRunThenTakes: the plan names the file, the two
// recipients the target's own metadata holds, the delivered names and the clear values
// it keeps; the real run then uses the planned branch, commit and title. The dry run
// left the store byte for byte, encrypted nothing and ran no git write or gh call.
func TestSeatInjectDryRunPlansWhatTheRealRunThenTakes(t *testing.T) {
	t.Parallel()

	f := newInjectFixture(t)
	mustWrite(t, filepath.Join(f.storeDir, "air.yaml"),
		injectTargetFile([]string{pubAir, pubRecovery}, "SPACE_HOST: space.example\n"+injectSealedBody), 0644)
	before := treeOf(t, f.storeDir)

	dry := f.options("EXTRA_KEY,NOVA_REDIS_BENCH_PASSWORD", false)
	dry.DryRun = true
	plan, err := RunSeatInject(dry)
	if err != nil {
		t.Fatalf("RunSeatInject --dry-run: %v", err)
	}
	sameTree(t, "seat inject --dry-run", before, treeOf(t, f.storeDir))
	assertNoValue(t, "the plan", plan)
	if got := readMaybe(t, f.sopsStdin); got != "" {
		t.Errorf("the dry run encrypted; sops stdin held:\n%s", got)
	}
	if got := readMaybe(t, f.ghArgs); got != "" {
		t.Errorf("the dry run called gh:\n%s", got)
	}
	for _, write := range []string{"checkout", "add", "commit", "push", "pull"} {
		for _, arg := range strings.Split(readMaybe(t, f.gitArgs), "\n") {
			if arg == write {
				t.Errorf("the dry run ran git %s:\n%s", write, readMaybe(t, f.gitArgs))
			}
		}
	}

	if got, want := planField(t, plan, "recipients"), pubAir+","+pubRecovery; got != want {
		t.Errorf("recipients=%s, want the target's own %s", got, want)
	}
	for _, want := range []string{"deliver=EXTRA_KEY,NOVA_REDIS_BENCH_PASSWORD", "keep-clear=SPACE_HOST", "from=rowan",
		"SECRETS SEAT INJECT DRY-RUN OK seat=air from=rowan names=2 nothing written"} {
		if !strings.Contains(plan, want) {
			t.Errorf("plan missing %q:\n%s", want, plan)
		}
	}

	// The real run, same fixture.
	if _, err := RunSeatInject(f.options("EXTRA_KEY,NOVA_REDIS_BENCH_PASSWORD", false)); err != nil {
		t.Fatalf("RunSeatInject: %v", err)
	}
	branch := planField(t, plan, "branch")
	git, gh := readMaybe(t, f.gitArgs), readMaybe(t, f.ghArgs)
	for _, w := range []string{"checkout\n-b\n" + branch + "\n", "commit\n-m\n" + quotedField(t, plan, "commit") + "\n"} {
		if !strings.Contains(git, w) {
			t.Errorf("the real run's git calls do not hold the planned %q:\n%s", w, git)
		}
	}
	if w := "pr\ncreate\n--head\n" + branch + "\n--title\n" + quotedField(t, plan, "title") + "\n"; !strings.Contains(gh, w) {
		t.Errorf("the real run's pull request is not the planned one %q:\n%s", w, gh)
	}
	air := f.read(t, "air.yaml")
	if !strings.Contains(air, "recipient: "+pubAir) || !strings.Contains(air, "recipient: "+pubRecovery) {
		t.Errorf("the real run did not seal to the planned recipients:\n%s", air)
	}
}

// TestSeatInjectDryRunRefusesWhereTheRealRunRefuses: the refusals that precede the
// encrypt (a name the source lacks, a source this key cannot open) are the dry run's too.
func TestSeatInjectDryRunRefusesWhereTheRealRunRefuses(t *testing.T) {
	t.Parallel()

	f := newInjectFixture(t)
	before := treeOf(t, f.storeDir)

	missing := f.options("NOT_IN_THE_SOURCE", true)
	missing.DryRun = true
	if _, err := RunSeatInject(missing); err == nil || !strings.Contains(err.Error(), "NOT_IN_THE_SOURCE") {
		t.Errorf("an --only name the source lacks was not refused by name: %v", err)
	}

	unopenable := f.options("NOVA_REDIS_BENCH_PASSWORD", true)
	unopenable.DryRun = true
	unopenable.KeyPath = f.airKey
	if _, err := RunSeatInject(unopenable); err == nil || !strings.Contains(err.Error(), "cannot be opened") {
		t.Errorf("a source this key cannot open was not refused: %v", err)
	}
	sameTree(t, "a refused seat inject --dry-run", before, treeOf(t, f.storeDir))
}
