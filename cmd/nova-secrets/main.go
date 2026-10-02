package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/buildinfo"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/verbflag"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/secrets"
	"github.com/mas-bandwidth/nova-tools/internal/tool"
)

const usage = `nova-secrets: encrypted secrets in a git repository, handed to one command at a time

how it works: the store is a git working copy with a .sops.yaml and one
sops-encrypted <seat>.yaml per seat; a seat is a named identity whose age key
file (mode 0600) opens that file. exec decrypts only the names in --only into
one command's environment; names lists the names without decrypting; no value
is ever printed. check and exec want the store on a branch with an upstream.
first run: keygen makes a key (it needs age-keygen); the other lines need a store
made with git init and a .sops.yaml naming your key, and sops on PATH.

usage:
  nova-secrets version  print this build identity (--version also accepted)
  nova-secrets exec   --store <dir> --as <name> --key <path> --sops <path> --only <NAME,...|all> [--require <NAME>]... -- <cmd> [args...]
  nova-secrets names  --store <dir> --as <name> [--max <n>] [--json]
  nova-secrets check  --store <dir> --as <name> --key <path> --sops <path> [--max <n>]
  nova-secrets gate   --store <dir> --base <git ref> --head <git ref> [--machines <registry>]
  nova-secrets keygen --as <name> --key <path> --age-keygen <path> [--store <dir>]
  nova-secrets place  --store <dir> --as <name> --key <path> --sops <path> --machine <name> --secret <name> [--path <remote path>] [--machines <file>] [--receipts <dir>] [--ssh <path>] [--dry-run]
  nova-secrets placed --machine <name> [--receipts <dir>]
  nova-secrets seal   --store <dir> --as <seat> --key <path> --sops <path> --name NAME [--stdin] [--no-pr] [--dry-run] [--gh <path>] [--git <path>]
  nova-secrets seat add --store <dir> --as <seat> --pub <age1…> --from <source seat> --only <NAME,...> --key <path> --sops <path>
  nova-secrets seat inject --store <dir> --as <seat> --from <source seat> --only <NAME,...> --key <path> --sops <path> [--no-pr] [--dry-run] [--gh <path>] [--git <path>]
  nova-secrets help

flags:
  --store <dir>        git working copy of the secrets store; check and exec also need it on a
                       named branch with an upstream tracking ref (see: nova-secrets check --help)
  --as <name>          seat name selecting <store>/<name>.yaml
  --key <path>         path to age private key identity file (mode 0600)
  --sops <path>        path to sops executable
  --age-keygen <path>  path to age-keygen executable
  --only <names|all>   comma-separated list of keys to deliver, or 'all' (exec only)
  --require <name>     assert key must be present in the file (repeatable)
  --max <n>            maximum items shown before MORE line (default 20, 0=unlimited)
  --json               names only: the result as one JSON object on stdout, a refusal included
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
  --from <seat>        a seat this machine can open, whose values are re-sealed (seat add, seat inject)
  --stdin              read the value from stdin instead of the terminal (seal only)
  --no-pr              stop after the commit; make no gh call; return the store to its starting branch (seal, seat inject)
  --dry-run            prints the plan and writes nothing (place, seal, seat inject): the file, the
                       recipients, the machine and remote path, the branch and the pull request the
                       real run would take, as PLAN lines ending in DRY-RUN OK, exit 0; no ssh, no
                       git write or push, no gh call, no sops encrypt, no value read or shown
  --gh <path>          path to the gh executable (seal, seat inject; default: gh)
  --git <path>         path to the git executable (seal, seat inject; default: git)

exit codes: 0 ran and passed, 1 check found the store red (one line per
failure), 2 could not run or refused (one line naming the remedy); exec ends with
the command's own status, and 125 when exec itself refused and the command never
ran.

example:
  nova-secrets keygen --as ada --key ~/.config/nova-secrets/ada.key --age-keygen /opt/homebrew/bin/age-keygen
  nova-secrets names  --store ./secrets --as ada
  nova-secrets check  --store ./secrets --as ada --key ~/.config/nova-secrets/ada.key --sops /opt/homebrew/bin/sops
  nova-secrets exec   --store ./secrets --as ada --key ~/.config/nova-secrets/ada.key --sops /opt/homebrew/bin/sops --only GH_TOKEN --require GH_TOKEN -- gh api user
  nova-secrets place  --store ./secrets --as ada --key ~/.config/nova-secrets/ada.key --sops /opt/homebrew/bin/sops --machine bench-a --secret DEEPSEEK_API_KEY --machines ./fleet.tsv
  nova-secrets place  --store ./secrets --as worker --key ~/.config/nova-secrets/worker.key --sops /opt/homebrew/bin/sops --machine bench --secret API_KEY --machines ./fleet.tsv --dry-run
  nova-secrets placed --machine bench-a
  nova-secrets seal   --store ./secrets --as worker --key ~/.config/nova-secrets/worker.key --sops /opt/homebrew/bin/sops --name API_KEY --dry-run
  nova-secrets seat add --store ./secrets --as bo --pub $BO_PUB --from ada --only GH_TOKEN,DEEPSEEK_API_KEY --key ~/.config/nova-secrets/ada.key --sops /opt/homebrew/bin/sops
  nova-secrets seat inject --store ./secrets --as bo --from ada --only NOVA_REDIS_BENCH_PASSWORD --key ~/.config/nova-secrets/ada.key --sops /opt/homebrew/bin/sops --no-pr
  nova-secrets seat inject --store ./secrets --as worker --from lead --only API_KEY --key ~/.config/nova-secrets/lead.key --sops /opt/homebrew/bin/sops --dry-run
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

// verbEffects is each verb's effect (STANDARD §2: an inspection, a local write, a store
// write or a delivery), printed in its own help above its flags.
var verbEffects = map[string]string{
	"exec":        "effect: delivery. Decrypts <store>/<as>.yaml with --key and becomes <cmd>, with only the --only values in its environment; writes nothing, prints no value.",
	"names":       "effect: inspection. Reads the key names in <store>/<as>.yaml without decrypting it; writes nothing.",
	"check":       "effect: inspection. Reads the store's files and git state and decrypts this seat's file with --key to prove it opens; writes nothing, prints no value.",
	"gate":        "effect: inspection. Diffs --base..--head in the store with git and judges every seat rule change; writes nothing, calls no network.",
	"keygen":      "effect: local write. Makes one age key file at --key (mode 0600) with --age-keygen, never over an existing file, and prints the .sops.yaml rule block for it.",
	"place":       "effect: delivery. Copies one value over ssh to --machine (on ssh's stdin, never in an argument) and writes a receipt under --receipts; --dry-run writes nothing and runs no ssh.",
	"placed":      "effect: inspection. Lists the receipts under --receipts for --machine, by name and hash; writes nothing.",
	"seal":        "effect: store write. Reads one value at a hidden prompt (or --stdin), seals it into <as>.yaml on a seal/ branch, commits, pushes, opens the pull request and merges it once approved; --no-pr stops after the commit; --dry-run writes nothing.",
	"seat add":    "effect: local write. Writes the new seat's rule into .sops.yaml and its <as>.yaml, re-sealed from --from; commits nothing.",
	"seat inject": "effect: store write. Re-seals the --only values from --from into the existing <as>.yaml on a seal/ branch, commits, pushes, opens the pull request and merges it once approved; --no-pr stops after the commit; --dry-run writes nothing.",
	"version":     "effect: inspection. Prints this build's identity; opens no store, key or program.",
}

// verbHelp is the lines a verb's help carries beyond its usage and flags: its effect,
// and for the verbs that read the store as committed, the store prerequisite.
func verbHelp(verb string) string {
	lines := verbEffects[verb] + "\n"
	if verb == "check" || verb == "exec" {
		lines += storeUpstreamHelp
	}
	return lines
}

// verbs is every verb, in the banner's order, for the refusal of one that is not.
const verbs = "exec, names, check, gate, keygen, place, placed, seal, seat add, seat inject, version, help"

// dryRunHelp is the one sentence --dry-run carries on every verb that takes it.
const dryRunHelp = "prints the plan and writes nothing"

// The flags several verbs share, each saying what it wants (ONBOARDING point 2).
const (
	storeUse = "the store's `dir`: a git working copy holding .sops.yaml and one <seat>.yaml per seat (required)"
	asUse    = "the seat's `name`, selecting <store>/<name>.yaml: letters, digits, - and _ (required)"
	keyUse   = "`path` of this seat's age private key file, mode 0600, as keygen made it (required)"
	sopsUse  = "`path` of the sops program, as printed by: command -v sops (required)"
	maxUse   = "`n` names to list before a MORE line; 0 lists all (default 20)"
	fromUse  = "the `seat` whose values are re-sealed: one this machine's --key opens (required)"
	onlyUse  = "the key `NAMES` to carry, comma separated: GH_TOKEN,API_KEY (required)"
	seatKey  = "`path` of this machine's age private key, the one that opens --from (required)"
	gitUse   = "`path` of the git program (default git)"
	ghUse    = "`path` of the gh program (default gh)"
	noPRUse  = "commit on a seal/ branch and stop: no push, no gh call; the store returns to its starting branch"
	receipts = "`dir` the placed receipts live in (default ~/.config/nova-secrets/placed)"
)

// parseVerb is verbflag.Parse with a refusal a cold reader acts on in one turn: an
// unknown flag is named with every flag the verb does take, and a value its flag cannot
// take with what that flag wants (the value itself is not repeated back).
func parseVerb(fs *flag.FlagSet, args []string) error {
	err := verbflag.Parse(fs, args)
	if err == nil {
		return nil
	}
	msg := err.Error()
	if name, ok := strings.CutPrefix(msg, "flag provided but not defined: "); ok {
		var names []string
		fs.VisitAll(func(f *flag.Flag) { names = append(names, "--"+f.Name) })
		return fmt.Errorf("unknown flag --%s; %s takes %s", strings.TrimLeft(name, "-"), fs.Name(), strings.Join(names, ", "))
	}
	// The flag package's "invalid value %q for flag -%s: %v": the flag's name follows
	// the last " for flag -".
	if i := strings.LastIndex(msg, " for flag -"); strings.HasPrefix(msg, "invalid ") && i >= 0 {
		name, _, _ := strings.Cut(msg[i+len(" for flag -"):], ":")
		if f := fs.Lookup(name); f != nil {
			kind, usage := flag.UnquoteUsage(f)
			return fmt.Errorf("--%s wants <%s>: %s; the value given is not one", f.Name, kind, usage)
		}
	}
	return err
}

// parseFlags parses a verb that takes flags only, refusing in one line at exit 2 a flag
// it does not take, a bad value or a stray argument; `<verb> help` is its help.
func parseFlags(fs *flag.FlagSet, args []string) {
	if len(args) > 0 && args[0] == "help" {
		panic(verbflag.Help{FS: fs})
	}
	err := parseVerb(fs, args)
	if err == nil && len(fs.Args()) > 0 {
		err = fmt.Errorf("unexpected argument %s; %s takes flags only", oneline.Quote(fs.Args()[0]), fs.Name())
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "SECRETS REFUSED: %s\n", oneline.WithRemedy(oneline.Err(err), "nova-secrets "+fs.Name()+" -h"))
		os.Exit(2)
	}
}

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
// column is the friend row's working count, written by the friend row loop
// from the friend's leased tasks; no beat writes the
// row (the retired nova-wake beat refused it), so the remedy is taking work
// through the queue, never a beat and never a hand-write (nova-tools#3807). The hand-write reached the
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
	return fmt.Errorf("redis-cli writing friend:%s:width by hand through nova-secrets exec is refused; that count is the friend row's (friend:%s working), written only by the friend row loop from the friend's leased tasks, never a redis-cli line (nova-tools #3447)", name, name)
}

func main() {
	// `<verb> -h` and `help <verb>` print that verb's help on stdout at exit 0, before
	// any store, key or helper program is opened (the CLI style's rule (b), #4505). Every
	// other path through secretsMain exits on its own; one that returns exits 0, as
	// main did before.
	code := 0
	func() {
		defer verbflag.RecoverWith(os.Stdout, "nova-secrets", usage, &code, verbHelp)
		secretsMain(os.Args)
	}()
	os.Exit(code)
}

func secretsMain(osArgs []string) {
	if len(osArgs) < 2 {
		fmt.Fprintf(os.Stderr, "SECRETS REFUSED: no arguments is not an invocation; run: nova-secrets help\n")
		os.Exit(2)
	}

	verb := osArgs[1]
	if verb == "version" || verb == "--version" {
		os.Exit(cmdVersion(osArgs[2:], os.Stdout, os.Stderr))
	}

	// Check refusal table first
	if msg, refused := disallowedVerbs[verb]; refused {
		fmt.Fprintf(os.Stderr, "SECRETS REFUSED: %s\n", oneline.WithRemedy(msg, "nova-secrets help"))
		os.Exit(2)
	}

	switch verb {
	case "help", "--help", "-h":
		if verb == "help" && len(osArgs) > 2 && osArgs[2] != "help" && !verbflag.IsHelp(osArgs[2]) {
			secretsMain(append(append([]string{osArgs[0]}, osArgs[2:]...), "--help"))
			return
		}
		fmt.Print(usage)
		os.Exit(0)

	case "exec":
		runExecCLI(osArgs[2:])

	case "names":
		runNamesCLI(osArgs[2:])

	case "check":
		runCheckCLI(osArgs[2:])

	case "gate":
		runGateCLI(osArgs[2:])

	case "keygen":
		runKeygenCLI(osArgs[2:])

	case "place":
		runPlaceCLI(osArgs[2:])

	case "placed":
		runPlacedCLI(osArgs[2:])

	case "seal":
		runSealCLI(osArgs[2:])

	case "seat":
		runSeatCLI(osArgs[2:])

	default:
		fmt.Fprintf(os.Stderr, "SECRETS REFUSED: unknown verb %s; the verbs are %s; run: nova-secrets help\n", oneline.Quote(verb), verbs)
		os.Exit(2)
	}
}

// cmdVersion answers from the running binary alone. It deliberately opens no store, key,
// or helper program, so an inventory can ask this before any credentials exist on a bench.
func cmdVersion(args []string, stdout, stderr io.Writer) int {
	verbflag.HelpIfAsked(args, "version")
	if len(args) != 0 {
		fmt.Fprintf(stderr, "SECRETS REFUSED: version takes no flags and no arguments, got %d; run: nova-secrets version\n", len(args))
		return 2
	}
	fmt.Fprintln(stdout, buildinfo.Line("nova-secrets", version))
	return 0
}

func runExecCLI(args []string) {
	// The flags stand before '--' and the command after it. With no '--' every argument
	// is a flag, and the missing command is named with the missing flags, in one line.
	flagArgs, cmdArgs := args, []string(nil)
	if i := slices.Index(args, "--"); i >= 0 {
		flagArgs, cmdArgs = args[:i], args[i+1:]
	}

	fs := flag.NewFlagSet("exec", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	storeFlag := fs.String("store", "", storeUse)
	asFlag := fs.String("as", "", asUse)
	keyFlag := fs.String("key", "", keyUse)
	sopsFlag := fs.String("sops", "", sopsUse)
	onlyFlag := fs.String("only", "", "the key `names` to put in the command's environment, comma separated (GH_TOKEN,API_KEY), or all (required)")
	var requireFlags stringSlice
	fs.Var(&requireFlags, "require", "a key `NAME` that must be in the seat's file and in --only, or exec refuses; repeatable")

	// -h among the flags, before the --, is the verb's help; after it, the command's.
	if len(flagArgs) > 0 && flagArgs[0] == "help" {
		panic(verbflag.Help{FS: fs})
	}
	if err := parseVerb(fs, flagArgs); err != nil {
		fmt.Fprintf(os.Stderr, "SECRETS EXEC FAIL flags: %s\n", oneline.WithRemedy(oneline.Err(err), "nova-secrets exec -h"))
		os.Exit(125)
	}

	// nova-tools#2676: the sprint table's working column is the friend's own
	// tool's to write, never a redis-cli line through this exec; the
	// hand-write of it went through because the store held REDISCLI_AUTH.
	if len(cmdArgs) > 0 {
		if err := refusedWidthHandWrite(cmdArgs); err != nil {
			fmt.Fprintf(os.Stderr, "SECRETS EXEC FAIL %s\n", oneline.Err(err))
			os.Exit(125)
		}
	}

	if len(fs.Args()) > 0 {
		fmt.Fprintf(os.Stderr, "SECRETS EXEC FAIL flags: unexpected argument %s before '--'; the command goes after '--': nova-secrets exec <flags> -- <cmd> [args...]; run: nova-secrets exec -h\n", oneline.Quote(fs.Args()[0]))
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
		// ignored: os.Unsetenv fails only on a name the platform cannot hold, and these names are fixed constants
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

	storeFlag := fs.String("store", "", storeUse)
	asFlag := fs.String("as", "", asUse)
	maxFlag := fs.Int("max", 20, maxUse)
	jsonFlag := fs.Bool("json", false, "print the result as one JSON object on stdout, a refusal included: result, facts, items, more")
	parseFlags(fs, args)

	r, err := secrets.RunNames(*storeFlag, *asFlag, *maxFlag)
	if *jsonFlag {
		os.Exit(namesJSON(os.Stdout, r, err))
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "SECRETS REFUSED: %s\n", oneline.WithRemedy(oneline.Err(err), "nova-secrets names -h"))
		os.Exit(2)
	}
	okLine, names, more := r.Lines()

	for _, n := range names {
		fmt.Println(n)
	}
	if more != "" {
		fmt.Println(more)
	}
	fmt.Println(okLine)
	os.Exit(0)
}

// namesJSON renders names' one value as JSON (STANDARD §2: one value, two renderings)
// and returns the exit code: the key names and counts, never a value.
func namesJSON(w io.Writer, r secrets.NamesReport, err error) int {
	o := tool.Done()
	if err != nil {
		o = tool.Refuse(err.Error())
		if !oneline.HasRemedy(err.Error()) {
			o.Remedy = "nova-secrets names -h"
		}
	} else {
		o.Fact("as", r.As).Fact("keys", r.Total).Fact("shown", len(r.Rows)).Fact("sealed", r.Sealed).Fact("clear", r.Clear)
		for _, n := range r.Rows {
			o.Item("key", "key", n.Name, "clear", n.Clear)
		}
		if len(r.Rows) < r.Total {
			o.More = append(o.More, tool.More{Kind: "key", Shown: len(r.Rows), Total: r.Total, Remedy: tool.MaxRemedy})
		}
	}
	o.Verb = "names"
	o.Render(w, true)
	return o.Exit
}

func runCheckCLI(args []string) {
	fs := flag.NewFlagSet("check", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	storeFlag := fs.String("store", "", storeUse)
	asFlag := fs.String("as", "", asUse)
	keyFlag := fs.String("key", "", keyUse)
	sopsFlag := fs.String("sops", "", sopsUse)
	maxFlag := fs.Int("max", 20, "`n` failures of each kind to list before a MORE line; 0 lists all (default 20)")
	parseFlags(fs, args)

	okLine, failLines, moreLines, summaryLine, code, err := secrets.RunCheck(*storeFlag, *asFlag, *keyFlag, *sopsFlag, *maxFlag)
	if err != nil {
		fmt.Fprintf(os.Stderr, "SECRETS REFUSED: %s\n", oneline.WithRemedy(oneline.Err(err), "nova-secrets check -h"))
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

	storeFlag := fs.String("store", "", "`dir` of the store checkout the pull request is against (required)")
	baseFlag := fs.String("base", "", "the pull request's base commit, a git `ref` (required)")
	headFlag := fs.String("head", "", "the pull request's head commit, a git `ref` (required)")
	machinesFlag := fs.String("machines", "", "the fleet machines registry `file`, whose seat column vouches for a new recipient; without it that rule does not run and APPROVE says machines=-")

	// Every gate flag takes one value. Package flag keeps the LAST of a repeated flag, so
	// `--head <ref> --head <other>` judged a diff the caller did not name first. A flag
	// named twice is refused before any ref is read.
	repeated := ""
	if err := parseOnce(fs, args, &repeated); err != nil {
		if repeated != "" {
			fmt.Fprintf(os.Stderr, "SECRETS REFUSED: --%s is given more than once; every gate flag takes one value; run: nova-secrets gate -h\n", oneline.Field(repeated))
			os.Exit(2)
		}
		fmt.Fprintf(os.Stderr, "SECRETS REFUSED: %s\n", oneline.WithRemedy(oneline.Err(err), "nova-secrets gate -h"))
		os.Exit(2)
	}

	if len(fs.Args()) > 0 {
		fmt.Fprintf(os.Stderr, "SECRETS REFUSED: unexpected argument %s; gate takes flags only; run: nova-secrets gate -h\n", oneline.Quote(fs.Args()[0]))
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

// parseOnce is verbflag.Parse with every flag of fs taking one value. Each value is
// wrapped in a onceValue for the parse and unwrapped when it returns, on every path --
// the -h path included, which unwinds as verbflag's Help panic to the dispatcher's
// Recover: the help it prints then reads the flags' own types, not the wrapper's.
func parseOnce(fs *flag.FlagSet, args []string, repeated *string) error {
	orig := map[string]flag.Value{}
	fs.VisitAll(func(f *flag.Flag) {
		orig[f.Name] = f.Value
		f.Value = &onceValue{Value: f.Value, name: f.Name, repeated: repeated}
	})
	defer fs.VisitAll(func(f *flag.Flag) { f.Value = orig[f.Name] })
	return parseVerb(fs, args)
}

// onceValue is a flag's value that refuses a second Set and names the flag in *repeated,
// so the refusal is this tool's own line rather than package flag's.
type onceValue struct {
	flag.Value
	name     string
	set      bool
	repeated *string
}

func (o *onceValue) Set(v string) error {
	if o.set {
		*o.repeated = o.name
		return fmt.Errorf("--%s is given more than once", o.name)
	}
	o.set = true
	return o.Value.Set(v)
}

func runKeygenCLI(args []string) {
	fs := flag.NewFlagSet("keygen", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	asFlag := fs.String("as", "", "the seat `name` the key is for: letters, digits, - and _ (required)")
	keyFlag := fs.String("key", "", "`path` to write the new private key to; its directory exists with mode 0700 and the file does not (required)")
	ageKeygenFlag := fs.String("age-keygen", "", "`path` of the age-keygen program, as printed by: command -v age-keygen (required)")
	storeFlag := fs.String("store", "", "`dir` of the store, whose recovery.pub fills the rule's recovery key; without it the rule carries <recovery key>")
	parseFlags(fs, args)

	// The order is the package's, not this function's: the rule block, then the next
	// step, then the OK line. A reader took a green keygen for a failure when the
	// verdict was at the top and the homework at the bottom (nova-tools#1393).
	lines, err := secrets.RunKeygen(*asFlag, *keyFlag, *ageKeygenFlag, *storeFlag)
	if err != nil {
		fmt.Fprintf(os.Stderr, "SECRETS REFUSED: %s\n", oneline.WithRemedy(oneline.Err(err), "nova-secrets keygen -h"))
		os.Exit(2)
	}

	for _, l := range lines {
		fmt.Println(l)
	}
	os.Exit(0)
}

// runSeatCLI dispatches the seat subverbs: `add` gives a new seat its first values,
// `inject` re-seals named values into a seat that exists. Every other change to a seat
// is a pull request against .sops.yaml that the store's gate reviews.
func runSeatCLI(args []string) {
	if len(args) == 0 {
		fmt.Fprintf(os.Stderr, "SECRETS REFUSED: seat takes a subverb, add (a new seat's first values) or inject (values into a seat that exists); run: nova-secrets seat add -h\n")
		os.Exit(2)
	}
	switch args[0] {
	case "add":
		runSeatAddCLI(args[1:])
	case "inject":
		runSeatInjectCLI(args[1:])
	case "help", "--help", "-h":
		fmt.Print(usage)
		os.Exit(0)
	default:
		fmt.Fprintf(os.Stderr, "SECRETS REFUSED: unknown seat subverb %s; the subverbs are add and inject; run: nova-secrets seat add -h\n", oneline.Quote(args[0]))
		os.Exit(2)
	}
}

func runSeatInjectCLI(args []string) {

	fs := flag.NewFlagSet("seat inject", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	storeFlag := fs.String("store", "", storeUse)
	asFlag := fs.String("as", "", "the existing `seat` receiving the values; its <seat>.yaml is in the store (required)")
	fromFlag := fs.String("from", "", fromUse)
	onlyFlag := fs.String("only", "", onlyUse)
	keyFlag := fs.String("key", "", seatKey)
	sopsFlag := fs.String("sops", "", sopsUse)
	ghFlag := fs.String("gh", "gh", ghUse)
	gitFlag := fs.String("git", "git", gitUse)
	noPRFlag := fs.Bool("no-pr", false, noPRUse)
	dryRunFlag := fs.Bool("dry-run", false, dryRunHelp)
	parseFlags(fs, args)

	line, err := secrets.RunSeatInject(secrets.SeatInjectOptions{
		StoreDir: *storeFlag,
		AsName:   *asFlag,
		From:     *fromFlag,
		Only:     *onlyFlag,
		KeyPath:  *keyFlag,
		SopsPath: *sopsFlag,
		GHPath:   *ghFlag,
		GitPath:  *gitFlag,
		NoPR:     *noPRFlag,
		DryRun:   *dryRunFlag,
		Progress: os.Stderr,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "SECRETS SEAT INJECT FAIL %s\n", oneline.WithRemedy(oneline.Err(err), "nova-secrets seat inject -h"))
		os.Exit(2)
	}
	fmt.Println(line)
	os.Exit(0)
}

func runSeatAddCLI(args []string) {

	fs := flag.NewFlagSet("seat add", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	storeFlag := fs.String("store", "", storeUse)
	asFlag := fs.String("as", "", "the new seat's `name`; the store holds no <name>.yaml yet (required)")
	pubFlag := fs.String("pub", "", "the new seat's age public `key` (age1…), from its own keygen receipt (required)")
	fromFlag := fs.String("from", "", fromUse)
	onlyFlag := fs.String("only", "", onlyUse)
	keyFlag := fs.String("key", "", seatKey)
	sopsFlag := fs.String("sops", "", sopsUse)
	parseFlags(fs, args)

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
		fmt.Fprintf(os.Stderr, "SECRETS SEAT ADD FAIL %s\n", oneline.WithRemedy(oneline.Err(err), "nova-secrets seat add -h"))
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

	storeFlag := fs.String("store", "", storeUse)
	asFlag := fs.String("as", "", asUse)
	keyFlag := fs.String("key", "", keyUse)
	sopsFlag := fs.String("sops", "", sopsUse)
	machineFlag := fs.String("machine", "", "the fleet machine's `name`, a row of --machines (required)")
	secretFlag := fs.String("secret", "", "the key `NAME` in <store>/<as>.yaml whose value is copied (required)")
	pathFlag := fs.String("path", "", "the remote `path` written (default <home>/.config/nova-secrets/<NAME>.env, home from the machine's row)")
	machinesFlag := fs.String("machines", "", "the fleet registry `file`: one machine per line, name, ssh target, home, tab separated (default ~/.config/nova-tools/fleet.tsv)")
	receiptsFlag := fs.String("receipts", "", receipts)
	sshFlag := fs.String("ssh", "ssh", "`path` of the ssh program (default ssh)")
	dryRunFlag := fs.Bool("dry-run", false, dryRunHelp)
	parseFlags(fs, args)

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
		DryRun:     *dryRunFlag,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "SECRETS REFUSED: %s\n", oneline.WithRemedy(oneline.Err(err), "nova-secrets place -h"))
		os.Exit(2)
	}
	fmt.Println(okLine)
	os.Exit(0)
}

func runPlacedCLI(args []string) {
	fs := flag.NewFlagSet("placed", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	machineFlag := fs.String("machine", "", "the fleet machine's `name` whose receipts are listed (required)")
	receiptsFlag := fs.String("receipts", "", receipts)
	parseFlags(fs, args)

	okLine, itemLines, err := secrets.RunPlaced(secrets.PlacedInput{
		Machine:  *machineFlag,
		Receipts: *receiptsFlag,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "SECRETS REFUSED: %s\n", oneline.WithRemedy(oneline.Err(err), "nova-secrets placed -h"))
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

	storeFlag := fs.String("store", "", storeUse)
	asFlag := fs.String("as", "", asUse)
	keyFlag := fs.String("key", "", keyUse)
	sopsFlag := fs.String("sops", "", sopsUse)
	nameFlag := fs.String("name", "", "the key `NAME` to seal: capitals, digits and _, as GH_TOKEN (required)")
	ghFlag := fs.String("gh", "gh", ghUse)
	gitFlag := fs.String("git", "git", gitUse)
	stdinFlag := fs.Bool("stdin", false, "read the value from standard input instead of a hidden terminal prompt; never put it in an argument")
	noPRFlag := fs.Bool("no-pr", false, noPRUse)
	dryRunFlag := fs.Bool("dry-run", false, dryRunHelp)
	parseFlags(fs, args)

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
		DryRun:          *dryRunFlag,
		UseStdin:        *stdinFlag,
		Stdin:           os.Stdin,
		StdinIsTerminal: stdinIsTerminal,
		Progress:        os.Stderr,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "SECRETS SEAL FAIL %s\n", oneline.WithRemedy(oneline.Err(err), "nova-secrets seal -h"))
		os.Exit(2)
	}

	fmt.Println(line)
	os.Exit(0)
}
