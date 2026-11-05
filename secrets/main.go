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
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"sort"
	"strings"
)

const usage = `nova-secrets: sops-encrypted secrets in a git store, handed to one command at a time.

The store is a git working copy: .sops.yaml (one creation rule per seat), recovery.pub
(the recovery key every seat file is also sealed to) and <seat>.yaml per seat. A seat's
age key file must be mode 0600 and owned by you. No verb prints a value.

  nova-secrets exec --store <dir> --as <seat> --key <path> --sops <path>
                    --only <NAME,...|all> [--require <NAME>]... -- <cmd> [args...]
      Decrypt <seat>.yaml and become <cmd> with only the --only values added to its
      environment. Exit: the command's own status; 125 when exec refused.
  nova-secrets names --store <dir> --as <seat> [--max <n>]
      List the key names in <seat>.yaml without decrypting it.
  nova-secrets check --store <dir> --as <seat> --key <path> --sops <path>
      Prove the key opens <seat>.yaml. Exit 1 when it does not.
  nova-secrets seal --store <dir> --as <seat> --key <path> --sops <path> --name <NAME> [--stdin]
      Read one value from stdin (one line), set NAME in <seat>.yaml (made when absent,
      under its .sops.yaml rule) and commit the file on the store's current branch.
      Pushing is yours: git -C <dir> push.
  nova-secrets seat add --store <dir> --as <new seat> --pub <age1...> --from <seat>
                    --only <NAME,...> --key <path> --sops <path>
      Add the new seat's rule to .sops.yaml and write <new seat>.yaml holding the
      --only values of --from, which --key opens. Commits nothing.
  nova-secrets keygen --as <seat> --key <path> --age-keygen <path> [--store <dir>]
      Make a new age key at <path> (never over a file) and print its .sops.yaml rule.
  nova-secrets version

Exit codes: 0 done, 1 check failed, 2 refused (exec: 125).
`

// version is set by a release build: -ldflags "-X main.version=v1.2.3".
var version = "dev"

// identityEnv is every variable sops reads an age identity from: none reaches sops
// (it gets --key alone) or the command exec becomes.
var identityEnv = []string{"SOPS_AGE_KEY", "SOPS_AGE_KEY_FILE", "SOPS_AGE_KEY_CMD", "SOPS_AGE_SSH_PRIVATE_KEY_FILE", "SOPS_KEYSERVICE"}

// markKey is the clear provenance line older releases wrote into seat files; it is
// not a secret, so it is never delivered or listed, and a seal drops it.
const markKey = "NOVA_SECRETS_WRITTEN_BY"

var seatName = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

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
}

func main() {
	t := tool{os.Stdin, os.Stdout, os.Stderr, os.Environ(), runProgram, become}
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
	if verb == "seat" {
		if len(rest) == 0 || rest[0] != "add" {
			return t.fail(verb, refuse(2, "the seat verb is: seat add"))
		}
		verb, rest = "seat add", rest[1:]
	}
	f := map[string]func([]string) error{"exec": t.exec, "names": t.names, "check": t.check, "seal": t.seal, "seat add": t.seatAdd, "keygen": t.keygen, "version": t.version}[verb]
	if f == nil {
		return t.fail(verb, refuse(2, "unknown verb %q (exec, names, check, seal, seat add, keygen, version, help)", verb))
	}
	return t.fail(verb, f(rest))
}

func (t tool) fail(verb string, err error) int {
	if err == nil {
		return 0
	}
	code := 2
	var r refusal
	if errors.As(err, &r) {
		code = r.code
	}
	if verb == "exec" && code == 2 {
		code = 125
	}
	fmt.Fprintf(t.stderr, "SECRETS %s REFUSED: %v\n", strings.ToUpper(verb), err)
	return code
}

// opts is every flag the verbs take; each verb names the ones it requires.
type opts struct {
	store, as, key, sops, only, name, pub, from, ageKeygen string
	require                                                []string
}

type list []string

func (l *list) String() string     { return strings.Join(*l, ",") }
func (l *list) Set(v string) error { *l = append(*l, v); return nil }

func parse(verb string, args []string, required ...string) (opts, []string, error) {
	var o opts
	fs := flag.NewFlagSet(verb, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	for name, p := range map[string]*string{"store": &o.store, "as": &o.as, "key": &o.key, "sops": &o.sops,
		"only": &o.only, "name": &o.name, "pub": &o.pub, "from": &o.from, "age-keygen": &o.ageKeygen} {
		fs.StringVar(p, name, "", "")
	}
	fs.Var((*list)(&o.require), "require", "")
	fs.Bool("stdin", false, "") // seal always reads stdin; the flag is accepted for older callers
	fs.Bool("no-pr", false, "") // accepted for older callers: seal never pushes or opens a pull request
	fs.Int("max", 0, "")        // accepted for older callers: names lists every name
	if err := fs.Parse(args); err != nil {
		return o, nil, refuse(2, "%v; run: nova-secrets help", err)
	}
	var missing []string
	for _, r := range required {
		if f := fs.Lookup(r); f.Value.String() == "" {
			missing = append(missing, "--"+r)
		}
	}
	if len(missing) > 0 {
		return o, nil, refuse(2, "missing %s; run: nova-secrets help", strings.Join(missing, ", "))
	}
	for _, s := range []string{o.as, o.from} {
		if s != "" && !seatName.MatchString(s) {
			return o, nil, refuse(2, "seat name %q: letters, digits, - and _ only", s)
		}
	}
	return o, fs.Args(), nil
}

func (o opts) file(seat string) string { return filepath.Join(o.store, seat+".yaml") }

func (t tool) exec(args []string) error {
	i := slices.Index(args, "--")
	if i < 0 || i == len(args)-1 {
		return refuse(2, "no command: nova-secrets exec <flags> -- <cmd> [args...]")
	}
	o, extra, err := parse("exec", args[:i], "store", "as", "key", "sops", "only")
	if err != nil {
		return err
	}
	if len(extra) > 0 {
		return refuse(2, "unexpected %q before --", extra[0])
	}
	argv := args[i+1:]
	if _, err := exec.LookPath(argv[0]); err != nil {
		return refuse(2, "command not found: %s", argv[0])
	}
	values, err := t.decrypt(o, o.as)
	if err != nil {
		return err
	}
	pick := sortedKeys(values)
	if o.only != "all" {
		if pick, err = names(o.only, values, o.file(o.as)); err != nil {
			return err
		}
	}
	for _, r := range o.require {
		if _, ok := values[r]; !ok {
			return refuse(2, "--require %s: not in %s", r, o.file(o.as))
		}
		if !slices.Contains(pick, r) {
			return refuse(2, "--require %s is not in --only", r)
		}
	}
	// The inherited environment loses every name the seat holds (a value left out
	// must not arrive another way) and every sops identity variable.
	drop := append(sortedKeys(values), identityEnv...)
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

// names is the --only list, sorted, every one of which the seat must hold.
func names(only string, values map[string]string, file string) ([]string, error) {
	var pick, missing []string
	for _, n := range strings.Split(only, ",") {
		if n = strings.TrimSpace(n); n == "" || slices.Contains(pick, n) {
			continue
		}
		if _, ok := values[n]; !ok {
			missing = append(missing, n)
		}
		pick = append(pick, n)
	}
	sort.Strings(pick)
	if len(missing) > 0 {
		return nil, refuse(2, "--only names %s, not in %s; run: nova-secrets names", strings.Join(missing, ", "), file)
	}
	return pick, nil
}

func (t tool) names(args []string) error {
	o, _, err := parse("names", args, "store", "as")
	if err != nil {
		return err
	}
	data, err := os.ReadFile(o.file(o.as))
	if err != nil {
		return refuse(2, "seat file %s: %v", o.file(o.as), err)
	}
	n := 0
	for _, line := range strings.Split(string(data), "\n") {
		k, v, ok := strings.Cut(line, ":")
		if !ok || k == "" || strings.ContainsAny(k[:1], " \t#-") || k == "sops" || k == markKey {
			continue
		}
		fmt.Fprintf(t.stdout, "SECRETS NAME key=%s clear=%t\n", k, !strings.HasPrefix(strings.TrimSpace(v), "ENC["))
		n++
	}
	fmt.Fprintf(t.stdout, "SECRETS NAMES OK as=%s keys=%d\n", o.as, n)
	return nil
}

func (t tool) check(args []string) error {
	o, _, err := parse("check", args, "store", "as", "key", "sops")
	if err != nil {
		return err
	}
	values, err := t.decrypt(o, o.as)
	if err != nil {
		return refuse(1, "%v", err)
	}
	fmt.Fprintf(t.stdout, "SECRETS CHECK OK as=%s keys=%d\n", o.as, len(values))
	return nil
}

func (t tool) seal(args []string) error {
	o, _, err := parse("seal", args, "store", "as", "key", "sops", "name")
	if err != nil {
		return err
	}
	if !envName.MatchString(o.name) {
		return refuse(2, "--name %q is not an environment variable name", o.name)
	}
	if f, ok := t.stdin.(*os.File); ok {
		if fi, err := f.Stat(); err == nil && fi.Mode()&os.ModeCharDevice != 0 {
			return refuse(2, "stdin is a terminal: pipe the value in, as: <source> | nova-secrets seal ... --stdin")
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
	values[o.name] = value
	if err := t.encrypt(o, o.as, values); err != nil {
		return err
	}
	file := o.as + ".yaml"
	if err := t.git(o.store, "add", "--", file); err != nil {
		return err
	}
	if err := t.git(o.store, "commit", "-q", "-m", "seal "+o.name+" into "+file, "--", file); err != nil {
		return err
	}
	fmt.Fprintf(t.stdout, "SECRETS SEAL OK as=%s name=%s file=%s committed; run: git -C %s push\n", o.as, o.name, file, o.store)
	return nil
}

var envName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

var agePub = regexp.MustCompile(`^age1[0-9a-z]{58}$`)

func (t tool) seatAdd(args []string) error {
	o, _, err := parse("seat add", args, "store", "as", "pub", "from", "only", "key", "sops")
	if err != nil {
		return err
	}
	if !agePub.MatchString(o.pub) {
		return refuse(2, "--pub is not an age public key (age1...)")
	}
	recovery, err := os.ReadFile(filepath.Join(o.store, "recovery.pub"))
	rec := strings.TrimSpace(string(recovery))
	if err != nil || !agePub.MatchString(rec) {
		return refuse(2, "store %s has no valid recovery.pub", o.store)
	}
	if _, err := os.Stat(o.file(o.as)); err == nil {
		return refuse(2, "%s already exists; seat add never overwrites a seat", o.file(o.as))
	}
	cfgPath := filepath.Join(o.store, ".sops.yaml")
	cfg, err := os.ReadFile(cfgPath)
	if err != nil || !bytes.Contains(cfg, []byte("creation_rules:")) {
		return refuse(2, "store %s has no .sops.yaml with creation_rules:", o.store)
	}
	rule := `^` + regexp.QuoteMeta(o.as) + `\.yaml$`
	if bytes.Contains(cfg, []byte(rule)) || bytes.Contains(cfg, []byte(o.pub)) || o.pub == rec {
		return refuse(2, ".sops.yaml already has a rule for %s or a seat with that key", o.as)
	}
	values, err := t.decrypt(o, o.from)
	if err != nil {
		return err
	}
	pick, err := names(o.only, values, o.file(o.from))
	if err != nil {
		return err
	}
	carried := map[string]string{}
	for _, k := range pick {
		carried[k] = values[k]
	}
	// The rule goes in first: sops picks the recipients from it.
	updated := string(cfg)
	if !strings.HasSuffix(updated, "\n") {
		updated += "\n"
	}
	updated += fmt.Sprintf("  - path_regex: %s\n    age: %s,%s\n", rule, o.pub, rec)
	if err := writeFile(cfgPath, []byte(updated), 0o644); err != nil {
		return err
	}
	if err := t.encrypt(o, o.as, carried); err != nil {
		if rerr := writeFile(cfgPath, cfg, 0o644); rerr != nil {
			return refuse(2, "%v, and .sops.yaml could not be put back (%v): run: git -C %s checkout -- .sops.yaml", err, rerr, o.store)
		}
		return err
	}
	fmt.Fprintf(t.stdout, "SECRETS SEAT ADD OK as=%s from=%s keys=%d file=%s.yaml; commit .sops.yaml and the file\n", o.as, o.from, len(carried), o.as)
	return nil
}

func (t tool) keygen(args []string) error {
	o, _, err := parse("keygen", args, "as", "key", "age-keygen")
	if err != nil {
		return err
	}
	if fi, err := os.Stat(filepath.Dir(o.key)); err != nil || !fi.IsDir() {
		return refuse(2, "key directory %s is absent; run: mkdir -m 700 -p %s", filepath.Dir(o.key), filepath.Dir(o.key))
	}
	if _, err := os.Lstat(o.key); err == nil {
		return refuse(2, "key file %s already exists; keygen never overwrites a key", o.key)
	}
	rec := "<recovery key>"
	if o.store != "" {
		b, err := os.ReadFile(filepath.Join(o.store, "recovery.pub"))
		if rec = strings.TrimSpace(string(b)); err != nil || !agePub.MatchString(rec) {
			return refuse(2, "store %s has no valid recovery.pub", o.store)
		}
	}
	if _, err := t.run(nil, nil, "", o.ageKeygen, "-o", o.key); err != nil {
		return refuse(2, "age-keygen failed (%v); its output is withheld", exitOf(err))
	}
	if err := os.Chmod(o.key, 0o600); err != nil {
		return refuse(2, "chmod 600 %s: %v", o.key, err)
	}
	out, err := t.run(nil, nil, "", o.ageKeygen, "-y", o.key)
	pub := strings.TrimSpace(string(out))
	if err != nil || !agePub.MatchString(pub) {
		return refuse(2, "age-keygen -y %s gave no public key", o.key)
	}
	fmt.Fprintf(t.stdout, "SECRETS RULE   creation_rules:\nSECRETS RULE     - path_regex: ^%s\\.yaml$\nSECRETS RULE       age: %s,%s\n", regexp.QuoteMeta(o.as), pub, rec)
	fmt.Fprintf(t.stdout, "SECRETS KEYGEN OK as=%s key=%s mode=0600 pub=%s\n", o.as, o.key, pub)
	return nil
}

func (t tool) version(args []string) error {
	if len(args) > 0 {
		return refuse(2, "version takes no arguments")
	}
	fmt.Fprintf(t.stdout, "nova-secrets %s %s/%s %s\n", version, runtime.GOOS, runtime.GOARCH, runtime.Version())
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

func sortedKeys(m map[string]string) []string {
	k := make([]string, 0, len(m))
	for n := range m {
		k = append(k, n)
	}
	sort.Strings(k)
	return k
}
