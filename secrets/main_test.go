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
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
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
	t           *testing.T
	store       string
	key         string
	calls       []call
	stdout      bytes.Buffer
	stderr      bytes.Buffer
	stdin       string
	environ     []string
	pins        string   // the control node's pin directory, holding the recovery pin
	became      []string // argv
	env         []string // the environment exec handed the command
	failEnc     bool
	failDec     bool
	decExit     int    // the fake sops -d's exit when it fails; 128 when 0
	noGit       bool   // git is not on PATH
	extraKey    string // the fake sops seals to this key too, as a hostile config would make it
	sopsMissing bool
	noAgeKeygen bool
	failGit     bool
	keyPub      string // what the fake age-keygen -y prints
	notRepo     bool   // git rev-parse fails
	top         string // what git rev-parse --show-toplevel prints; the directory asked about when ""
}

const pubA = "age1qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqa"
const pubB = "age1qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqb"
const pubC = "age1qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqc"
const pubRec = "age1qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqr"

func newHarness(t *testing.T) *harness {
	t.Helper()
	dir := t.TempDir()
	h := &harness{t: t, keyPub: pubA, store: filepath.Join(dir, "store"), key: filepath.Join(dir, "keys", "ada.key"), pins: filepath.Join(dir, "pins")}
	require.NoError(t, os.MkdirAll(h.pins, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(h.pins, "recovery.pub"), []byte(pubRec+"\n"), 0o600))
	require.NoError(t, os.MkdirAll(h.store, 0o700))
	require.NoError(t, os.MkdirAll(filepath.Dir(h.key), 0o700))
	require.NoError(t, os.WriteFile(h.key, []byte("not a real key: the fake sops never reads it\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(h.store, "recovery.pub"), []byte(pubRec+"\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(h.store, ".sops.yaml"),
		[]byte("creation_rules:\n  - path_regex: ^ada\\.yaml$\n    age: "+pubA+","+pubRec+"\n"), 0o644))
	h.environ = []string{"PATH=" + os.Getenv("PATH"), "HOME=/home/ada", "SOPS_AGE_KEY=AGE-SECRET-KEY-INHERITED", "GH_TOKEN=inherited", "KEEP=yes"}
	return h
}

func sealed(values map[string]string, recipients ...string) []byte {
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
	if len(recipients) == 0 {
		fmt.Fprintf(&b, "sops:\n    age: []\n#fake %s\n", hex.EncodeToString(plain))
		return []byte(b.String())
	}
	b.WriteString("sops:\n    age:\n")
	for _, r := range recipients {
		fmt.Fprintf(&b, "        - recipient: %s\n", r)
	}
	fmt.Fprintf(&b, "#fake %s\n", hex.EncodeToString(plain))
	return []byte(b.String())
}

// opens is whether the key opens a fake seat file: one that lists no recipients (seed) opens with any key.
func (h *harness) opens(data []byte) bool {
	rs, _ := recipientsOf(data)
	return len(rs) == 0 || slices.Contains(rs, h.keyPub)
}

// ruleKeys is the age list of the rule the fake sops applies to file, as the real one reads .sops.yaml in dir.
func (h *harness) ruleKeys(dir, file string) []string {
	cfg, _ := os.ReadFile(filepath.Join(dir, ".sops.yaml"))
	return ageOf(ruleFor(cfg, file))
}

func (h *harness) seed(seat string, values map[string]string) {
	require.NoError(h.t, os.WriteFile(filepath.Join(h.store, seat+".yaml"), sealed(values, h.ruleKeys(h.store, seat+".yaml")...), 0o600))
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
func (e exitErr) ExitCode() int { return int(e) }

func (h *harness) run(stdin io.Reader, env []string, dir, name string, args ...string) ([]byte, error) {
	c := call{env: env, dir: dir, name: name, args: args}
	if stdin != nil {
		b, _ := io.ReadAll(stdin) // ignored: a bytes reader
		c.stdin = string(b)
	}
	h.calls = append(h.calls, c)
	switch {
	case name == "sops" && args[0] == "-d":
		if h.failDec {
			if h.decExit != 0 {
				return nil, exitErr(h.decExit)
			}
			return nil, exitErr(128)
		}
		data, err := os.ReadFile(filepath.Join(dir, args[len(args)-1]))
		if err != nil || !h.opens(data) {
			return nil, exitErr(128)
		}
		_, hx, ok := strings.Cut(string(data), "#fake ")
		if !ok {
			return nil, exitErr(128)
		}
		return hex.DecodeString(strings.TrimSpace(hx))
	case name == "sops" && (args[0] == "-e" || args[0] == "--config"):
		if h.failEnc {
			return nil, exitErr(1)
		}
		var v map[string]string
		if err := json.Unmarshal([]byte(c.stdin), &v); err != nil {
			return nil, err
		}
		keys := h.ruleKeys(dir, args[2]) // -e --filename-override <file>: the rule .sops.yaml has for it
		if args[0] == "--config" {       // --config /dev/null -e --age <keys> --filename-override <file>: those keys only
			keys = strings.Split(args[4], ",")
		}
		if h.extraKey != "" {
			keys = append(keys, h.extraKey)
		}
		return sealed(v, keys...), nil
	case name == "age-keygen" && args[0] == "-o":
		return nil, os.WriteFile(args[1], []byte("AGE-SECRET-KEY-FAKE\n"), 0o644)
	case name == "age-keygen" && args[0] == "-y" && filepath.Base(args[1]) == "recovery.key":
		return []byte(pubRec + "\n"), nil
	case name == "age-keygen" && args[0] == "-y":
		return []byte(h.keyPub + "\n"), nil
	case name == "git" && args[0] == "rev-parse": // the store is a repository, so is a directory with a .git; nothing else is
		if h.notRepo {
			return nil, exitErr(128)
		}
		if h.top != "" {
			return []byte(h.top + "\n"), nil
		}
		if strings.HasPrefix(dir, h.store) {
			return []byte(dir + "\n"), nil
		}
		for d := dir; ; d = filepath.Dir(d) {
			if _, err := os.Stat(filepath.Join(d, ".git")); err == nil {
				return []byte(d + "\n"), nil
			}
			if filepath.Dir(d) == d {
				return nil, exitErr(128)
			}
		}
	case name == "git" && h.failGit:
		return nil, exitErr(1)
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
		lookPath: func(name string) (string, error) {
			if name == "no-such-program-here" || name == "sops" && h.sopsMissing || name == "age-keygen" && h.noAgeKeygen || name == "git" && h.noGit {
				return "", errors.New("not found")
			}
			return "/bin/" + name, nil
		},
	}
	return t.main(args)
}

func (h *harness) seat(verb string, extra ...string) []string {
	return append([]string{verb, "--store", h.store, "--as", "ada", "--key", h.key, "--sops", "sops"}, extra...)
}

// set builds a set call: NAME comes straight after the verb.
func (h *harness) set(name string, extra ...string) []string {
	return append([]string{"set", name, "--store", h.store, "--as", "ada", "--key", h.key, "--pins", h.pins, "--sops", "sops"}, extra...)
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
	h.seed("ada", map[string]string{"GH_TOKEN": "ghp-sealed-one", "API_KEY": "sk-sealed-two", "OTHER": "sealed-three", markKey: "seal v1"})
	code := h.main(h.seat("exec", "--only", "API_KEY", "--", "true", "x")...)
	require.Equal(t, 0, code, h.stderr.String())
	assert.Equal(t, []string{"true", "x"}, h.became)
	env := envMap(h.env)
	assert.Equal(t, "sk-sealed-two", env["API_KEY"])
	assert.NotContains(t, env, "GH_TOKEN", "a name the seat holds is dropped from the inherited environment")
	assert.NotContains(t, env, "OTHER")
	assert.NotContains(t, env, "SOPS_AGE_KEY")
	assert.Equal(t, "yes", env["KEEP"])
	assert.Equal(t, "SECRETS EXEC STARTING as=ada keys=1 cmd=true\n", h.stderr.String())
	assert.Empty(t, h.stdout.String(), "stdout is the command's alone: a caller parses it")
	sops := envMap(h.calls[0].env)
	assert.Equal(t, h.key, sops["SOPS_AGE_KEY_FILE"])
	assert.NotEqual(t, "/home/ada", sops["HOME"], "sops gets a HOME of its own, so no keys.txt of the caller's is read")
	assert.NotContains(t, sops, "SOPS_AGE_KEY")
	h.noLeak("ghp-sealed-one", "sk-sealed-two", "sealed-three")
}

func TestExecRefusalsAre125AndNeverRun(t *testing.T) {
	t.Parallel()
	for name, args := range map[string][]string{
		"unknown name":    {"--only", "API_KEY,NOPE", "--", "true"},
		"unknown command": {"--only", "API_KEY", "--", "no-such-program-here"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			h := newHarness(t)
			h.seed("ada", map[string]string{"API_KEY": "sk-sealed-two", "OTHER": "sealed-three"})
			assert.Equal(t, 125, h.main(h.seat("exec", args...)...))
			assert.Nil(t, h.became)
			assert.Regexp(t, `^SECRETS EXEC REFUSED reason=(no-name|no-command): `, h.stderr.String())
			h.noLeak("sk-sealed-two", "sealed-three")
		})
	}
}

// A malformed exec is a usage error: exit 2, one line, the command never runs.
func TestExecUsageErrorsAre2AndNameEveryMissingFlag(t *testing.T) {
	t.Parallel()
	for name, args := range map[string][]string{
		"no command":         {"--only", "API_KEY", "--"},
		"no dash dash":       {"--only", "API_KEY", "true"},
		"argument before --": {"--only", "API_KEY", "stray", "--", "true"},
		"unknown flag":       {"--only", "API_KEY", "--x", "--", "true"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			h := newHarness(t)
			h.seed("ada", map[string]string{"API_KEY": "sk-sealed-two"})
			assert.Equal(t, 2, h.main(h.seat("exec", args...)...))
			assert.Nil(t, h.became)
			assert.Regexp(t, `^nova-secrets exec: .*; run: nova-secrets exec -h\n$`, h.stderr.String())
			assert.NotContains(t, h.stderr.String(), "flag provided")
		})
	}
	h := newHarness(t)
	assert.Equal(t, 2, h.main("exec", "--x"))
	assert.Equal(t, "nova-secrets exec: unknown flag --x; run: nova-secrets exec -h\n", h.stderr.String())
	assert.Equal(t, 2, h.main("exec"))
	for _, w := range []string{"--only", "--key", "command"} {
		assert.Contains(t, h.stderr.String(), w)
	}
	assert.Len(t, strings.Split(strings.TrimSpace(h.stderr.String()), "\n"), 1)
	assert.Equal(t, 2, h.main("no-such-verb"))
	assert.Contains(t, h.stderr.String(), "run: nova-secrets help")
	assert.Equal(t, 2, h.main("exec", "--store", h.store, "--key", h.key, "--", "true"))
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
	h.seed("ada", map[string]string{"OLD": "sealed-old", markKey: "seal v1"})
	h.stdin = "fresh-value-123\n"
	require.Equal(t, 0, h.main(h.set("NEW")...), h.stderr.String())
	assert.Equal(t, map[string]string{"OLD": "sealed-old", "NEW": "fresh-value-123"}, h.open("ada"), "the old mark is dropped")
	var git [][]string
	for _, c := range h.calls {
		if c.name == "git" && !slices.Contains([]string{"rev-parse", "status", "ls-files", "diff"}, c.args[0]) {
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

func TestSealIsGone(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	assert.Equal(t, 2, h.main(h.seat("seal", "--name", "NEW")...))
	assert.Contains(t, h.stderr.String(), `unknown verb "seal"`)
}

func TestRequireIsAHiddenAliasOfOnly(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.seed("ada", map[string]string{"API_KEY": "sk-1", "OTHER": "sealed-3"})
	require.Equal(t, 0, h.main(h.seat("exec", "--only", "API_KEY", "--require", "OTHER", "--", "true")...), h.stderr.String())
	assert.Equal(t, "sealed-3", envMap(h.env)["OTHER"])
	assert.Equal(t, "sk-1", envMap(h.env)["API_KEY"])
	require.Equal(t, 0, h.main(h.seat("exec", "--require=API_KEY", "--", "true")...), h.stderr.String())
	assert.Equal(t, 125, h.main(h.seat("exec", "--require", "NOPE", "--", "true")...))
	h.main("help")
	assert.NotContains(t, h.stdout.String(), "--require", "hidden")
}

const catchAllRules = "creation_rules:\n  - age: " + pubA + "\n"

func (h *harness) rules() []map[string]string {
	b, err := os.ReadFile(filepath.Join(h.store, ".sops.yaml"))
	require.NoError(h.t, err)
	var doc struct {
		Rules []map[string]string `yaml:"creation_rules"`
	}
	require.NoError(h.t, yaml.Unmarshal(b, &doc), string(b))
	return doc.Rules
}

func (h *harness) sopsYAML(body string) {
	require.NoError(h.t, os.WriteFile(filepath.Join(h.store, ".sops.yaml"), []byte(body), 0o644))
}

func TestSeatAddEditsTheRecipients(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	args := []string{"seat", "add", "bo", pubB, "--store", h.store}
	require.Equal(t, 0, h.main(args...), h.stderr.String())
	assert.Equal(t, []map[string]string{
		{"path_regex": `^ada\.yaml$`, "age": pubA + "," + pubRec},
		{"path_regex": `^bo\.yaml$`, "age": pubB + "," + pubRec},
	}, h.rules())
	assert.NoFileExists(t, filepath.Join(h.store, "bo.yaml"))
	assert.Equal(t, [][]string{{"add", "--", ".sops.yaml"}, {"commit", "-q", "-m", "seat bo", "--", ".sops.yaml"}}, h.gitCalls(), "it commits only .sops.yaml")
	assert.Len(t, h.calls, 2, "git add and git commit, no other program")

	assert.Equal(t, 2, h.main(args...), "the rule for bo exists")
	assert.Contains(t, h.stderr.String(), "already has a rule")
	for _, pub := range []string{pubB, pubRec, "age1short"} {
		assert.Equal(t, 2, h.main("seat", "add", "cy", pub, "--store", h.store), pub)
	}
	assert.Equal(t, 2, h.main("seat", "add", "cy", "--store", h.store), "the key is required")
}

func TestSeatAddNeedsTheRecoveryKey(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	before, _ := os.ReadFile(filepath.Join(h.store, ".sops.yaml"))
	require.NoError(t, os.Remove(filepath.Join(h.store, "recovery.pub")))
	assert.Equal(t, 2, h.main("seat", "add", "bo", pubB, "--store", h.store))
	assert.Contains(t, h.stderr.String(), "recovery.pub is missing")
	after, _ := os.ReadFile(filepath.Join(h.store, ".sops.yaml"))
	assert.Equal(t, string(before), string(after))
}

func TestSeatAddKeepsTheFileValid(t *testing.T) {
	t.Parallel()
	for name, body := range map[string]string{
		"column-zero list":       "creation_rules:\n- path_regex: ^ada\\.yaml$\n  age: " + pubA + "," + pubRec + "\n",
		"trailing top-level key": "creation_rules:\n  - path_regex: ^ada\\.yaml$\n    age: " + pubA + "," + pubRec + "\nstores:\n  x: 1\n",
		"comments":               "# the rules\ncreation_rules:\n  # ada\n  - path_regex: ^ada\\.yaml$\n    age: " + pubA + "," + pubRec + "\n",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			h := newHarness(t)
			h.sopsYAML(body)
			require.Equal(t, 0, h.main("seat", "add", "bo", pubB, "--store", h.store), h.stderr.String())
			rules := h.rules()
			require.Len(t, rules, 2)
			assert.Equal(t, `^ada\.yaml$`, rules[0]["path_regex"])
			assert.Equal(t, `^bo\.yaml$`, rules[1]["path_regex"])
		})
	}
	h := newHarness(t)
	h.sopsYAML("creation_rules: [unclosed\n")
	assert.Equal(t, 2, h.main("seat", "add", "bo", pubB, "--store", h.store))
	b, _ := os.ReadFile(filepath.Join(h.store, ".sops.yaml"))
	assert.Equal(t, "creation_rules: [unclosed\n", string(b), "an unparsable file is left alone")
}

func TestSeatAddGoesBeforeACatchAll(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.sopsYAML("creation_rules:\n  - path_regex: ^ada\\.yaml$\n    age: " + pubA + "," + pubRec + "\n  - age: " + pubRec + "\n  - path_regex: .*\n    age: " + pubRec + "\n")
	require.Equal(t, 0, h.main("seat", "add", "bo", pubB, "--store", h.store), h.stderr.String())
	rules := h.rules()
	require.Len(t, rules, 4)
	assert.Equal(t, `^bo\.yaml$`, rules[1]["path_regex"], "before the first rule that would shadow it")
	assert.Equal(t, pubRec, rules[2]["age"])
	assert.Equal(t, ".*", rules[3]["path_regex"])
}

func TestStoreAndKeyAreRequired(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.seed("ada", map[string]string{"API_KEY": "sk-sealed-two"})
	h.stdin = "v\n"
	for _, args := range [][]string{
		{"exec", "--as", "ada", "--store", h.store, "--only", "API_KEY", "--", "true"},
	} {
		code := h.main(args...)
		assert.Contains(t, []int{2, 125}, code, "%v", args)
		assert.Contains(t, h.stderr.String(), "missing --")
		assert.Empty(t, h.calls, "%v", args)
	}
}

func TestRelativeKeyIsCheckedWhereSopsWillReadIt(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.seed("ada", map[string]string{"API_KEY": "sk-sealed-two"})
	// The key in the store is world-readable; a private decoy of the same relative name
	// sits in the working directory. Both resolve to the same absolute path.
	require.NoError(t, os.Chmod(h.key, 0o644))
	rel, err := filepath.Rel(mustGetwd(t), h.key)
	require.NoError(t, err)
	require.NotEqual(t, h.key, rel)
	assert.Equal(t, 125, h.main("exec", "--store", h.store, "--as", "ada", "--key", rel, "--only", "API_KEY", "--", "true"))
	assert.Contains(t, h.stderr.String(), "mode 0600")
	assert.Empty(t, h.calls)
	require.NoError(t, os.Chmod(h.key, 0o600))
	require.Equal(t, 0, h.main("exec", "--store", h.store, "--as", "ada", "--key", rel, "--only", "API_KEY", "--", "true"), h.stderr.String())
	assert.Equal(t, h.key, envMap(h.calls[0].env)["SOPS_AGE_KEY_FILE"], "sops is given the absolute path")
}

func mustGetwd(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	require.NoError(t, err)
	return wd
}

func TestExecDefaultsFromTheEnvironment(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.seed("ada", map[string]string{"API_KEY": "sk-sealed-two"})
	h.environ = append(h.environ[:1:1], "NOVA_SECRETS_SEAT=ada", "NOVA_SECRETS_STORE="+h.store, "NOVA_SECRETS_KEY="+h.key)
	require.Equal(t, 0, h.main("exec", "--only", "API_KEY", "--", "true"), h.stderr.String())
	assert.Equal(t, "sk-sealed-two", envMap(h.env)["API_KEY"])
}

func TestDecryptRefusesWhatIsNoEnvironmentVariable(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	plain := `{"GOOD":"v","nested":{"a":"secret-nested"}}`
	require.NoError(t, os.WriteFile(filepath.Join(h.store, "ada.yaml"), []byte("#fake "+hex.EncodeToString([]byte(plain))+"\n"), 0o600))
	assert.Equal(t, 125, h.main(h.seat("exec", "--only", "GOOD", "--", "true")...))
	assert.Contains(t, h.stderr.String(), "\"nested\" is not a single value")
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
	for _, args := range [][]string{{"get"}, {"check"}, {"keygen"}, {"seat"}, {"seat", "inject"}, {"set", "--bogus"}, {"set", "X", "--as", "../x"}} {
		assert.Equal(t, 2, h.main(args...), "%v", args)
	}
}

func (h *harness) gitCalls() [][]string {
	var out [][]string
	for _, c := range h.calls {
		if c.name == "git" && !slices.Contains([]string{"rev-parse", "status", "ls-files", "diff"}, c.args[0]) {
			out = append(out, c.args)
		}
	}
	return out
}

func TestInitCreatesTheStore(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.notRepo = true
	store := filepath.Join(t.TempDir(), "new")
	h.environ = append(h.environ, "NOVA_SECRETS_SEAT=ada")
	require.Equal(t, 0, h.main("init", "--store", store, "--recovery", pubRec), h.stderr.String())
	// the next field is this node's seat add with its key's public half, shell-quoted then field-escaped
	assert.Equal(t, "SECRETS INIT OK store="+store+"\nnext: nova-secrets seat add ada "+h.keyPub+" --store "+store+"\n", h.stdout.String())
	b, err := os.ReadFile(filepath.Join(store, ".sops.yaml"))
	require.NoError(t, err)
	assert.Equal(t, "creation_rules: []\n", string(b))
	b, err = os.ReadFile(filepath.Join(store, "recovery.pub"))
	require.NoError(t, err)
	assert.Equal(t, pubRec+"\n", string(b))
	assert.NoFileExists(t, filepath.Join(store, "README.md"))
	files := []string{".sops.yaml", "recovery.pub"}
	assert.Equal(t, [][]string{{"init", "-q"}, append([]string{"add", "--"}, files...),
		append([]string{"commit", "-q", "-m", "init secrets store", "--"}, files...)}, h.gitCalls(), "git init, since it is no repository")
	h.calls = nil
	require.Equal(t, 0, h.main("seat", "add", "bo", pubB, "--store", store), h.stderr.String(), "the rule list init wrote is one seat add accepts")
}

// TestNextNamesTheGivenStoreAndSeat: a throwaway store's next: lines act on that store and the seat just
// added, never this machine's default store or seat.
func TestNextNamesTheGivenStoreAndSeat(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.notRepo = true
	dir := t.TempDir()
	store, pins := filepath.Join(dir, "store"), filepath.Join(dir, "pins")
	h.environ = append(h.environ, "NOVA_SECRETS_SEAT=ada", "NOVA_SECRETS_KEY="+h.key)
	require.Equal(t, 0, h.main("init", "--store", store, "--pins", pins, "--recovery", pubRec), h.stderr.String())
	assert.Equal(t, "SECRETS INIT OK store="+store+"\nnext: nova-secrets seat add ada "+pubA+" --store "+store+"\n", h.stdout.String(),
		"seat add takes no --pins; the pins init wrote are the ones set reads")
	h.stdout.Reset()
	require.Equal(t, 0, h.main("seat", "add", "here", pubB, "--store", store), h.stderr.String())
	assert.Equal(t, "SECRETS SEAT ADD OK seat=here committed\nnext: nova-secrets set DB_PASSWORD --as here --store "+store+
		" --pins /home/ada/.config/nova-secrets/pins\n", h.stdout.String(), "here's key is not known here: no --key")
	h.stdout.Reset()
	h.environ = append(h.environ, "NOVA_SECRETS_PINS="+pins)
	require.Equal(t, 0, h.main("seat", "add", "ada", pubA, "--store", store), h.stderr.String())
	assert.Equal(t, "SECRETS SEAT ADD OK seat=ada committed\nnext: nova-secrets set DB_PASSWORD --as ada --store "+store+
		" --key "+h.key+" --pins "+pins+"\n", h.stdout.String(), "ada's key is here and is the one added")
}

func TestInitIntoAnEmptyRepositoryOfItsOwn(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	store := filepath.Join(t.TempDir(), "new")
	require.NoError(t, os.MkdirAll(filepath.Join(store, ".git"), 0o755))
	require.Equal(t, 0, h.main("init", "--store", store, "--recovery", pubRec), h.stderr.String())
	assert.Len(t, h.gitCalls(), 2, "no git init: it is its own top level")
}

func TestInitRefusals(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	assert.Equal(t, 2, h.main("init", "--store", h.store, "--recovery", pubRec))
	assert.Contains(t, h.stderr.String(), "store "+h.store+" exists; use: nova-secrets seat add")
	assert.Equal(t, 2, h.main("init"))
	assert.Contains(t, h.stderr.String(), "--recovery <age-public-key>", "every missing flag at once")
	assert.Equal(t, 2, h.main("init", "--store", filepath.Join(t.TempDir(), "a", "b"), "--recovery", pubRec), "never a parent tree")
	assert.Equal(t, 2, h.main("init", "--store", filepath.Join(t.TempDir(), "n"), "--recovery", "AGE-SECRET-KEY-1ABC"))
	assert.Empty(t, h.gitCalls())

	// A project directory is never written into: its README stays.
	proj := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(proj, "README.md"), []byte("mine\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(proj, "main.go"), []byte("x\n"), 0o644))
	assert.Equal(t, 2, h.main("init", "--store", proj, "--recovery", pubRec))
	assert.Contains(t, h.stderr.String(), "is not empty (it holds README.md, main.go)")
	b, _ := os.ReadFile(filepath.Join(proj, "README.md"))
	assert.Equal(t, "mine\n", string(b))
	assert.Empty(t, h.gitCalls())

	// A directory inside another work tree is refused, absent or empty.
	h.top = "/work/parent"
	empty := t.TempDir()
	for _, dir := range []string{empty, filepath.Join(empty, "new")} {
		assert.Equal(t, 2, h.main("init", "--store", dir, "--recovery", pubRec), dir)
		assert.Contains(t, h.stderr.String(), "is inside the git work tree /work/parent")
	}
	assert.Empty(t, h.gitCalls())
	assert.NoDirExists(t, filepath.Join(empty, "new"))
}

func TestNamesListsOnlyRealNamesNeverValues(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	body := "GH_TOKEN: ENC[AES256_GCM,data:sealed-needle,tag:x,type:str]\n" +
		"nested:\n    inner: ENC[x]\n" +
		"\"SECRETS SET OK forged\\e[31m\": ENC[x]\n" +
		"? [a, b]\n: ENC[x]\n" +
		"API_KEY: ENC[AES256_GCM,data:z]\n" + markKey + ": x\nsops:\n    age:\n        - recipient: " + pubA + "\n"
	require.NoError(t, os.WriteFile(filepath.Join(h.store, "ada.yaml"), []byte(body), 0o600))
	require.Equal(t, 0, h.main("names", "--store", h.store, "--as", "ada"), h.stderr.String())
	assert.Equal(t, "GH_TOKEN\nAPI_KEY\n", h.stdout.String(), "the sops block, the old mark and every non-name key are not printed")
	assert.Equal(t, "SECRETS NAMES OK as=ada names=2 refused=3\n", h.stderr.String())
	assert.Empty(t, h.calls, "no program runs, no key is read")
	h.noLeak("sealed-needle")
	assert.Equal(t, 2, h.main("names", "--store", h.store, "--as", "nobody"))
	assert.Contains(t, h.stderr.String(), "no nobody.yaml in "+h.store+": wrong --as? seats here: ada; or create it: nova-secrets set NAME --as nobody")
	assert.Equal(t, 2, h.main("names", "--store", h.store))
}

func TestWrongKeyRemedies(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.keyPub = pubB // the key is pubB
	h.failDec = true
	file := func(rec string) { // ada.yaml with recipients rec
		require.NoError(t, os.WriteFile(filepath.Join(h.store, "ada.yaml"), []byte("A: ENC[x]\nsops:\n    age:\n        - recipient: "+rec+"\n        - recipient: "+pubRec+"\n"), 0o600))
	}
	exec := func() string {
		assert.Equal(t, 125, h.main(h.seat("exec", "--only", "A", "--", "true")...))
		return h.stderr.String()
	}
	// The rule names pubA, the key is pubB.
	file(pubA)
	assert.Equal(t, "SECRETS EXEC REFUSED reason=not-recipient: key "+pubB+" is not the key in .sops.yaml for ada ("+pubA+"): wrong --key/--as, or the key was replaced; edit the seat's age: in .sops.yaml then: cd "+h.store+" && SOPS_AGE_KEY_FILE=<recovery.key> sops updatekeys -y ada.yaml\n", exec())
	// The rule names the key, the file does not: the file is stale.
	h.sopsYAML("creation_rules:\n  - path_regex: ^ada\\.yaml$\n    age: " + pubB + "," + pubRec + "\n")
	file(pubA)
	assert.Contains(t, exec(), "reason=not-recipient: key "+pubB+" is the key in .sops.yaml for ada but not among ada.yaml's 2 recipients: re-key it with: cd "+h.store+" && SOPS_AGE_KEY_FILE=<recovery.key> sops updatekeys -y ada.yaml")
	// Everything agrees: sops's own failure, with the command that ran, pasteable.
	file(pubB)
	got := exec()
	assert.Contains(t, got, "reason=sops: ")
	assert.Contains(t, got, "sops -d failed (exit status 128): no key given opens it (wrong --key, or the file was sealed to other keys); its output is withheld; run: cd "+h.store+" && HOME=$(mktemp -d) SOPS_AGE_KEY_FILE="+h.key+" sops -d --output /dev/null --output-type json ada.yaml")
	// A file changed since it was sealed: sops exit 51, named, with the restore line.
	h.failDec, h.decExit = true, 51
	got = exec()
	assert.Contains(t, got, "sops -d failed (exit status 51): the file changed since it was sealed (MAC mismatch); restore it: git -C "+h.store+" checkout -- ada.yaml, or seal it again; its output is withheld; run: ")
	// A key file that is no age key: said so, instead of "wrong --key".
	h.decExit, h.keyPub = 128, "garbage"
	got = exec()
	assert.Contains(t, got, "sops -d failed (exit status 128): the key file is not an age key (age-keygen -y "+h.key+" prints none)")
	h.failDec, h.decExit, h.keyPub = false, 0, pubB
	// No git on PATH: said so, with the install line, not "not a git repository".
	h.noGit = true
	h.stdin = "value-x-xyz\n"
	assert.Equal(t, 2, h.main(h.set("X")...))
	assert.Contains(t, h.stderr.String(), "SECRETS SET REFUSED reason=git: git not found on PATH; install it: apt install git")
	h.noGit = false
	// No age-keygen: say so.
	h.noAgeKeygen = true
	assert.Contains(t, exec(), "age-keygen not found")
}

func TestOtherRemedies(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.seed("ada", map[string]string{"A": "v"})
	h.sopsMissing = true
	assert.Equal(t, 125, h.main(h.seat("exec", "--only", "A", "--", "true")...))
	assert.Contains(t, h.stderr.String(), "reason=no-sops: sops not found at sops; install sops 3.x")

	h = newHarness(t)
	h.notRepo = true
	h.stdin = "v\n"
	assert.Equal(t, 2, h.main(h.set("NEW")...))
	assert.Contains(t, h.stderr.String(), h.store+" is not a git repository; run: git -C "+h.store+" init && git -C "+h.store+" add -A && git -C "+h.store+" commit -m store")
	assert.Empty(t, h.gitCalls())

	h = newHarness(t)
	h.seed("bo", map[string]string{"B": "v"})
	h.seed("cy", map[string]string{"C": "v"})
	assert.Equal(t, 125, h.main(h.seat("exec", "--only", "A", "--", "true")...))
	assert.Contains(t, h.stderr.String(), "reason=no-seat: no ada.yaml in "+h.store+": wrong --as? seats here: bo,cy; or create it: nova-secrets set NAME --as ada --store "+h.store+" --key <file>")

	h = newHarness(t)
	require.NoError(t, os.Remove(filepath.Join(h.store, "recovery.pub")))
	assert.Equal(t, 2, h.main("seat", "add", "bo", pubB, "--store", h.store))
	assert.Contains(t, h.stderr.String(), h.store+"/recovery.pub is missing or not an age public key; run: age-keygen -y <recovery.key> > "+h.store+"/recovery.pub, then commit it")

	h = newHarness(t)
	require.NoError(t, os.Remove(filepath.Join(h.store, ".sops.yaml")))
	assert.Equal(t, 2, h.main("seat", "add", "bo", pubB, "--store", h.store))
	assert.Contains(t, h.stderr.String(), h.store+"/.sops.yaml is missing; create it with the line: creation_rules: []")
	assert.NotContains(t, h.stderr.String(), "init")

	h = newHarness(t) // no rule matches the new seat: sops cannot create the file
	h.sopsYAML("creation_rules: []\n")
	h.failEnc = true
	h.stdin = "v\n"
	assert.Equal(t, 2, h.main(h.set("NEW")...))
	assert.Contains(t, h.stderr.String(), "no rule for ada.yaml in "+h.store+"/.sops.yaml; run: nova-secrets seat add ada <age-public-key> --store "+h.store)

	h = newHarness(t) // git failing after the file is written says so; git failing in init does not
	h.failGit = true
	h.stdin = "v\n"
	assert.Equal(t, 2, h.main(h.set("NEW")...))
	assert.Contains(t, h.stderr.String(), "reason=git: ")
	assert.Contains(t, h.stderr.String(), "the file is written: run: git -C "+h.store+" status")
	h.notRepo, h.stderr = true, bytes.Buffer{}
	assert.Equal(t, 2, h.main("init", "--store", filepath.Join(t.TempDir(), "n"), "--recovery", pubRec))
	assert.Contains(t, h.stderr.String(), "git init in")
	assert.NotContains(t, h.stderr.String(), "file is written")
}

func TestSetFromDevNullIsAnEmptyValueNotATerminal(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	f, err := os.Open(os.DevNull)
	require.NoError(t, err)
	defer func() { require.NoError(t, f.Close()) }()
	tl := tool{stdin: f, stdout: &h.stdout, stderr: &h.stderr, environ: h.environ, run: h.run}
	assert.Equal(t, 2, tl.main(h.set("NEW")))
	assert.Contains(t, h.stderr.String(), "empty or multi-line value on stdin")
	assert.NotContains(t, h.stderr.String(), "terminal")
}

func TestVerbHelp(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	for verb, other := range map[string]string{"init": "nova-secrets names", "names": "nova-secrets set", "set": "nova-secrets exec", "exec": "nova-secrets init", "seat add": "nova-secrets set"} {
		for _, args := range [][]string{append(strings.Fields(verb), "-h"), append(strings.Fields(verb), "--help"), append([]string{"help"}, strings.Fields(verb)...)} {
			if verb == "exec" && args[0] == "exec" {
				args = append(args, "--only", "X")
			}
			assert.Equal(t, 0, h.main(args...), "%v", args)
			assert.Contains(t, h.stdout.String(), "nova-secrets "+verb+" ", "%v", args)
			assert.NotContains(t, h.stdout.String(), other, "only the verb's own paragraph: %v", args)
			assert.Empty(t, h.calls)
		}
	}
	h.stdout.Reset()
	h.main("exec", "-h")
	assert.NotContains(t, h.stdout.String(), "--require")
	h.seed("ada", map[string]string{"X": "v"})
	h.stdout.Reset()
	assert.Equal(t, 0, h.main(h.seat("exec", "--only", "X", "--", "true", "-h")...), "-h after -- belongs to the command: %s", h.stderr.String())
	assert.Equal(t, []string{"true", "-h"}, h.became)
	assert.Equal(t, 2, h.main("help", "bogus"))
	assert.Equal(t, 2, h.main("seat"))
}

func TestFirstSetChecksTheKeyAgainstTheRule(t *testing.T) {
	t.Parallel()
	h := newHarness(t) // the rule for ada names pubA
	h.keyPub = pubB
	h.stdin = "v-first\n"
	assert.Equal(t, 2, h.main(h.set("NEW")...))
	assert.Contains(t, h.stderr.String(), "key "+pubB+" is not the key in .sops.yaml for ada ("+pubA+"): wrong --key/--as, or the key was replaced")
	assert.NoFileExists(t, filepath.Join(h.store, "ada.yaml"))
	for _, c := range h.calls {
		assert.NotEqual(t, "sops", c.name, "nothing is encrypted under the wrong key")
	}
	h.keyPub = pubA
	h.stdin = "v-first\n"
	require.Equal(t, 0, h.main(h.set("NEW")...), h.stderr.String())
	assert.Equal(t, map[string]string{"NEW": "v-first"}, h.open("ada"))
}

func TestSeatAddWritesABlockListAndCommits(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.environ = append(h.environ, "NOVA_SECRETS_SEAT=ada", "NOVA_SECRETS_KEY="+h.key)
	h.sopsYAML("creation_rules: []\n")
	require.Equal(t, 0, h.main("seat", "add", "bo", pubB, "--store", h.store), h.stderr.String())
	b, err := os.ReadFile(filepath.Join(h.store, ".sops.yaml"))
	require.NoError(t, err)
	assert.Equal(t, "creation_rules:\n  - path_regex: ^bo\\.yaml$\n    age: "+pubB+","+pubRec+"\n", string(b))
	// bo's key is not on this machine: no --key, set reads NOVA_SECRETS_KEY
	assert.Equal(t, "SECRETS SEAT ADD OK seat=bo committed\nnext: nova-secrets set DB_PASSWORD --as bo --store "+h.store+
		" --pins /home/ada/.config/nova-secrets/pins\n", h.stdout.String())
	h.failGit = true
	assert.Equal(t, 2, h.main("seat", "add", "cy", pub3, "--store", h.store))
	assert.Contains(t, h.stderr.String(), ".sops.yaml is written: run: git -C "+h.store+" status")
}

const pub3 = "age1qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqc"

func TestSeatListIsReadOnly(t *testing.T) {
	t.Parallel()
	h := newHarness(t) // a rule for ada; add a rule for bo with no file, and a file for cy with no rule
	h.sopsYAML("creation_rules:\n  - path_regex: ^ada\\.yaml$\n    age: " + pubA + "\n  - path_regex: ^bo\\.yaml$\n    age: " + pubB + "\n  - age: " + pubRec + "\n")
	h.seed("ada", map[string]string{"A": "v"})
	h.seed("cy", map[string]string{"C": "v"})
	require.Equal(t, 0, h.main("seat", "list", "--store", h.store), h.stderr.String())
	assert.Equal(t, "ada file=yes rule=yes\nbo file=no rule=yes\ncy file=yes rule=no\n", h.stdout.String())
	assert.Equal(t, "SECRETS SEAT LIST OK seats=3\n", h.stderr.String())
	assert.Empty(t, h.calls)
	require.NoError(t, os.Remove(filepath.Join(h.store, ".sops.yaml")))
	assert.Equal(t, 2, h.main("seat", "list", "--store", h.store))
	assert.Equal(t, 0, h.main("seat", "list", "-h"))
	assert.Contains(t, h.stdout.String(), "nova-secrets seat list ")
}

func TestUnknownNameSaysHowToListNames(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.seed("ada", map[string]string{"API_KEY": "v"})
	assert.Equal(t, 125, h.main(h.seat("exec", "--only", "NOPE", "--", "true")...))
	assert.Contains(t, h.stderr.String(), "holds no such name; run: nova-secrets names --store "+h.store+" --as ada")
}

func TestSeatVerbsAndAbsentStore(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	assert.Equal(t, 0, h.main("seat", "-h"))
	assert.Contains(t, h.stdout.String(), "nova-secrets seat add ")
	assert.Contains(t, h.stdout.String(), "nova-secrets seat list ")
	assert.Equal(t, 2, h.main("seat", "list", "--store", h.store, "--as", "ada"))
	assert.Equal(t, "nova-secrets seat list: unknown flag --as; run: nova-secrets seat list -h\n", h.stderr.String())
	gone := filepath.Join(t.TempDir(), "gone")
	for _, args := range [][]string{{"seat", "list", "--store", gone}, {"seat", "add", "bo", pubB, "--store", gone}, {"names", "--store", gone, "--as", "ada"}} {
		assert.Equal(t, 2, h.main(args...), "%v", args)
		assert.Contains(t, h.stderr.String(), "store "+gone+" does not exist; run: nova-secrets init --store "+gone+" --recovery <age-public-key>", "%v", args)
	}
	assert.Empty(t, h.calls)
}

func TestSetValueSizeLimit(t *testing.T) {
	t.Parallel()
	for n, ok := range map[int]bool{65536: true, 65537: false, 3 << 20: false} {
		h := newHarness(t)
		h.stdin = strings.Repeat("v", n) + "\n"
		code := h.main(h.set("NEW")...)
		if ok {
			require.Equal(t, 0, code, "%d: %s", n, h.stderr.String())
			assert.Len(t, h.open("ada")["NEW"], n)
			continue
		}
		assert.Equal(t, 2, code, "%d", n)
		assert.Equal(t, "nova-secrets set: value is over 65536 bytes; a secret is a key or token; run: nova-secrets set -h\n", h.stderr.String())
		assert.NoFileExists(t, filepath.Join(h.store, "ada.yaml"), "nothing is written")
	}
	h := newHarness(t) // CRLF after a full-size value is still in range
	h.stdin = strings.Repeat("v", 65536) + "\r\n"
	assert.Equal(t, 0, h.main(h.set("NEW")...), h.stderr.String())
}

func TestExecStripsItsOwnInputsFromTheChild(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.seed("ada", map[string]string{"API_KEY": "v"})
	h.environ = append(h.environ, "NOVA_SECRETS_SEAT=ada", "NOVA_SECRETS_STORE="+h.store, "NOVA_SECRETS_KEY="+h.key)
	require.Equal(t, 0, h.main("exec", "--only", "API_KEY", "--", "true"), h.stderr.String())
	for _, k := range []string{"NOVA_SECRETS_KEY", "NOVA_SECRETS_STORE", "NOVA_SECRETS_SEAT"} {
		assert.NotContains(t, envMap(h.env), k)
	}
	assert.Equal(t, "v", envMap(h.env)["API_KEY"])
}

// endless is a stdin that never ends; set must refuse it after reading a bounded amount.
type endless struct{ read int }

func (e *endless) Read(p []byte) (int, error) {
	e.read += len(p)
	for i := range p {
		p[i] = 'v'
	}
	return len(p), nil
}

func TestSetRefusesAnEndlessStdin(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	in := &endless{}
	tl := tool{stdin: in, stdout: &h.stdout, stderr: &h.stderr, environ: h.environ, run: h.run}
	assert.Equal(t, 2, tl.main(h.set("NEW")))
	assert.Contains(t, h.stderr.String(), "value is over 65536 bytes")
	assert.Less(t, in.read, 1<<20, "it stops reading")
}

// Each way exec can fail to read prints one line with its reason word and no value; the command never runs.
func TestExecRefusalReasons(t *testing.T) {
	t.Parallel()
	for want, c := range map[string]func(h *harness) []string{
		"no-store": func(h *harness) []string {
			return []string{"exec", "--only", "A", "--store", h.store + "-gone", "--key", h.key, "--as", "ada", "--", "true"}
		},
		"no-key": func(h *harness) []string {
			return []string{"exec", "--only", "A", "--store", h.store, "--key", h.key + "-gone", "--as", "ada", "--sops", "sops", "--", "true"}
		},
		"no-seat":    func(h *harness) []string { return h.seat("exec", "--only", "A", "--as", "cy", "--", "true") },
		"no-name":    func(h *harness) []string { return h.seat("exec", "--only", "A,NOPE", "--", "true") },
		"no-command": func(h *harness) []string { return h.seat("exec", "--only", "A", "--", "no-such-program-here") },
		"not-recipient": func(h *harness) []string {
			h.keyPub, h.failDec = pubB, true
			return h.seat("exec", "--only", "A", "--", "true")
		},
		"no-sops": func(h *harness) []string { h.sopsMissing = true; return h.seat("exec", "--only", "A", "--", "true") },
		"sops":    func(h *harness) []string { h.failDec = true; return h.seat("exec", "--only", "A", "--", "true") },
	} {
		t.Run(want, func(t *testing.T) {
			t.Parallel()
			h := newHarness(t)
			h.seed("ada", map[string]string{"A": "a-sealed-one"})
			assert.Equal(t, 125, h.main(c(h)...))
			assert.Regexp(t, `^SECRETS EXEC REFUSED reason=`+want+`: [^\n]+\n$`, h.stderr.String())
			assert.Nil(t, h.became)
			h.noLeak("a-sealed-one")
		})
	}
}

// fresh is a harness for a node with nothing at the defaults: HOME is empty, no NOVA_SECRETS_* but the seat.
func fresh(t *testing.T) (*harness, string) {
	h := newHarness(t)
	home := t.TempDir()
	dir := filepath.Join(home, ".config", "nova-secrets")
	h.store, h.key, h.pins = filepath.Join(dir, "store"), filepath.Join(dir, "ada.key"), filepath.Join(dir, "pins")
	h.environ = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "NOVA_SECRETS_SEAT=ada"}
	h.stdin = fakeKey("api", "FRESH") + "\n"
	return h, dir
}

// On a fresh node, set makes the recovery key, this node's key, the pin, the store and the seat, then sets.
func TestSetBootstrapsAFreshNode(t *testing.T) {
	t.Parallel()
	h, dir := fresh(t)
	require.Equal(t, 0, h.main("set", "TAILSCALE_API_KEY", "--sops", "sops"), h.stderr.String())
	rk := filepath.Join(dir, "recovery.key")
	assert.Equal(t, "SECRETS SET NEW store="+h.store+" seat=ada key="+h.key+": back up "+rk+", the recovery key, off this machine now: it opens every seat if a key is lost\n",
		strings.SplitAfter(h.stdout.String(), "\n")[0])
	assert.Contains(t, h.stdout.String(), "SECRETS SET OK as=ada name=TAILSCALE_API_KEY file=ada.yaml committed")
	for _, k := range []string{rk, h.key} {
		fi, err := os.Stat(k)
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(0o600), fi.Mode().Perm(), k)
	}
	b, err := os.ReadFile(filepath.Join(h.pins, "recovery.pub"))
	require.NoError(t, err)
	assert.Equal(t, pubRec+"\n", string(b))
	b, err = os.ReadFile(filepath.Join(h.store, "recovery.pub"))
	require.NoError(t, err)
	assert.Equal(t, pubRec+"\n", string(b))
	assert.Equal(t, []string{pubA, pubRec}, h.ruleKeys(h.store, "ada.yaml"), "the seat's rule: its key, then the recovery key")
	assert.Equal(t, map[string]string{"TAILSCALE_API_KEY": fakeKey("api", "FRESH")}, h.open("ada"))
	assert.Contains(t, h.gitCalls(), []string{"init", "-q"})
	h.noLeak(fakeKey("api", "FRESH"))
	// the next set finds it all there and makes nothing
	h.stdin = "second\n"
	require.Equal(t, 0, h.main("set", "OTHER", "--sops", "sops"), h.stderr.String())
	assert.NotContains(t, h.stdout.String(), "SECRETS SET NEW")
	// a bad value makes nothing
	h2, dir2 := fresh(t)
	h2.stdin = "\n"
	assert.Equal(t, 2, h2.main("set", "X", "--sops", "sops"))
	assert.NoDirExists(t, dir2)
}

// No store at the default but a key, the recovery key or the pin there: refused, nothing written or overwritten.
func TestSetRefusesAPartialNode(t *testing.T) {
	t.Parallel()
	for _, have := range []string{"ada.key", "recovery.key", "pins/recovery.pub"} {
		h, dir := fresh(t)
		p := filepath.Join(dir, have)
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o700))
		require.NoError(t, os.WriteFile(p, []byte("mine\n"), 0o600))
		assert.Equal(t, 2, h.main("set", "TAILSCALE_API_KEY", "--sops", "sops"), have)
		assert.Contains(t, h.stderr.String(), "SECRETS SET REFUSED reason=partial: there is no store at "+h.store+", but "+p+" exist", have)
		b, _ := os.ReadFile(p) // ignored: it was written above
		assert.Equal(t, "mine\n", string(b), have)
		assert.NoDirExists(t, h.store, have)
		for _, other := range []string{"ada.key", "recovery.key", "pins/recovery.pub"} {
			if other != have {
				assert.NoFileExists(t, filepath.Join(dir, other), have)
			}
		}
		assert.Empty(t, h.calls, have)
	}
	// a store named elsewhere that is not there is no-store, as before: no bootstrap
	h, _ := fresh(t)
	assert.Equal(t, 2, h.main("set", "X", "--store", filepath.Join(t.TempDir(), "elsewhere"), "--sops", "sops"))
	assert.Contains(t, h.stderr.String(), "reason=no-store")
	assert.Empty(t, h.calls)
}
