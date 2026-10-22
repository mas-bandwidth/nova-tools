package secrets

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The Air seat, 2026-09-18. A new bench generated its key and then there was nothing
// to give it: `seal` must DECRYPT the seat file before it can fold a value into it,
// and the only key that opens a new seat's file is the new seat's own -- which is on
// the bench that has no values yet. The store's PR #15 broke the circle by hand, with
// a sops pipe from a seat this machine COULD open into the new seat's file.
//
// `seat add` is that hand pipe as a verb. It re-seals named values out of a source seat
// into a new seat's file, writes the new seat's rule, and never prints a value.

// bech32 is age's alphabet: no 1, b, i or o. A key built from it passes
// IsValidAgePublicKey, so the fixture needs no age-keygen.
const bech32 = "qpzry9x8gf2tvdw0s3jn54khce6mua7l"

func testPub(lead byte) string {
	body := bech32 + bech32[:26] // 58 characters after the age1 prefix
	return "age1" + string(lead) + body[1:]
}

var (
	pubRowan    = testPub('q')
	pubAir      = testPub('p')
	pubRecovery = testPub('z')
	pubStranger = testPub('r')
)

// fakeSopsScript is a fake that refuses what the real one refuses. A lenient fake is
// how `seal` shipped broken once already (the encrypt that needs a file argument even
// when the bytes come from stdin): this one insists on the /dev/stdin argument, on a
// creation rule that actually matches the file being written -- no rule, no recipients,
// no ciphertext -- and, on the way back, on sops metadata in the file and on the
// identity being one of the recipients that file records.
const fakeSopsScript = `#!/bin/sh
# Written ONCE per test binary (TestMain) and reached through a per-fixture
# symlink, so no test writes an executable while another test forks: a child
# between fork and exec still holds the writer's descriptor, and the exec then
# fails with ETXTBSY ("text file busy", space shard 1/4 of run 36206780711).
# $0 is the symlink's own path, so the fixture's files sit beside it.
here=$(dirname "$0")
ARGS="$here/sops.args"
STDIN="$here/sops.stdin"
if [ "$1" = "--version" ]; then echo "sops 3.13.3"; exit 0; fi
printf '%s\n' "$@" >> "$ARGS"

if [ "$1" = "-d" ]; then
  f="$2"
  if [ ! -f "$f" ]; then echo "sops: cannot open $f" >&2; exit 1; fi
  if ! grep -q '^sops:' "$f"; then echo "sops metadata not found in $f" >&2; exit 2; fi
  if [ -z "$SOPS_AGE_KEY_FILE" ] || [ ! -f "$SOPS_AGE_KEY_FILE" ]; then
    echo "no identity file" >&2; exit 128
  fi
  pub=$(sed -n 's/^# public key: //p' "$SOPS_AGE_KEY_FILE")
  if [ -z "$pub" ] || ! grep -q -- "- recipient: $pub" "$f"; then
    echo "no identity matched any of the recipients" >&2; exit 128
  fi
  sed -n '/^PLAINTEXT$/,$p' "$f" | tail -n +2
  exit 0
fi

for last; do :; done
if [ "$last" != "/dev/stdin" ]; then echo "error: no file specified" >&2; exit 100; fi
pwd -P > "$ARGS.cwd"
fn=""
prev=""
for a in "$@"; do
  if [ "$prev" = "--filename-override" ]; then fn="$a"; fi
  prev="$a"
done
if [ -z "$fn" ]; then echo "error: no file specified" >&2; exit 100; fi
if [ ! -f .sops.yaml ]; then echo "config file not found" >&2; exit 1; fi
base=$(basename "$fn" .yaml)
rec=$(awk -v base="$base" '
  index($0, "path_regex:") > 0 { hit = index($0, "path_regex: ^" base "\\.yaml$") > 0; next }
  hit && index($0, "age:") > 0 { line = $0; sub(/^ *age: */, "", line); print line; exit }
' .sops.yaml)
if [ -z "$rec" ]; then echo "error: no matching creation rules found" >&2; exit 1; fi
cat > "$STDIN"
echo "sops:"
echo "    age:"
for r in $(echo "$rec" | tr ',' ' '); do echo "        - recipient: $r"; done
echo "PLAINTEXT"
cat "$STDIN"
exit 0
`

type seatFixture struct {
	dir       string
	storeDir  string
	rowanKey  string
	airKey    string
	sopsPath  string
	sopsArgs  string
	sopsStdin string
}

// fakeCipher is what the fake sops writes: the recipients it recorded, then the bytes.
func fakeCipher(recipients []string, plaintext string) string {
	var b strings.Builder
	b.WriteString("sops:\n    age:\n")
	for _, r := range recipients {
		b.WriteString("        - recipient: " + r + "\n")
	}
	b.WriteString("PLAINTEXT\n")
	b.WriteString(plaintext)
	return b.String()
}

func newSeatFixture(t *testing.T) *seatFixture {
	t.Helper()
	skipPOSIXFakesOnWindows(t)
	dir := t.TempDir()
	f := &seatFixture{
		dir:       dir,
		storeDir:  filepath.Join(dir, "store"),
		sopsArgs:  filepath.Join(dir, "sops.args"),
		sopsStdin: filepath.Join(dir, "sops.stdin"),
	}
	mustMkdir(t, f.storeDir, 0755)
	mustMkdir(t, filepath.Join(f.storeDir, ".git"), 0755)
	mustWrite(t, filepath.Join(f.storeDir, ".sops.yaml"),
		"creation_rules:\n  - path_regex: ^rowan\\.yaml$\n    age: "+pubRowan+","+pubRecovery+"\n", 0644)
	mustWrite(t, filepath.Join(f.storeDir, "recovery.pub"), pubRecovery+"\n", 0644)
	mustWrite(t, filepath.Join(f.storeDir, "rowan.yaml"),
		fakeCipher([]string{pubRowan, pubRecovery},
			"GH_TOKEN: ghp_carried\nDEEPSEEK_API_KEY: sk_carried\nLEFT_BEHIND: nope\n"), 0644)

	keyDir := filepath.Join(dir, "keys")
	mustMkdir(t, keyDir, 0700)
	f.rowanKey = filepath.Join(keyDir, "rowan.key")
	mustWrite(t, f.rowanKey, "AGE-SECRET-KEY-1ROWAN\n# public key: "+pubRowan+"\n", 0600)
	f.airKey = filepath.Join(keyDir, "air.key")
	mustWrite(t, f.airKey, "AGE-SECRET-KEY-1AIR\n# public key: "+pubAir+"\n", 0600)

	f.sopsPath = filepath.Join(dir, "sops")
	require.NoError(t, os.Symlink(sharedFakeSops, f.sopsPath))
	return f
}

func (f *seatFixture) options(t *testing.T, only string) SeatAddOptions {
	t.Helper()
	return SeatAddOptions{
		StoreDir: f.storeDir,
		AsName:   "air",
		Pub:      pubAir,
		From:     "rowan",
		Only:     only,
		KeyPath:  f.rowanKey,
		SopsPath: f.sopsPath,
	}
}

// sharedFakeSops is the one fake sops executable of this test binary, written
// before any test runs (see fakeSopsScript for why).
var sharedFakeSops string

// sharedSopsOpensAll and sharedSopsOpensNone are two blunter fakes, written beside it:
// a sops whose -d opens every file it is given, and one that opens none, for the check
// that a seat's key opens exactly the files that list it.
var sharedSopsOpensAll, sharedSopsOpensNone string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "secrets-fake-sops")
	if err != nil {
		panic(err)
	}
	sharedFakeSops = filepath.Join(dir, "sops")
	sharedSopsOpensAll = filepath.Join(dir, "sops-opens-all")
	sharedSopsOpensNone = filepath.Join(dir, "sops-opens-none")
	for path, script := range map[string]string{
		sharedFakeSops:      fakeSopsScript,
		sharedSopsOpensAll:  "#!/bin/sh\nexit 0\n",
		sharedSopsOpensNone: "#!/bin/sh\necho 'no identity matched any of the recipients' >&2\nexit 128\n",
	} {
		if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
			panic(err)
		}
	}
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

func mustMkdir(t *testing.T, path string, perm os.FileMode) {
	t.Helper()
	require.NoError(t, os.MkdirAll(path, perm))
	require.NoError(t, os.Chmod(path, perm))
}

func mustWrite(t *testing.T, path, content string, perm os.FileMode) {
	t.Helper()
	require.NoError(t, os.WriteFile(path, []byte(content), perm))
	require.NoError(t, os.Chmod(path, perm))
}

func (f *seatFixture) read(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(f.storeDir, rel))
	if err != nil {
		return ""
	}
	return string(b)
}

// TestSeatAddReSealsNamedValuesIntoTheNewSeatsFile is the circle broken: the values
// come out of a seat this machine can open and go into a file only the new seat's key
// and the recovery key can open.
func TestSeatAddReSealsNamedValuesIntoTheNewSeatsFile(t *testing.T) {
	t.Parallel()

	f := newSeatFixture(t)
	lines, err := RunSeatAdd(f.options(t, "GH_TOKEN,DEEPSEEK_API_KEY"))
	require.NoError(t, err, "RunSeatAdd: %v", err)

	// The rule the new seat's file is governed by, written before the encrypt so the
	// encrypt has recipients to find.
	cfg := f.read(t, ".sops.yaml")
	assert.Contains(t, cfg, "- path_regex: ^air\\.yaml$", ".sops.yaml carries no rule for the new seat:\n%s", cfg)
	assert.Contains(t, cfg, "age: "+pubAir+","+pubRecovery, "the new rule does not name the new key and the recovery key:\n%s", cfg)
	assert.Contains(t, cfg, "- path_regex: ^rowan\\.yaml$", "the source seat's rule was lost:\n%s", cfg)

	// The file, encrypted to those recipients and no others.
	air := f.read(t, "air.yaml")
	require.NotEmpty(t, air, "no air.yaml was written")
	assert.Contains(t, air, "recipient: "+pubAir, "air.yaml is not sealed to the new seat and the recovery key:\n%s", air)
	assert.Contains(t, air, "recipient: "+pubRecovery, "air.yaml is not sealed to the new seat and the recovery key:\n%s", air)
	assert.NotContains(t, air, "recipient: "+pubRowan, "air.yaml is also readable by the source seat; the rule, not the source, picks recipients:\n%s", air)

	// The named values travelled; the unnamed one did not.
	stdin, err := os.ReadFile(f.sopsStdin)
	require.NoError(t, err, "the encrypt child was never handed anything on stdin: %v", err)
	for _, want := range []string{"GH_TOKEN:", "DEEPSEEK_API_KEY:", "ghp_carried", "sk_carried"} {
		assert.Contains(t, string(stdin), want, "encrypt stdin missing %q:\n%s", want, stdin)
	}
	assert.NotContains(t, string(stdin), "LEFT_BEHIND", "a key nobody asked for was carried over:\n%s", stdin)

	// No value, anywhere a person or a log can see it.
	argv, _ := os.ReadFile(f.sopsArgs)
	joined := strings.Join(lines, "\n") + "\n" + string(argv)
	for _, secret := range []string{"ghp_carried", "sk_carried"} {
		assert.NotContains(t, joined, secret, "a value leaked into the receipt or into argv:\n%s", joined)
	}

	// The verdict is the last line, the lesson keygen learned on the same day.
	last := lines[len(lines)-1]
	assert.True(t, strings.HasPrefix(last, "SECRETS SEAT ADD OK "), "the last line is not the verdict:\n%s", strings.Join(lines, "\n"))
	for _, want := range []string{"as=air", "from=rowan", "keys=2", "file=air.yaml"} {
		assert.Contains(t, last, want, "the OK line carries no %q: %s", want, last)
	}
}

// TestSeatAddLeavesAFileOnlyTheNewSeatCanOpen is the assertion the whole verb exists
// for, proven through the fake's own recipient check rather than by reading the rule.
func TestSeatAddLeavesAFileOnlyTheNewSeatCanOpen(t *testing.T) {
	t.Parallel()

	f := newSeatFixture(t)
	_, err := RunSeatAdd(f.options(t, "GH_TOKEN"))
	require.NoError(t, err, "RunSeatAdd: %v", err)
	airFile := filepath.Join(f.storeDir, "air.yaml")
	_, err = sealDecrypt(realExecCommand, f.sopsPath, f.airKey, airFile)
	assert.NoError(t, err, "the new seat cannot open its own file: %v", err)
	_, err = sealDecrypt(realExecCommand, f.sopsPath, f.rowanKey, airFile)
	assert.Error(t, err, "the source seat can still open the new seat's file; the rule granted the wrong key")
}

// TestSeatAddRefusesWhenTheSourceSeatCannotBeOpenedHere: the refusal Glenn would have
// hit had he pointed the verb at a seat this bench has no key for. It must cost
// nothing -- no rule, no file, no half-written store.
func TestSeatAddRefusesWhenTheSourceSeatCannotBeOpenedHere(t *testing.T) {
	t.Parallel()

	f := newSeatFixture(t)
	before := f.read(t, ".sops.yaml")

	opts := f.options(t, "GH_TOKEN")
	opts.KeyPath = f.airKey // the new seat's key opens nothing in this store yet
	_, err := RunSeatAdd(opts)
	require.Error(t, err, "RunSeatAdd accepted a source seat this machine cannot open")
	assert.Contains(t, err.Error(), "rowan", "the refusal does not name the source seat: %v", err)
	got := f.read(t, ".sops.yaml")
	assert.Equal(t, before, got, ".sops.yaml was edited by a refused run:\n%s", got)
	assert.Empty(t, f.read(t, "air.yaml"), "a refused run left a seat file behind")
}

// TestSeatAddRefusesAnExistingTargetFile: a verb that can overwrite a seat file is a
// verb that can drop every value a seat holds.
func TestSeatAddRefusesAnExistingTargetFile(t *testing.T) {
	t.Parallel()

	f := newSeatFixture(t)
	mustWrite(t, filepath.Join(f.storeDir, "air.yaml"), "sops:\n", 0644)
	_, err := RunSeatAdd(f.options(t, "GH_TOKEN"))
	require.Error(t, err, "RunSeatAdd overwrote an existing seat file")
	assert.Contains(t, err.Error(), "air.yaml", "the refusal does not name the file: %v", err)
	got := f.read(t, "air.yaml")
	assert.Equal(t, "sops:\n", got, "the existing file was touched:\n%s", got)
}

// TestSeatAddRefusesAnExistingRule: the rule is the grant. A verb that rewrites one
// silently is the recipient edit that never went through a review.
func TestSeatAddRefusesAnExistingRule(t *testing.T) {
	t.Parallel()

	f := newSeatFixture(t)
	mustWrite(t, filepath.Join(f.storeDir, ".sops.yaml"),
		"creation_rules:\n  - path_regex: ^rowan\\.yaml$\n    age: "+pubRowan+","+pubRecovery+
			"\n  - path_regex: ^air\\.yaml$\n    age: "+pubStranger+","+pubRecovery+"\n", 0644)
	before := f.read(t, ".sops.yaml")
	_, err := RunSeatAdd(f.options(t, "GH_TOKEN"))
	require.Error(t, err, "RunSeatAdd rewrote a rule that already existed")
	got := f.read(t, ".sops.yaml")
	assert.Equal(t, before, got, ".sops.yaml changed under a refusal:\n%s", got)
}

// TestSeatAddRefusesAKeyTheSourceDoesNotCarry, naming the key and never a value.
func TestSeatAddRefusesAKeyTheSourceDoesNotCarry(t *testing.T) {
	t.Parallel()

	f := newSeatFixture(t)
	before := f.read(t, ".sops.yaml")
	_, err := RunSeatAdd(f.options(t, "GH_TOKEN,ABSENT_KEY"))
	require.Error(t, err, "RunSeatAdd accepted a key the source seat does not carry")
	assert.Contains(t, err.Error(), "ABSENT_KEY", "the refusal does not name the missing key: %v", err)
	for _, secret := range []string{"ghp_carried", "sk_carried"} {
		assert.NotContains(t, err.Error(), secret, "the refusal carries a value: %v", err)
	}
	got := f.read(t, ".sops.yaml")
	assert.Equal(t, before, got, ".sops.yaml changed under a refusal:\n%s", got)
	assert.Empty(t, f.read(t, "air.yaml"), "a refused run left a seat file behind")
}

// TestSeatAddRefusesAnUnusableInvocation costs one refusal per shape and touches nothing.
func TestSeatAddRefusesAnUnusableInvocation(t *testing.T) {
	t.Parallel()

	f := newSeatFixture(t)
	for _, tc := range []struct {
		name string
		fix  func(o *SeatAddOptions)
		want string
	}{
		{"no --as", func(o *SeatAddOptions) { o.AsName = "" }, "--as"},
		{"no --pub", func(o *SeatAddOptions) { o.Pub = "" }, "--pub"},
		{"bad --pub", func(o *SeatAddOptions) { o.Pub = "age1nope" }, "--pub"},
		{"no --from", func(o *SeatAddOptions) { o.From = "" }, "--from"},
		{"--from is --as", func(o *SeatAddOptions) { o.From = "air" }, "--from"},
		{"no --only", func(o *SeatAddOptions) { o.Only = "" }, "--only"},
		{"lowercase --only", func(o *SeatAddOptions) { o.Only = "gh_token" }, "gh_token"},
		{"no --key", func(o *SeatAddOptions) { o.KeyPath = "" }, "--key"},
		{"no --sops", func(o *SeatAddOptions) { o.SopsPath = "" }, "--sops"},
	} {
		opts := f.options(t, "GH_TOKEN")
		tc.fix(&opts)
		_, err := RunSeatAdd(opts)
		if !assert.Error(t, err, "%s: accepted", tc.name) {
			continue
		}
		assert.Contains(t, err.Error(), tc.want, "%s: refusal does not name %q: %v", tc.name, tc.want, err)
	}
}

// TestTheFakeSopsRefusesWhatRealSopsRefuses. A fake that says yes where the real tool
// says no is a green test over a broken verb -- it has happened here once already, so
// the fake is held to the three refusals this verb depends on.
func TestTheFakeSopsRefusesWhatRealSopsRefuses(t *testing.T) {
	t.Parallel()

	f := newSeatFixture(t)

	// A clear file has no sops metadata, and sops will not decrypt one.
	clear := filepath.Join(f.dir, "clear.yaml")
	mustWrite(t, clear, "GH_TOKEN: inthclear\n", 0644)
	_, err := sealDecrypt(realExecCommand, f.sopsPath, f.rowanKey, clear)
	assert.Error(t, err, "the fake decrypted a file with no sops metadata")

	// An encrypt whose file matches no creation rule has no recipients, and sops refuses
	// rather than writing something nobody can open.
	_, _, err = sealEncrypt(realExecCommand, f.sopsPath, f.rowanKey, f.storeDir, "stranger.yaml", "seat add", []byte("K: v\n"))
	assert.Error(t, err, "the fake encrypted a file no creation rule matches")

	// And the file argument the real tool insists on even when the bytes are on stdin.
	out, err := realExecCommand(strings.NewReader("K: v\n"),
		sealSopsEnv(f.rowanKey, f.dir), f.storeDir, f.sopsPath,
		"-e", "--filename-override", "rowan.yaml", "--input-type", "yaml", "--output-type", "yaml")
	assert.Error(t, err, "the fake encrypted with no file argument: %s", out)
}
