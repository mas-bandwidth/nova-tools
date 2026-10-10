// Command nova-secrets keeps a team's secrets sops-encrypted in a git repository (the
// store) and hands the values one command needs to that command alone, in its
// environment; no verb prints a value. The verbs are thin: each parses its flags here and
// calls pkg/secrets, which holds every check and every refusal. run is the whole
// tool on its arguments and three streams, so a test drives it in process; main only
// hands it the process's own.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"

	"github.com/mas-bandwidth/nova-tools/pkg/buildinfo"
	"github.com/mas-bandwidth/nova-tools/pkg/nsprint/verbflag"
	"github.com/mas-bandwidth/nova-tools/pkg/oneline"
	"github.com/mas-bandwidth/nova-tools/pkg/secrets"
)

const usage = `nova-secrets: encrypted secrets in a git repository, handed to one command at a time

how it works: the store is a git working copy holding a .sops.yaml (one rule per
seat naming its recipients), a recovery.pub (the recovery key every file is also
sealed to) and one sops-encrypted <seat>.yaml per seat; a seat is a named identity
whose age key file (mode 0600) opens its file. exec decrypts only the --only names
into one command's environment; names reads names without decrypting; no value is printed.
first run: setup: makes a store from an empty directory (it needs age-keygen
and sops on PATH); first value: seals one with --stdin, the path a harness takes.

usage:
  nova-secrets version  print this build identity (--version also accepted)
  nova-secrets exec   --store <dir> --as <name> --key <path> --sops <path>
                      --only <NAME,...|all> [--require <NAME>]... -- <cmd> [args...]
  nova-secrets names  --store <dir> --as <name> [--max <n>] [--json]
  nova-secrets check  --store <dir> --as <name> --key <path> --sops <path> [--max <n>]
  nova-secrets gate   --store <dir> --base <git ref> --head <git ref> [--machines <registry>]
  nova-secrets keygen --as <name> --key <path> --age-keygen <path> [--store <dir>]
  nova-secrets place  --store <dir> --as <name> --key <path> --sops <path>
                      --machine <name> --secret <name> [--path <remote path>]
                      [--machines <file>] [--receipts <dir>] [--ssh <path>] [--dry-run]
  nova-secrets placed --machine <name> [--receipts <dir>]
  nova-secrets seal   --store <dir> --as <seat> --key <path> --sops <path>
                      --name NAME [--stdin] [--no-pr] [--dry-run] [--gh <path>] [--git <path>]
  nova-secrets seat add --store <dir> --as <seat> --pub <age1…>
                      --from <source seat> --only <NAME,...> --key <path> --sops <path>
  nova-secrets seat inject --store <dir> --as <seat> --from <source seat>
                      --only <NAME,...> --key <path> --sops <path> [--no-pr]
                      [--dry-run] [--gh <path>] [--git <path>]
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
  --from <seat>        a seat this machine can open, whose values are re-sealed (seat add,
                       seat inject)
  --stdin              read the value from stdin instead of the terminal, the path a harness
                       takes (seal only)
  --no-pr              stop after the commit; make no gh call; return the store to its
                       starting branch (seal, seat inject)
  --dry-run            prints the plan and writes nothing (place, seal, seat inject): the file, the
                       recipients, the machine and remote path, the branch and the pull request the
                       real run would take, as PLAN lines ending in DRY-RUN OK, exit 0; no ssh, no
                       git write or push, no gh call, no sops encrypt, no value shown. It does
                       decrypt the seat file the real run reads first (place: --as's, to find
                       --secret; seal: the existing <as>.yaml, to say add or replace; seat inject:
                       --from's, to find the names), so --key must open it; seal reads no new value.
                       seal and seat inject read the store at HEAD, so their store must be
                       committed; a store with no commit yet is refused with the commit
                       that starts it
  --gh <path>          path to the gh executable (seal, seat inject; default: gh)
  --git <path>         path to the git executable (seal, seat inject; default: git)

exit codes: 0 ran and passed, 1 a verb ran and said no (check's failures, or a
gate verdict, one line per failure), 2 could not run or refused (one line naming
the remedy); exec ends with the command's own status, and 125 when exec itself
refused and the command never ran.

setup: needs age-keygen and sops on PATH; run these from an empty directory first
  mkdir -m 700 -p ~/.config/nova-secrets
  nova-secrets keygen --as recovery --key ~/.config/nova-secrets/recovery.key \
    --age-keygen "$(command -v age-keygen)"
  mkdir -p ./secrets && git -C ./secrets init -q -b main
  sed -n 's/^# public key: //p' ~/.config/nova-secrets/recovery.key > ./secrets/recovery.pub
  nova-secrets keygen --as ada --key ~/.config/nova-secrets/ada.key \
    --age-keygen "$(command -v age-keygen)" \
    --store ./secrets | sed -n 's/^SECRETS RULE   //p' > ./secrets/.sops.yaml
  git -C ./secrets add -A && git -C ./secrets commit -qm 'a new store'
  git init -q --bare ./secrets.git
  git -C ./secrets remote add origin ../secrets.git
  git -C ./secrets push -qu origin main

first value:
  printf '%s\n' 'a-token-value' | nova-secrets seal --store ./secrets --as ada \
    --key ~/.config/nova-secrets/ada.key --sops /opt/homebrew/bin/sops \
    --name GH_TOKEN --stdin --no-pr

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
	"place":       "effect: delivery. Copies one value over ssh to --machine (on ssh's stdin, never in an argument) and writes a receipt under --receipts naming the sealed bytes it decrypted (file, blob) and the store's HEAD (head), never a hash of the value; it reads the seat file once and decrypts a private copy; --dry-run still decrypts that file to find --secret, prints no value, writes nothing and runs no ssh.",
	"placed":      "effect: inspection. Lists the receipts under --receipts for --machine, never a hash of a value: per secret its remote path, file (the seat file it was sealed in), blob (the git blob id of the sealed bytes place decrypted), head (the store's HEAD commit when place started, which need not hold that blob) and stamp; writes nothing.",
	"seal":        "effect: store write. Reads one value at a hidden prompt (or --stdin), seals it into <as>.yaml on a seal/ branch, commits, pushes, opens the pull request and merges it once approved; --no-pr stops after the commit; --dry-run reads no new value, decrypts the existing <as>.yaml (when there is one) to say whether NAME is added or replaced, prints no value and writes nothing.",
	"seat add":    "effect: local write. Writes the new seat's rule into .sops.yaml and its <as>.yaml, re-sealed from --from; commits nothing.",
	"seat inject": "effect: store write. Re-seals the --only values from --from into the existing <as>.yaml on a seal/ branch, commits, pushes, opens the pull request and merges it once approved; --no-pr stops after the commit; --dry-run decrypts the --from seat's file to find the names, prints no value and writes nothing.",
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
const dryRunHelp = "prints the plan and writes nothing; seal and seat inject read the store at HEAD, so their store must be committed"

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

// parseVerb is verbflag.Parse with the house wording of its errors (verbflag.Explain,
// the one wording every tool on the skeleton prints): an unknown flag is named with the
// flags the verb takes and the nearest of them, a value its flag cannot take with what
// that flag wants, and the value itself is never repeated back.
func parseVerb(fs *flag.FlagSet, args []string) error {
	if err := verbflag.Parse(fs, args); err != nil {
		return fmt.Errorf("%s", verbflag.Explain(fs, err))
	}
	return nil
}

// parseFlags parses a verb that takes flags only: a flag it does not take, a bad value
// or a stray argument is the error; `<verb> help` is its help.
func parseFlags(fs *flag.FlagSet, args []string) error {
	if len(args) > 0 && args[0] == "help" {
		panic(verbflag.Help{FS: fs})
	}
	err := parseVerb(fs, args)
	if err == nil && len(fs.Args()) > 0 {
		err = fmt.Errorf("unexpected argument %s; %s takes flags only", oneline.Quote(fs.Args()[0]), fs.Name())
	}
	return err
}

// streams are what a verb reads and writes: the process's own from main, a test's
// buffers when it drives run in process.
type streams struct {
	stdin          io.Reader
	stdout, stderr io.Writer
}

// refuse prints a verb's refusal in the tool's one grammar, `SECRETS <VERB> REFUSED:
// <why>; run: <remedy>` (the verb's help when the reason names no remedy of its own), on
// stderr, and returns code: 2, or 125 for exec, whose refusals stand apart from the
// statuses of the command it runs.
func (s streams) refuse(verb string, code int, err error) int {
	fmt.Fprintf(s.stderr, "SECRETS %s REFUSED: %s\n", strings.ToUpper(verb), oneline.WithRemedy(oneline.Err(err), "nova-secrets "+verb+" -h"))
	return code
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

type stringSlice []string

func (s *stringSlice) String() string {
	return strings.Join(*s, ",")
}

func (s *stringSlice) Set(val string) error {
	*s = append(*s, val)
	return nil
}

// init gives the seat-file mark the build's version (SPEC-SECRETS "gate"); an ordinary build keeps "dev".
func init() {
	if version != "" {
		secrets.MarkVersion = version
	}
}

func main() { os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr)) }

// run is nova-secrets on args, the words after its name, and returns its exit code.
// `<verb> -h` and `help <verb>` print that verb's help on stdout at exit 0, before any
// store, key or helper program is opened (the CLI style's rule (b)): the parse raises
// verbflag's Help, and the deferred RecoverWith prints it.
func run(args []string, stdin io.Reader, stdout, stderr io.Writer) (code int) {
	defer verbflag.RecoverWith(stdout, "nova-secrets", usage, &code, verbHelp)
	return dispatch(args, streams{stdin: stdin, stdout: stdout, stderr: stderr})
}

func dispatch(args []string, s streams) int {
	if len(args) == 0 {
		fmt.Fprintf(s.stderr, "SECRETS REFUSED: no arguments is not an invocation; run: nova-secrets help\n")
		return 2
	}

	verb := args[0]
	if verb == "version" || verb == "--version" {
		return cmdVersion(args[1:], s.stdout, s.stderr)
	}

	// Check refusal table first
	if msg, refused := disallowedVerbs[verb]; refused {
		fmt.Fprintf(s.stderr, "SECRETS REFUSED: %s\n", oneline.WithRemedy(msg, "nova-secrets help"))
		return 2
	}

	switch verb {
	case "help", "--help", "-h":
		if verb == "help" && len(args) > 1 && args[1] != "help" && !verbflag.IsHelp(args[1]) {
			return dispatch(append(slices.Clone(args[1:]), "--help"), s)
		}
		fmt.Fprintf(s.stdout, "%s", usage)
		return 0
	case "exec":
		return runExecCLI(args[1:], s)
	case "names":
		return runNamesCLI(args[1:], s)
	case "check":
		return runCheckCLI(args[1:], s)
	case "gate":
		return runGateCLI(args[1:], s)
	case "keygen":
		return runKeygenCLI(args[1:], s)
	case "place":
		return runPlaceCLI(args[1:], s)
	case "placed":
		return runPlacedCLI(args[1:], s)
	case "seal":
		return runSealCLI(args[1:], s)
	case "seat":
		return runSeatCLI(args[1:], s)
	default:
		fmt.Fprintf(s.stderr, "SECRETS REFUSED: unknown verb %s; the verbs are %s; run: nova-secrets help\n", oneline.Quote(verb), verbs)
		return 2
	}
}

// cmdVersion answers from the running binary alone. It deliberately opens no store, key,
// or helper program, so an inventory can ask this before any credentials exist on a bench.
func cmdVersion(args []string, stdout, stderr io.Writer) int {
	verbflag.HelpIfAsked(args, "version")
	if len(args) != 0 {
		fmt.Fprintf(stderr, "SECRETS VERSION REFUSED: version takes no flags and no arguments, got %d; run: nova-secrets version\n", len(args))
		return 2
	}
	fmt.Fprintln(stdout, buildinfo.Line("nova-secrets", version))
	return 0
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

func runPlacedCLI(args []string, s streams) int {
	fs := flag.NewFlagSet("placed", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	machineFlag := fs.String("machine", "", "the fleet machine's `name` whose receipts are listed (required)")
	receiptsFlag := fs.String("receipts", "", receipts)
	if err := parseFlags(fs, args); err != nil {
		return s.refuse("placed", 2, err)
	}

	okLine, itemLines, err := secrets.RunPlaced(secrets.PlacedInput{
		Machine:  *machineFlag,
		Receipts: *receiptsFlag,
	})
	if err != nil {
		return s.refuse("placed", 2, err)
	}
	fmt.Fprintln(s.stdout, okLine)
	for _, l := range itemLines {
		fmt.Fprintln(s.stdout, l)
	}
	return 0
}
