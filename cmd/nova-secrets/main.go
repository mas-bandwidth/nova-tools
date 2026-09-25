package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/buildinfo"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/secrets"
)

const usage = `nova-secrets: credentials for seats, pools and services (see docs/SPEC-SECRETS.md)

usage:
  nova-secrets version  print this build identity (--version also accepted)
  nova-secrets exec   --store <dir> --as <name> --key <path> --sops <path> --only <NAME,...|all> [--require <NAME>]... -- <cmd> [args...]
  nova-secrets names  --store <dir> --as <name> [--max <n>]
  nova-secrets check  --store <dir> --as <name> --key <path> --sops <path> [--max <n>]
  nova-secrets gate   --store <dir> --base <git ref> --head <git ref> [--machines <registry>]
  nova-secrets keygen --as <name> --key <path> --age-keygen <path> [--store <dir>]
  nova-secrets place  --store <dir> --as <name> --key <path> --sops <path> --machine <name> --secret <name> [--path <remote path>] [--machines <file>] [--receipts <dir>] [--ssh <path>]
  nova-secrets placed --machine <name> [--receipts <dir>]
  nova-secrets seal   --store <dir> --as <seat> --key <path> --sops <path> --name NAME [--stdin] [--no-pr] [--gh <path>] [--git <path>]
  nova-secrets seat add --store <dir> --as <seat> --pub <age1…> --from <source seat> --only <NAME,...> --key <path> --sops <path>
  nova-secrets help

flags:
  --store <dir>        git working copy of the secrets store; check and exec also need it on a
                       named branch with an upstream tracking ref (see: nova-secrets check --help)
  --as <name>          seat name selecting <store>/<name>.yaml
  --key <path>         path to age private key identity file (mode 0600)
  --sops <path>        path to sops executable
  --age-keygen <path>  path to age-keygen executable
  --only <names|all>   comma-separated list of keys to inject, or 'all'
  --require <name>     assert key must be present in the file (repeatable)
  --max <n>            maximum items shown before MORE line (default 20, 0=unlimited)
  --machine <name>     fleet machine to place a secret on (its target comes from --machines)
  --secret <name>      the key in <store>/<as>.yaml to copy to the machine
  --path <remote path> remote path to write; default <home>/.config/nova-secrets/<secret>.env
  --machines <file>    place: fleet registry file: name, ssh target, home, tab separated
                       gate: the fleet machines registry whose seat column vouches for a
                       new recipient; without it that rule does not run and the APPROVE
                       line says machines=-
  --receipts <dir>     where placed receipts live; default ~/.config/nova-secrets/placed
  --ssh <path>         ssh executable to use (default ssh)
  --name NAME          key to seal (seal only)
  --pub <age1…>        the new seat's age public key, from its own keygen receipt (seat add only)
  --from <seat>        a seat this machine can open, whose values are re-sealed (seat add only)
  --stdin              read the value from stdin instead of the terminal (seal only)
  --no-pr              stop after the commit; make no gh call; return the store to its starting branch (seal only)
  --gh <path>          path to the gh executable (seal only, default: gh)
  --git <path>         path to the git executable (seal only, default: git)

example:
  nova-secrets keygen --as rowan --key ~/.config/nova-secrets/rowan.key --age-keygen /opt/homebrew/bin/age-keygen
  nova-secrets names  --store ./secrets --as rowan
  nova-secrets check  --store ./secrets --as rowan --key ~/.config/nova-secrets/rowan.key --sops /opt/homebrew/bin/sops
  nova-secrets exec   --store ./secrets --as rowan --key ~/.config/nova-secrets/rowan.key --sops /opt/homebrew/bin/sops --only GH_TOKEN --require GH_TOKEN -- gh api user
  nova-secrets place  --store ./secrets --as rowan --key ~/.config/nova-secrets/rowan.key --sops /opt/homebrew/bin/sops --machine mini --secret DEEPSEEK_API_KEY --machines ./fleet.tsv
  nova-secrets placed --machine mini
  nova-secrets seat add --store ./secrets --as air --pub age1… --from rowan --only GH_TOKEN,DEEPSEEK_API_KEY --key ~/.config/nova-secrets/rowan.key --sops /opt/homebrew/bin/sops
`

// storeUpstreamHelp is the store prerequisite check and exec enforce (SPEC-SECRETS
// invariant 8), printed by both verbs' own help so a caller learns it before the
// refusal does (nova-tools#3550).
const storeUpstreamHelp = `store prerequisite:
  --store must be a git working copy on a named branch (not a detached HEAD) with an
  upstream tracking ref: branch.<name>.remote and branch.<name>.merge in .git/config,
  and HEAD must equal that remote-tracking ref. Everything is read from .git as files;
  no network call is made.

  why: the working copy must be the store, not a memory of it. HEAD behind its ref is
  a value a rotation replaced; HEAD ahead of it is a local edit nobody reviewed. Both
  are refused, and neither can be told apart without an upstream to compare against.

  next, when it is refused:
    no upstream on a store with a remote:
      git -C <store> switch <the branch that tracks the remote>
      git -C <store> branch --set-upstream-to=<remote>/<branch>   (the remote branch exists)
    a local/offline store with no remote, given a local bare one:
      git init --bare <dir> && git -C <store> remote add origin <dir> && git -C <store> push -u origin <branch>
    HEAD behind or ahead of its ref:
      git -C <store> pull --ff-only
`

const checkUsage = `nova-secrets check: verify the store and this seat's file and key, printing no value

usage:
  nova-secrets check --store <dir> --as <name> --key <path> --sops <path> [--max <n>]

exit: 0 green (one OK line with head=<short sha>), 1 red (one line per failure),
2 refused (one SECRETS REFUSED line naming the remedy)

` + storeUpstreamHelp

const execUsage = `nova-secrets exec: run a command with only the named keys from this seat's file in its environment

usage:
  nova-secrets exec --store <dir> --as <name> --key <path> --sops <path> --only <NAME,...|all> [--require <NAME>]... -- <cmd> [args...]

exit: the command's own code; 125 when exec itself fails (one SECRETS EXEC FAIL line
naming the remedy) and the command never runs

` + storeUpstreamHelp

// isHelpArg is the spelling of a verb's own help request.
func isHelpArg(a string) bool { return a == "--help" || a == "-h" || a == "help" }

// version is empty in ordinary builds and is filled only by a release stamp.
var version string

var disallowedVerbs = map[string]string{
	"get":        "Refused forever. No verb prints a secret value and no flag makes one. A person who must see a value holds the key and runs 'sops -d <file>' with their own hands.",
	"print":      "Refused forever. No verb prints a secret value and no flag makes one. A person who must see a value holds the key and runs 'sops -d <file>' with their own hands.",
	"show":       "Refused forever. No verb prints a secret value and no flag makes one. A person who must see a value holds the key and runs 'sops -d <file>' with their own hands.",
	"cat":        "Refused forever. No verb prints a secret value and no flag makes one. A person who must see a value holds the key and runs 'sops -d <file>' with their own hands.",
	"put":        "run 'sops <store>/<name>.yaml' or 'sops set'; then git add, git commit, and a pull request the other collaborator approves.",
	"set":        "run 'sops <store>/<name>.yaml' or 'sops set'; then git add, git commit, and a pull request the other collaborator approves.",
	"add":        "run 'sops <store>/<name>.yaml' or 'sops set'; then git add, git commit, and a pull request the other collaborator approves.",
	"edit":       "run 'sops <store>/<name>.yaml' or 'sops set'; then git add, git commit, and a pull request the other collaborator approves.",
	"rotate":     "rotate at the provider's console (store administrator), then sops, then an approved pull request, then a pull on every bench, then a probe. See docs/SPEC-SECRETS.md Rotation.",
	"delete":     "run 'sops unset' or 'git rm', and rotate whatever the deleted value was.",
	"rm":         "run 'sops unset' or 'git rm', and rotate whatever the deleted value was.",
	"unset":      "run 'sops unset' or 'git rm', and rotate whatever the deleted value was.",
	"recipients": "open a pull request against .sops.yaml editing one rule, approved by the other collaborator and merged under the ruleset, then sops updatekeys in a second one.",
	"grant":      "open a pull request against .sops.yaml editing one rule, approved by the other collaborator and merged under the ruleset, then sops updatekeys in a second one.",
	"revoke":     "open a pull request against .sops.yaml editing one rule, approved by the other collaborator and merged under the ruleset, then sops updatekeys in a second one.",
	"keychain":   "reading or writing the macOS Keychain is not supported by this tool on any bench, ever.",
	"daemon":     "not supported. Every call opens the file again; a cached plaintext is a plaintext with a lifetime nobody is watching.",
	"agent":      "not supported. Every call opens the file again; a cached plaintext is a plaintext with a lifetime nobody is watching.",
	"cache":      "not supported. Every call opens the file again; a cached plaintext is a plaintext with a lifetime nobody is watching.",
	"session":    "not supported. Every call opens the file again; a cached plaintext is a plaintext with a lifetime nobody is watching.",
}

// sopsIdentityEnv is every variable in sops' documented age identity lookup. None of
// them reaches the command exec starts.
var sopsIdentityEnv = []string{
	"SOPS_AGE_KEY_FILE",
	"SOPS_AGE_KEY",
	"SOPS_AGE_KEY_CMD",
	"SOPS_AGE_SSH_PRIVATE_KEY_FILE",
	"SOPS_KEYSERVICE",
}

type stringSlice []string

func (s *stringSlice) String() string {
	return strings.Join(*s, ",")
}

func (s *stringSlice) Set(val string) error {
	*s = append(*s, val)
	return nil
}

// redisWidthWriteVerbs are the redis-cli spellings that write or remove a
// string key. redis takes its command names in any case; the width key is a
// string, so the hash and list writes are not this set.
var redisWidthWriteVerbs = map[string]bool{
	"set": true, "setnx": true, "setex": true, "psetex": true, "getset": true,
	"mset": true, "msetnx": true, "del": true, "unlink": true,
}

// friendWidthName reports the friend a token names as its working column,
// friend:<name>:width -- the sprint table's key -- or "" when it is not that
// key (nova-tools#2676).
func friendWidthName(tok string) string {
	if !strings.HasPrefix(tok, "friend:") || !strings.HasSuffix(tok, ":width") {
		return ""
	}
	name := strings.TrimSuffix(strings.TrimPrefix(tok, "friend:"), ":width")
	if !secrets.IsValidAsName(name) {
		return ""
	}
	return name
}

// refusedWidthHandWrite is the one command exec refuses for a reason that is
// another tool's law (nova-tools#2676): redis-cli writing
// friend:<name>:width, the sprint table's working column. Since #3447 that
// column is the friend row's working count, written by the row loop
// (rowan-tools friend-row) from the friend's leased tasks; nova-wake beat
// refuses the row, so the remedy is taking work through the queue, never a
// beat and never a hand-write (nova-tools#3807). The hand-write reached the
// store only because the store held REDISCLI_AUTH. Reads of the key still run.
func refusedWidthHandWrite(cmdArgs []string) error {
	base := filepath.Base(cmdArgs[0])
	if base != "redis-cli" && base != "redis-cli.exe" {
		return nil
	}
	writes, name := false, ""
	for i := 1; i < len(cmdArgs); i++ {
		tok := cmdArgs[i]
		if redisWidthWriteVerbs[strings.ToLower(tok)] {
			writes = true
		}
		if n := friendWidthName(tok); n != "" {
			name = n
		}
	}
	if !writes || name == "" {
		return nil
	}
	return fmt.Errorf("redis-cli writing friend:%s:width is the sprint table's working column written by hand through nova-secrets exec; that count is the friend row's (friend:%s working), written only by the friend row loop (rowan-tools friend-row) from the friend's leased tasks, never a redis-cli line; take work through the queue: nova-sprint task take --as %s (nova-tools #3447)", name, name, name)
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "SECRETS REFUSED: no arguments is not an invocation; run: nova-secrets help\n")
		os.Exit(2)
	}

	verb := os.Args[1]
	if verb == "version" || verb == "--version" {
		os.Exit(cmdVersion(os.Args[2:], os.Stdout, os.Stderr))
	}

	// Check refusal table first
	if msg, refused := disallowedVerbs[verb]; refused {
		fmt.Fprintf(os.Stderr, "SECRETS REFUSED: %s\n", msg)
		os.Exit(2)
	}

	switch verb {
	case "help", "--help", "-h":
		fmt.Print(usage)
		os.Exit(0)

	case "exec":
		runExecCLI(os.Args[2:])

	case "names":
		runNamesCLI(os.Args[2:])

	case "check":
		runCheckCLI(os.Args[2:])

	case "gate":
		runGateCLI(os.Args[2:])

	case "keygen":
		runKeygenCLI(os.Args[2:])

	case "place":
		runPlaceCLI(os.Args[2:])

	case "placed":
		runPlacedCLI(os.Args[2:])

	case "seal":
		runSealCLI(os.Args[2:])

	case "seat":
		runSeatCLI(os.Args[2:])

	default:
		fmt.Fprintf(os.Stderr, "SECRETS REFUSED: unknown verb %q; run: nova-secrets help\n", oneline.Field(verb))
		os.Exit(2)
	}
}

// cmdVersion answers from the running binary alone. It deliberately opens no store, key,
// or helper program, so an inventory can ask this before any credentials exist on a bench.
func cmdVersion(args []string, stdout, stderr io.Writer) int {
	if len(args) != 0 {
		fmt.Fprintf(stderr, "nova-secrets version: takes no flags and no arguments, got %d\n", len(args))
		return 2
	}
	fmt.Fprintln(stdout, buildinfo.Line("nova-secrets", version))
	return 0
}

func runExecCLI(args []string) {
	if len(args) > 0 && isHelpArg(args[0]) {
		fmt.Print(execUsage)
		os.Exit(0)
	}

	// Find '--' delimiter
	delimiterIdx := -1
	for i, arg := range args {
		if arg == "--" {
			delimiterIdx = i
			break
		}
	}

	if delimiterIdx == -1 {
		fmt.Fprintf(os.Stderr, "SECRETS EXEC FAIL invocation: missing '--' delimiter before command\n")
		os.Exit(125)
	}

	flagArgs := args[:delimiterIdx]
	cmdArgs := args[delimiterIdx+1:]

	if len(cmdArgs) == 0 {
		fmt.Fprintf(os.Stderr, "SECRETS EXEC FAIL invocation: no command specified after '--'\n")
		os.Exit(125)
	}

	// nova-tools#2676: the sprint table's working column is the friend's own
	// tool's to write, never a redis-cli line through this exec; the
	// hand-write of it went through because the store held REDISCLI_AUTH.
	if err := refusedWidthHandWrite(cmdArgs); err != nil {
		fmt.Fprintf(os.Stderr, "SECRETS EXEC FAIL %s\n", oneline.Err(err))
		os.Exit(125)
	}

	fs := flag.NewFlagSet("exec", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	storeFlag := fs.String("store", "", "store dir")
	asFlag := fs.String("as", "", "seat name")
	keyFlag := fs.String("key", "", "key path")
	sopsFlag := fs.String("sops", "", "sops path")
	onlyFlag := fs.String("only", "", "only keys")
	var requireFlags stringSlice
	fs.Var(&requireFlags, "require", "required key")

	if err := fs.Parse(flagArgs); err != nil {
		fmt.Fprintf(os.Stderr, "SECRETS EXEC FAIL flags: %s\n", oneline.Err(err))
		os.Exit(125)
	}

	if len(fs.Args()) > 0 {
		fmt.Fprintf(os.Stderr, "SECRETS EXEC FAIL flags: unexpected argument %q before '--'\n", oneline.Field(fs.Args()[0]))
		os.Exit(125)
	}

	// A .git that is a FILE is a worktree or a submodule: its real repository lives
	// elsewhere, so HEAD and the tracking ref read here would not be the store's. Refuse
	// naming that fact, the sentence check prints, not "no .git directory" (SPEC-SECRETS
	// test 20).
	if *storeFlag != "" {
		if fi, err := os.Lstat(*storeFlag + string(os.PathSeparator) + ".git"); err == nil && !fi.IsDir() {
			fmt.Fprintf(os.Stderr, "SECRETS EXEC FAIL store %s: .git is a file (a worktree or submodule); expected a directory working copy\n", oneline.Escape(*storeFlag))
			os.Exit(125)
		}
	}

	// sops' identity lookup is defeated for the command as well as for the sops child:
	// every variable in it is dropped from this process before anything runs, so the
	// command, which this process becomes, never inherits a route to another key
	// (SPEC-SECRETS test 2). The sops child's environment is built, not edited, inside
	// the package.
	for _, name := range sopsIdentityEnv {
		_ = os.Unsetenv(name)
	}

	code, err := secrets.RunExec(*storeFlag, *asFlag, *keyFlag, *sopsFlag, *onlyFlag, requireFlags, cmdArgs)
	if err != nil {
		fmt.Fprintf(os.Stderr, "SECRETS EXEC FAIL %s\n", oneline.Err(err))
		os.Exit(code)
	}
}

func runNamesCLI(args []string) {
	fs := flag.NewFlagSet("names", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	storeFlag := fs.String("store", "", "store dir")
	asFlag := fs.String("as", "", "seat name")
	maxFlag := fs.Int("max", 20, "max items")

	if err := fs.Parse(args); err != nil {
		fmt.Fprintf(os.Stderr, "SECRETS REFUSED: %s\n", oneline.Err(err))
		os.Exit(2)
	}

	if len(fs.Args()) > 0 {
		fmt.Fprintf(os.Stderr, "SECRETS REFUSED: unexpected argument %q\n", oneline.Field(fs.Args()[0]))
		os.Exit(2)
	}

	okLine, names, more, err := secrets.RunNames(*storeFlag, *asFlag, *maxFlag)
	if err != nil {
		fmt.Fprintf(os.Stderr, "SECRETS REFUSED: %s\n", oneline.Err(err))
		os.Exit(2)
	}

	for _, n := range names {
		fmt.Println(n)
	}
	if more != "" {
		fmt.Println(more)
	}
	fmt.Println(okLine)
	os.Exit(0)
}

func runCheckCLI(args []string) {
	if len(args) > 0 && isHelpArg(args[0]) {
		fmt.Print(checkUsage)
		os.Exit(0)
	}

	fs := flag.NewFlagSet("check", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	storeFlag := fs.String("store", "", "store dir")
	asFlag := fs.String("as", "", "seat name")
	keyFlag := fs.String("key", "", "key path")
	sopsFlag := fs.String("sops", "", "sops path")
	maxFlag := fs.Int("max", 20, "max items")

	if err := fs.Parse(args); err != nil {
		fmt.Fprintf(os.Stderr, "SECRETS REFUSED: %s\n", oneline.Err(err))
		os.Exit(2)
	}

	if len(fs.Args()) > 0 {
		fmt.Fprintf(os.Stderr, "SECRETS REFUSED: unexpected argument %q\n", oneline.Field(fs.Args()[0]))
		os.Exit(2)
	}

	okLine, failLines, moreLines, summaryLine, code, err := secrets.RunCheck(*storeFlag, *asFlag, *keyFlag, *sopsFlag, *maxFlag)
	if err != nil {
		fmt.Fprintf(os.Stderr, "SECRETS REFUSED: %s\n", oneline.Err(err))
		os.Exit(code)
	}

	if code == 0 {
		fmt.Println(okLine)
		os.Exit(0)
	}

	// Failure output
	for _, l := range failLines {
		fmt.Fprintln(os.Stderr, l)
	}
	for _, m := range moreLines {
		fmt.Fprintln(os.Stderr, m)
	}
	fmt.Fprintln(os.Stderr, summaryLine)
	os.Exit(code)
}

func runGateCLI(args []string) {
	fs := flag.NewFlagSet("gate", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	storeFlag := fs.String("store", "", "store dir")
	baseFlag := fs.String("base", "", "base git ref")
	headFlag := fs.String("head", "", "head git ref")
	machinesFlag := fs.String("machines", "", "fleet machines registry; its seat column vouches for a new recipient")

	if err := fs.Parse(args); err != nil {
		fmt.Fprintf(os.Stderr, "SECRETS REFUSED: %s\n", oneline.Err(err))
		os.Exit(2)
	}

	if len(fs.Args()) > 0 {
		fmt.Fprintf(os.Stderr, "SECRETS REFUSED: unexpected argument %q\n", oneline.Field(fs.Args()[0]))
		os.Exit(2)
	}

	line, code := secrets.RunGate(secrets.GateInput{
		StoreDir:     *storeFlag,
		Base:         *baseFlag,
		Head:         *headFlag,
		MachinesPath: *machinesFlag,
	})
	if code != 0 {
		fmt.Fprintln(os.Stderr, line)
	} else {
		fmt.Println(line)
	}
	os.Exit(code)
}

func runKeygenCLI(args []string) {
	fs := flag.NewFlagSet("keygen", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	asFlag := fs.String("as", "", "seat name")
	keyFlag := fs.String("key", "", "key path")
	ageKeygenFlag := fs.String("age-keygen", "", "age-keygen path")
	storeFlag := fs.String("store", "", "store dir")

	if err := fs.Parse(args); err != nil {
		fmt.Fprintf(os.Stderr, "SECRETS REFUSED: %s\n", oneline.Err(err))
		os.Exit(2)
	}

	if len(fs.Args()) > 0 {
		fmt.Fprintf(os.Stderr, "SECRETS REFUSED: unexpected argument %q\n", oneline.Field(fs.Args()[0]))
		os.Exit(2)
	}

	// The order is the package's, not this function's: the rule block, then the next
	// step, then the OK line LAST. Glenn read a green keygen as a failure because the
	// verdict was at the top and the homework at the bottom (nova-tools#1393).
	lines, err := secrets.RunKeygen(*asFlag, *keyFlag, *ageKeygenFlag, *storeFlag)
	if err != nil {
		fmt.Fprintf(os.Stderr, "SECRETS REFUSED: %s\n", oneline.Err(err))
		os.Exit(2)
	}

	for _, l := range lines {
		fmt.Println(l)
	}
	os.Exit(0)
}

// runSeatCLI dispatches the seat subverbs. `add` is the only one: every other change to
// a seat is a pull request against .sops.yaml that the store's gate reviews.
func runSeatCLI(args []string) {
	if len(args) == 0 {
		fmt.Fprintf(os.Stderr, "SECRETS REFUSED: seat takes a subverb; the only one is 'add'; run: nova-secrets help\n")
		os.Exit(2)
	}
	switch args[0] {
	case "add":
		runSeatAddCLI(args[1:])
	case "help", "--help", "-h":
		fmt.Print(usage)
		os.Exit(0)
	default:
		fmt.Fprintf(os.Stderr, "SECRETS REFUSED: unknown seat subverb %q; the only one is 'add'\n", oneline.Field(args[0]))
		os.Exit(2)
	}
}

func runSeatAddCLI(args []string) {
	if len(args) > 0 && (args[0] == "--help" || args[0] == "-h" || args[0] == "help") {
		fmt.Print(usage)
		os.Exit(0)
	}

	fs := flag.NewFlagSet("seat add", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	storeFlag := fs.String("store", "", "store dir")
	asFlag := fs.String("as", "", "the new seat")
	pubFlag := fs.String("pub", "", "the new seat's age public key")
	fromFlag := fs.String("from", "", "a seat this machine can open")
	onlyFlag := fs.String("only", "", "keys to carry over")
	keyFlag := fs.String("key", "", "this machine's key path")
	sopsFlag := fs.String("sops", "", "sops path")

	if err := fs.Parse(args); err != nil {
		fmt.Fprintf(os.Stderr, "SECRETS REFUSED: %s\n", oneline.Err(err))
		os.Exit(2)
	}
	if len(fs.Args()) > 0 {
		fmt.Fprintf(os.Stderr, "SECRETS REFUSED: unexpected argument %q\n", oneline.Field(fs.Args()[0]))
		os.Exit(2)
	}

	lines, err := secrets.RunSeatAdd(secrets.SeatAddOptions{
		StoreDir: *storeFlag,
		AsName:   *asFlag,
		Pub:      *pubFlag,
		From:     *fromFlag,
		Only:     *onlyFlag,
		KeyPath:  *keyFlag,
		SopsPath: *sopsFlag,
		Progress: os.Stderr,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "SECRETS SEAT ADD FAIL %s\n", oneline.Err(err))
		os.Exit(2)
	}
	for _, l := range lines {
		fmt.Println(l)
	}
	os.Exit(0)
}

func runPlaceCLI(args []string) {
	fs := flag.NewFlagSet("place", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	storeFlag := fs.String("store", "", "store dir")
	asFlag := fs.String("as", "", "seat name")
	keyFlag := fs.String("key", "", "key path")
	sopsFlag := fs.String("sops", "", "sops path")
	machineFlag := fs.String("machine", "", "fleet machine name")
	secretFlag := fs.String("secret", "", "secret key name")
	pathFlag := fs.String("path", "", "remote path")
	machinesFlag := fs.String("machines", "", "fleet registry file")
	receiptsFlag := fs.String("receipts", "", "receipts dir")
	sshFlag := fs.String("ssh", "ssh", "ssh executable")

	if err := fs.Parse(args); err != nil {
		fmt.Fprintf(os.Stderr, "SECRETS REFUSED: %s\n", oneline.Err(err))
		os.Exit(2)
	}
	if len(fs.Args()) > 0 {
		fmt.Fprintf(os.Stderr, "SECRETS REFUSED: unexpected argument %q\n", oneline.Field(fs.Args()[0]))
		os.Exit(2)
	}

	okLine, err := secrets.RunPlace(secrets.PlaceInput{
		StoreDir:   *storeFlag,
		AsName:     *asFlag,
		KeyPath:    *keyFlag,
		SopsPath:   *sopsFlag,
		Machine:    *machineFlag,
		Secret:     *secretFlag,
		RemotePath: *pathFlag,
		Machines:   *machinesFlag,
		Receipts:   *receiptsFlag,
		SSH:        *sshFlag,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "SECRETS REFUSED: %s\n", oneline.Err(err))
		os.Exit(2)
	}
	fmt.Println(okLine)
	os.Exit(0)
}

func runPlacedCLI(args []string) {
	fs := flag.NewFlagSet("placed", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	machineFlag := fs.String("machine", "", "fleet machine name")
	receiptsFlag := fs.String("receipts", "", "receipts dir")

	if err := fs.Parse(args); err != nil {
		fmt.Fprintf(os.Stderr, "SECRETS REFUSED: %s\n", oneline.Err(err))
		os.Exit(2)
	}
	if len(fs.Args()) > 0 {
		fmt.Fprintf(os.Stderr, "SECRETS REFUSED: unexpected argument %q\n", oneline.Field(fs.Args()[0]))
		os.Exit(2)
	}

	okLine, itemLines, err := secrets.RunPlaced(secrets.PlacedInput{
		Machine:  *machineFlag,
		Receipts: *receiptsFlag,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "SECRETS REFUSED: %s\n", oneline.Err(err))
		os.Exit(2)
	}
	fmt.Println(okLine)
	for _, l := range itemLines {
		fmt.Println(l)
	}
	os.Exit(0)
}

func runSealCLI(args []string) {
	fs := flag.NewFlagSet("seal", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	storeFlag := fs.String("store", "", "store dir")
	asFlag := fs.String("as", "", "seat name")
	keyFlag := fs.String("key", "", "key path")
	sopsFlag := fs.String("sops", "", "sops path")
	nameFlag := fs.String("name", "", "key to seal")
	ghFlag := fs.String("gh", "gh", "gh path")
	gitFlag := fs.String("git", "git", "git path")
	stdinFlag := fs.Bool("stdin", false, "read value from stdin")
	noPRFlag := fs.Bool("no-pr", false, "stop after commit; return the store to its starting branch")

	if err := fs.Parse(args); err != nil {
		fmt.Fprintf(os.Stderr, "SECRETS REFUSED: %s\n", oneline.Err(err))
		os.Exit(2)
	}
	if len(fs.Args()) > 0 {
		fmt.Fprintf(os.Stderr, "SECRETS REFUSED: unexpected argument %q\n", oneline.Field(fs.Args()[0]))
		os.Exit(2)
	}

	fi, statErr := os.Stdin.Stat()
	stdinIsTerminal := statErr == nil && fi.Mode()&os.ModeCharDevice != 0

	line, err := secrets.RunSeal(secrets.SealOptions{
		StoreDir:        *storeFlag,
		AsName:          *asFlag,
		KeyPath:         *keyFlag,
		SopsPath:        *sopsFlag,
		Name:            *nameFlag,
		GHPath:          *ghFlag,
		GitPath:         *gitFlag,
		NoPR:            *noPRFlag,
		UseStdin:        *stdinFlag,
		Stdin:           os.Stdin,
		StdinIsTerminal: stdinIsTerminal,
		Progress:        os.Stderr,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "SECRETS SEAL FAIL %s\n", oneline.Err(err))
		os.Exit(2)
	}

	fmt.Println(line)
	os.Exit(0)
}
