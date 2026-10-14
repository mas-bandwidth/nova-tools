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
	"github.com/mas-bandwidth/nova-tools/pkg/tool"
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
                      --only <NAME,...|all> [--require <NAME>]... [--json] -- <cmd> [args...]
  nova-secrets names  --store <dir> --as <name> [--max <n>] [--json]
  nova-secrets check  --store <dir> --as <name> --key <path> --sops <path> [--max <n>] [--json]
  nova-secrets gate   --store <dir> --base <git ref> --head <git ref> [--machines <registry>]
                      [--json]
  nova-secrets keygen --as <name> --key <path> --age-keygen <path> [--store <dir>] [--json]
  nova-secrets place  --store <dir> --as <name> --key <path> --sops <path>
                      --machine <name> --secret <name> [--path <remote path>]
                      [--machines <file>] [--receipts <dir>] [--ssh <path>] [--dry-run] [--json]
  nova-secrets placed --machine <name> [--receipts <dir>] [--json]
  nova-secrets seal   --store <dir> --as <seat> --key <path> --sops <path>
                      --name NAME [--stdin] [--no-pr] [--dry-run] [--gh <path>] [--git <path>]
                      [--json]
  nova-secrets seat add --store <dir> --as <seat> --pub <age1…>
                      --from <source seat> --only <NAME,...> --key <path> --sops <path> [--json]
  nova-secrets seat inject --store <dir> --as <seat> --from <source seat>
                      --only <NAME,...> --key <path> --sops <path> [--no-pr]
                      [--dry-run] [--gh <path>] [--git <path>] [--json]
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
  --json               one JSON object on stdout for every verb but version; a refusal included
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
  nova-secrets keygen --as bo --key ~/.config/nova-secrets/bo.key --age-keygen /opt/homebrew/bin/age-keygen && BO_PUB=$(sed -n 's/^# public key: //p' ~/.config/nova-secrets/bo.key) # public half of that key file
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
		return runExecMaybeJSON(args[1:], s) // SPEC-SECRETS exec, --json
	case "names":
		return runNamesCLI(args[1:], s) // SPEC-SECRETS names: names renders its own --json
	case "check":
		return runWithJSON("check", args[1:], s, checkValueFlags, runCheckCLI)
	case "gate":
		return runWithJSON("gate", args[1:], s, gateValueFlags, runGateCLI)
	case "keygen":
		return runWithJSON("keygen", args[1:], s, keygenValueFlags, runKeygenCLI)
	case "place":
		return runWithJSON("place", args[1:], s, placeValueFlags, runPlaceCLI)
	case "placed":
		return runWithJSON("placed", args[1:], s, placedValueFlags, runPlacedCLI)
	case "seal":
		return runWithJSON("seal", args[1:], s, sealValueFlags, runSealCLI)
	case "seat":
		return runSeatMaybeJSON(args[1:], s)
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

// Value-taking flags. A bool flag (dry-run, stdin, no-pr, json) does not consume the
// next word, so `--store --json` is the store's value and not --json (docs/SPEC-SECRETS.md
// `--json`). --sops and --age-keygen stay the absolute paths the flag was given.
var (
	execValueFlags       = map[string]bool{"store": true, "as": true, "key": true, "sops": true, "only": true, "require": true}
	checkValueFlags      = map[string]bool{"store": true, "as": true, "key": true, "sops": true, "max": true}
	gateValueFlags       = map[string]bool{"store": true, "base": true, "head": true, "machines": true}
	keygenValueFlags     = map[string]bool{"as": true, "key": true, "age-keygen": true, "store": true}
	placeValueFlags      = map[string]bool{"store": true, "as": true, "key": true, "sops": true, "machine": true, "secret": true, "path": true, "machines": true, "receipts": true, "ssh": true}
	placedValueFlags     = map[string]bool{"machine": true, "receipts": true}
	sealValueFlags       = map[string]bool{"store": true, "as": true, "key": true, "sops": true, "name": true, "gh": true, "git": true}
	seatAddValueFlags    = map[string]bool{"store": true, "as": true, "pub": true, "from": true, "only": true, "key": true, "sops": true}
	seatInjectValueFlags = map[string]bool{"store": true, "as": true, "from": true, "only": true, "key": true, "sops": true, "gh": true, "git": true}
)

// jsonAsked reports whether --json was named as its own flag (docs/SPEC-SECRETS.md `--json`).
// A value-taking flag's argument is skipped, and words after `--` belong to the command.
func jsonAsked(args []string, takesValue map[string]bool) bool {
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" || !strings.HasPrefix(a, "-") {
			return false
		}
		name, val, hasVal := splitFlag(a)
		if name == "json" {
			if !hasVal {
				return true
			}
			return jsonOn(val)
		}
		if takesValue[name] && !hasVal {
			i++
		}
	}
	return false
}

// stripJSONFlag removes a --json flag and keeps every other word (docs/SPEC-SECRETS.md
// `--json`). The verb's own parser then sees the call it already knew.
func stripJSONFlag(args []string, takesValue map[string]bool) []string {
	var out []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" || !strings.HasPrefix(a, "-") {
			out = append(out, args[i:]...)
			return out
		}
		name, _, hasVal := splitFlag(a)
		if name == "json" {
			continue
		}
		out = append(out, a)
		if takesValue[name] && !hasVal && i+1 < len(args) {
			i++
			out = append(out, args[i])
		}
	}
	return out
}

// splitFlag splits `--name` or `--name=value`. A short `-json` is the same name.
func splitFlag(a string) (name, val string, hasVal bool) {
	a = strings.TrimLeft(a, "-")
	name, val, hasVal = strings.Cut(a, "=")
	return name, val, hasVal
}

// runWithJSON runs a verb, and when --json was asked renders its lines as one JSON
// object on stdout (docs/SPEC-SECRETS.md `--json`). A help panic is left for run's
// recover: this function does not catch it.
func runWithJSON(verb string, args []string, s streams, takesValue map[string]bool, runCLI func([]string, streams) int) int {
	if !jsonAsked(args, takesValue) {
		return runCLI(args, s)
	}
	code, stdout, stderr, err := captureLines(stripJSONFlag(args, takesValue), s, runCLI)
	if err != nil {
		return refuseJSON(verb, 2, "could not capture "+verb+": "+err.Error(), s.stdout)
	}
	return renderLinesJSON(verb, code, stdout, stderr, s.stdout)
}

// runSeatMaybeJSON dispatches seat's subverbs onto --json (docs/SPEC-SECRETS.md `--json`).
func runSeatMaybeJSON(args []string, s streams) int {
	if len(args) > 0 && args[0] == "add" {
		return runWithJSON("seat add", args[1:], s, seatAddValueFlags, runSeatAddCLI)
	}
	if len(args) > 0 && args[0] == "inject" {
		return runWithJSON("seat inject", args[1:], s, seatInjectValueFlags, runSeatInjectCLI)
	}
	if jsonAsked(args, nil) {
		return runWithJSON("seat", args, s, nil, runSeatCLI)
	}
	return runSeatCLI(args, s)
}

// runExecMaybeJSON is exec under --json (docs/SPEC-SECRETS.md `exec` and `--json`).
// A refusal returns before execve and is rendered in process. A call that might
// replace this process is refused: describing the child's exit needs a child, and
// this file does not start one (the spawn door is outside this package's import fence).
func runExecMaybeJSON(args []string, s streams) int {
	if !jsonAsked(args, execValueFlags) {
		return runExecCLI(args, s)
	}
	stripped := stripJSONFlag(args, execValueFlags)
	if execMightLaunch(stripped) {
		return refuseJSON("exec", 125, "exec --json of a launch replaces this process; the child's exit is not described from inside it", s.stdout)
	}
	return runWithJSON("exec", args, s, execValueFlags, runExecCLI)
}

// execMightLaunch reports whether RunExec might reach execve (docs/SPEC-SECRETS.md
// `exec`). It is false only when the call is known to return first: help, a missing
// flag, an unknown flag, no command, or the width hand-write. It does not look up
// --sops or --age-keygen, and it does not look up the command.
func execMightLaunch(args []string) bool {
	flagArgs, cmdArgs := args, []string(nil)
	if i := slices.Index(args, "--"); i >= 0 {
		flagArgs, cmdArgs = args[:i], args[i+1:]
	}
	if len(cmdArgs) == 0 || cmdArgs[0] == "" {
		return false
	}
	got := map[string]string{}
	for i := 0; i < len(flagArgs); i++ {
		a := flagArgs[i]
		if a == "help" || a == "-h" || a == "--help" || !strings.HasPrefix(a, "-") {
			return false
		}
		name, val, hasVal := splitFlag(a)
		if name == "json" {
			continue
		}
		if !execValueFlags[name] {
			return false
		}
		if !hasVal {
			if i+1 >= len(flagArgs) {
				return false
			}
			i++
			val = flagArgs[i]
		}
		got[name] = val
	}
	for _, name := range []string{"store", "as", "key", "sops", "only"} {
		if got[name] == "" {
			return false
		}
	}
	if err := refusedWidthHandWrite(cmdArgs); err != nil {
		return false
	}
	return true
}

// captureLines runs a verb with its stdout and stderr on pipes and returns both
// texts (docs/SPEC-SECRETS.md `--json`). The reads run beside the verb so a full
// pipe cannot stall it. No byte is written from this function.
func captureLines(args []string, s streams, runCLI func([]string, streams) int) (code int, stdout, stderr string, err error) {
	outR, outW, err := os.Pipe()
	if err != nil {
		return 0, "", "", err
	}
	errR, errW, err := os.Pipe()
	if err != nil {
		rerr := outR.Close()
		werr := outW.Close()
		if rerr != nil {
			return 0, "", "", rerr
		}
		if werr != nil {
			return 0, "", "", werr
		}
		return 0, "", "", err
	}
	outC := make(chan string, 1)
	errC := make(chan string, 1)
	go drain(outR, outC)
	go drain(errR, errC)
	var werr, eerr error
	closed := false
	closeWrites := func() {
		if closed {
			return
		}
		closed = true
		werr = outW.Close()
		eerr = errW.Close()
	}
	defer closeWrites()
	code = runCLI(args, streams{stdin: s.stdin, stdout: outW, stderr: errW})
	closeWrites()
	stdout = <-outC
	stderr = <-errC
	if werr != nil {
		return code, stdout, stderr, werr
	}
	if eerr != nil {
		return code, stdout, stderr, eerr
	}
	return code, stdout, stderr, nil
}

// drain reads one pipe to the end (docs/SPEC-SECRETS.md `--json`). A read or
// close error still sends the bytes already taken: those are the verb's lines.
func drain(r *os.File, c chan<- string) {
	b, err := io.ReadAll(r)
	cerr := r.Close()
	// ignored: a short read of the verb's own pipe; the bytes already taken are the lines
	if err != nil {
		c <- string(b)
		return
	}
	// ignored: a close after that read; the bytes already taken are the lines
	if cerr != nil {
		c <- string(b)
		return
	}
	c <- string(b)
}

// jsonOn is --json's boolean form without a second parser (docs/SPEC-SECRETS.md `--json`).
// Bare --json is handled by the caller. The accepted spellings are ParseBool's true set.
func jsonOn(v string) bool {
	switch v {
	case "1", "t", "T", "true", "TRUE", "True":
		return true
	default:
		return false
	}
}

// refuseJSON is a --json refusal that never reached the verb (docs/SPEC-SECRETS.md `--json`).
func refuseJSON(verb string, code int, why string, w io.Writer) int {
	o := tool.Refuse(why)
	o.Verb = verb
	o.Exit = code
	o.Remedy = "nova-secrets " + verb + " -h"
	o.Render(w, true)
	return o.Exit
}

// renderLinesJSON turns the verb's tool lines into one JSON object (docs/SPEC-SECRETS.md
// `--json`). Lines that are not tool lines are dropped, so a secret on stderr
// is not a field.
func renderLinesJSON(verb string, code int, stdout, stderr string, w io.Writer) int {
	var o *tool.Out
	switch {
	case code == 0:
		o = tool.Done()
	case code == 1:
		o = tool.Fail()
	default:
		o = tool.Refuse()
		o.Exit = code
	}
	o.Verb = verb
	for _, line := range strings.Split(stdout+"\n"+stderr, "\n") {
		absorbToolLine(o, line)
	}
	if o.Status == tool.Refused && o.Remedy == "" {
		joined := strings.Join(o.Why, " ")
		if !oneline.HasRemedy(joined) {
			o.Remedy = "nova-secrets " + verb + " -h"
		}
	}
	o.Render(w, true)
	return o.Exit
}

// absorbToolLine copies one tool line's facts into o (docs/SPEC-SECRETS.md `--json`).
// A line that is not a SECRETS or GATE line is ignored.
func absorbToolLine(o *tool.Out, line string) {
	line = strings.TrimSpace(line)
	if line == "" || (!strings.Contains(line, "SECRETS ") && !strings.HasPrefix(line, "GATE ")) {
		return
	}
	if i := strings.Index(line, " REFUSED:"); i >= 0 {
		reason := strings.TrimSpace(line[i+len(" REFUSED:"):])
		if j := strings.Index(reason, "; run: "); j >= 0 {
			o.Remedy = strings.TrimSpace(reason[j+len("; run: "):])
			reason = strings.TrimSpace(reason[:j])
		}
		o.Status = tool.Refused
		if o.Exit == 0 {
			o.Exit = 2
		}
		if reason != "" {
			o.Why = append(o.Why, reason)
		}
		return
	}
	if strings.Contains(line, " MORE ") {
		addMore(o, line)
		return
	}
	body, kind, prose := splitToolBody(line)
	if prose != "" && (kind == "FAIL" || kind == "FAILED") {
		o.ItemText("fail", prose, fieldsAsKV(body)...)
		return
	}
	fields := parseKV(body)
	if prose == "" && len(fields) == 0 && body != "" && kind != "" && kind != "OK" && kind != "APPROVE" && kind != "DRY-RUN OK" {
		// A rule block, a note or a next step has no key=value. The line's text is the fact.
		o.ItemText(strings.ToLower(kind), body)
		return
	}
	for _, f := range fields {
		o.Fact(f.k, factValue(f.v))
	}
	if prose != "" {
		o.ItemText(strings.ToLower(kind), prose)
	}
}

// splitToolBody drops the SECRETS or GATE token and the verb, and returns the status
// word, the field text before a prose tail, and that tail.
func splitToolBody(line string) (body, kind, prose string) {
	rest := line
	switch {
	case strings.HasPrefix(rest, "SECRETS "):
		rest = strings.TrimPrefix(rest, "SECRETS ")
	case strings.HasPrefix(rest, "GATE "):
		rest = strings.TrimPrefix(rest, "GATE ")
	}
	// The verb may be several words (SEAT ADD, SEAT INJECT, DRY-RUN). The status word
	// is the first of these, longest first so DRY-RUN OK is not read as OK alone.
	for _, word := range []string{"DRY-RUN OK", "DRY-RUN", "APPROVE", "REFUSED", "FAILED", "FAIL", "OK", "PLAN", "NOTE", "NEXT", "RULE", "NAMES", "NAME", "ITEM"} {
		i := strings.Index(rest, word)
		if i < 0 {
			continue
		}
		before := strings.TrimSpace(rest[:i])
		if strings.Contains(before, "=") {
			continue
		}
		kind = word
		rest = strings.TrimSpace(rest[i+len(word):])
		break
	}
	if j := strings.Index(rest, ": "); j >= 0 {
		return strings.TrimSpace(rest[:j]), kind, strings.TrimSpace(rest[j+2:])
	}
	return rest, kind, ""
}

func addMore(o *tool.Out, line string) {
	remedy := ""
	if j := strings.Index(line, " run: "); j >= 0 {
		remedy = strings.TrimSpace(line[j+len(" run: "):])
		line = line[:j]
	}
	kind := ""
	shown, total := 0, 0
	for _, f := range parseKV(line) {
		switch f.k {
		case "kind":
			kind = f.v
		case "shown":
			shown = atoiField(f.v)
		case "total":
			total = atoiField(f.v)
		}
	}
	if kind == "" {
		return
	}
	o.More = append(o.More, tool.More{Kind: kind, Shown: shown, Total: total, Remedy: remedy})
}

type kv struct{ k, v string }

func parseKV(line string) []kv {
	var out []kv
	for _, tok := range strings.Fields(line) {
		k, v, ok := strings.Cut(tok, "=")
		if !ok || k == "" {
			continue
		}
		out = append(out, kv{k, v})
	}
	return out
}

func fieldsAsKV(body string) []any {
	var kvOut []any
	for _, f := range parseKV(body) {
		kvOut = append(kvOut, f.k, factValue(f.v))
	}
	return kvOut
}

// factValue keeps a token a number only when the decimal spelling round-trips, so
// mode=0600 stays the text the line printed (docs/SPEC-SECRETS.md `--json`).
func factValue(v string) any {
	switch v {
	case "true":
		return true
	case "false":
		return false
	}
	n, ok := decimalInt(v)
	if !ok {
		return v
	}
	return n
}

func atoiField(v string) int {
	n, ok := decimalInt(v)
	if !ok {
		return 0
	}
	return n
}

// decimalInt accepts a decimal token whose spelling round-trips (docs/SPEC-SECRETS.md
// `--json`). A leading zero, an empty sign, or a value that does not fit in an int
// stays text at the caller.
func decimalInt(v string) (int, bool) {
	if v == "" {
		return 0, false
	}
	neg := false
	i := 0
	if v[0] == '-' {
		neg = true
		i = 1
	}
	if i == len(v) || (len(v)-i > 1 && v[i] == '0') {
		return 0, false
	}
	const lim = int(^uint(0) >> 1)
	n := 0
	for ; i < len(v); i++ {
		d := v[i]
		if d < '0' || d > '9' {
			return 0, false
		}
		digit := int(d - '0')
		if n > (lim-digit)/10 {
			return 0, false
		}
		n = n*10 + digit
	}
	if neg {
		n = -n
	}
	if spellInt(n) != v {
		return 0, false
	}
	return n, true
}

func spellInt(n int) string {
	if n == 0 {
		return "0"
	}
	neg := false
	if n < 0 {
		neg = true
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
