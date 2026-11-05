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

// usage is the whole help. A verb's -h prints the paragraph that starts with its
// name; nova-secrets help prints everything.
const usage = `nova-secrets: sops-encrypted secrets in a git store, handed to one command at a time.
A new store takes four commands, in this order: init, seat add, set, exec.

nova-secrets init --store <dir> --recovery <age-public-key>
  Create a store in <dir>, which must be absent or empty (its parent must exist) and not inside another git
  work tree: git init, .sops.yaml with no rules, recovery.pub, one commit. Refuses a store that exists.
  The recovery key is a public key (age-keygen -y <file>); keep its private half offline.
  Prints: SECRETS INIT OK store=<dir> next: nova-secrets seat add <seat> <age-pubkey> --store <dir>

nova-secrets seat add <seat> <age-public-key> --store <dir>
  Add the seat's rule to .sops.yaml: its key and the recovery key may open <seat>.yaml. Commits .sops.yaml.
  The seat is letters, digits, - and _.

nova-secrets seat list --store <dir>
  List the seats the rules in .sops.yaml name and the seat files present, one "<seat> file=yes|no rule=yes|no" per line.

nova-secrets set NAME --store <dir> --key <file> [--as <seat>] [--sops <path>]
  Read one value from stdin (one non-empty line of at most 65536 bytes, never a terminal), set NAME in <seat>.yaml and commit that
  file. Pushing is yours. Example: pass show db | nova-secrets set DB_PASSWORD --store S --key K --as web

nova-secrets names --store <dir> [--as <seat>]
  List the names in <seat>.yaml, one per line, from the file's plaintext keys. No key, no sops, no value read.
  A key that is no environment variable name is not printed and is counted as refused=<n> on stderr.

nova-secrets exec --only NAME[,NAME] --store <dir> --key <file> [--as <seat>] [--sops <path>] -- <cmd> [args...]
  Decrypt <seat>.yaml and become <cmd> with only the --only values added to its environment.
  Exit: the command's own status; 125 when exec refused at run time and the command never ran.

--store and --key are required where a verb lists them (or NOVA_SECRETS_STORE / NOVA_SECRETS_KEY); the
current directory is never assumed. --as defaults to $NOVA_SECRETS_SEAT, else the short hostname; --sops
to sops on PATH. No verb prints a value. Exit codes: 0 done, 2 refused (exec: 125).
`

// helpFor is the paragraph for a verb, or the whole text for "".
func helpFor(verb string) (string, bool) {
	if verb == "" {
		return usage, true
	}
	var out string
	for _, p := range strings.SplitAfter(usage, "\n\n") {
		if strings.HasPrefix(p, "nova-secrets "+verb+" ") {
			out += p
		}
	}
	return out, out != ""
}

// identityEnv is every variable that names an age identity or this tool's own inputs: none
// reaches sops (it gets --key alone) or the command exec becomes.
var identityEnv = []string{"NOVA_SECRETS_KEY", "NOVA_SECRETS_STORE", "NOVA_SECRETS_SEAT", "SOPS_AGE_KEY", "SOPS_AGE_KEY_FILE", "SOPS_AGE_KEY_CMD", "SOPS_AGE_SSH_PRIVATE_KEY_FILE", "SOPS_KEYSERVICE"}

// markKey is a clear provenance line older releases wrote into seat files; it is
// not a secret, so it is never delivered, and a set drops it.
// maxValue is the longest secret set accepts: a key or a token. A longer one would also
// overflow the environment exec hands a command.
const maxValue = 65536

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
	code  int
	msg   string
	usage bool // a malformed invocation: "nova-secrets <verb>: <msg>; run: nova-secrets <verb> -h"
	sops  bool // sops itself failed
}

func (r refusal) Error() string { return r.msg }

func refuse(format string, a ...any) error {
	return refusal{2, fmt.Sprintf(format, a...), false, false}
}

// bad is a malformed invocation: exit 2, even for exec.
func bad(format string, a ...any) error { return refusal{2, fmt.Sprintf(format, a...), true, false} }

func (t tool) main(args []string) int {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		fmt.Fprint(t.stdout, usage)
		return 0
	}
	verb, rest := args[0], args[1:]
	if verb == "help" {
		verb, rest = strings.Join(rest, " "), nil
		if h, ok := helpFor(verb); ok {
			fmt.Fprint(t.stdout, h)
			return 0
		}
		return t.unknownVerb(verb)
	}
	if verb == "seat" && len(rest) > 0 && (rest[0] == "add" || rest[0] == "list") {
		verb, rest = "seat "+rest[0], rest[1:]
	}
	if h, ok := helpFor(verb); ok {
		if a := beforeDashes(verb, rest); slices.Contains(a, "-h") || slices.Contains(a, "--help") {
			fmt.Fprint(t.stdout, h)
			return 0
		}
	}
	var err error
	switch verb {
	case "init":
		err = t.initStore(rest)
	case "names":
		err = t.names(rest)
	case "exec":
		err = t.exec(rest)
	case "set":
		err = t.set(rest)
	case "seat add":
		err = t.seatAdd(rest)
	case "seat list":
		err = t.seatList(rest)
	case "seat":
		verb, err = "seat add", bad("the seat verbs are: seat add <seat> <age-public-key> --store <dir>, and seat list --store <dir>")
	default:
		return t.unknownVerb(verb)
	}
	if err == nil {
		return 0
	}
	var r refusal
	errors.As(err, &r)
	if r.usage {
		fmt.Fprintf(t.stderr, "nova-secrets %s: %s; run: nova-secrets %s -h\n", verb, r.msg, verb)
		return 2
	}
	fmt.Fprintf(t.stderr, "SECRETS %s REFUSED: %v\n", strings.ToUpper(verb), err)
	if verb == "exec" {
		return 125
	}
	return 2
}

func (t tool) unknownVerb(verb string) int {
	fmt.Fprintf(t.stderr, "nova-secrets: unknown verb %q (init, seat add, seat list, set, names, exec); run: nova-secrets help\n", verb)
	return 2
}

func beforeDashes(verb string, args []string) []string {
	if i := slices.Index(args, "--"); verb == "exec" && i >= 0 {
		return args[:i]
	}
	return args
}

type opts struct{ store, as, key, sops, only, recovery string }

// parse reads flags after the verb's positional arguments (npos of them) and
// fills in the defaults; also names inputs the caller found missing, so one refusal names them all.
func (t tool) parse(verb string, args []string, npos int, needKey bool, also ...string) (o opts, pos []string, err error) {
	for len(pos) < npos && len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		pos, args = append(pos, args[0]), args[1:]
	}
	fs := flag.NewFlagSet(verb, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	for name, p := range map[string]*string{"store": &o.store, "as": &o.as, "key": &o.key, "sops": &o.sops, "only": &o.only, "recovery": &o.recovery} {
		fs.StringVar(p, name, "", "")
	}
	fs.Func("require", "", func(v string) error { o.only += "," + v; return nil }) // hidden alias of --only, for existing units
	if err = fs.Parse(args); err != nil {
		msg := err.Error()
		if n, ok := strings.CutPrefix(msg, "flag provided but not defined: "); ok {
			msg = "unknown flag --" + strings.TrimLeft(n, "-")
		} else if n, ok := strings.CutPrefix(msg, "flag needs an argument: "); ok {
			msg = "--" + strings.TrimLeft(n, "-") + " needs a value"
		}
		return o, nil, bad("%s", msg)
	}
	if pos = append(pos, fs.Args()...); len(pos) != npos {
		return o, nil, bad("takes %d argument(s) before its flags, got %d", npos, len(pos))
	}
	var missing []string
	if verb == "exec" && o.only == "" {
		missing = append(missing, "--only NAME[,NAME]")
	}
	if o.store == "" {
		if o.store = envValue(t.environ, "NOVA_SECRETS_STORE"); o.store == "" {
			missing = append(missing, "--store <dir> (or NOVA_SECRETS_STORE)")
		}
	}
	if verb == "init" && o.recovery == "" {
		missing = append(missing, "--recovery <age-public-key>")
	}
	if needKey && o.key == "" {
		if o.key = envValue(t.environ, "NOVA_SECRETS_KEY"); o.key == "" {
			missing = append(missing, "--key <file> (or NOVA_SECRETS_KEY)")
		}
	}
	if missing = append(missing, also...); len(missing) > 0 {
		return o, nil, bad("missing %s (no default; the current directory is never assumed)", strings.Join(missing, ", "))
	}
	if _, err := os.Stat(o.store); err != nil && verb != "init" {
		return o, nil, refuse("store %s does not exist; run: nova-secrets init --store %s --recovery <age-public-key>", o.store, o.store)
	}
	if o.sops == "" {
		o.sops = "sops"
	}
	noSeat := verb == "init" || strings.HasPrefix(verb, "seat ")
	if noSeat && o.as != "" {
		return o, nil, bad("unknown flag --as")
	}
	if o.as == "" && !noSeat {
		if o.as = envValue(t.environ, "NOVA_SECRETS_SEAT"); o.as == "" {
			h, _ := os.Hostname() // an empty name fails the seat check below
			o.as, _, _ = strings.Cut(h, ".")
		}
	}
	if !noSeat && !seatName.MatchString(o.as) {
		return o, nil, bad("seat name %q: letters, digits, - and _ only", o.as)
	}
	if needKey { // absolute before the mode check and before sops (which runs in the store)
		if o.key, err = filepath.Abs(o.key); err != nil {
			return o, nil, refuse("key path %q cannot be made absolute: %v", o.key, err)
		}
	}
	return o, pos, nil
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

func q(s string) string {
	if s != "" && strings.Trim(s, "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789_./:=@%+-") == "" {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func (t tool) exec(args []string) error {
	flags, argv := args, []string(nil)
	if i := slices.Index(args, "--"); i >= 0 {
		flags, argv = args[:i], args[i+1:]
	}
	var also []string
	if len(argv) == 0 {
		also = []string{"a command after --"}
	}
	o, _, err := t.parse("exec", flags, 0, true, also...)
	if err != nil {
		return err
	}
	if _, err := t.lookPath(argv[0]); err != nil {
		return refuse("command not found: %s", argv[0])
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
		return refuse("unknown secret name(s) %s: %s holds no such name; run: nova-secrets names --store %s --as %s", strings.Join(missing, ", "), o.file(o.as), o.store, o.as)
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
		return refuse("exec %s: %v; the command never started", argv[0], err)
	}
	return nil
}

func (t tool) set(args []string) error {
	o, pos, err := t.parse("set", args, 1, true)
	if err != nil {
		return err
	}
	name := pos[0]
	if !envName.MatchString(name) {
		return bad("NAME %q is not an environment variable name", name)
	}
	if f, ok := t.stdin.(*os.File); ok && isTerminal(f) {
		return refuse("set: stdin is a terminal; pipe the value in, as: <source> | nova-secrets set NAME ...; run: nova-secrets set -h")
	}
	raw, err := io.ReadAll(io.LimitReader(t.stdin, maxValue+3)) // the value, one more byte to see it is over, and a CRLF
	if err != nil {
		return refuse("reading the value from stdin: %v", err)
	}
	value := strings.TrimSuffix(strings.TrimSuffix(string(raw), "\n"), "\r")
	if len(value) > maxValue {
		rest, _ := io.Copy(io.Discard, t.stdin) // only to say how big it was
		return bad("value is %d bytes; a secret is a key or token, at most %d bytes", int64(len(value))+rest, maxValue)
	}
	if value == "" || strings.ContainsAny(value, "\r\n\x00") {
		return refuse("set: empty or multi-line value on stdin; the value must be one non-empty line; run: nova-secrets set -h")
	}
	if _, err := t.toplevel(o.store); err != nil {
		return refuse("%s is not a git repository; run: git -C %s init && git -C %s add -A && git -C %s commit -m store", o.store, o.store, o.store, o.store)
	}
	values := map[string]string{} // a seat's first value starts its file
	if _, err := os.Stat(o.file(o.as)); err == nil {
		if values, err = t.decrypt(o, o.as); err != nil {
			return err
		}
	} else if _, err := t.keyVsRule(o, o.as); err != nil { // the first encrypt is checked like the later decrypts
		return err
	}
	values[name] = value
	if err := t.encrypt(o, o.as, values); err != nil {
		return err
	}
	file := o.as + ".yaml"
	for _, g := range [][]string{{"add", "--", file}, {"commit", "-q", "-m", "set " + name + " in " + file, "--", file}} {
		if err := t.git(o.store, g...); err != nil {
			return fmt.Errorf("%w; the file is written: run: git -C %s status", err, o.store)
		}
	}
	fmt.Fprintf(t.stdout, "SECRETS SET OK as=%s name=%s file=%s committed; run: git -C %s push\n", o.as, name, file, o.store)
	return nil
}

func (t tool) seatAdd(args []string) error {
	o, pos, err := t.parse("seat add", args, 2, false)
	if err != nil {
		return err
	}
	seat, pub := pos[0], pos[1]
	if !seatName.MatchString(seat) || !agePub.MatchString(pub) {
		return bad("usage: seat add <seat> <age1...>; the seat is letters, digits, - and _")
	}
	b, err := os.ReadFile(filepath.Join(o.store, "recovery.pub"))
	rec := strings.TrimSpace(string(b))
	if err != nil || !agePub.MatchString(rec) {
		return refuse("%s/recovery.pub is missing or not an age public key; run: age-keygen -y <recovery.key> > %s/recovery.pub, then commit it", o.store, o.store)
	}
	if rec == pub {
		return refuse("that key is the recovery key, not a seat's")
	}
	cfgPath := filepath.Join(o.store, ".sops.yaml")
	cfg, err := os.ReadFile(cfgPath)
	if err != nil {
		return refuse("%s is missing; create it with the line: creation_rules: []", cfgPath)
	}
	out, err := addRule(cfg, seat, pub+","+rec)
	if err != nil {
		return err
	}
	if err := writeFile(cfgPath, out, 0o644); err != nil {
		return err
	}
	for _, g := range [][]string{{"add", "--", ".sops.yaml"}, {"commit", "-q", "-m", "seat " + seat, "--", ".sops.yaml"}} {
		if err := t.git(o.store, g...); err != nil {
			return fmt.Errorf("%w; .sops.yaml is written: run: git -C %s status", err, o.store)
		}
	}
	fmt.Fprintf(t.stdout, "SECRETS SEAT ADD OK seat=%s committed; next: the seat's own key runs: nova-secrets set NAME --as %s --store %s --key <file>\n", seat, seat, o.store)
	return nil
}

var ruleSeat = regexp.MustCompile(`^\^([A-Za-z0-9_-]+)\\\.yaml\$$`)

// seatList prints every seat a rule names or a file exists for, and whether its file exists.
func (t tool) seatList(args []string) error {
	o, _, err := t.parse("seat list", args, 0, false)
	if err != nil {
		return err
	}
	cfg, err := os.ReadFile(filepath.Join(o.store, ".sops.yaml"))
	_, top, perr := parseTop(cfg)
	if err != nil || perr != nil {
		return refuse("%s/.sops.yaml is missing or does not parse; create it with the line: creation_rules: []", o.store)
	}
	type seatState struct{ file, rule bool }
	seats := map[string]seatState{}
	for _, r := range items(field(top, "creation_rules")) {
		if pr := field(r, "path_regex"); pr != nil {
			if m := ruleSeat.FindStringSubmatch(pr.Value); m != nil {
				seats[m[1]] = seatState{rule: true}
			}
		}
	}
	for _, s := range seatsIn(o.store) {
		st := seats[s]
		st.file = true
		seats[s] = st
	}
	for _, s := range slices.Sorted(maps.Keys(seats)) {
		yn := map[bool]string{true: "yes", false: "no"}
		fmt.Fprintf(t.stdout, "%s file=%s rule=%s\n", s, yn[seats[s].file], yn[seats[s].rule])
	}
	fmt.Fprintf(t.stderr, "SECRETS SEAT LIST OK seats=%d\n", len(seats))
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
// file's mode and owner check. A failure carries the command that ran, in a form
// that is safe to paste: a scratch HOME, and a decrypt that writes to /dev/null.
func (t tool) sopsRun(o opts, stdin io.Reader, args ...string) ([]byte, error) {
	if _, err := t.lookPath(o.sops); err != nil {
		return nil, refuse("sops not found at %s; install sops 3.x", o.sops)
	}
	if err := checkKey(o.key); err != nil {
		return nil, err
	}
	home, err := os.MkdirTemp("", "nova-secrets-*")
	if err != nil {
		return nil, refuse("temporary directory: %v", err)
	}
	defer func() { _ = os.RemoveAll(home) }() // ignored: an empty directory of our own; failing leaves only it
	out, err := t.run(stdin, t.sopsEnv(o.key, home), o.store, o.sops, args...)
	if err != nil {
		shown := slices.Clone(args)
		if args[0] == "-d" {
			shown = slices.Insert(shown, 1, "--output", "/dev/null")
		}
		for i := range shown {
			shown[i] = q(shown[i])
		}
		return nil, refusal{2, fmt.Sprintf("sops %s failed (%v); its output is withheld; ran: cd %s && HOME=$(mktemp -d) SOPS_AGE_KEY_FILE=%s %s %s",
			args[0], exitOf(err), q(o.store), q(o.key), q(o.sops), strings.Join(shown, " ")), false, true}
	}
	return out, nil
}

// diagnose names why sops failed on a seat's file when the key is the cause. It
// compares the key's public half (age-keygen -y) with the seat's rule in .sops.yaml
// and with the file's own recipients; it reads no value.
func (t tool) diagnose(o opts, seat string, fail error) error {
	if _, err := t.lookPath("age-keygen"); err != nil {
		return refuse("%v; age-keygen not found, so the key cannot be checked: install age", fail)
	}
	file := seat + ".yaml"
	pub, err := t.keyVsRule(o, seat)
	if err != nil || pub == "" {
		if err == nil {
			err = fail
		}
		return err
	}
	data, _ := os.ReadFile(o.file(seat))
	if rs, ok := recipientsOf(data); ok && !slices.Contains(rs, pub) {
		return refuse("key %s is the key in .sops.yaml for %s but not among %s's %d recipients: re-key it with: cd %s && SOPS_AGE_KEY_FILE=<recovery.key> sops updatekeys -y %s",
			pub, seat, file, len(rs), o.store, file)
	}
	return fail
}

// keyVsRule is the public half of --key (age-keygen -y) and a refusal when the seat's
// rule in .sops.yaml names another key. It is "" and nil when it cannot tell.
func (t tool) keyVsRule(o opts, seat string) (string, error) {
	out, err := t.run(nil, t.sopsEnv(o.key, os.TempDir()), o.store, "age-keygen", "-y", o.key)
	pub := strings.TrimSpace(string(out))
	if err != nil || !agePub.MatchString(pub) {
		return "", nil
	}
	file := seat + ".yaml"
	cfg, _ := os.ReadFile(filepath.Join(o.store, ".sops.yaml"))
	if age := field(ruleFor(cfg, file), "age"); age != nil && !slices.Contains(strings.Split(age.Value, ","), pub) {
		return pub, refuse("key %s is not the key in .sops.yaml for %s (%s): wrong --key/--as, or the key was replaced; edit the seat's age: in .sops.yaml then: cd %s && SOPS_AGE_KEY_FILE=<recovery.key> sops updatekeys -y %s",
			pub, seat, strings.Split(age.Value, ",")[0], o.store, file)
	}
	return pub, nil
}

func seatsIn(store string) []string {
	entries, _ := os.ReadDir(store)
	var seats []string
	for _, e := range entries {
		if s, ok := strings.CutSuffix(e.Name(), ".yaml"); ok && !e.IsDir() && seatName.MatchString(s) {
			seats = append(seats, s)
		}
	}
	return seats
}

// noSeatFile is the refusal for a seat with no file: it lists the seats there are.
func noSeatFile(o opts, seat string) error {
	have := "none"
	if s := seatsIn(o.store); len(s) > 0 {
		have = strings.Join(s, ",")
	}
	return refuse("no %s.yaml in %s: wrong --as? seats here: %s; or create it: nova-secrets set NAME --as %s --store %s --key <file>", seat, o.store, have, seat, o.store)
}

// decrypt opens <seat>.yaml into its flat name -> value map.
func (t tool) decrypt(o opts, seat string) (map[string]string, error) {
	file := seat + ".yaml"
	if _, err := os.Stat(o.file(seat)); err != nil {
		return nil, noSeatFile(o, seat)
	}
	out, err := t.sopsRun(o, nil, "-d", "--output-type", "json", file)
	if err != nil {
		var r refusal
		if errors.As(err, &r) && r.sops {
			return nil, t.diagnose(o, seat, err)
		}
		return nil, err
	}
	var raw map[string]any
	dec := json.NewDecoder(bytes.NewReader(out))
	dec.UseNumber()
	if err := dec.Decode(&raw); err != nil {
		return nil, refuse("%s did not decrypt to a map of names", file)
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
			return nil, refuse("%s: %s is not a single value", file, k)
		}
		if !envName.MatchString(k) || strings.Contains(s, "\x00") {
			return nil, refuse("%s: %q is not an environment variable name, or its value holds a NUL", file, k)
		}
		values[k] = s
	}
	return values, nil
}

// encrypt writes values to <seat>.yaml, the plaintext on sops's stdin only.
func (t tool) encrypt(o opts, seat string, values map[string]string) error {
	plain, err := json.Marshal(values)
	if err != nil {
		return refuse("encoding the seat: %v", err)
	}
	out, err := t.sopsRun(o, bytes.NewReader(plain), "-e", "--filename-override", seat+".yaml", "--input-type", "json", "--output-type", "yaml", "/dev/stdin")
	if err != nil {
		var r refusal
		cfg, _ := os.ReadFile(filepath.Join(o.store, ".sops.yaml"))
		if errors.As(err, &r) && r.sops && ruleFor(cfg, seat+".yaml") == nil {
			return refuse("no rule for %s.yaml in %s/.sops.yaml (or it does not parse); run: nova-secrets seat add %s <age-public-key> --store %s", seat, o.store, seat, o.store)
		}
		return err
	}
	return writeFile(o.file(seat), out, 0o600)
}

// gitRun runs git in dir without the caller's repository variables.
func (t tool) gitRun(dir string, args ...string) ([]byte, error) {
	var env []string
	for _, kv := range t.environ {
		if k, _, _ := strings.Cut(kv, "="); !slices.Contains([]string{"GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE"}, k) {
			env = append(env, kv)
		}
	}
	return t.run(nil, env, dir, "git", args...)
}

func (t tool) git(dir string, args ...string) error {
	if _, err := t.gitRun(dir, args...); err != nil {
		return refuse("git %s in %s failed (%v)", args[0], dir, exitOf(err))
	}
	return nil
}

func (t tool) toplevel(dir string) (string, error) {
	out, err := t.gitRun(dir, "rev-parse", "--show-toplevel")
	return strings.TrimSpace(string(out)), err
}

func sameDir(a, b string) bool {
	real := func(p string) string {
		if r, err := filepath.EvalSymlinks(p); err == nil {
			p = r
		}
		abs, _ := filepath.Abs(p)
		return abs
	}
	return real(a) == real(b)
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
		return refuse("writing %s: %v", path, err)
	}
	return nil
}

func exitOf(err error) string {
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return fmt.Sprintf("exit %d", ee.ExitCode())
	}
	return err.Error()
}

func parseTop(data []byte) (doc, top *yaml.Node, err error) {
	doc = &yaml.Node{}
	if err = yaml.Unmarshal(data, doc); err != nil || doc.Kind != yaml.DocumentNode || len(doc.Content) != 1 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, nil, errors.New("not a YAML map")
	}
	return doc, doc.Content[0], nil
}

func field(m *yaml.Node, key string) *yaml.Node {
	for i := 0; m != nil && i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

func items(n *yaml.Node) []*yaml.Node {
	if n == nil || n.Kind != yaml.SequenceNode {
		return nil
	}
	return n.Content
}

// matches is whether a creation rule applies to file: a rule without path_regex
// matches every file.
func matches(rule *yaml.Node, file string) bool {
	pr := field(rule, "path_regex")
	if pr == nil || pr.Value == "" {
		return true
	}
	re, err := regexp.Compile(pr.Value)
	return err == nil && re.MatchString(file)
}

// ruleFor is the first creation rule that matches file (sops uses the first), or nil.
func ruleFor(cfg []byte, file string) *yaml.Node {
	_, top, err := parseTop(cfg)
	if err != nil {
		return nil
	}
	for _, r := range items(field(top, "creation_rules")) {
		if r.Kind == yaml.MappingNode && matches(r, file) {
			return r
		}
	}
	return nil
}

// recipientsOf lists the age recipients in a sops file's metadata.
func recipientsOf(data []byte) ([]string, bool) {
	_, top, err := parseTop(data)
	if err != nil {
		return nil, false
	}
	var out []string
	for _, a := range items(field(field(top, "sops"), "age")) {
		if r := field(a, "recipient"); r != nil {
			out = append(out, r.Value)
		}
	}
	return out, true
}

// addRule inserts the seat's rule before the first rule that would shadow it,
// keeping every existing rule.
func addRule(cfg []byte, seat, age string) ([]byte, error) {
	doc, top, err := parseTop(cfg)
	if err != nil {
		return nil, refuse(".sops.yaml does not parse as a YAML map; it is left as it was")
	}
	rules := field(top, "creation_rules")
	if rules == nil || rules.Kind != yaml.SequenceNode {
		return nil, refuse(".sops.yaml has no creation_rules list; it is left as it was")
	}
	pathRegex, file := `^`+regexp.QuoteMeta(seat)+`\.yaml$`, seat+".yaml"
	at := len(rules.Content)
	for i, r := range rules.Content {
		if r.Kind != yaml.MappingNode {
			return nil, refuse(".sops.yaml: a creation rule is not a map; it is left as it was")
		}
		if pr := field(r, "path_regex"); pr != nil && pr.Value == pathRegex {
			return nil, refuse(".sops.yaml already has a rule for %s", seat)
		}
		if a := field(r, "age"); a != nil && strings.Contains(a.Value, strings.SplitN(age, ",", 2)[0]) {
			return nil, refuse(".sops.yaml already has a seat with that key")
		}
		if at == len(rules.Content) && matches(r, file) {
			at = i
		}
	}
	str := func(v string) *yaml.Node { return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: v} }
	rule := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map", Content: []*yaml.Node{str("path_regex"), str(pathRegex), str("age"), str(age)}}
	if len(rules.Content) == 0 {
		rules.Style = 0 // creation_rules: [] becomes a block list
	}
	rules.Content = slices.Insert(rules.Content, at, rule)
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(doc); err != nil || enc.Close() != nil {
		return nil, refuse("writing .sops.yaml: %v", err)
	}
	return buf.Bytes(), nil
}

// names prints the names in a seat file. A sops file keeps its keys in the clear;
// the sops block is skipped and no value is read. A key that is no environment
// variable name, or whose value is no single value, is counted and never printed:
// a key is data from a file anyone with push access can edit.
func (t tool) names(args []string) error {
	o, _, err := t.parse("names", args, 0, false)
	if err != nil {
		return err
	}
	b, err := os.ReadFile(o.file(o.as))
	if err != nil {
		return noSeatFile(o, o.as)
	}
	_, top, err := parseTop(b)
	if err != nil {
		return refuse("%s does not parse as a sops file", o.file(o.as))
	}
	var out strings.Builder
	shown, refused := 0, 0
	for i := 0; i+1 < len(top.Content); i += 2 {
		k, v := top.Content[i], top.Content[i+1]
		switch {
		case k.Value == "sops" || k.Value == markKey:
		case k.Kind != yaml.ScalarNode || k.Tag != "!!str" || !envName.MatchString(k.Value) || v.Kind != yaml.ScalarNode:
			refused++
		default:
			out.WriteString(k.Value + "\n")
			shown++
		}
	}
	fmt.Fprint(t.stdout, out.String())
	fmt.Fprintf(t.stderr, "SECRETS NAMES OK as=%s names=%d refused=%d\n", o.as, shown, refused)
	return nil
}

// initStore creates the store: git, .sops.yaml with no rules, recovery.pub, one commit.
func (t tool) initStore(args []string) error {
	o, _, err := t.parse("init", args, 0, false)
	if err != nil {
		return err
	}
	rec := strings.TrimSpace(o.recovery)
	if !agePub.MatchString(rec) {
		return bad("--recovery wants an age public key (age1...), not a private key or a file; make one: age-keygen -o recovery.key, then: age-keygen -y recovery.key")
	}
	at := o.store // where git is asked about: the store, or its parent while it is absent
	entries, err := os.ReadDir(o.store)
	absent := errors.Is(err, os.ErrNotExist)
	if absent {
		at = filepath.Dir(o.store)
	} else if err != nil {
		return refuse("%s cannot be read: %v", o.store, err)
	}
	var names []string
	for _, e := range entries {
		if names = append(names, e.Name()); e.Name() == ".sops.yaml" || e.Name() == "recovery.pub" {
			return refuse("store %s exists; use: nova-secrets seat add <seat> <age-public-key> --store %s", o.store, o.store)
		}
	}
	if names = slices.DeleteFunc(names, func(n string) bool { return n == ".git" }); len(names) > 0 {
		return refuse("%s is not empty (it holds %s); init needs a directory that is absent or empty, and never touches files that are there", o.store, strings.Join(names[:min(len(names), 5)], ", "))
	}
	top, gerr := t.toplevel(at)
	if gerr == nil && !sameDir(top, o.store) {
		return refuse("%s is inside the git work tree %s; make the store a repository of its own: mkdir -p %s && git -C %s init, then run init again", o.store, top, o.store, o.store)
	}
	if absent {
		if err := os.Mkdir(o.store, 0o755); err != nil {
			return refuse("creating %s: %v (its parent must exist)", o.store, err)
		}
	}
	if gerr != nil {
		if err := t.git(o.store, "init", "-q"); err != nil {
			return err
		}
	}
	files := []string{".sops.yaml", "recovery.pub"}
	for i, body := range []string{"creation_rules: []\n", rec + "\n"} {
		if err := writeFile(filepath.Join(o.store, files[i]), []byte(body), 0o644); err != nil {
			return err
		}
	}
	if err := t.git(o.store, append([]string{"add", "--"}, files...)...); err != nil {
		return err
	}
	if err := t.git(o.store, append([]string{"commit", "-q", "-m", "init secrets store", "--"}, files...)...); err != nil {
		return err
	}
	fmt.Fprintf(t.stdout, "SECRETS INIT OK store=%s next: nova-secrets seat add <seat> <age-pubkey> --store %s\n", o.store, o.store)
	return nil
}
