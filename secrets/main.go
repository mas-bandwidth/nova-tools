// Command nova-secrets keeps secrets sops-encrypted in a git working copy (the
// store), one file per seat, and hands the values a command asks for by name to
// that command alone, in its environment. Values travel stdin -> sops -> the
// child's environment only: never an argument, a log line, an error or a file in
// the clear. sops and age-keygen do the cryptography.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

const usage = `nova-secrets: sops-encrypted secrets in a git store, handed to one command at a time.

  nova-secrets exec --only NAME[,NAME] --store <dir> --key <file> [--as <seat>] [--sops <path>] -- <cmd> [args...]
      Decrypt <seat>.yaml and become <cmd> with only the --only values added to its
      environment. Exit: the command's own status; 125 when exec refused.
  nova-secrets set NAME --store <dir> --key <file> [--as <seat>] [--sops <path>]
      Read one value from stdin (one line), set NAME in <seat>.yaml and commit it.
  nova-secrets seat add <seat> <age-public-key> --store <dir>
      Add the seat's recipient rule to .sops.yaml. Commits nothing.
  nova-secrets help

--store and --key are required (or NOVA_SECRETS_STORE / NOVA_SECRETS_KEY); the current
directory is never assumed. --as defaults to $NOVA_SECRETS_SEAT, else the short hostname;
--sops to sops on PATH. No verb prints a value.
Exit codes: 0 done, 2 refused (exec: 125).
`

// identityEnv is every variable sops reads an age identity from: none reaches sops
// (it gets --key alone) or the command exec becomes.
var identityEnv = []string{"SOPS_AGE_KEY", "SOPS_AGE_KEY_FILE", "SOPS_AGE_KEY_CMD", "SOPS_AGE_SSH_PRIVATE_KEY_FILE", "SOPS_KEYSERVICE"}

// markKey is a clear provenance line older releases wrote into seat files; it is
// not a secret, so it is never delivered, and a set drops it.
const markKey = "NOVA_SECRETS_WRITTEN_BY"

var (
	seatName = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
	envName  = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	agePub   = regexp.MustCompile(`^age1[0-9a-z]{58}$`)
)

// tool is nova-secrets with its outside world: the program runner and process
// replacement are fields so unit tests can stand in for sops, git and exec.
type tool struct {
	stdin          io.Reader
	stdout, stderr io.Writer
	environ        []string
	// run starts name with args in dir (the current one when ""), env and stdin, and
	// returns its stdout. Its stderr is never shown: it can carry a value.
	run    func(stdin io.Reader, env []string, dir, name string, args ...string) ([]byte, error)
	become func(argv, env []string) error
	// lookPath finds the command exec will become.
	lookPath func(name string) (string, error)
}

func main() {
	t := tool{os.Stdin, os.Stdout, os.Stderr, os.Environ(), runProgram, become, exec.LookPath}
	os.Exit(t.main(os.Args[1:]))
}

func runProgram(stdin io.Reader, env []string, dir, name string, args ...string) ([]byte, error) {
	cmd := exec.Command(name, args...)
	cmd.Stdin, cmd.Env, cmd.Dir = stdin, env, dir
	return cmd.Output()
}

type refusal struct {
	code int
	msg  string
}

func (r refusal) Error() string { return r.msg }

func refuse(code int, format string, a ...any) error {
	return refusal{code, fmt.Sprintf(format, a...)}
}

func (t tool) main(args []string) int {
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		fmt.Fprint(t.stdout, usage)
		return 0
	}
	verb, rest := args[0], args[1:]
	var err error
	switch verb {
	case "exec":
		err = t.exec(rest)
	case "set", "seal": // seal is the old name of set, kept for one release
		verb = "set"
		err = t.set(rest)
	case "seat":
		if len(rest) == 0 || rest[0] != "add" {
			err = refuse(2, "the seat verb is: seat add <seat> <age-public-key>")
		} else {
			verb, err = "seat add", t.seatAdd(rest[1:])
		}
	default:
		err = refuse(2, "unknown verb %q (exec, set, seat add, help)", verb)
	}
	if err == nil {
		return 0
	}
	code := 2
	var r refusal
	if errors.As(err, &r) {
		code = r.code
	}
	if verb == "exec" {
		code = 125
	}
	fmt.Fprintf(t.stderr, "SECRETS %s REFUSED: %v\n", strings.ToUpper(verb), err)
	return code
}

// opts is the flags the verbs share.
type opts struct{ store, as, key, sops, only string }

type list []string

func (l *list) String() string     { return strings.Join(*l, ",") }
func (l *list) Set(v string) error { *l = append(*l, v); return nil }

// parse reads flags after the verb's positional arguments (npos of them) and
// fills in the defaults. require is the deprecated --require (exec only).
func (t tool) parse(verb string, args []string, npos int, needKey bool) (o opts, pos, require []string, err error) {
	for len(pos) < npos && len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		pos, args = append(pos, args[0]), args[1:]
	}
	fs := flag.NewFlagSet(verb, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	for name, p := range map[string]*string{"store": &o.store, "as": &o.as, "key": &o.key, "sops": &o.sops, "only": &o.only} {
		fs.StringVar(p, name, "", "")
	}
	fs.Var((*list)(&require), "require", "")
	fs.String("name", "", "") // seal's old spelling of set's NAME
	fs.Bool("stdin", false, "")
	fs.Bool("no-pr", false, "")
	if err = fs.Parse(args); err != nil {
		return o, nil, nil, refuse(2, "%v; run: nova-secrets help", err)
	}
	if n := fs.Lookup("name").Value.String(); n != "" {
		pos = append(pos, n)
	}
	if pos = append(pos, fs.Args()...); len(pos) != npos {
		return o, nil, nil, refuse(2, "%s takes %d argument(s) before its flags; run: nova-secrets help", verb, npos)
	}
	if o.store == "" {
		o.store = envValue(t.environ, "NOVA_SECRETS_STORE")
	}
	if o.store == "" {
		return o, nil, nil, refuse(2, "missing --store <dir> (or NOVA_SECRETS_STORE): there is no default, the current directory is never assumed")
	}
	if o.sops == "" {
		o.sops = "sops"
	}
	if o.as == "" {
		if o.as = envValue(t.environ, "NOVA_SECRETS_SEAT"); o.as == "" {
			h, _ := os.Hostname() // an empty name fails the seat check below
			o.as, _, _ = strings.Cut(h, ".")
		}
	}
	if !seatName.MatchString(o.as) {
		return o, nil, nil, refuse(2, "seat name %q: letters, digits, - and _ only", o.as)
	}
	if needKey {
		if o.key == "" {
			o.key = envValue(t.environ, "NOVA_SECRETS_KEY")
		}
		if o.key == "" {
			return o, nil, nil, refuse(2, "missing --key <file> (or NOVA_SECRETS_KEY): there is no default")
		}
		// Absolute before the mode check and before sops (which runs in the store).
		if o.key, err = filepath.Abs(o.key); err != nil {
			return o, nil, nil, refuse(2, "key path %q cannot be made absolute: %v", o.key, err)
		}
	}
	return o, pos, require, nil
}

func envValue(env []string, name string) string {
	for _, kv := range env {
		if k, v, _ := strings.Cut(kv, "="); k == name {
			return v
		}
	}
	return ""
}

func (o opts) file(seat string) string { return filepath.Join(o.store, seat+".yaml") }

func (t tool) exec(args []string) error {
	i := slices.Index(args, "--")
	if i < 0 || i == len(args)-1 {
		return refuse(2, "no command: nova-secrets exec --only NAME -- <cmd> [args...]")
	}
	o, _, require, err := t.parse("exec", args[:i], 0, true)
	if err != nil {
		return err
	}
	if o.only == "" {
		return refuse(2, "missing --only NAME[,NAME]; run: nova-secrets help")
	}
	argv := args[i+1:]
	if _, err := t.lookPath(argv[0]); err != nil {
		return refuse(2, "command not found: %s", argv[0])
	}
	values, err := t.decrypt(o, o.as)
	if err != nil {
		return err
	}
	var pick, missing []string
	for _, n := range strings.Split(o.only, ",") {
		if n = strings.TrimSpace(n); n == "" || slices.Contains(pick, n) {
			continue
		}
		if _, ok := values[n]; !ok {
			missing = append(missing, n)
		}
		pick = append(pick, n)
	}
	if len(missing) > 0 {
		return refuse(2, "unknown secret name(s) %s: %s holds no such name", strings.Join(missing, ", "), o.file(o.as))
	}
	for _, r := range require { // every --only name is already required; --require must agree with --only
		if !slices.Contains(pick, r) {
			return refuse(2, "--require %s is not in --only", r)
		}
	}
	// The inherited environment loses every name the seat holds (a value left out
	// must not arrive another way) and every sops identity variable.
	drop := slices.Concat(slices.Collect(maps.Keys(values)), identityEnv)
	var env []string
	for _, kv := range t.environ {
		k, _, _ := strings.Cut(kv, "=")
		if !slices.ContainsFunc(drop, func(d string) bool { return d == k || runtime.GOOS == "windows" && strings.EqualFold(d, k) }) {
			env = append(env, kv)
		}
	}
	for _, k := range pick {
		env = append(env, k+"="+values[k])
	}
	fmt.Fprintf(t.stderr, "SECRETS EXEC OK as=%s keys=%d cmd=%s\n", o.as, len(pick), filepath.Base(argv[0]))
	if err := t.become(argv, env); err != nil {
		return refuse(2, "exec %s: %v; the command never started", argv[0], err)
	}
	return nil
}

func (t tool) set(args []string) error {
	o, pos, _, err := t.parse("set", args, 1, true)
	if err != nil {
		return err
	}
	name := pos[0]
	if !envName.MatchString(name) {
		return refuse(2, "NAME %q is not an environment variable name", name)
	}
	if f, ok := t.stdin.(*os.File); ok {
		if fi, err := f.Stat(); err == nil && fi.Mode()&os.ModeCharDevice != 0 {
			return refuse(2, "stdin is a terminal: pipe the value in, as: <source> | nova-secrets set NAME")
		}
	}
	raw, err := io.ReadAll(io.LimitReader(t.stdin, 1<<20))
	if err != nil {
		return refuse(2, "reading the value from stdin: %v", err)
	}
	value := strings.TrimSuffix(strings.TrimSuffix(string(raw), "\n"), "\r")
	if value == "" || strings.ContainsAny(value, "\r\n\x00") {
		return refuse(2, "the value on stdin must be one non-empty line")
	}
	values := map[string]string{} // a seat's first value starts its file
	if _, err := os.Stat(o.file(o.as)); err == nil {
		if values, err = t.decrypt(o, o.as); err != nil {
			return err
		}
	}
	values[name] = value
	if err := t.encrypt(o, o.as, values); err != nil {
		return err
	}
	file := o.as + ".yaml"
	if err := t.git(o.store, "add", "--", file); err != nil {
		return err
	}
	if err := t.git(o.store, "commit", "-q", "-m", "set "+name+" in "+file, "--", file); err != nil {
		return err
	}
	fmt.Fprintf(t.stdout, "SECRETS SET OK as=%s name=%s file=%s committed; run: git -C %s push\n", o.as, name, file, o.store)
	return nil
}

func (t tool) seatAdd(args []string) error {
	o, pos, _, err := t.parse("seat add", args, 2, false)
	if err != nil {
		return err
	}
	seat, pub := pos[0], pos[1]
	if !seatName.MatchString(seat) || !agePub.MatchString(pub) {
		return refuse(2, "usage: seat add <seat> <age1...>; the seat is letters, digits, - and _")
	}
	b, err := os.ReadFile(filepath.Join(o.store, "recovery.pub"))
	rec := strings.TrimSpace(string(b))
	if err != nil || !agePub.MatchString(rec) {
		return refuse(2, "store %s has no valid recovery.pub", o.store)
	}
	if rec == pub {
		return refuse(2, "that key is the recovery key, not a seat's")
	}
	cfgPath := filepath.Join(o.store, ".sops.yaml")
	cfg, err := os.ReadFile(cfgPath)
	if err != nil {
		return refuse(2, "store %s has no .sops.yaml", o.store)
	}
	out, err := addRule(cfg, seat, pub+","+rec)
	if err != nil {
		return err
	}
	if err := writeFile(cfgPath, out, 0o644); err != nil {
		return err
	}
	fmt.Fprintf(t.stdout, "SECRETS SEAT ADD OK seat=%s; commit .sops.yaml, then the seat's own key runs: nova-secrets set NAME --as %s\n", seat, seat)
	return nil
}

// sopsEnv is sops's whole environment: PATH, the one key file, and a HOME and config
// directory of its own, so no identity of the caller's (keys.txt included) is read.
func (t tool) sopsEnv(key, home string) []string {
	env := []string{"SOPS_AGE_KEY_FILE=" + key, "HOME=" + home, "XDG_CONFIG_HOME=" + home, "APPDATA=" + home}
	for _, kv := range t.environ {
		if k, _, _ := strings.Cut(kv, "="); slices.Contains([]string{"PATH", "TMPDIR", "SYSTEMROOT", "TEMP", "TMP"}, strings.ToUpper(k)) {
			env = append(env, kv)
		}
	}
	return env
}

// sopsRun runs sops in the store with --key as its only identity, after the key
// file's mode and owner check.
func (t tool) sopsRun(o opts, stdin io.Reader, args ...string) ([]byte, error) {
	if err := checkKey(o.key); err != nil {
		return nil, err
	}
	home, err := os.MkdirTemp("", "nova-secrets-*")
	if err != nil {
		return nil, refuse(2, "temporary directory: %v", err)
	}
	defer func() { _ = os.RemoveAll(home) }() // ignored: an empty directory of our own; failing leaves only it
	out, err := t.run(stdin, t.sopsEnv(o.key, home), o.store, o.sops, args...)
	if err != nil {
		return nil, refuse(2, "sops %s failed (%v); its output is withheld: run sops yourself to see why", args[0], exitOf(err))
	}
	return out, nil
}

// decrypt opens <seat>.yaml into its flat name -> value map.
func (t tool) decrypt(o opts, seat string) (map[string]string, error) {
	file := seat + ".yaml"
	if _, err := os.Stat(o.file(seat)); err != nil {
		return nil, refuse(2, "seat file %s is absent (a new seat starts with: nova-secrets seat add)", o.file(seat))
	}
	out, err := t.sopsRun(o, nil, "-d", "--output-type", "json", file)
	if err != nil {
		return nil, err
	}
	var raw map[string]any
	dec := json.NewDecoder(bytes.NewReader(out))
	dec.UseNumber()
	if err := dec.Decode(&raw); err != nil {
		return nil, refuse(2, "%s did not decrypt to a map of names", file)
	}
	values := map[string]string{}
	for k, v := range raw {
		if k == markKey || k == "sops" {
			continue
		}
		var s string
		switch v := v.(type) {
		case string:
			s = v
		case json.Number:
			s = v.String()
		case bool:
			s = fmt.Sprint(v)
		case nil:
		default: // a nested value is no environment variable; the value is not shown
			return nil, refuse(2, "%s: %s is not a single value", file, k)
		}
		if !envName.MatchString(k) || strings.Contains(s, "\x00") {
			return nil, refuse(2, "%s: %q is not an environment variable name, or its value holds a NUL", file, k)
		}
		values[k] = s
	}
	return values, nil
}

// encrypt writes values to <seat>.yaml, the plaintext on sops's stdin only.
func (t tool) encrypt(o opts, seat string, values map[string]string) error {
	plain, err := json.Marshal(values)
	if err != nil {
		return refuse(2, "encoding the seat: %v", err)
	}
	out, err := t.sopsRun(o, bytes.NewReader(plain), "-e", "--filename-override", seat+".yaml", "--input-type", "json", "--output-type", "yaml", "/dev/stdin")
	if err != nil {
		return err
	}
	return writeFile(o.file(seat), out, 0o600)
}

func (t tool) git(dir string, args ...string) error {
	var env []string
	for _, kv := range t.environ {
		if k, _, _ := strings.Cut(kv, "="); !slices.Contains([]string{"GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE"}, k) {
			env = append(env, kv)
		}
	}
	if _, err := t.run(nil, env, dir, "git", args...); err != nil {
		return refuse(2, "git %s in %s failed (%v); the file is written: run: git -C %s status", args[0], dir, exitOf(err), dir)
	}
	return nil
}

// writeFile replaces path through a temporary file beside it and a rename.
func writeFile(path string, data []byte, mode os.FileMode) error {
	tmp := filepath.Join(filepath.Dir(path), "."+filepath.Base(path)+".tmp")
	_ = os.Remove(tmp) // ignored: a leftover from a failed run, or nothing
	err := os.WriteFile(tmp, data, mode)
	if err == nil {
		err = os.Rename(tmp, path)
	}
	if err != nil {
		_ = os.Remove(tmp) // ignored: the write already failed, and that is the error reported
		return refuse(2, "writing %s: %v", path, err)
	}
	return nil
}

// exitOf names how a program failed without its output.
func exitOf(err error) string {
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return fmt.Sprintf("exit %d", ee.ExitCode())
	}
	return err.Error()
}

// addRule parses .sops.yaml and inserts the seat's rule before the first rule that
// would shadow it (sops uses the first matching rule), keeping every existing rule.
func addRule(cfg []byte, seat, age string) ([]byte, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(cfg, &doc); err != nil || doc.Kind != yaml.DocumentNode || len(doc.Content) != 1 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, refuse(2, ".sops.yaml does not parse as a YAML map; it is left as it was")
	}
	var rules *yaml.Node
	top := doc.Content[0]
	for i := 0; i+1 < len(top.Content); i += 2 {
		if top.Content[i].Value == "creation_rules" && top.Content[i+1].Kind == yaml.SequenceNode {
			rules = top.Content[i+1]
		}
	}
	if rules == nil {
		return nil, refuse(2, ".sops.yaml has no creation_rules list; it is left as it was")
	}
	pathRegex := `^` + regexp.QuoteMeta(seat) + `\.yaml$`
	file := seat + ".yaml"
	at := len(rules.Content)
	for i, r := range rules.Content {
		if r.Kind != yaml.MappingNode {
			return nil, refuse(2, ".sops.yaml: a creation rule is not a map; it is left as it was")
		}
		shadows := true // a rule without path_regex matches every file
		for j := 0; j+1 < len(r.Content); j += 2 {
			switch r.Content[j].Value {
			case "path_regex":
				v := r.Content[j+1].Value
				if v == pathRegex {
					return nil, refuse(2, ".sops.yaml already has a rule for %s", seat)
				}
				re, err := regexp.Compile(v)
				shadows = v == "" || err == nil && re.MatchString(file)
			case "age":
				if strings.Contains(r.Content[j+1].Value, strings.SplitN(age, ",", 2)[0]) {
					return nil, refuse(2, ".sops.yaml already has a seat with that key")
				}
			}
		}
		if shadows && at == len(rules.Content) {
			at = i
		}
	}
	rule := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map", Content: []*yaml.Node{
		{Kind: yaml.ScalarNode, Tag: "!!str", Value: "path_regex"}, {Kind: yaml.ScalarNode, Tag: "!!str", Value: pathRegex},
		{Kind: yaml.ScalarNode, Tag: "!!str", Value: "age"}, {Kind: yaml.ScalarNode, Tag: "!!str", Value: age},
	}}
	rules.Content = slices.Insert(rules.Content, at, rule)
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(&doc); err != nil || enc.Close() != nil {
		return nil, refuse(2, "writing .sops.yaml: %v", err)
	}
	return buf.Bytes(), nil
}
