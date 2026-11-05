package main

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The fake sops seals a seat file as one "NAME: ENC[fake]" line per name and a last
// "#fake <hex of the JSON>" line; it records every call so a test can prove no value
// reached an argument, and what sops's environment was.
type call struct {
	stdin     string
	env       []string
	dir, name string
	args      []string
}

type harness struct {
	t       *testing.T
	store   string
	key     string
	calls   []call
	stdout  bytes.Buffer
	stderr  bytes.Buffer
	stdin   string
	environ []string
	became  []string // argv
	env     []string // the environment exec handed the command
	failEnc bool
}

const pubA = "age1qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqa"
const pubB = "age1qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqb"
const pubRec = "age1qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqr"

func newHarness(t *testing.T) *harness {
	t.Helper()
	dir := t.TempDir()
	h := &harness{t: t, store: filepath.Join(dir, "store"), key: filepath.Join(dir, "keys", "ada.key")}
	require.NoError(t, os.MkdirAll(h.store, 0o700))
	require.NoError(t, os.MkdirAll(filepath.Dir(h.key), 0o700))
	require.NoError(t, os.WriteFile(h.key, []byte("not a real key: the fake sops never reads it\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(h.store, "recovery.pub"), []byte(pubRec+"\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(h.store, ".sops.yaml"),
		[]byte("creation_rules:\n  - path_regex: ^ada\\.yaml$\n    age: "+pubA+","+pubRec+"\n"), 0o644))
	h.environ = []string{"PATH=" + os.Getenv("PATH"), "HOME=/home/ada", "SOPS_AGE_KEY=AGE-SECRET-KEY-INHERITED", "GH_TOKEN=inherited", "KEEP=yes"}
	return h
}

func sealed(values map[string]string) []byte {
	plain, _ := json.Marshal(values) // ignored: a map of strings always encodes
	var b strings.Builder
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(&b, "%s: ENC[fake]\n", k)
	}
	fmt.Fprintf(&b, "sops:\n    age: []\n#fake %s\n", hex.EncodeToString(plain))
	return []byte(b.String())
}

func (h *harness) seed(seat string, values map[string]string) {
	require.NoError(h.t, os.WriteFile(filepath.Join(h.store, seat+".yaml"), sealed(values), 0o600))
}

func (h *harness) open(seat string) map[string]string {
	data, err := os.ReadFile(filepath.Join(h.store, seat+".yaml"))
	require.NoError(h.t, err)
	_, hx, ok := strings.Cut(string(data), "#fake ")
	require.True(h.t, ok)
	plain, err := hex.DecodeString(strings.TrimSpace(hx))
	require.NoError(h.t, err)
	var v map[string]string
	require.NoError(h.t, json.Unmarshal(plain, &v))
	return v
}

type exitErr int

func (e exitErr) Error() string { return fmt.Sprintf("exit status %d", int(e)) }

func (h *harness) run(stdin io.Reader, env []string, dir, name string, args ...string) ([]byte, error) {
	c := call{env: env, dir: dir, name: name, args: args}
	if stdin != nil {
		b, _ := io.ReadAll(stdin) // ignored: a bytes reader
		c.stdin = string(b)
	}
	h.calls = append(h.calls, c)
	switch {
	case name == "sops" && args[0] == "-d":
		data, err := os.ReadFile(filepath.Join(dir, args[len(args)-1]))
		if err != nil {
			return nil, exitErr(128)
		}
		_, hx, ok := strings.Cut(string(data), "#fake ")
		if !ok {
			return nil, exitErr(128)
		}
		return hex.DecodeString(strings.TrimSpace(hx))
	case name == "sops" && args[0] == "-e":
		if h.failEnc {
			return nil, exitErr(1)
		}
		var v map[string]string
		if err := json.Unmarshal([]byte(c.stdin), &v); err != nil {
			return nil, err
		}
		return sealed(v), nil
	case name == "age-keygen" && args[0] == "-o":
		return nil, os.WriteFile(args[1], []byte("AGE-SECRET-KEY-FAKE\n"), 0o644)
	case name == "age-keygen" && args[0] == "-y":
		return []byte(pubB + "\n"), nil
	case name == "git":
		return nil, nil
	}
	return nil, errors.New("unexpected program " + name)
}

func (h *harness) main(args ...string) int {
	h.stdout.Reset()
	h.stderr.Reset()
	t := tool{
		stdin: strings.NewReader(h.stdin), stdout: &h.stdout, stderr: &h.stderr, environ: h.environ, run: h.run,
		become: func(argv, env []string) error { h.became, h.env = argv, env; return nil },
	}
	return t.main(args)
}

func (h *harness) seat(verb string, extra ...string) []string {
	return append([]string{verb, "--store", h.store, "--as", "ada", "--key", h.key, "--sops", "sops"}, extra...)
}

// set builds a set call: NAME comes straight after the verb.
func (h *harness) set(name string, extra ...string) []string {
	return append([]string{"set", name, "--store", h.store, "--as", "ada", "--key", h.key, "--sops", "sops"}, extra...)
}

// noLeak holds every value to the rule: never in an argument, an output or an error,
// and the plaintext only ever on sops's stdin.
func (h *harness) noLeak(values ...string) {
	h.t.Helper()
	for _, v := range values {
		assert.NotContains(h.t, h.stdout.String(), v)
		assert.NotContains(h.t, h.stderr.String(), v)
		assert.NotContains(h.t, strings.Join(h.became, " "), v)
		for _, c := range h.calls {
			assert.NotContains(h.t, strings.Join(c.args, " "), v, "%s argv", c.name)
			assert.NotContains(h.t, strings.Join(c.env, " "), v, "%s env", c.name)
			if c.name != "sops" {
				assert.NotContains(h.t, c.stdin, v, "%s stdin", c.name)
			}
		}
	}
}

func envMap(env []string) map[string]string {
	m := map[string]string{}
	for _, kv := range env {
		k, v, _ := strings.Cut(kv, "=")
		m[k] = v
	}
	return m
}

func TestExecDeliversOnlyTheNamedValues(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.seed("ada", map[string]string{"GH_TOKEN": "ghp-sealed-one", "API_KEY": "sk-sealed-two", "OTHER": "sealed-three", markKey: "seal v1.6.29"})
	code := h.main(h.seat("exec", "--only", "API_KEY", "--require=API_KEY", "--", "true", "x")...)
	require.Equal(t, 0, code, h.stderr.String())
	assert.Equal(t, []string{"true", "x"}, h.became)
	env := envMap(h.env)
	assert.Equal(t, "sk-sealed-two", env["API_KEY"])
	assert.NotContains(t, env, "GH_TOKEN", "a name the seat holds is dropped from the inherited environment")
	assert.NotContains(t, env, "OTHER")
	assert.NotContains(t, env, "SOPS_AGE_KEY")
	assert.Equal(t, "yes", env["KEEP"])
	assert.Equal(t, "SECRETS EXEC OK as=ada keys=1 cmd=true\n", h.stderr.String())
	sops := envMap(h.calls[0].env)
	assert.Equal(t, h.key, sops["SOPS_AGE_KEY_FILE"])
	assert.NotEqual(t, "/home/ada", sops["HOME"], "sops gets a HOME of its own, so no keys.txt of the caller's is read")
	assert.NotContains(t, sops, "SOPS_AGE_KEY")
	h.noLeak("ghp-sealed-one", "sk-sealed-two", "sealed-three")
}

func TestExecRefusalsAre125AndNeverRun(t *testing.T) {
	t.Parallel()
	for name, args := range map[string][]string{
		"unknown name":        {"--only", "API_KEY,NOPE", "--", "true"},
		"require not present": {"--only", "API_KEY", "--require", "NOPE", "--", "true"},
		"require not in only": {"--only", "API_KEY", "--require", "OTHER", "--", "true"},
		"no command":          {"--only", "API_KEY", "--"},
		"no dash dash":        {"--only", "API_KEY", "true"},
		"unknown command":     {"--only", "API_KEY", "--", "no-such-program-here"},
		"argument before --":  {"--only", "API_KEY", "stray", "--", "true"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			h := newHarness(t)
			h.seed("ada", map[string]string{"API_KEY": "sk-sealed-two", "OTHER": "sealed-three"})
			assert.Equal(t, 125, h.main(h.seat("exec", args...)...))
			assert.Nil(t, h.became)
			assert.Contains(t, h.stderr.String(), "SECRETS EXEC REFUSED: ")
			h.noLeak("sk-sealed-two", "sealed-three")
		})
	}
	h := newHarness(t)
	assert.Equal(t, 125, h.main("exec", "--", "true"))
	assert.Contains(t, h.stderr.String(), "missing --only")
}

func TestKeyFileMustBePrivate(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.seed("ada", map[string]string{"API_KEY": "sk-sealed-two"})
	require.NoError(t, os.Chmod(h.key, 0o640))
	assert.Equal(t, 125, h.main(h.seat("exec", "--only", "API_KEY", "--", "true")...))
	assert.Contains(t, h.stderr.String(), "mode 0600")
	assert.Empty(t, h.calls, "sops never runs with a key file others can read")

	link := h.key + ".link"
	require.NoError(t, os.Chmod(h.key, 0o600))
	require.NoError(t, os.Symlink(h.key, link))
	assert.Equal(t, 125, h.main("exec", "--store", h.store, "--as", "ada", "--key", link, "--only", "API_KEY", "--", "true"))
	assert.Empty(t, h.calls, "a symlink is not a key file")
}

func TestSetSetsOneValueAndCommits(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.seed("ada", map[string]string{"OLD": "sealed-old", markKey: "seal v1.6.29"})
	h.stdin = "fresh-value-123\n"
	require.Equal(t, 0, h.main(h.set("NEW")...), h.stderr.String())
	assert.Equal(t, map[string]string{"OLD": "sealed-old", "NEW": "fresh-value-123"}, h.open("ada"), "the old mark is dropped")
	var git [][]string
	for _, c := range h.calls {
		if c.name == "git" {
			git = append(git, c.args)
			assert.Equal(t, h.store, c.dir)
		}
	}
	assert.Equal(t, [][]string{{"add", "--", "ada.yaml"}, {"commit", "-q", "-m", "set NEW in ada.yaml", "--", "ada.yaml"}}, git)
	assert.Contains(t, h.stdout.String(), "SECRETS SET OK as=ada name=NEW file=ada.yaml")
	h.noLeak("fresh-value-123", "sealed-old")
}

func TestSetStartsASeatFile(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.stdin = "first\r\n"
	require.Equal(t, 0, h.main(h.set("GH_TOKEN")...), h.stderr.String())
	assert.Equal(t, map[string]string{"GH_TOKEN": "first"}, h.open("ada"))
}

func TestSetRefusesABadValue(t *testing.T) {
	t.Parallel()
	for _, in := range []string{"", "\n", "two\nlines\n", "nul\x00here\n"} {
		h := newHarness(t)
		h.seed("ada", map[string]string{"OLD": "sealed-old"})
		h.stdin = in
		assert.Equal(t, 2, h.main(h.set("NEW")...), "%q", in)
		assert.Contains(t, h.stderr.String(), "one non-empty line")
		assert.Empty(t, h.calls)
		h.noLeak("two", "lines", "nul")
	}
	h := newHarness(t)
	h.stdin = "v\n"
	assert.Equal(t, 2, h.main(h.set("1BAD")...))
}

func TestSetLeavesNoPlaintextOnDisk(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.stdin = "plain-needle-987\n"
	require.Equal(t, 0, h.main(h.set("TOKEN")...), h.stderr.String())
	require.NoError(t, filepath.Walk(filepath.Dir(h.store), func(p string, fi os.FileInfo, err error) error {
		require.NoError(t, err)
		if !fi.IsDir() {
			b, err := os.ReadFile(p)
			require.NoError(t, err)
			assert.NotContains(t, string(b), "plain-needle-987", p)
		}
		return nil
	}))
	for _, c := range h.calls {
		if c.name == "sops" {
			assert.Contains(t, c.stdin, "plain-needle-987", "the value reaches sops on stdin only")
		}
	}
}

func TestSealIsAHiddenAliasOfSet(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.stdin = "old-caller\n"
	require.Equal(t, 0, h.main(h.seat("seal", "--name", "NEW", "--stdin", "--no-pr")...), h.stderr.String())
	assert.Equal(t, map[string]string{"NEW": "old-caller"}, h.open("ada"))
}

func TestSeatAddEditsTheRecipients(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	before, err := os.ReadFile(filepath.Join(h.store, ".sops.yaml"))
	require.NoError(t, err)
	args := []string{"seat", "add", "bo", pubB, "--store", h.store}
	require.Equal(t, 0, h.main(args...), h.stderr.String())
	after, err := os.ReadFile(filepath.Join(h.store, ".sops.yaml"))
	require.NoError(t, err)
	assert.Equal(t, string(before)+"  - path_regex: ^bo\\.yaml$\n    age: "+pubB+","+pubRec+"\n", string(after))
	assert.NoFileExists(t, filepath.Join(h.store, "bo.yaml"))
	assert.Empty(t, h.calls, "no program runs")

	assert.Equal(t, 2, h.main(args...), "the rule for bo exists")
	assert.Contains(t, h.stderr.String(), "already has a rule")
	for _, pub := range []string{pubB, pubRec, "age1short"} {
		assert.Equal(t, 2, h.main("seat", "add", "cy", pub, "--store", h.store), pub)
	}
	assert.Equal(t, 2, h.main("seat", "add", "cy", "--store", h.store), "the key is required")
}

func TestExecDefaultsFromTheEnvironment(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.seed("ada", map[string]string{"API_KEY": "sk-sealed-two"})
	h.environ = append(h.environ[:1:1], "NOVA_SECRETS_SEAT=ada", "HOME="+filepath.Dir(filepath.Dir(h.key)))
	require.NoError(t, os.MkdirAll(filepath.Join(filepath.Dir(filepath.Dir(h.key)), ".config", "nova-secrets"), 0o700))
	require.NoError(t, os.Rename(h.key, filepath.Join(filepath.Dir(filepath.Dir(h.key)), ".config", "nova-secrets", "ada.key")))
	require.Equal(t, 0, h.main("exec", "--store", h.store, "--only", "API_KEY", "--", "true"), h.stderr.String())
	assert.Equal(t, "sk-sealed-two", envMap(h.env)["API_KEY"])
}

func TestDecryptRefusesWhatIsNoEnvironmentVariable(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	plain := `{"GOOD":"v","nested":{"a":"secret-nested"}}`
	require.NoError(t, os.WriteFile(filepath.Join(h.store, "ada.yaml"), []byte("#fake "+hex.EncodeToString([]byte(plain))+"\n"), 0o600))
	assert.Equal(t, 125, h.main(h.seat("exec", "--only", "GOOD", "--", "true")...))
	assert.Contains(t, h.stderr.String(), "nested is not a single value")
	h.noLeak("secret-nested")
	plain = `{"N":12,"T":true,"E":null}`
	require.NoError(t, os.WriteFile(filepath.Join(h.store, "ada.yaml"), []byte("#fake "+hex.EncodeToString([]byte(plain))+"\n"), 0o600))
	require.Equal(t, 0, h.main(h.seat("exec", "--only", "N,T,E", "--", "true")...), h.stderr.String())
	env := envMap(h.env)
	assert.Equal(t, []string{"12", "true", ""}, []string{env["N"], env["T"], env["E"]})
}

func TestVerbs(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	assert.Equal(t, 0, h.main("help"))
	assert.Contains(t, h.stdout.String(), "nova-secrets exec")
	for _, args := range [][]string{{"get"}, {"names"}, {"check"}, {"keygen"}, {"seat"}, {"seat", "inject"}, {"set", "--bogus"}, {"set", "X", "--as", "../x"}} {
		assert.Equal(t, 2, h.main(args...), "%v", args)
	}
}
