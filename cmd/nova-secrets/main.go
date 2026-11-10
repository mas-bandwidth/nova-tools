// Command nova-secrets keeps secrets sops-encrypted in a git working copy (the
// store), one file per seat, and hands the values a command asks for by name to
// that command alone, in its environment. Values travel stdin -> sops -> the
// child's environment only: never an argument, a log line, an error or a file in
// the clear. sops and age-keygen do the cryptography.
package main

import (
	"bytes"
	"cmp"
	"crypto/hmac"
	"crypto/sha256"
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

nova-secrets init --store <dir> --recovery <age-public-key> [--pins <dir>]
  Create a store in <dir>, which must be absent or empty (its parent must exist) and not inside another git
  work tree: git init, .sops.yaml with no rules, recovery.pub, one commit. Refuses a store that exists, but finishes one
  whose commit failed.
  The recovery key is a public key (age-keygen -y <file>); keep its private half offline. --pins also writes
  it to <dir>/recovery.pub, the control node's pin, outside the store (grant reads it).
  Prints: SECRETS INIT OK store=<dir>, then next: <this node's seat add, or the age-keygen -o that makes its key first>

nova-secrets seat add <seat> <age-public-key> --store <dir>
  Add the seat's rule to .sops.yaml: its key and the recovery key may open <seat>.yaml. Commits .sops.yaml.
  The seat is letters, digits, - and _.

nova-secrets seat list --store <dir>
  List the seats the rules in .sops.yaml name and the seat files present, one "<seat> file=yes|no rule=yes|no" per line.

nova-secrets seat remove <seat> --store <dir> --pins <dir>
  Remove the seat's rule from .sops.yaml and its files <seat>.yaml and <seat>.grants.yaml, in one commit. Refused
  while <pins>/<seat>.ledger says this node granted it values (delete that ledger and <pins>/<seat>.pub first), and
  for this node's own seat while any ledger remains. A value is removed from a seat with sops: sops unset <seat>.yaml '["NAME"]'.

nova-secrets set NAME --store <dir> --key <file> --pins <dir> [--as <seat>] [--recovery <age1>] [--sops <path>]
  Read one value from stdin (one non-empty line of at most 65536 bytes; on a terminal it asks, and the paste is not shown), set NAME in <seat>.yaml and commit that
  file. Pushing is yours. The file is sealed to --key's public half and the recovery pin <dir>/recovery.pub only;
  --recovery writes that pin when it is absent. Example: pass show db | nova-secrets set DB_PASSWORD --store S --key K --pins P --as web
  On a fresh node (no store at the default ~/.config/nova-secrets/store, and no key, recovery key or recovery pin
  either) set makes them first, in one go: recovery.key (0600) beside the key, its public half pinned in
  <pins>/recovery.pub, the store (init), this node's key (0600) and its seat (seat add); then
  SECRETS SET NEW ... back up <recovery.key> .... A store absent with any of the others there is refused
  (reason=partial): nothing that exists is overwritten.

nova-secrets grant NAME[,NAME] --to <seat> --pins <dir> --store <dir> --key <file> [--as <seat>] [--pub <age1>]
  [--recovery <age1>] [--sops <path>]
  Write <seat>.grants.yaml from the named values of the --as seat (the control node's), encrypted to the seat's
  pinned key and the pinned recovery key only, whatever .sops.yaml says; the granting key never opens it. It is
  rewritten whole, keeping the names this node's ledger <dir>/<seat>.ledger says it granted. Pins are <dir>/<seat>.pub and <dir>/recovery.pub on this machine;
  --pub and --recovery write one that is absent and refuse one that differs (reason=key-changed), as does a
  seat rule whose key is not the pin. A grant that would change nothing writes nothing. Pushing is yours.

nova-secrets names --store <dir> [--as <seat>]
  List the names in <seat>.yaml and <seat>.grants.yaml, one per line, from the plaintext keys. No key, no value read.
  A key that is no environment variable name is not printed and is counted as refused=<n> on stderr.

nova-secrets exec --only NAME[,NAME] --store <dir> --key <file> [--as <seat>] [--sops <path>] -- <cmd> [args...]
  Decrypt <seat>.yaml and <seat>.grants.yaml (a name in both is refused) and become <cmd> with only the --only values added to its environment.
  A --only value that is null or empty is refused.
  Prints SECRETS EXEC STARTING as=<seat> keys=<n> cmd=<name> on stderr, then becomes the command (nothing of the tool
  runs after it: the shell reports a command killed by a signal as 128+N, as for any process).
  Exit: the command's own status; 125 when exec refused at run time and the command never ran.
  On a machine nova-fleet joined, every shell of the login (ssh <machine> '<cmd>' too) has NOVA_SECRETS_STORE and
  NOVA_SECRETS_SEAT, so a bare exec opens the machine's own seat. Clone a private repository there with the token given to git only by a credential helper
  that reads it from the environment (never argv, never a file):
    nova-secrets exec --only GH_GATE_TOKEN -- git -c credential.helper= -c credential.helper='!f() { test "$1" = get && echo username=x-access-token && echo "password=$GH_GATE_TOKEN"; }; f' clone https://github.com/<owner>/<repo>.git

--store defaults to $NOVA_SECRETS_STORE, else ~/.config/nova-secrets/store; --key to $NOVA_SECRETS_KEY, else
~/.config/nova-secrets/<seat>.key; --pins to $NOVA_SECRETS_PINS, else ~/.config/nova-secrets/pins. The current
directory is never assumed. --as defaults to $NOVA_SECRETS_SEAT, else the short hostname; --sops
to sops on PATH. No verb prints a value. Exit codes: 0 done, 2 refused (exec: 125). A refusal is one stderr line,
SECRETS <VERB> REFUSED reason=<r>: <why>, r one of no-store no-seat no-key not-recipient no-name no-sops
no-command sops git other, and for set, grant and the files they read no-pin key-changed untrusted-rule
wrong-recipients duplicate-name not-regular grants-changed recovery-key partial.
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

// identityEnv names an age identity or this tool's own inputs; none reaches sops or exec's command.
var identityEnv = []string{"NOVA_SECRETS_KEY", "NOVA_SECRETS_STORE", "NOVA_SECRETS_SEAT", "SOPS_AGE_KEY", "SOPS_AGE_KEY_FILE", "SOPS_AGE_KEY_CMD", "SOPS_AGE_SSH_PRIVATE_KEY_FILE", "SOPS_KEYSERVICE"}

// maxValue is the longest secret set accepts: a key or a token.
const maxValue = 65536

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
	usage bool   // a malformed invocation: "nova-secrets <verb>: <msg>; run: nova-secrets <verb> -h"
	sops  bool   // sops itself failed
	why   string // the reason= word; "" prints as other
}

func (r refusal) Error() string { return r.msg }

func refuse(format string, a ...any) error { return deny("", format, a...) }

// deny is a refusal with its reason word, which a caller (a play under no_log) can show: it names no value.
func deny(why, format string, a ...any) error {
	return refusal{2, fmt.Sprintf(format, a...), false, false, why}
}

// bad is a malformed invocation: exit 2, even for exec.
func bad(format string, a ...any) error {
	return refusal{2, fmt.Sprintf(format, a...), true, false, ""}
}

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
	if verb == "seat" && len(rest) > 0 && (rest[0] == "add" || rest[0] == "list" || rest[0] == "remove") {
		verb, rest = "seat "+rest[0], rest[1:]
	}
	if h, ok := helpFor(verb); ok {
		a := rest // exec's flags end at --: a -h after it is the command's
		if i := slices.Index(rest, "--"); verb == "exec" && i >= 0 {
			a = rest[:i]
		}
		if slices.Contains(a, "-h") || slices.Contains(a, "--help") {
			fmt.Fprint(t.stdout, h)
			return 0
		}
	}
	var err error
	if run, ok := map[string]func(tool, []string) error{"init": tool.initStore, "names": tool.names, "exec": tool.exec, "set": tool.set,
		"seat add": tool.seatAdd, "seat list": tool.seatList, "seat remove": tool.seatRemove, "grant": tool.grant}[verb]; ok {
		err = run(t, rest)
	} else if verb == "seat" {
		verb, err = "seat add", bad("the seat verbs are: seat add <seat> <age-public-key> --store <dir>, seat list --store <dir>, and seat remove <seat> --store <dir>")
	} else {
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
	if r.why == "" {
		r.why = "other"
	}
	fmt.Fprintf(t.stderr, "SECRETS %s REFUSED reason=%s: %v\n", strings.ToUpper(verb), r.why, err)
	if verb == "exec" {
		return 125
	}
	return 2
}

func (t tool) unknownVerb(verb string) int {
	fmt.Fprintf(t.stderr, "nova-secrets: unknown verb %q (init, seat add, seat list, seat remove, set, grant, names, exec); run: nova-secrets help\n", verb)
	return 2
}

type opts struct{ store, as, key, sops, only, recovery, to, pins, pub string }

// parse reads flags after the verb's positional arguments (npos of them) and
// fills in the defaults; also names inputs the caller found missing, so one refusal names them all.
func (t tool) parse(verb string, args []string, npos int, needKey bool, also ...string) (o opts, pos []string, err error) {
	for len(pos) < npos && len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		pos, args = append(pos, args[0]), args[1:]
	}
	fs := flag.NewFlagSet(verb, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	for name, p := range map[string]*string{"store": &o.store, "as": &o.as, "key": &o.key, "sops": &o.sops, "only": &o.only, "recovery": &o.recovery, "to": &o.to, "pins": &o.pins, "pub": &o.pub} {
		fs.StringVar(p, name, "", "")
	}
	if err = fs.Parse(args); err != nil {
		msg := err.Error()
		if n, ok := strings.CutPrefix(msg, "flag provided but not defined: "); ok {
			msg = "unknown flag --" + strings.TrimLeft(n, "-")
		} else if n, ok := strings.CutPrefix(msg, "flag needs an argument: "); ok {
			msg = "--" + strings.TrimLeft(n, "-") + " needs a value"
		}
		return o, nil, bad("%s", msg)
	}
	takes := map[string]string{"init": "store recovery pins", "seat add": "store", "seat list": "store", "seat remove": "store pins", "names": "store as",
		"set": "store key pins as recovery sops", "grant": "to pins store key as pub recovery sops", "exec": "only store key as sops"}[verb]
	fs.Visit(func(f *flag.Flag) {
		if err == nil && !slices.Contains(strings.Fields(takes), f.Name) {
			err = bad("unknown flag --%s", f.Name)
		}
	})
	if err != nil {
		return o, nil, err
	}
	if pos = append(pos, fs.Args()...); len(pos) != npos {
		return o, nil, bad("takes %d argument(s) before its flags, got %d", npos, len(pos))
	}
	var missing []string
	if verb == "exec" && strings.Trim(o.only, ", \t") == "" { // --only , names nothing
		missing = append(missing, "--only NAME[,NAME]")
	}
	if o.store == "" {
		if o.store = envValue(t.environ, "NOVA_SECRETS_STORE"); o.store == "" {
			o.store = filepath.Join(filepath.Dir(t.keyFile("x")), "store")
		}
	}
	if verb == "init" && o.recovery == "" {
		missing = append(missing, "--recovery <age-public-key>")
	}
	if verb == "grant" && o.to == "" {
		missing = append(missing, "--to <seat>")
	}
	if o.pins == "" && slices.Contains([]string{"grant", "set", "seat remove"}, verb) {
		if o.pins = envValue(t.environ, "NOVA_SECRETS_PINS"); o.pins == "" {
			o.pins = filepath.Join(filepath.Dir(t.keyFile("x")), "pins")
		}
	}
	if needKey && o.key == "" {
		if o.key = envValue(t.environ, "NOVA_SECRETS_KEY"); o.key == "" {
			o.key = t.keyFile(cmp.Or(o.as, t.ownSeat()))
		}
	}
	if missing = append(missing, also...); len(missing) > 0 {
		return o, nil, bad("missing %s (no default; the current directory is never assumed)", strings.Join(missing, ", "))
	}
	if _, err := os.Stat(o.store); err != nil && verb != "init" && verb != "set" { // set: bootstrap says
		return o, nil, deny("no-store", "store %s does not exist; run: nova-secrets init --store %s --recovery <age-public-key>", o.store, o.store)
	}
	if o.sops == "" {
		o.sops = "sops"
	}
	noSeat := verb == "init" || strings.HasPrefix(verb, "seat ")
	if o.as == "" && !noSeat {
		o.as = t.ownSeat() // an empty name fails the seat check below
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

// ownSeat is this node's seat: $NOVA_SECRETS_SEAT, else the short hostname.
func (t tool) ownSeat() string {
	if s := envValue(t.environ, "NOVA_SECRETS_SEAT"); s != "" {
		return s
	}
	h, _ := os.Hostname()
	s, _, _ := strings.Cut(h, ".")
	return s
}

// keyFile is where a seat's key is kept: $NOVA_SECRETS_KEY for this node's own seat, else
// $HOME/.config/nova-secrets/<seat>.key.
func (t tool) keyFile(seat string) string {
	if k := envValue(t.environ, "NOVA_SECRETS_KEY"); k != "" && seat == t.ownSeat() {
		return k
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(cmp.Or(envValue(t.environ, "HOME"), home), ".config", "nova-secrets", seat+".key")
}

// next is the command to run next, each word shell-quoted (q); an OK line is followed by it on a line "next: <command>".
func next(argv ...string) string {
	words := make([]string, len(argv))
	for i, a := range argv {
		words[i] = q(a)
	}
	return strings.Join(words, " ")
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
	also := map[bool][]string{true: {"a command after --"}}[len(argv) == 0]
	o, _, err := t.parse("exec", flags, 0, true, also...)
	if err != nil {
		return err
	}
	if _, err := t.lookPath(argv[0]); err != nil {
		return deny("no-command", "command not found: %s", argv[0])
	}
	values, err := t.decrypt(o, o.as)
	if err != nil {
		return err
	}
	var pick, missing, empty []string
	for _, n := range strings.Split(o.only, ",") {
		if n = strings.TrimSpace(n); n == "" || slices.Contains(pick, n) {
			continue
		}
		if v, ok := values[n]; !ok {
			missing = append(missing, n)
		} else if v == "" { // a null or "" in the file: set never writes one
			empty = append(empty, n)
		}
		pick = append(pick, n)
	}
	if len(missing) > 0 {
		return deny("no-name", "unknown secret name(s) %s: %s holds no such name; run: nova-secrets names --store %s --as %s", strings.Join(missing, ", "), o.file(o.as), o.store, o.as)
	}
	if len(empty) > 0 {
		return refuse("%s is empty (null or \"\") in %s's files, and a command is never handed an empty secret; set it: run: nova-secrets set %s --store %s --key %s --as %s",
			strings.Join(empty, ", "), o.as, empty[0], q(o.store), q(o.key), o.as)
	}
	// The inherited environment loses every name the seat holds (a value left out
	// must not arrive another way) and every sops identity variable.
	drop := slices.Concat(slices.Collect(maps.Keys(values)), identityEnv)
	env := envWhere(t.environ, func(k string) bool {
		return !slices.ContainsFunc(drop, func(d string) bool { return d == k || runtime.GOOS == "windows" && strings.EqualFold(d, k) })
	})
	for _, k := range pick {
		env = append(env, k+"="+values[k])
	}
	fmt.Fprintf(t.stderr, "SECRETS EXEC STARTING as=%s keys=%d cmd=%s\n", o.as, len(pick), filepath.Base(argv[0])) // the tool is replaced next: nothing of it runs after
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
	if err := t.bootstrap(o, true); err != nil {
		return err
	}
	if err := t.ready(o); err != nil {
		return err
	}
	var raw []byte
	if f, ok := t.stdin.(*os.File); ok && isTerminal(f) {
		fmt.Fprintf(t.stderr, "%s (paste it, then enter; it is not shown): ", name)
		raw, err = readHidden(f)
		fmt.Fprintln(t.stderr)
	} else {
		raw, err = io.ReadAll(io.LimitReader(t.stdin, maxValue+3)) // the value, one more byte to see it is over, and a CRLF
	}
	if err != nil {
		return refuse("reading the value from stdin: %v", err)
	}
	value := strings.TrimSuffix(strings.TrimSuffix(string(raw), "\n"), "\r")
	if len(value) > maxValue {
		return bad("value is over %d bytes; a secret is a key or token", maxValue)
	}
	if value == "" || strings.ContainsAny(value, "\r\n\x00") {
		return refuse("set: empty or multi-line value on stdin; the value must be one non-empty line; run: nova-secrets set -h")
	}
	if err := t.bootstrap(o, false); err != nil {
		return err
	}
	if _, err := t.toplevel(o.store); err != nil {
		return refuse("%s is not a git repository; run: git -C %s init && git -C %s add -A && git -C %s commit -m store", o.store, o.store, o.store, o.store)
	}
	rec, err := t.pinnedRecovery(o)
	if err != nil {
		return err
	}
	values := map[string]string{} // a seat's first value starts its file
	if _, ok, err := seatFile(o, o.as+".yaml"); err != nil {
		return err
	} else if ok {
		if values, err = t.decryptFile(o, o.as, o.as+".yaml"); err != nil {
			return err
		}
	} else if _, err := t.keyVsRule(o, o.as); err != nil { // the first encrypt is checked like the later decrypts
		return err
	}
	if granted, err := plainNames(o, o.as+".grants.yaml"); err != nil {
		return err
	} else if slices.Contains(granted, name) {
		return deny("duplicate-name", "%s is granted to %s already (%s.grants.yaml); a name lives in one file: use another name, or stop granting it", name, o.as, o.as)
	}
	values[name] = value
	if err := t.encrypt(o, o.as, values, rec); err != nil {
		return err
	}
	file := o.as + ".yaml"
	if err := t.commit(o, "", "set "+name+" in "+file, "the file is written: run:", file); err != nil {
		return err
	}
	fmt.Fprintf(t.stdout, "SECRETS SET OK as=%s name=%s file=%s committed; run: git -C %s push\n", o.as, name, file, o.store)
	return nil
}

// ready refuses a set that could not finish, before the value is read, so it is given once: a program set runs is
// missing, the store's directory (the nearest one there is, for a store set makes) cannot be written, or git has no
// author or committer for the commit.
func (t tool) ready(o opts) error {
	if err := t.sopsFound(o); err != nil {
		return err
	}
	if _, err := t.lookPath("age-keygen"); err != nil {
		return refuse("age-keygen not found on PATH; install age: apt install age (Debian, Ubuntu), brew install age (macOS)")
	}
	dir := existingDir(o.store)
	if err := writable(dir); err != nil {
		return refuse("%s cannot be written, and the store is kept there: %v; run: ls -ld %s", dir, err, q(dir))
	}
	for _, who := range []string{"GIT_AUTHOR_IDENT", "GIT_COMMITTER_IDENT"} {
		if _, err := t.gitRun(dir, "var", who); errors.As(err, new(refusal)) { // git itself is missing
			return err
		} else if err != nil {
			return deny("git", "git has no commit identity, and set commits what it writes; nothing was read: run: git config --global user.name 'Your Name' && git config --global user.email you@example.com")
		}
	}
	return nil
}

// bootstrap makes a fresh node's store when set finds none at the default: a recovery key and this node's key
// (0600, beside each other), the recovery pin, the store (init) and the seat (seat add), in that order. A store
// absent elsewhere is no-store; absent with a key, the recovery key or the pin there is partial: a store made now
// would not match them, and nothing that exists is ever overwritten. With check it refuses the same and makes nothing.
func (t tool) bootstrap(o opts, check bool) error {
	dir := filepath.Dir(t.keyFile("x"))
	rk := filepath.Join(dir, "recovery.key")
	resume := false // a store this bootstrap made and stopped in before the seat (a failed commit): finished with its keys
	if _, err := os.Stat(o.store); err == nil {
		cfg, _ := os.ReadFile(filepath.Join(o.store, ".sops.yaml")) // ignored: an unreadable one is no store to finish
		if _, kerr := os.Lstat(rk); o.store != filepath.Join(dir, "store") || string(cfg) != "creation_rules: []\n" || kerr != nil {
			return nil
		}
		resume = true
	}
	if o.store != filepath.Join(dir, "store") {
		return deny("no-store", "store %s does not exist; run: nova-secrets init --store %s --recovery <age-public-key>", o.store, o.store)
	}
	var there []string
	for _, p := range []string{o.key, rk, filepath.Join(o.pins, "recovery.pub")} {
		if _, err := os.Lstat(p); err == nil && !resume {
			there = append(there, p)
		}
	}
	if len(there) > 0 {
		return deny("partial", "there is no store at %s, but %s: a store made now would not match, and nothing that exists is overwritten; "+
			"put the store back (git clone <its upstream> %s), or move those aside and run again", o.store, strings.Join(there, " and ")+" exist", o.store)
	}
	if check {
		return nil
	}
	made := resume // the recovery key exists from here, so its backup line is printed whatever follows
	defer func() {
		if made {
			fmt.Fprintf(t.stdout, "SECRETS SET NEW store=%s seat=%s key=%s: back up %s, the recovery key, off this machine now: it opens every seat if a key is lost\n", o.store, o.as, o.key, rk)
		}
	}()
	pub := map[string]string{}
	for _, k := range []string{rk, o.key} {
		if !resume {
			if err := os.MkdirAll(filepath.Dir(k), 0o700); err != nil {
				return refuse("creating %s: %v", filepath.Dir(k), err)
			}
			if _, err := t.run(nil, t.environ, filepath.Dir(k), "age-keygen", "-o", k); err != nil {
				return refuse("age-keygen -o %s: %v; install age", k, err)
			}
			made = true
			if err := os.Chmod(k, 0o600); err != nil {
				return refuse("chmod 600 %s: %v", k, err)
			}
		}
		p, err := t.publicOf(opts{store: filepath.Dir(k), key: k})
		if err != nil {
			return err
		}
		pub[k] = p
	}
	quiet := t
	quiet.stdout = io.Discard
	if !resume || t.initPending(o.store, pub[rk]) {
		if err := quiet.initStore([]string{"--store", o.store, "--recovery", pub[rk], "--pins", o.pins}); err != nil {
			return fmt.Errorf("%w, then run the same nova-secrets set again", err) // which finishes this store with these keys
		}
	}
	return quiet.seatAdd([]string{o.as, pub[o.key], "--store", o.store})
}

func (t tool) seatAdd(args []string) error {
	o, pos, err := t.parse("seat add", args, 2, false)
	if err != nil {
		return err
	}
	seat, pub := pos[0], pos[1]
	if !seatName.MatchString(seat) || !agePub.MatchString(pub) || strings.EqualFold(seat, "recovery") {
		return bad("usage: seat add <seat> <age1...>; the seat is letters, digits, - and _, and not recovery")
	}
	b, err := os.ReadFile(filepath.Join(o.store, "recovery.pub"))
	rec := strings.TrimSpace(string(b))
	if err != nil || !agePub.MatchString(rec) {
		return refuse("%s/recovery.pub is missing or not an age public key; run: age-keygen -y <recovery.key> > %s/recovery.pub, then commit it", o.store, o.store)
	}
	if rec == pub {
		return refuse("that key is the recovery key, not a seat's; make the seat a key of its own: run: age-keygen -o %s", q(t.keyFile(seat)))
	}
	cfgPath := filepath.Join(o.store, ".sops.yaml")
	cfg, err := os.ReadFile(cfgPath)
	if err != nil {
		return refuse("%s is missing; create it with the line: creation_rules: []", cfgPath)
	}
	out, err := addRule(cfg, seat, pub+","+rec)
	if err != nil {
		return fmt.Errorf("%w; run: nova-secrets seat list --store %s", err, q(o.store))
	}
	if err := writeFile(cfgPath, out, 0o644); err != nil {
		return err
	}
	if err := t.commit(o, "", "seat "+seat, ".sops.yaml is written: run:", ".sops.yaml"); err != nil {
		return err
	}
	home, _ := os.UserHomeDir()
	pins := cmp.Or(envValue(t.environ, "NOVA_SECRETS_PINS"), filepath.Join(cmp.Or(envValue(t.environ, "HOME"), home), ".config", "nova-secrets", "pins"))
	// where init --pins put this store's recovery pin, as the guide does: beside the store
	beside := filepath.Join(filepath.Dir(o.store), "pins")
	if b, _ := os.ReadFile(filepath.Join(beside, "recovery.pub")); envValue(t.environ, "NOVA_SECRETS_PINS") == "" && strings.TrimSpace(string(b)) == rec { // ignored: no pin there
		pins = beside
	}
	// next: set as the seat just added, on this store; --key only when this machine holds that seat's key, else set reads NOVA_SECRETS_KEY
	then := []string{"nova-secrets", "set", "DB_PASSWORD", "--as", seat, "--store", o.store}
	key := t.keyFile(seat)
	if _, err := os.Stat(key); err == nil {
		if k, err := t.publicOf(opts{store: o.store, key: key}); err == nil && k == pub {
			then = append(then, "--key", key)
		}
	}
	fmt.Fprintf(t.stdout, "SECRETS SEAT ADD OK seat=%s committed\nnext: %s\n", seat, next(append(then, "--pins", pins)...))
	return nil
}

var ruleSeat = regexp.MustCompile(`^\^([A-Za-z0-9_-]+)\\\.yaml\$$`)

// seatRemove takes a seat's rule and files out of the store in one commit.
func (t tool) seatRemove(args []string) error {
	o, pos, err := t.parse("seat remove", args, 1, false)
	if err != nil {
		return err
	}
	seat := pos[0]
	if !seatName.MatchString(seat) {
		return bad("usage: seat remove <seat>; the seat is letters, digits, - and _")
	}
	if _, err := t.toplevel(o.store); err != nil {
		if errors.As(err, new(refusal)) {
			return err
		}
		return refuse("%s is not a git repository; run: git -C %s status", o.store, o.store)
	}
	if err := t.pinsOutside(o); err != nil { // a real pin directory, not any empty one: its silence is the guard
		return err
	}
	if _, err := pinned(o.pins, "recovery", ""); err != nil {
		return err
	}
	if _, err := os.Lstat(filepath.Join(o.pins, seat+".ledger")); err == nil {
		return refuse("this node granted %s values (%s/%s.ledger); delete that ledger and the pin %s/%s.pub first, then run again", seat, o.pins, seat, o.pins, seat)
	}
	if ledgers, _ := filepath.Glob(filepath.Join(o.pins, "*.ledger")); strings.EqualFold(seat, t.ownSeat()) && len(ledgers) > 0 {
		return refuse("%s is this node's own seat, the one that granted %d seat(s) their values (%s/*.ledger); remove those seats first, then run again", seat, len(ledgers), o.pins)
	}
	cfgPath := filepath.Join(o.store, ".sops.yaml")
	cfg, err := os.ReadFile(cfgPath)
	if err != nil {
		return refuse("%s is missing; create it with the line: creation_rules: []", cfgPath)
	}
	out, hadRule, err := removeRule(cfg, seat)
	if err != nil {
		return err
	}
	var files []string
	for _, f := range []string{seat + ".yaml", seat + ".grants.yaml"} {
		if _, ok, err := seatFile(o, f); err != nil {
			return err
		} else if ok {
			files = append(files, f)
		}
	}
	if !hadRule && len(files) == 0 {
		return deny("no-seat", "no rule and no file for %s in %s; run: nova-secrets seat list --store %s", seat, o.store, o.store)
	}
	if hadRule {
		if err := writeFile(cfgPath, out, 0o644); err != nil {
			return err
		}
		files = append(files, ".sops.yaml")
	}
	for _, f := range files {
		if f != ".sops.yaml" {
			if err := os.Remove(filepath.Join(o.store, f)); err != nil {
				return refuse("removing %s: %v", f, err)
			}
		}
	}
	if err := t.commit(o, "-A", "seat remove "+seat, "the files are removed: run:", files...); err != nil {
		return err
	}
	fmt.Fprintf(t.stdout, "SECRETS SEAT REMOVE OK seat=%s rule=%s files=%d committed; run: git -C %s push\n", seat, map[bool]string{true: "removed", false: "none"}[hadRule], len(files)-map[bool]int{true: 1, false: 0}[hadRule], o.store)
	return nil
}

// removeRule is .sops.yaml without the seat's rule, and whether there was one.
func removeRule(cfg []byte, seat string) (out []byte, had bool, err error) {
	pathRegex := `^` + regexp.QuoteMeta(seat) + `\.yaml$`
	out, err = editRules(cfg, func(rules *yaml.Node) error {
		kept := rules.Content[:0:0]
		for _, r := range rules.Content {
			if pr := field(r, "path_regex"); r.Kind == yaml.MappingNode && pr != nil && pr.Value == pathRegex {
				continue
			}
			kept = append(kept, r)
		}
		had = len(kept) != len(rules.Content)
		rules.Content = kept
		if len(kept) == 0 {
			rules.Style = yaml.FlowStyle // back to creation_rules: []
		}
		return nil
	})
	return out, had, err
}

// editRules is .sops.yaml after edit has changed its creation_rules list; on any refusal it is left as it was.
func editRules(cfg []byte, edit func(rules *yaml.Node) error) ([]byte, error) {
	doc, top, err := parseTop(cfg)
	if err != nil {
		return nil, refuse(".sops.yaml does not parse as a YAML map; it is left as it was")
	}
	rules := field(top, "creation_rules")
	if rules == nil || rules.Kind != yaml.SequenceNode {
		return nil, refuse(".sops.yaml has no creation_rules list; it is left as it was")
	}
	if err := edit(rules); err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(doc); err != nil || enc.Close() != nil {
		return nil, refuse("writing .sops.yaml: %v", err)
	}
	return buf.Bytes(), nil
}

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
		seats[s] = seatState{file: true, rule: seats[s].rule}
	}
	for _, s := range slices.Sorted(maps.Keys(seats)) {
		yn := map[bool]string{true: "yes", false: "no"}
		fmt.Fprintf(t.stdout, "%s file=%s rule=%s\n", s, yn[seats[s].file], yn[seats[s].rule])
	}
	fmt.Fprintf(t.stderr, "SECRETS SEAT LIST OK seats=%d\n", len(seats))
	return nil
}

// sopsEnv is sops's whole environment: PATH, the one key file, and a HOME of its own.
func (t tool) sopsEnv(key, home string) []string {
	keep := []string{"PATH", "TMPDIR", "SYSTEMROOT", "TEMP", "TMP"}
	return append([]string{"SOPS_AGE_KEY_FILE=" + key, "HOME=" + home, "XDG_CONFIG_HOME=" + home, "APPDATA=" + home},
		envWhere(t.environ, func(k string) bool { return slices.Contains(keep, strings.ToUpper(k)) })...)
}

// envWhere is the entries of env whose name keep accepts, in order.
func envWhere(env []string, keep func(name string) bool) []string {
	var out []string
	for _, kv := range env {
		if k, _, _ := strings.Cut(kv, "="); keep(k) {
			out = append(out, kv)
		}
	}
	return out
}

// sopsFound refuses when --sops is not a program that can be run.
func (t tool) sopsFound(o opts) error {
	if _, err := t.lookPath(o.sops); err != nil {
		install := map[bool]string{true: "brew install sops", false: "the release binary from https://github.com/getsops/sops/releases on PATH, executable"}[runtime.GOOS == "darwin"]
		return deny("no-sops", "sops not found at %s; install sops 3.x: %s, or pass --sops <path>", o.sops, install)
	}
	return nil
}

// sopsRun runs sops in the store with --key as its only identity, after the key
// file's mode and owner check. A failure carries the command that ran, in a form
// that is safe to paste: a scratch HOME, and a decrypt that writes to /dev/null.
func (t tool) sopsRun(o opts, stdin io.Reader, args ...string) ([]byte, error) {
	if err := t.sopsFound(o); err != nil {
		return nil, err
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
		_, keyErr := t.publicOf(o) // a key file that is no age key explains an exit 128 better than "wrong --key"
		return nil, refusal{2, fmt.Sprintf("sops %s failed (%v)%s; its output is withheld; run: cd %s && HOME=$(mktemp -d) SOPS_AGE_KEY_FILE=%s %s %s",
			args[0], exitOf(err), sopsWhy(err, o, args[len(args)-1], keyErr == nil), q(o.store), q(o.key), q(o.sops), strings.Join(shown, " ")), false, true, "sops"}
	}
	return out, nil
}

// diagnose names why sops failed on a seat's file when the key is the cause. It
// compares the key's public half (age-keygen -y) with the seat's rule in .sops.yaml
// and with the file's own recipients; it reads no value.
func (t tool) diagnose(o opts, seat, file string, fail error) error {
	if _, err := t.lookPath("age-keygen"); err != nil {
		return refuse("%v; age-keygen not found, so the key cannot be checked: install age", fail)
	}
	pub, err := t.keyVsRule(o, seat)
	if err != nil || pub == "" {
		if err == nil {
			err = fail
		}
		return err
	}
	data, _ := os.ReadFile(filepath.Join(o.store, file))
	if rs, ok := recipientsOf(data); ok && !slices.Contains(rs, pub) {
		return deny("not-recipient", "key %s is the key in .sops.yaml for %s but not among %s's %d recipients: re-key it with: cd %s && SOPS_AGE_KEY_FILE=<recovery.key> sops updatekeys -y %s",
			pub, seat, file, len(rs), o.store, file)
	}
	return fail
}

// publicOf is the public half of --key (age-keygen -y).
func (t tool) publicOf(o opts) (string, error) {
	out, err := t.run(nil, t.sopsEnv(o.key, os.TempDir()), o.store, "age-keygen", "-y", o.key)
	if pub := strings.TrimSpace(string(out)); err == nil && agePub.MatchString(pub) {
		return pub, nil
	}
	return "", deny("no-key", "age-keygen -y %s did not print an age public key; is it an age key, and is age-keygen on PATH?", o.key)
}

// keyVsRule is the public half of --key and a refusal when the seat's rule in .sops.yaml names another key. It is
// "" and nil when it cannot tell.
func (t tool) keyVsRule(o opts, seat string) (string, error) {
	pub, err := t.publicOf(o)
	if err != nil {
		return "", nil
	}
	file := seat + ".yaml"
	cfg, _ := os.ReadFile(filepath.Join(o.store, ".sops.yaml"))
	if age := field(ruleFor(cfg, file), "age"); age != nil && !slices.Contains(strings.Split(age.Value, ","), pub) {
		return pub, deny("not-recipient", "key %s is not the key in .sops.yaml for %s (%s): wrong --key/--as, or the key was replaced; edit the seat's age: in .sops.yaml then: cd %s && SOPS_AGE_KEY_FILE=<recovery.key> sops updatekeys -y %s",
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
	return deny("no-seat", "no %s.yaml in %s: wrong --as? seats here: %s; or create it: nova-secrets set NAME --as %s --store %s --key <file> --pins <dir>", seat, o.store, have, seat, o.store)
}

// decrypt opens <seat>.yaml and <seat>.grants.yaml into one flat name -> value map; a name in both is refused.
func (t tool) decrypt(o opts, seat string) (map[string]string, error) {
	values, found := map[string]string{}, false
	for _, file := range []string{seat + ".yaml", seat + ".grants.yaml"} {
		if _, ok, err := seatFile(o, file); err != nil {
			return nil, err
		} else if !ok {
			continue
		}
		found = true
		vs, err := t.decryptFile(o, seat, file)
		if err != nil {
			return nil, err
		}
		for k, v := range vs {
			if _, dup := values[k]; dup {
				return nil, deny("duplicate-name", "%s is in both %s.yaml and %s.grants.yaml; a name lives in one file: remove it with: sops unset %s '[\"%s\"]', or stop granting it", k, seat, seat, filepath.Join(o.store, seat+".yaml"), k)
			}
			values[k] = v
		}
	}
	if !found {
		return nil, noSeatFile(o, seat)
	}
	return values, nil
}

// seatFile is the store's file name; ok is false when it is absent. Anything there but a regular file (a symlink to
// another seat's file, a directory) is refused, so a seat is only ever read and written as itself.
func seatFile(o opts, name string) (string, bool, error) {
	p := filepath.Join(o.store, name)
	fi, err := os.Lstat(p)
	if errors.Is(err, os.ErrNotExist) {
		return p, false, nil
	}
	if err != nil || !fi.Mode().IsRegular() {
		return p, false, deny("not-regular", "%s is not a regular file (a symlink, or a directory); a seat file is read only as itself: remove it, then run again", p)
	}
	return p, true, nil
}

// plainNames lists the names in a seat file's plaintext keys, with no key and no sops; none when it is absent.
func plainNames(o opts, name string) ([]string, error) {
	p, ok, err := seatFile(o, name)
	if err != nil || !ok {
		return nil, err
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, refuse("reading %s: %v; run: ls -l %s", p, err, q(p))
	}
	_, top, err := parseTop(b)
	if err != nil {
		return nil, refuse("%s does not parse as a sops file (anchors, aliases and merge keys are refused)", p)
	}
	var names []string
	for i := 0; i+1 < len(top.Content); i += 2 {
		if k := top.Content[i].Value; k != "sops" {
			names = append(names, k)
		}
	}
	return names, nil
}

// decryptFile opens one seat file into its flat name -> value map.
func (t tool) decryptFile(o opts, seat, file string) (map[string]string, error) {
	out, err := t.sopsRun(o, nil, "-d", "--output-type", "json", file)
	if err != nil {
		var r refusal
		if errors.As(err, &r) && r.sops {
			return nil, t.diagnose(o, seat, file, err)
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
		if k == "sops" {
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
			return nil, refuse("%s: %q is not a single value", file, k)
		}
		if !envName.MatchString(k) || strings.Contains(s, "\x00") {
			return nil, refuse("%s: %q is not an environment variable name, or its value holds a NUL", file, k)
		}
		values[k] = s
	}
	return values, nil
}

// pinsOutside refuses a pin directory that is the store, inside it, or inside any git work tree: a pin a pusher can
// write is no pin.
func (t tool) pinsOutside(o opts) error {
	// symlinks on both paths are resolved as far as they exist (macOS /var -> /private/var, a store named through a
	// link), and git is asked about the part of the pin path that exists, before the directory is made: a refused
	// one is never made
	pins, store := realPath(o.pins), realPath(o.store)
	if rel, err := filepath.Rel(store, pins); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return deny("no-pin", "pin directory %s is the store or inside it; pins live outside the store, as ~/.config/nova-secrets/pins", o.pins)
	}
	if top, err := t.toplevel(existingDir(o.pins)); err == nil && top != "" {
		return deny("no-pin", "pin directory %s is inside the git work tree %s; pins live outside any repository, as ~/.config/nova-secrets/pins", o.pins, top)
	}
	if err := os.MkdirAll(o.pins, 0o700); err != nil {
		return deny("no-pin", "pin directory %s: %v", o.pins, err)
	}
	return nil
}

// pinnedRecovery is the recovery pin, refused when the store's recovery.pub is another key.
func (t tool) pinnedRecovery(o opts) (string, error) {
	if err := t.pinsOutside(o); err != nil {
		return "", err
	}
	self, err := t.publicOf(o)
	if err != nil {
		return "", err
	}
	b, _ := os.ReadFile(filepath.Join(o.store, "recovery.pub"))
	storeRec := strings.TrimSpace(string(b))
	if o.recovery != "" && o.recovery != storeRec { // checked before a pin is written, so a refused one is never on disk
		return "", deny("key-changed", "%s/recovery.pub is not the recovery key given, %s: the store was edited, or --recovery is wrong; settle it, then run again", o.store, o.recovery)
	}
	if o.recovery == self || storeRec == self {
		return "", deny("recovery-key", "the recovery key is for recovery only; use the seat's own key (--key is the private half of %s)", self)
	}
	rec, err := pinned(o.pins, "recovery", o.recovery)
	if err != nil {
		return "", err
	}
	if rec == self {
		return "", deny("recovery-key", "the recovery key is for recovery only; use the seat's own key (--key is the private half of the pinned recovery key %s)", self)
	}
	if storeRec != rec {
		return "", deny("key-changed", "%s/recovery.pub is not the pinned recovery key %s: the store was edited; restore it, then run again", o.store, rec)
	}
	return rec, nil
}

// encrypt writes values to <seat>.yaml, the plaintext on sops's stdin only, sealed with sops --config /dev/null to
// the public half of --key and the recovery pin: nothing in the store chooses a recipient. The rule must say the
// same, so exec's checks agree. Model: cmd/nova-secrets/tla/Recipients.tla, Set.
func (t tool) encrypt(o opts, seat string, values map[string]string, rec string) error {
	cfg, err := plainConfig(o)
	if err != nil {
		return err
	}
	rule := ruleFor(cfg, seat+".yaml")
	if rule == nil {
		return deny("no-seat", "no rule for %s.yaml in %s/.sops.yaml; run: nova-secrets seat add %s <age-public-key> --store %s", seat, o.store, seat, o.store)
	}
	pub, err := t.publicOf(o)
	if err != nil {
		return err
	}
	age := ageOf(rule)
	if len(rule.Content) != 4 || field(rule, "path_regex") == nil || len(age) != 2 || age[1] != rec {
		return deny("untrusted-rule", "the rule for %s.yaml is not the one seat add writes (path_regex, and age: the seat's key then the recovery key), so a value could reach another key: fix it, then run again", seat)
	}
	if age[0] != pub {
		return deny("key-changed", "the rule for %s.yaml names %s, not --key's %s: the store was edited, or the seat has a new key; fix the rule, then run again", seat, age[0], pub)
	}
	out, err := t.seal(o, values, seat+".yaml", pub, rec)
	if err != nil {
		return err
	}
	return writeFile(o.file(seat), out, 0o600)
}

// seal encrypts values for file with sops --config /dev/null to exactly pub and rec, the plaintext on sops's stdin
// only, and refuses what sops returns unless it is sealed to those two keys alone (sealedTo).
func (t tool) seal(o opts, values map[string]string, file, pub, rec string) ([]byte, error) {
	plain, _ := json.Marshal(values) // ignored: a map of strings always encodes
	out, err := t.sopsRun(o, bytes.NewReader(plain), "--config", os.DevNull, "-e", "--age", pub+","+rec, "--filename-override", file, "--input-type", "json", "--output-type", "yaml", "/dev/stdin")
	if err == nil {
		err = sealedTo(out, file, []string{pub, rec})
	}
	return out, err
}

// plainConfig is .sops.yaml, refused when it is not a plain YAML map: sops resolves anchors, aliases and merge keys
// that a check of the unresolved tree would not see.
func plainConfig(o opts) ([]byte, error) {
	cfg, err := os.ReadFile(filepath.Join(o.store, ".sops.yaml"))
	if _, _, perr := parseTop(cfg); err != nil || perr != nil {
		return nil, deny("untrusted-rule", "%s/.sops.yaml is missing or not a plain YAML map (anchors, aliases and merge keys are refused); fix it, then run again", o.store)
	}
	return cfg, nil
}

// ageOf is a rule's age list, each key trimmed.
func ageOf(r *yaml.Node) []string {
	a := field(r, "age")
	if a == nil {
		return nil
	}
	keys := strings.Split(a.Value, ",")
	for i := range keys {
		keys[i] = strings.TrimSpace(keys[i])
	}
	return keys
}

// sealedTo refuses what sops returned unless its age recipients are exactly want and it names no other key source;
// nothing is written before this check.
func sealedTo(out []byte, file string, want []string) error {
	_, top, err := parseTop(out)
	meta := field(top, "sops")
	if err != nil || meta == nil || meta.Kind != yaml.MappingNode {
		return deny("wrong-recipients", "sops returned no sops metadata for %s; nothing is written", file)
	}
	for i := 0; i+1 < len(meta.Content); i += 2 {
		k, v := meta.Content[i].Value, meta.Content[i+1]
		if slices.Contains([]string{"kms", "gcp_kms", "azure_kv", "hc_vault", "pgp", "key_groups"}, k) && (len(v.Content) > 0 || v.Kind == yaml.ScalarNode && v.Value != "" && v.Tag != "!!null") {
			return deny("wrong-recipients", "sops sealed %s to a %s key too; nothing is written", file, k)
		}
	}
	got, _ := recipientsOf(out)
	slices.Sort(got)
	if !slices.Equal(got, slices.Sorted(slices.Values(want))) {
		return deny("wrong-recipients", "sops sealed %s to %d age keys, not exactly %s; nothing is written", file, len(got), strings.Join(want, ","))
	}
	return nil
}

// gitRun runs git in dir without the caller's repository variables.
func (t tool) gitRun(dir string, args ...string) ([]byte, error) {
	if _, err := t.lookPath("git"); err != nil {
		return nil, deny("git", "git not found on PATH; install it: apt install git (Debian, Ubuntu), brew install git (macOS), winget install Git.Git (Windows)")
	}
	repo := []string{"GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE"}
	return t.run(nil, envWhere(t.environ, func(k string) bool { return !slices.Contains(repo, k) }), dir, "git", args...)
}

func (t tool) git(dir string, args ...string) error {
	if _, err := t.gitRun(dir, args...); err != nil {
		return deny("git", "git %s in %s failed (%v)", args[0], dir, exitOf(err))
	}
	return nil
}

func (t tool) toplevel(dir string) (string, error) {
	out, err := t.gitRun(dir, "rev-parse", "--show-toplevel")
	return strings.TrimSpace(string(out)), err
}

// realPath is p absolute, with symlinks resolved along the longest prefix of it that exists.
func realPath(p string) string {
	abs, _ := filepath.Abs(p)
	dir := existingDir(abs)
	r, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return abs
	}
	rel, _ := filepath.Rel(dir, abs)
	return filepath.Join(r, rel)
}

// existingDir is the longest prefix of p that exists, p itself included.
func existingDir(p string) string {
	abs, _ := filepath.Abs(p)
	for dir := abs; ; dir = filepath.Dir(dir) {
		if _, err := os.Lstat(dir); err == nil || filepath.Dir(dir) == dir {
			return dir
		}
	}
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
		return refuse("writing %s: %v; run: ls -ld %s", path, err, filepath.Dir(path))
	}
	return nil
}

// sopsWhy names a sops exit code whose reason carries no value: 51 is a file changed since it was sealed (a MAC
// mismatch; a seat's own file is restored from git, a grants file by a fresh grant), 128 a file no given key opens
// (or a key file that is no age key at all). Anything else stays withheld.
func sopsWhy(err error, o opts, file string, keyOK bool) string {
	var ee interface{ ExitCode() int } // *exec.ExitError, or a fake's
	if !errors.As(err, &ee) {
		return ""
	}
	base := filepath.Base(file)
	switch ee.ExitCode() {
	case 51:
		if seat, ok := strings.CutSuffix(base, ".grants.yaml"); ok {
			return fmt.Sprintf(": the file changed since it was sealed (MAC mismatch); a fresh grant from the control node writes it again: nova-secrets grant <names> --to %s --pins <dir> --store %s", seat, q(o.store))
		}
		return fmt.Sprintf(": the file changed since it was sealed (MAC mismatch); restore it: git -C %s checkout -- %s, or seal it again", q(o.store), q(base))
	case 128:
		if !keyOK {
			return fmt.Sprintf(": the key file is not an age key (age-keygen -y %s prints none); the seat's key is AGE-SECRET-KEY-1..., one line", q(o.key))
		}
		return ": no key given opens it (wrong --key, or the file was sealed to other keys)"
	}
	return ""
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
	if err = yaml.Unmarshal(data, doc); err != nil || doc.Kind != yaml.DocumentNode || len(doc.Content) != 1 || doc.Content[0].Kind != yaml.MappingNode || !plain(doc) {
		return nil, nil, errors.New("not a plain YAML map")
	}
	return doc, doc.Content[0], nil
}

// plain is whether a YAML tree has no anchor, alias or merge key: what is checked is then what sops reads.
func plain(n *yaml.Node) bool {
	if n.Anchor != "" || n.Kind == yaml.AliasNode || n.Tag == "!!merge" {
		return false
	}
	return !slices.ContainsFunc(n.Content, func(c *yaml.Node) bool { return !plain(c) })
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
	pathRegex, file := `^`+regexp.QuoteMeta(seat)+`\.yaml$`, seat+".yaml"
	return editRules(cfg, func(rules *yaml.Node) error {
		at := len(rules.Content)
		for i, r := range rules.Content {
			if r.Kind != yaml.MappingNode {
				return refuse(".sops.yaml: a creation rule is not a map; it is left as it was")
			}
			if pr := field(r, "path_regex"); pr != nil && pr.Value == pathRegex {
				return refuse(".sops.yaml already has a rule for %s (a new key for it: seat remove it first)", seat)
			}
			if a := field(r, "age"); a != nil && strings.Contains(a.Value, strings.SplitN(age, ",", 2)[0]) {
				return refuse(".sops.yaml already has a seat with that key")
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
		return nil
	})
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
	var out strings.Builder
	var seen []string
	shown, refused, found := 0, 0, false
	for _, file := range []string{o.as + ".yaml", o.as + ".grants.yaml"} {
		p, ok, err := seatFile(o, file)
		if err != nil {
			return err
		} else if !ok {
			continue
		}
		found = true
		b, err := os.ReadFile(p)
		if err != nil {
			return refuse("reading %s: %v; run: ls -l %s", p, err, q(p))
		}
		_, top, err := parseTop(b)
		if err != nil {
			return refuse("%s does not parse as a sops file (anchors, aliases and merge keys are refused)", p)
		}
		for i := 0; i+1 < len(top.Content); i += 2 {
			k, v := top.Content[i], top.Content[i+1]
			switch {
			case k.Value == "sops":
			case k.Kind != yaml.ScalarNode || k.Tag != "!!str" || !envName.MatchString(k.Value) || v.Kind != yaml.ScalarNode:
				refused++
			case slices.Contains(seen, k.Value):
				return deny("duplicate-name", "%s is in both %s.yaml and %s.grants.yaml; a name lives in one file: remove it with: sops unset %s '[\"%s\"]', or stop granting it", k.Value, o.as, o.as, filepath.Join(o.store, o.as+".yaml"), k.Value)
			default:
				seen = append(seen, k.Value)
				out.WriteString(k.Value + "\n")
				shown++
			}
		}
	}
	if !found {
		return noSeatFile(o, o.as)
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
	if o.pins != "" { // the pin directory and the pin are checked, and the pin written, before any store write
		if err := t.pinsOutside(o); err != nil {
			return err
		}
		if _, err := pinned(o.pins, "recovery", rec); err != nil {
			return err
		}
	}
	at := o.store // where git is asked about: the store, or its parent while it is absent
	entries, err := os.ReadDir(o.store)
	absent := errors.Is(err, os.ErrNotExist)
	if absent {
		at = filepath.Dir(o.store)
	} else if err != nil {
		return refuse("%s cannot be read: %v", o.store, err)
	}
	pending := t.initPending(o.store, rec) // written by an init whose commit failed: this one commits it
	var names []string
	for _, e := range entries {
		if names = append(names, e.Name()); !pending && (e.Name() == ".sops.yaml" || e.Name() == "recovery.pub") {
			return refuse("store %s exists; use: nova-secrets seat add <seat> <age-public-key> --store %s", o.store, o.store)
		}
	}
	if names = slices.DeleteFunc(names, func(n string) bool { return n == ".git" || pending && (n == ".sops.yaml" || n == "recovery.pub") }); len(names) > 0 {
		return refuse("%s is not empty (it holds %s); init needs a directory that is absent or empty, and never touches files that are there", o.store, strings.Join(names[:min(len(names), 5)], ", "))
	}
	top, gerr := t.toplevel(at)
	if gerr == nil && realPath(top) != realPath(o.store) {
		return refuse("%s is inside the git work tree %s; make the store a repository of its own: mkdir -p %s && git -C %s init, then run init again", o.store, top, o.store, o.store)
	}
	if absent {
		if err := os.Mkdir(o.store, 0o755); err != nil {
			return refuse("creating %s: %v (its parent must exist)", o.store, err)
		}
	}
	if gerr != nil {
		if err := t.git(o.store, "init", "-q"); err != nil {
			return fmt.Errorf("%w; git says why: run: git init %s, then init again", err, q(o.store))
		}
	}
	files := []string{".sops.yaml", "recovery.pub"}
	for i, body := range []string{"creation_rules: []\n", rec + "\n"} {
		if err := writeFile(filepath.Join(o.store, files[i]), []byte(body), 0o644); err != nil {
			return err
		}
	}
	if err := t.commit(o, "", "init secrets store", "the store is written: run:", files...); err != nil {
		return err
	}
	seat := t.ownSeat() // next: its seat add, with its key's public half; the age-keygen -o that makes the key first
	key := t.keyFile(seat)
	then := next("age-keygen", "-o", key)
	if pub, err := t.publicOf(opts{store: o.store, key: key}); err == nil {
		then = next("nova-secrets", "seat", "add", seat, pub, "--store", o.store)
	}
	fmt.Fprintf(t.stdout, "SECRETS INIT OK store=%s\nnext: %s\n", o.store, then)
	return nil
}

// initPending is whether store holds what init writes for the recovery key rec and no commit: an init whose commit
// failed, which a second init finishes.
func (t tool) initPending(store, rec string) bool {
	cfg, _ := os.ReadFile(filepath.Join(store, ".sops.yaml"))   // ignored: absent or unreadable is not what init wrote
	pub, _ := os.ReadFile(filepath.Join(store, "recovery.pub")) // ignored: as above
	return string(cfg) == "creation_rules: []\n" && string(pub) == rec+"\n" && t.git(store, "rev-parse", "-q", "--verify", "HEAD") != nil
}

// pinned is the pin <dir>/<name>.pub, a public key kept on the control node outside the store, so nothing a
// pusher writes into the store can choose who a grant reaches. want, when given, writes an absent pin and must
// match a present one.
func pinned(dir, name, want string) (string, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", deny("no-pin", "pin directory %s: %v", dir, err)
	}
	if fi, err := os.Lstat(dir); err != nil || !fi.IsDir() || checkPrivate(dir, fi) != nil {
		return "", deny("no-pin", "pin directory %s must be a directory of yours that no one else can write; run: chmod 700 %s", dir, dir)
	}
	p := filepath.Join(dir, name+".pub")
	fi, err := os.Lstat(p)
	if errors.Is(err, os.ErrNotExist) {
		if want == "" {
			return "", deny("no-pin", "no pin for %s in %s; pin it once with %s", name, dir, map[bool]string{true: "--recovery <the recovery public key>", false: "--pub <the machine's age public key, read from the machine>"}[name == "recovery"])
		}
		if !agePub.MatchString(want) {
			return "", bad("%q is not an age public key (age1...)", want)
		}
		return want, writeFile(p, []byte(want+"\n"), 0o644)
	}
	b, _ := os.ReadFile(p)
	pin := strings.TrimSpace(string(b))
	if err != nil || !fi.Mode().IsRegular() || checkPrivate(p, fi) != nil || !agePub.MatchString(pin) {
		return "", deny("no-pin", "%s must be a regular file of yours, writable only by you, holding one age public key", p)
	}
	if want != "" && want != pin {
		return "", deny("key-changed", "%s is pinned to %s, not %s: a rebuilt machine's new key is a person's to pin (remove %s, then grant with --pub)", name, pin, want, p)
	}
	return pin, nil
}

// grant writes <to>.grants.yaml from the --as seat's values, sealed with sops --config /dev/null to the pinned seat
// key and the pinned recovery key: .sops.yaml, operators and the store's recovery.pub choose nothing, and the
// granting key is never a recipient. Model: cmd/nova-secrets/tla/Recipients.tla, Grant.
func (t tool) grant(args []string) error {
	o, pos, err := t.parse("grant", args, 1, true)
	if err != nil {
		return err
	}
	names := strings.Split(pos[0], ",")
	for _, n := range names {
		if !envName.MatchString(n) {
			return bad("NAME %q is not an environment variable name", n)
		}
	}
	if !seatName.MatchString(o.to) || strings.EqualFold(o.to, o.as) || strings.EqualFold(o.to, "recovery") {
		return bad("--to %q: another seat's name (letters, digits, - and _; not the granting seat, in any case, nor recovery)", o.to)
	}
	rec, err := t.pinnedRecovery(o)
	if err != nil {
		return err
	}
	self, err := t.publicOf(o)
	if err != nil {
		return err
	}
	if o.pub == self || rec == self { // before the pin is written, so a refused one is never on disk
		return deny("no-pin", "the pin for %s is this node's own key %s; a grant never reaches the granting key: pin the machine's key", map[bool]string{true: o.to, false: "recovery"}[o.pub == self], self)
	}
	cfg, err := plainConfig(o)
	if err != nil {
		return err
	}
	ruleKey := ""
	if r := ruleFor(cfg, o.to+".yaml"); r != nil && len(ageOf(r)) > 0 {
		ruleKey = ageOf(r)[0]
	}
	if o.pub != "" && ruleKey != o.pub { // before the pin is written, so a refused --pub is never on disk
		return deny("key-changed", "the rule for %s.yaml in .sops.yaml does not name --pub's key %s first (it names %q): the wrong seat, the store was edited, or the machine has a new key (a person settles it)", o.to, o.pub, ruleKey)
	}
	pub, err := pinned(o.pins, o.to, o.pub)
	if err != nil {
		return err
	}
	if pub == self {
		return deny("no-pin", "the pin for %s is this node's own key %s; a grant never reaches the granting key: remove the pin, then grant with --pub <the machine's key>", o.to, self)
	}
	if ruleKey != pub {
		return deny("key-changed", "the rule for %s.yaml in .sops.yaml does not name the pinned key %s first: the store was edited, or the machine has a new key (a person re-pins it)", o.to, pub)
	}
	from, err := t.decrypt(o, o.as)
	if err != nil {
		return err
	}
	file := o.to + ".grants.yaml"
	inFile, err := plainNames(o, file)
	if err != nil {
		return err
	}
	ledgerPath := filepath.Join(o.pins, o.to+".ledger")
	if fi, err := os.Lstat(ledgerPath); err == nil && (!fi.Mode().IsRegular() || checkPrivate(ledgerPath, fi) != nil) {
		return deny("not-regular", "%s is not a regular file of yours (a symlink?); the ledger is read only as itself: remove it, then run again", ledgerPath)
	}
	prior, _ := os.ReadFile(ledgerPath)
	held := ledgerNames(prior) // what this node granted, never what the store's file says it holds
	_, present, _ := seatFile(o, file)
	if present && !slices.Equal(slices.Sorted(slices.Values(inFile)), held) { // an absent file is written again from the ledger
		return deny("grants-changed", "%s names %d value(s), not the %d this node's ledger granted: the store was edited, or the ledger is lost; remove %s from the store, commit, then grant every name again", file, len(inFile), len(held), file)
	}
	names = slices.Compact(slices.Sorted(slices.Values(append(names, held...))))
	values := map[string]string{}
	var absent []string
	for _, n := range names {
		if v, ok := from[n]; ok {
			values[n] = v
		} else {
			absent = append(absent, n)
		}
	}
	if len(absent) > 0 {
		return deny("no-name", "%s holds no %s to grant (%s.grants.yaml is rewritten whole, keeping its names); set it there first", o.as, strings.Join(absent, ", "), o.to)
	}
	ledger, err := t.ledgerOf(o, names, values)
	if err != nil {
		return err
	}
	// unchanged only when the store holds the very bytes this node wrote: a file a pusher re-sealed to the same
	// keys with other values is rewritten; one written but not committed (a failed commit) is committed now
	if data, _ := os.ReadFile(filepath.Join(o.store, file)); string(prior) == ledger+sealedLine(data) && sealedTo(data, file, []string{pub, rec}) == nil && slices.Equal(held, names) {
		_, tracked := t.gitRun(o.store, "ls-files", "--error-unmatch", "--", file)
		_, clean := t.gitRun(o.store, "diff", "--quiet", "HEAD", "--", file)
		if tracked == nil && clean == nil { // tracked and committed: an ignored or a changed file is not
			fmt.Fprintf(t.stdout, "SECRETS GRANT OK from=%s to=%s names=%d changed=0 unchanged\n", o.as, o.to, len(names))
			return nil
		}
		if err := t.commitGrant(o, file, names); err != nil {
			return err
		}
		fmt.Fprintf(t.stdout, "SECRETS GRANT OK from=%s to=%s names=%d changed=0 committed (written before, not committed until now); run: git -C %s push\n", o.as, o.to, len(names), o.store)
		return nil
	}
	out, err := t.seal(o, values, file, pub, rec)
	if err != nil {
		return err
	}
	if err := writeFile(filepath.Join(o.store, file), out, 0o600); err != nil {
		return err
	}
	if err := writeFile(filepath.Join(o.pins, o.to+".ledger"), []byte(ledger+sealedLine(out)), 0o600); err != nil {
		undo := map[bool]string{true: next("git", "-C", o.store, "checkout", "--", file), false: next("rm", "--", filepath.Join(o.store, file))}[present]
		return fmt.Errorf("%w; %s is written but this node's ledger is not, so the next grant would refuse it as edited: put it back: run: %s, then grant again", err, file, undo)
	}
	if err := t.commitGrant(o, file, names); err != nil {
		return err
	}
	fmt.Fprintf(t.stdout, "SECRETS GRANT OK from=%s to=%s names=%d changed=1 committed; run: git -C %s push\n", o.as, o.to, len(names), o.store)
	return nil
}

// commit stages files (git add, with flag when one is given) and commits them as msg; a failure says done, what is
// on disk already, before the git status line that shows it.
func (t tool) commit(o opts, flag, msg, done string, files ...string) error {
	for _, g := range [][]string{append(strings.Fields("add "+flag+" --"), files...), append([]string{"commit", "-q", "-m", msg, "--"}, files...)} {
		if err := t.git(o.store, g...); err != nil {
			if g[0] == "commit" && t.git(o.store, "var", "GIT_COMMITTER_IDENT") != nil { // git commits only with an author
				return fmt.Errorf("%w: git has no commit identity; %s git -C %s config user.name 'Your Name' && git -C %s config user.email you@example.com && %s",
					err, done, q(o.store), q(o.store), next(append([]string{"git", "-C", o.store}, g...)...))
			}
			return fmt.Errorf("%w; %s git -C %s status", err, done, o.store)
		}
	}
	return nil
}

// commitGrant commits a grants file; the ledger records it already, so a failed commit is finished by the next grant.
func (t tool) commitGrant(o opts, file string, names []string) error { // -f: a pusher's .gitignore does not hide it
	return t.commit(o, "-f", "grant "+strings.Join(names, ",")+" to "+file, "the file is written and the ledger records it: the next grant commits it, or:", file)
}

// sealedLine is the ledger's last line, the digest of the ciphertext this node wrote.
func sealedLine(out []byte) string { return fmt.Sprintf("#sealed %x\n", sha256.Sum256(out)) }

// ledgerNames are the names a ledger records, sorted.
func ledgerNames(ledger []byte) []string {
	names := []string{}
	for _, l := range strings.Split(string(ledger), "\n") {
		if n, _, ok := strings.Cut(l, " "); ok && !strings.HasPrefix(n, "#") {
			names = append(names, n)
		}
	}
	slices.Sort(names)
	return names
}

// ledgerOf is the control node's record of a grant: one "NAME HMAC" line per name, the HMAC of seat, name and
// value keyed by a hash of the granting key file, so it shows whether a value changed without holding one; the
// digest of the ciphertext written follows it (sealedLine).
func (t tool) ledgerOf(o opts, names []string, values map[string]string) (string, error) {
	secret, err := os.ReadFile(o.key)
	if err != nil {
		return "", deny("no-key", "key file %s: %v", o.key, err)
	}
	k := sha256.Sum256(append([]byte("nova-secrets grant ledger v1\x00"), secret...))
	var b strings.Builder
	for _, n := range names {
		m := hmac.New(sha256.New, k[:])
		m.Write([]byte(o.to + "\x00" + n + "\x00" + values[n]))
		fmt.Fprintf(&b, "%s %x\n", n, m.Sum(nil))
	}
	return b.String(), nil
}
