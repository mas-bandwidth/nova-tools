package secrets

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/testbin"
)

// The store's move to hetzner, 2026-09-27. Every bench seat that already existed
// needed the store's new NOVA_REDIS_BENCH_PASSWORD, sealed that night into the
// coordinator's seat. `seal` runs only where the target's key lives; `seat add`
// refuses a seat file that exists. `seat inject` is the pipe between the two: the
// named values out of a seat this machine can open, into an existing seat's file,
// encrypted to that file's own recipients, carried on seal's road.

// injectFixture is a store with two seats -- rowan, the source this machine opens,
// and air, the target it cannot -- and fake sops, git and gh.
type injectFixture struct {
	*seatFixture
	gitPath string
	ghPath  string
	gitArgs string
	ghArgs  string
}

// injectTargetFile is what the fake sops would have written for air: the target's
// recipients in its metadata, and sealed values this machine's key cannot open.
func injectTargetFile(recipients []string, body string) string {
	var b strings.Builder
	b.WriteString(body)
	b.WriteString("sops:\n    age:\n")
	for _, r := range recipients {
		b.WriteString("        - recipient: " + r + "\n")
	}
	return b.String()
}

const injectSealedBody = "GH_TOKEN: ENC[AES256_GCM,data:oldtoken,iv:a,tag:b,type:str]\n" +
	"NOVA_REDIS_BENCH_PASSWORD: ENC[AES256_GCM,data:oldpassword,iv:a,tag:b,type:str]\n"

func newInjectFixture(t *testing.T) *injectFixture {
	t.Helper()
	f := &injectFixture{seatFixture: newSeatFixture(t)}
	mustWrite(t, filepath.Join(f.storeDir, ".sops.yaml"),
		"creation_rules:\n  - path_regex: ^rowan\\.yaml$\n    age: "+pubRowan+","+pubRecovery+
			"\n  - path_regex: ^air\\.yaml$\n    unencrypted_regex: ^(SPACE_HOST)$\n    age: "+pubAir+","+pubRecovery+"\n", 0644)
	// The source holds the new values; LEFT_BEHIND is the name nobody asked for.
	mustWrite(t, filepath.Join(f.storeDir, "rowan.yaml"),
		fakeCipher([]string{pubRowan, pubRecovery},
			"GH_TOKEN: ghp_current\nNOVA_REDIS_BENCH_PASSWORD: redis_new\nEXTRA_KEY: extra_new\nLEFT_BEHIND: nope\n"), 0644)
	mustWrite(t, filepath.Join(f.storeDir, "air.yaml"),
		injectTargetFile([]string{pubAir, pubRecovery}, injectSealedBody), 0644)

	f.gitArgs = filepath.Join(f.dir, "git.args")
	f.ghArgs = filepath.Join(f.dir, "gh.args")
	f.gitPath = f.writeScript(t, "git",
		"printf '%s\\n' \"$@\" >> \""+f.gitArgs+"\"\n"+
			"if [ \"$1\" = \"rev-parse\" ] && [ \"$2\" = \"--abbrev-ref\" ]; then echo \"main\"; fi\n"+
			"if [ \"$1\" = \"rev-parse\" ] && [ \"$2\" = \"HEAD\" ]; then echo \"1111111111111111111111111111111111111111\"; fi\n"+
			"exit 0\n")
	f.ghPath = f.writeScript(t, "gh",
		"printf '%s\\n' \"$@\" >> \""+f.ghArgs+"\"\n"+
			"if [ \"$1 $2\" = \"pr create\" ]; then echo \"https://example.com/mas-bandwidth/secrets/pull/42\"; fi\n"+
			"if [ \"$1 $2\" = \"pr view\" ]; then echo \"APPROVED\"; fi\n"+
			"exit 0\n")
	return f
}

func (f *injectFixture) writeScript(t *testing.T, name, body string) string {
	t.Helper()
	p := filepath.Join(f.dir, name)
	require.NoError(t, testbin.WriteExecutable(p, []byte("#!/bin/sh\n"+body), 0755))
	return p
}

func (f *injectFixture) options(only string, noPR bool) SeatInjectOptions {
	return SeatInjectOptions{
		StoreDir: f.storeDir,
		AsName:   "air",
		From:     "rowan",
		Only:     only,
		KeyPath:  f.rowanKey,
		SopsPath: f.sopsPath,
		GitPath:  f.gitPath,
		GHPath:   f.ghPath,
		NoPR:     noPR,
		Now:      func() time.Time { return time.Date(2026, 9, 27, 1, 30, 0, 0, time.UTC) },
		Check:    func(storeDir, asName, keyPath, sopsPath string) error { return nil },
		Gate:     func(GateInput) (string, int) { return "GATE APPROVE files=0 machines=-", 0 },
	}
}

// injectSecrets are every value the fixture holds; none may reach a line or argv.
var injectSecrets = []string{"ghp_current", "redis_new", "extra_new", "nope", "oldtoken", "oldpassword"}

func assertNoValue(t *testing.T, what, text string) {
	t.Helper()
	for _, s := range injectSecrets {
		assert.NotContains(t, text, s, "a value reached %s:\n%s", what, text)
	}
}

// TestSeatInjectReSealsNamedValuesIntoAnExistingSeat is the night of the move: the
// password the coordinator's seat holds goes into the bench seat's file, the file's
// other name stays (re-sealed from the source), the recipients are the file's own,
// and the change is committed on a seal branch.
func TestSeatInjectReSealsNamedValuesIntoAnExistingSeat(t *testing.T) {
	t.Parallel()

	f := newInjectFixture(t)
	cfgBefore := f.read(t, ".sops.yaml")
	line, err := RunSeatInject(f.options("NOVA_REDIS_BENCH_PASSWORD", true))
	require.NoError(t, err, "RunSeatInject: %v", err)

	stdin := readMaybe(t, f.sopsStdin)
	for _, want := range []string{"NOVA_REDIS_BENCH_PASSWORD: 'redis_new'", "GH_TOKEN: 'ghp_current'"} {
		assert.Contains(t, stdin, want, "encrypt stdin missing %q:\n%s", want, stdin)
	}
	for _, no := range []string{"LEFT_BEHIND", "EXTRA_KEY", "oldtoken", "oldpassword"} {
		assert.NotContains(t, stdin, no, "encrypt stdin carries %q, which nobody asked for or which this key cannot read:\n%s", no, stdin)
	}
	n := strings.Count(stdin, "NOVA_REDIS_BENCH_PASSWORD:")
	assert.Equal(t, 1, n, "NOVA_REDIS_BENCH_PASSWORD appears %d times, want 1:\n%s", n, stdin)

	// Encrypted to the target's own recipients: the fake picks them out of the rule,
	// which the verb held equal to the file's metadata before encrypting.
	air := f.read(t, "air.yaml")
	assert.Contains(t, air, "recipient: "+pubAir, "air.yaml is not sealed to the seat and the recovery key:\n%s", air)
	assert.Contains(t, air, "recipient: "+pubRecovery, "air.yaml is not sealed to the seat and the recovery key:\n%s", air)
	assert.NotContains(t, air, "recipient: "+pubRowan, "air.yaml became readable by the source seat:\n%s", air)
	// The grant is not this verb's to change: .sops.yaml is the same rules with the same
	// recipients, and the only edit is the mark admitted to air.yaml's rule, which predates it.
	got := f.read(t, ".sops.yaml")
	wantCfg, changed, err := seatMarkRule([]byte(cfgBefore), "air.yaml")
	require.NoError(t, err)
	require.True(t, changed, "the fixture's rule already admits the mark")
	assert.Equal(t, string(wantCfg), got, "inject edited .sops.yaml beyond admitting the mark; the grant is not this verb's to change:\n%s", got)

	git := readMaybe(t, f.gitArgs)
	for _, want := range []string{"checkout\n-b\nseal/air-NOVA_REDIS_BENCH_PASSWORD-20260927-013000", "add\nair.yaml", "commit\n-m\ninject NOVA_REDIS_BENCH_PASSWORD into air.yaml from rowan", "checkout\n-f\nmain"} {
		assert.Contains(t, git, want, "git calls missing %q:\n%s", want, git)
	}
	if strings.Contains(git, "push") || readMaybe(t, f.ghArgs) != "" {
		assert.Fail(t, fmt.Sprintf("--no-pr pushed or called gh:\ngit:\n%s\ngh:\n%s", git, readMaybe(t, f.ghArgs)))
	}
	want := "SECRETS SEAT INJECT OK seat=air from=rowan names=1 committed branch=seal/air-NOVA_REDIS_BENCH_PASSWORD-20260927-013000"
	assert.Equal(t, want, line, "OK line:\n got %s\nwant %s", line, want)
	assertNoValue(t, "the OK line", line)
	assertNoValue(t, "sops argv", readMaybe(t, f.sopsArgs))
	assertNoValue(t, "git argv", git)
}

// TestSeatInjectAddsANewNameAndKeepsTheClearOnes: a name the target never held is
// added after the names it had; a value the rule permits in the clear is kept byte
// for byte from the target, and two --only names come out in one file, sorted.
func TestSeatInjectAddsANewNameAndKeepsTheClearOnes(t *testing.T) {
	t.Parallel()

	f := newInjectFixture(t)
	mustWrite(t, filepath.Join(f.storeDir, "air.yaml"),
		injectTargetFile([]string{pubAir, pubRecovery}, "SPACE_HOST: space.example\n"+injectSealedBody), 0644)
	line, err := RunSeatInject(f.options("EXTRA_KEY,NOVA_REDIS_BENCH_PASSWORD", true))
	require.NoError(t, err, "RunSeatInject: %v", err)
	stdin := readMaybe(t, f.sopsStdin)
	want := "SPACE_HOST: space.example\nGH_TOKEN: 'ghp_current'\nNOVA_REDIS_BENCH_PASSWORD: 'redis_new'\nEXTRA_KEY: 'extra_new'\nNOVA_SECRETS_WRITTEN_BY: seat inject dev\n"
	assert.Equal(t, want, stdin, "encrypt stdin:\n got %q\nwant %q", stdin, want)
	assert.Contains(t, line, "names=2", "OK line does not count both names or name the branch by them: %s", line)
	assert.Contains(t, line, "branch=seal/air-EXTRA_KEY+NOVA_REDIS_BENCH_PASSWORD-", "OK line does not count both names or name the branch by them: %s", line)
}

// TestSeatInjectRefusalsTouchNothing: every refusal names its door, leaves the store
// byte for byte, runs no git, and carries no value.
func TestSeatInjectRefusalsTouchNothing(t *testing.T) {
	t.Parallel()

	thirdKey := pubStranger
	for _, tc := range []struct {
		name string
		fix  func(t *testing.T, f *injectFixture, o *SeatInjectOptions)
		want []string
	}{
		{"absent target file", func(t *testing.T, f *injectFixture, o *SeatInjectOptions) {
			o.AsName = "mini"
		}, []string{"mini.yaml", "seat add"}},
		{"source this key cannot open", func(t *testing.T, f *injectFixture, o *SeatInjectOptions) {
			o.KeyPath = f.airKey
		}, []string{"rowan", "cannot be opened"}},
		{"a name the source does not carry", func(t *testing.T, f *injectFixture, o *SeatInjectOptions) {
			o.Only = "NOVA_REDIS_BENCH_PASSWORD,ABSENT_KEY"
		}, []string{"ABSENT_KEY", "nova-secrets names"}},
		{"a name outside the shape", func(t *testing.T, f *injectFixture, o *SeatInjectOptions) {
			o.Only = "redis_password"
		}, []string{"redis_password", "[A-Z][A-Z0-9_]*"}},
		{"--from is --as", func(t *testing.T, f *injectFixture, o *SeatInjectOptions) {
			o.From = "air"
		}, []string{"--from", "seal"}},
		{"target holds a sealed name the source lacks", func(t *testing.T, f *injectFixture, o *SeatInjectOptions) {
			mustWrite(t, filepath.Join(f.storeDir, "air.yaml"),
				injectTargetFile([]string{pubAir, pubRecovery}, injectSealedBody+"BENCH_ONLY: ENC[AES256_GCM,data:x,type:str]\n"), 0644)
		}, []string{"BENCH_ONLY", "nova-secrets seal"}},
		{"target without sops metadata", func(t *testing.T, f *injectFixture, o *SeatInjectOptions) {
			mustWrite(t, filepath.Join(f.storeDir, "air.yaml"), injectSealedBody, 0644)
		}, []string{"air.yaml", "sops metadata"}},
		{"target with a third recipient", func(t *testing.T, f *injectFixture, o *SeatInjectOptions) {
			mustWrite(t, filepath.Join(f.storeDir, "air.yaml"),
				injectTargetFile([]string{pubAir, pubRecovery, thirdKey}, injectSealedBody), 0644)
		}, []string{"air.yaml", "recipients differ from .sops.yaml", "sops updatekeys air.yaml"}},
		{"target without the recovery key", func(t *testing.T, f *injectFixture, o *SeatInjectOptions) {
			mustWrite(t, filepath.Join(f.storeDir, "air.yaml"),
				injectTargetFile([]string{pubAir, thirdKey}, injectSealedBody), 0644)
		}, []string{"air.yaml", "recipients differ from .sops.yaml", "sops updatekeys air.yaml"}},
		{"rule and metadata disagree", func(t *testing.T, f *injectFixture, o *SeatInjectOptions) {
			mustWrite(t, filepath.Join(f.storeDir, "air.yaml"),
				injectTargetFile([]string{thirdKey, pubRecovery}, injectSealedBody), 0644)
		}, []string{"air.yaml", "updatekeys"}},
		{"target holds a plain value", func(t *testing.T, f *injectFixture, o *SeatInjectOptions) {
			mustWrite(t, filepath.Join(f.storeDir, "air.yaml"),
				injectTargetFile([]string{pubAir, pubRecovery}, injectSealedBody+"PLAIN_KEY: sk-inthclear\n"), 0644)
		}, []string{"PLAIN_KEY", "plain value"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newInjectFixture(t)
			opts := f.options("NOVA_REDIS_BENCH_PASSWORD", true)
			tc.fix(t, f, &opts)
			airBefore, cfgBefore := f.read(t, "air.yaml"), f.read(t, ".sops.yaml")
			line, err := RunSeatInject(opts)
			require.Error(t, err, "accepted: %s", line)
			for _, w := range tc.want {
				assert.Contains(t, err.Error(), w, "refusal does not say %q: %v", w, err)
			}
			assertNoValue(t, "the refusal", err.Error())
			assert.Equal(t, airBefore, f.read(t, "air.yaml"), "a refused run changed the store")
			assert.Equal(t, cfgBefore, f.read(t, ".sops.yaml"), "a refused run changed the store")
			got := readMaybe(t, f.gitArgs)
			assert.Empty(t, got, "a refused run ran git:\n%s", got)
		})
	}
}

// TestSeatInjectRefusesAnUnusableInvocation costs one refusal per shape.
func TestSeatInjectRefusesAnUnusableInvocation(t *testing.T) {
	t.Parallel()

	f := newInjectFixture(t)
	for _, tc := range []struct {
		name string
		fix  func(o *SeatInjectOptions)
		want string
	}{
		{"no --store", func(o *SeatInjectOptions) { o.StoreDir = "" }, "--store"},
		{"no --as", func(o *SeatInjectOptions) { o.AsName = "" }, "--as"},
		{"no --from", func(o *SeatInjectOptions) { o.From = "" }, "--from"},
		{"no --only", func(o *SeatInjectOptions) { o.Only = "" }, "--only"},
		{"no --key", func(o *SeatInjectOptions) { o.KeyPath = "" }, "--key"},
		{"no --sops", func(o *SeatInjectOptions) { o.SopsPath = "" }, "--sops"},
	} {
		opts := f.options("NOVA_REDIS_BENCH_PASSWORD", true)
		tc.fix(&opts)
		_, err := RunSeatInject(opts)
		if !assert.Error(t, err, "%s: accepted", tc.name) {
			continue
		}
		assert.Contains(t, err.Error(), tc.want, "%s: refusal does not name %q: %v", tc.name, tc.want, err)
	}
}

// TestSeatInjectWalksSealsRoadToTheMerge: without --no-pr the change is pushed, the
// pull request opened with a body the gate's reader can place, the approval waited
// for, the request merged and the store pulled -- seal's road, so the gate sees one
// shape from both verbs.
func TestSeatInjectWalksSealsRoadToTheMerge(t *testing.T) {
	t.Parallel()

	f := newInjectFixture(t)
	opts := f.options("NOVA_REDIS_BENCH_PASSWORD", false)
	var progress strings.Builder
	opts.Progress = &progress
	line, err := RunSeatInject(opts)
	require.NoError(t, err, "RunSeatInject: %v", err)
	gh := readMaybe(t, f.ghArgs)
	for _, want := range []string{"pr\ncreate\n--head\nseal/air-NOVA_REDIS_BENCH_PASSWORD-20260927-013000", "inject NOVA_REDIS_BENCH_PASSWORD into air.yaml from rowan",
		"nova-secrets seat inject from rowan", "pr\nview\n42", "pr\nmerge\n42\n--squash"} {
		assert.Contains(t, gh, want, "gh calls missing %q:\n%s", want, gh)
	}
	assertNoValue(t, "gh argv", gh)
	git := strings.ReplaceAll(readMaybe(t, f.gitArgs), "\n", " ")
	back, pull := strings.Index(git, "checkout -f"), strings.LastIndex(git, "pull")
	assert.Contains(t, git, "push -u origin seal/air-", "after the merge git must push, checkout -f the starting branch and then pull; got: %s", git)
	assert.GreaterOrEqual(t, back, 0, "after the merge git must push, checkout -f the starting branch and then pull; got: %s", git)
	assert.GreaterOrEqual(t, pull, 0, "after the merge git must push, checkout -f the starting branch and then pull; got: %s", git)
	assert.LessOrEqual(t, back, pull, "after the merge git must push, checkout -f the starting branch and then pull; got: %s", git)
	want := "SECRETS SEAT INJECT OK seat=air from=rowan names=1 pr=#42 merged"
	assert.Equal(t, want, line, "OK line:\n got %s\nwant %s", line, want)
	got := progress.String()
	for _, want := range []string{"reading rowan.yaml", "encrypting 1 value(s) to air.yaml's own recipients",
		"committing on branch seal/air-NOVA_REDIS_BENCH_PASSWORD-", "pushing the branch", "opening the pull request",
		"pull request #42 is open; waiting", "approved; merging #42", "checking the seat decrypts"} {
		assert.Contains(t, got, want, "progress missing %q:\n%s", want, got)
	}
	for _, l := range strings.Split(strings.TrimSpace(got), "\n") {
		assert.True(t, strings.HasPrefix(l, "seat inject: "), "progress line without the seat inject: prefix: %q", l)
	}
	assertNoValue(t, "progress", got)
}
