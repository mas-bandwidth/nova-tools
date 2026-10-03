package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/verbflag"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/secrets"
	"github.com/mas-bandwidth/nova-tools/internal/tool"
)

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

// dryRunHelp is the one sentence --dry-run carries on every verb that takes it.
const dryRunHelp = "prints the plan and writes nothing"

// The flags several verbs share, each saying what it wants (ONBOARDING point 2).
const (
	storeUse = "the store's `dir`: a git working copy holding .sops.yaml and one <seat>.yaml per seat (required)"
	asUse    = "the seat's `name`, selecting <store>/<name>.yaml: letters, digits, - and _ (required)"
	keyUse   = "`path` of this seat's age private key file, mode 0600, as keygen made it (required)"
	sopsUse  = "`path` of the sops program, as printed by: command -v sops (required)"
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
	// The skeleton (internal/tool) dispatches, answers help and version, and renders.
	// A verb the tool refuses forever is answered here, before the skeleton, because
	// the skeleton has no refusal table (its unknown-verb answer names the verbs).
	if len(os.Args) > 1 {
		if msg, refused := disallowedVerbs[os.Args[1]]; refused {
			fmt.Fprintf(os.Stderr, "SECRETS REFUSED: %s\n", oneline.WithRemedy(msg, "nova-secrets help"))
			os.Exit(2)
		}
	}
	os.Exit(secretsTool().Main())
}

// tail is the words after a verb on this process's command line: what the verb's
// kept printer reads, since the skeleton hands a Run only its parsed flags.
func tail(verb string) []string { return os.Args[1+len(strings.Fields(verb)):] }

// secretsTool is the command: its verbs and the shared skeleton (internal/tool)
// that dispatches, refuses, answers help and version, and renders. Every verb but
// names keeps its own printer (internal/secrets renders the receipt lines, which
// the skeleton's token would change) and answers tool.Exit; names returns the one
// value its text and JSON renderings share.
func secretsTool() *tool.Tool {
	return &tool.Tool{
		Name:  "nova-secrets",
		What:  "encrypted secrets in a git repository, handed to one command at a time",
		Stamp: version,
		How: `the store is a git working copy: .sops.yaml, recovery.pub, and one <seat>.yaml
per seat, opened by that seat's age key (mode 0600); check and exec also need --store
on a named branch with an upstream tracking ref. exec decrypts only the --only names
into one command's environment; names reads names without decrypting; no value is
printed. first run: keygen makes a key and prints its rule; seal writes a seat's first value.`,
		ExitTable: "0 ran and passed, 1 check found the store red (one line per failure), 2 could not run or refused (one line naming the remedy); exec ends with the command's own status, and 125 when exec itself refused and the command never ran.",
		Default:   "exec",
		Verbs: []tool.Verb{
			{
				Name:    "exec",
				Usage:   "exec --store <dir> --as <name> --key <path> --sops <path> --only <NAME,...|all> [--require <NAME>]... -- <cmd> [args...]",
				Example: "exec --store ./secrets --as ada --key ~/.config/nova-secrets/ada.key --sops /opt/homebrew/bin/sops --only GH_TOKEN --require GH_TOKEN -- gh api user",
				Effect:  tool.Delivery,
				Detail:  storeUpstreamHelp,
				Flags: func(f *tool.Flags) {
					f.Prints()
					f.String("store", "", storeUse)
					f.String("as", "", asUse)
					f.String("key", "", keyUse)
					f.String("sops", "", sopsUse)
					f.String("only", "", "the key `names` to put in the command's environment, comma separated (GH_TOKEN,API_KEY), or all (required)")
					f.Var(&stringSlice{}, "require", "a key `NAME` that must be in the seat's file and in --only, or exec refuses; repeatable")
				},
				Run: func(c *tool.Call) *tool.Out { runExecCLI(tail("exec")); return nil },
			},
			{
				Name:    "names",
				Usage:   "names --store <dir> --as <name> [--max <n>] [--json]",
				Example: "names --store ./secrets --as ada",
				Effect:  tool.Inspection,
				Flags: func(f *tool.Flags) {
					f.String("store", "", storeUse)
					f.String("as", "", asUse)
					f.Max()
				},
				Run: runNames,
			},
			{
				Name:    "check",
				Usage:   "check --store <dir> --as <name> --key <path> --sops <path> [--max <n>]",
				Example: "check --store ./secrets --as ada --key ~/.config/nova-secrets/ada.key --sops /opt/homebrew/bin/sops",
				Effect:  tool.Inspection,
				Detail:  storeUpstreamHelp,
				Flags: func(f *tool.Flags) {
					f.Prints()
					f.String("store", "", storeUse)
					f.String("as", "", asUse)
					f.String("key", "", keyUse)
					f.String("sops", "", sopsUse)
					f.Int("max", 20, "`n` failures of each kind to list before a MORE line; 0 lists all (default 20)")
				},
				Run: runCheckCLI,
			},
			{
				Name:   "gate",
				Usage:  "gate --store <dir> --base <git ref> --head <git ref> [--machines <registry>]",
				Effect: tool.Inspection,
				Flags: func(f *tool.Flags) {
					f.Prints()
					f.String("store", "", "`dir` of the store checkout the pull request is against (required)")
					f.String("base", "", "the pull request's base commit, a git `ref` (required)")
					f.String("head", "", "the pull request's head commit, a git `ref` (required)")
					f.String("machines", "", "the fleet machines registry `file`, whose seat column vouches for a new recipient; without it that rule does not run and APPROVE says machines=-")
				},
				Run: func(c *tool.Call) *tool.Out { runGateCLI(tail("gate")); return nil },
			},
			{
				Name:    "keygen",
				Usage:   "keygen --as <name> --key <path> --age-keygen <path> [--store <dir>]",
				Example: "keygen --as ada --key ~/.config/nova-secrets/ada.key --age-keygen /opt/homebrew/bin/age-keygen",
				Effect:  tool.LocalWrite,
				Flags: func(f *tool.Flags) {
					f.Prints()
					f.String("as", "", "the seat `name` the key is for: letters, digits, - and _ (required)")
					f.String("key", "", "`path` to write the new private key to; its directory exists with mode 0700 and the file does not (required)")
					f.String("age-keygen", "", "`path` of the age-keygen program, as printed by: command -v age-keygen (required)")
					f.String("store", "", "`dir` of the store, whose recovery.pub fills the rule's recovery key; without it the rule carries <recovery key>")
				},
				Run: runKeygenCLI,
			},
			{
				Name:    "place",
				Usage:   "place --store <dir> --as <name> --key <path> --sops <path> --machine <name> --secret <name> [--path <remote path>] [--machines <file>] [--receipts <dir>] [--ssh <path>] [--dry-run]",
				Example: "place --store ./secrets --as ada --key ~/.config/nova-secrets/ada.key --sops /opt/homebrew/bin/sops --machine bench-a --secret DEEPSEEK_API_KEY --machines ./fleet.tsv\nplace --store ./secrets --as worker --key ~/.config/nova-secrets/worker.key --sops /opt/homebrew/bin/sops --machine bench --secret API_KEY --machines ./fleet.tsv --dry-run",
				Effect:  tool.Delivery,
				DryRun:  true,
				Flags: func(f *tool.Flags) {
					f.Prints()
					f.String("store", "", storeUse)
					f.String("as", "", asUse)
					f.String("key", "", keyUse)
					f.String("sops", "", sopsUse)
					f.String("machine", "", "the fleet machine's `name`, a row of --machines (required)")
					f.String("secret", "", "the key `NAME` in <store>/<as>.yaml whose value is copied (required)")
					f.String("path", "", "the remote `path` written (default <home>/.config/nova-secrets/<NAME>.env, home from the machine's row)")
					f.String("machines", "", "the fleet registry `file`: one machine per line, name, ssh target, home, tab separated (default ~/.config/nova-tools/fleet.tsv)")
					f.String("receipts", "", receipts)
					f.String("ssh", "ssh", "`path` of the ssh program (default ssh)")
				},
				Run: runPlaceCLI,
			},
			{
				Name:    "placed",
				Usage:   "placed --machine <name> [--receipts <dir>]",
				Example: "placed --machine bench-a",
				Effect:  tool.Inspection,
				Flags: func(f *tool.Flags) {
					f.Prints()
					f.String("machine", "", "the fleet machine's `name` whose receipts are listed (required)")
					f.String("receipts", "", receipts)
				},
				Run: runPlacedCLI,
			},
			{
				Name:    "seal",
				Usage:   "seal --store <dir> --as <seat> --key <path> --sops <path> --name NAME [--stdin] [--no-pr] [--dry-run] [--gh <path>] [--git <path>]",
				Example: "seal --store ./secrets --as worker --key ~/.config/nova-secrets/worker.key --sops /opt/homebrew/bin/sops --name API_KEY --dry-run",
				Effect:  tool.LocalWrite,
				DryRun:  true,
				Flags: func(f *tool.Flags) {
					f.Prints()
					f.String("store", "", storeUse)
					f.String("as", "", asUse)
					f.String("key", "", keyUse)
					f.String("sops", "", sopsUse)
					f.String("name", "", "the key `NAME` to seal: capitals, digits and _, as GH_TOKEN (required)")
					f.String("gh", "gh", ghUse)
					f.String("git", "git", gitUse)
					f.Bool("stdin", false, "read the value from standard input instead of a hidden terminal prompt; never put it in an argument")
					f.Bool("no-pr", false, noPRUse)
				},
				Run: runSealCLI,
			},
			{
				Name:    "seat add",
				Usage:   "seat add --store <dir> --as <seat> --pub <age1…> --from <source seat> --only <NAME,...> --key <path> --sops <path>",
				Example: "seat add --store ./secrets --as bo --pub $BO_PUB --from ada --only GH_TOKEN,DEEPSEEK_API_KEY --key ~/.config/nova-secrets/ada.key --sops /opt/homebrew/bin/sops",
				Effect:  tool.LocalWrite,
				Flags: func(f *tool.Flags) {
					f.Prints()
					f.String("store", "", storeUse)
					f.String("as", "", "the new seat's `name`; the store holds no <name>.yaml yet (required)")
					f.String("pub", "", "the new seat's age public `key` (age1…), from its own keygen receipt (required)")
					f.String("from", "", fromUse)
					f.String("only", "", onlyUse)
					f.String("key", "", seatKey)
					f.String("sops", "", sopsUse)
				},
				Run: runSeatAddCLI,
			},
			{
				Name:    "seat inject",
				Usage:   "seat inject --store <dir> --as <seat> --from <source seat> --only <NAME,...> --key <path> --sops <path> [--no-pr] [--dry-run] [--gh <path>] [--git <path>]",
				Example: "seat inject --store ./secrets --as bo --from ada --only NOVA_REDIS_BENCH_PASSWORD --key ~/.config/nova-secrets/ada.key --sops /opt/homebrew/bin/sops --no-pr\nseat inject --store ./secrets --as worker --from lead --only API_KEY --key ~/.config/nova-secrets/lead.key --sops /opt/homebrew/bin/sops --dry-run",
				Effect:  tool.LocalWrite,
				DryRun:  true,
				Flags: func(f *tool.Flags) {
					f.Prints()
					f.String("store", "", storeUse)
					f.String("as", "", "the existing `seat` receiving the values; its <seat>.yaml is in the store (required)")
					f.String("from", "", fromUse)
					f.String("only", "", onlyUse)
					f.String("key", "", seatKey)
					f.String("sops", "", sopsUse)
					f.String("gh", "gh", ghUse)
					f.String("git", "git", gitUse)
					f.Bool("no-pr", false, noPRUse)
				},
				Run: runSeatInjectCLI,
			},
		},
	}
}

// runNames renders names' one value: its kept line rendering as a bare payload, or
// its --json as the JSON of the same facts, items and MORE cut (STANDARD §2: one
// value, two renderings). It never prints a value.
func runNames(c *tool.Call) *tool.Out {
	r, err := secrets.RunNames(c.Str("store"), c.Str("as"), c.Int("max"))
	if c.Bool("json") {
		return namesOut(r, err)
	}
	if err != nil {
		o := tool.Refuse(err.Error())
		if !oneline.HasRemedy(err.Error()) {
			o.Remedy = "nova-secrets names -h"
		}
		return o
	}
	okLine, nameLines, moreLine := r.Lines()
	lines := append(append([]string{}, nameLines...), moreLine)
	lines = append(lines, okLine)
	return tool.Payload(strings.Join(lines, "\n"))
}

// namesOut renders names' one value as the JSON of its facts, items and MORE cut,
// a refusal included, and returns the exit code: the key names and counts, never
// a value.
func namesOut(r secrets.NamesReport, err error) *tool.Out {
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
	return o
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

func runCheckCLI(c *tool.Call) *tool.Out {
	okLine, failLines, moreLines, summaryLine, code, err := secrets.RunCheck(c.Str("store"), c.Str("as"), c.Str("key"), c.Str("sops"), c.Int("max"))
	if err != nil {
		fmt.Fprintf(c.Stderr, "SECRETS REFUSED: %s\n", oneline.WithRemedy(oneline.Err(err), "nova-secrets check -h"))
		return tool.Exit(code)
	}

	if code == 0 {
		fmt.Fprintln(c.Stdout, okLine)
		return tool.Exit(0)
	}

	// Failure output
	for _, l := range failLines {
		fmt.Fprintln(c.Stderr, l)
	}
	for _, m := range moreLines {
		fmt.Fprintln(c.Stderr, m)
	}
	fmt.Fprintln(c.Stderr, summaryLine)
	return tool.Exit(code)
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

func runKeygenCLI(c *tool.Call) *tool.Out {
	// The order is the package's, not this function's: the rule block, then the next
	// step, then the OK line. A reader took a green keygen for a failure when the
	// verdict was at the top and the homework at the bottom (nova-tools#1393).
	lines, err := secrets.RunKeygen(c.Str("as"), c.Str("key"), c.Str("age-keygen"), c.Str("store"))
	if err != nil {
		fmt.Fprintf(c.Stderr, "SECRETS REFUSED: %s\n", oneline.WithRemedy(oneline.Err(err), "nova-secrets keygen -h"))
		return tool.Exit(2)
	}

	for _, l := range lines {
		fmt.Fprintln(c.Stdout, l)
	}
	return tool.Exit(0)
}

func runSeatInjectCLI(c *tool.Call) *tool.Out {
	line, err := secrets.RunSeatInject(secrets.SeatInjectOptions{
		StoreDir: c.Str("store"),
		AsName:   c.Str("as"),
		From:     c.Str("from"),
		Only:     c.Str("only"),
		KeyPath:  c.Str("key"),
		SopsPath: c.Str("sops"),
		GHPath:   c.Str("gh"),
		GitPath:  c.Str("git"),
		NoPR:     c.Bool("no-pr"),
		DryRun:   c.Bool("dry-run"),
		Progress: c.Stderr,
	})
	if err != nil {
		fmt.Fprintf(c.Stderr, "SECRETS SEAT INJECT FAIL %s\n", oneline.WithRemedy(oneline.Err(err), "nova-secrets seat inject -h"))
		return tool.Exit(2)
	}
	fmt.Fprintln(c.Stdout, line)
	return tool.Exit(0)
}

func runSeatAddCLI(c *tool.Call) *tool.Out {
	lines, err := secrets.RunSeatAdd(secrets.SeatAddOptions{
		StoreDir: c.Str("store"),
		AsName:   c.Str("as"),
		Pub:      c.Str("pub"),
		From:     c.Str("from"),
		Only:     c.Str("only"),
		KeyPath:  c.Str("key"),
		SopsPath: c.Str("sops"),
		Progress: c.Stderr,
	})
	if err != nil {
		fmt.Fprintf(c.Stderr, "SECRETS SEAT ADD FAIL %s\n", oneline.WithRemedy(oneline.Err(err), "nova-secrets seat add -h"))
		return tool.Exit(2)
	}
	for _, l := range lines {
		fmt.Fprintln(c.Stdout, l)
	}
	return tool.Exit(0)
}

func runPlaceCLI(c *tool.Call) *tool.Out {
	okLine, err := secrets.RunPlace(secrets.PlaceInput{
		StoreDir:   c.Str("store"),
		AsName:     c.Str("as"),
		KeyPath:    c.Str("key"),
		SopsPath:   c.Str("sops"),
		Machine:    c.Str("machine"),
		Secret:     c.Str("secret"),
		RemotePath: c.Str("path"),
		Machines:   c.Str("machines"),
		Receipts:   c.Str("receipts"),
		SSH:        c.Str("ssh"),
		DryRun:     c.Bool("dry-run"),
	})
	if err != nil {
		fmt.Fprintf(c.Stderr, "SECRETS REFUSED: %s\n", oneline.WithRemedy(oneline.Err(err), "nova-secrets place -h"))
		return tool.Exit(2)
	}
	fmt.Fprintln(c.Stdout, okLine)
	return tool.Exit(0)
}

func runPlacedCLI(c *tool.Call) *tool.Out {
	okLine, itemLines, err := secrets.RunPlaced(secrets.PlacedInput{
		Machine:  c.Str("machine"),
		Receipts: c.Str("receipts"),
	})
	if err != nil {
		fmt.Fprintf(c.Stderr, "SECRETS REFUSED: %s\n", oneline.WithRemedy(oneline.Err(err), "nova-secrets placed -h"))
		return tool.Exit(2)
	}
	fmt.Fprintln(c.Stdout, okLine)
	for _, l := range itemLines {
		fmt.Fprintln(c.Stdout, l)
	}
	return tool.Exit(0)
}

func runSealCLI(c *tool.Call) *tool.Out {
	fi, statErr := os.Stdin.Stat()
	stdinIsTerminal := statErr == nil && fi.Mode()&os.ModeCharDevice != 0

	line, err := secrets.RunSeal(secrets.SealOptions{
		StoreDir:        c.Str("store"),
		AsName:          c.Str("as"),
		KeyPath:         c.Str("key"),
		SopsPath:        c.Str("sops"),
		Name:            c.Str("name"),
		GHPath:          c.Str("gh"),
		GitPath:         c.Str("git"),
		NoPR:            c.Bool("no-pr"),
		DryRun:          c.Bool("dry-run"),
		UseStdin:        c.Bool("stdin"),
		Stdin:           c.Stdin,
		StdinIsTerminal: stdinIsTerminal,
		Progress:        c.Stderr,
	})
	if err != nil {
		fmt.Fprintf(c.Stderr, "SECRETS SEAL FAIL %s\n", oneline.WithRemedy(oneline.Err(err), "nova-secrets seal -h"))
		return tool.Exit(2)
	}

	fmt.Fprintln(c.Stdout, line)
	return tool.Exit(0)
}
