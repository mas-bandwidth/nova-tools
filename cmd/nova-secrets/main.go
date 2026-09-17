package main

import (
	"flag"
	"fmt"
	"io"
	"os"
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
  nova-secrets keygen --as <name> --key <path> --age-keygen <path> [--store <dir>]
  nova-secrets place  --store <dir> --as <name> --key <path> --sops <path> --machine <name> --secret <name> [--path <remote path>] [--machines <file>] [--receipts <dir>] [--ssh <path>]
  nova-secrets placed --machine <name> [--receipts <dir>]
  nova-secrets help

flags:
  --store <dir>        git working copy of the secrets store
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
  --machines <file>    fleet registry file: name, ssh target, home, tab separated
  --receipts <dir>     where placed receipts live; default ~/.config/nova-secrets/placed
  --ssh <path>         ssh executable to use (default ssh)

example:
  nova-secrets keygen --as rowan --key ~/.config/nova-secrets/rowan.key --age-keygen /opt/homebrew/bin/age-keygen
  nova-secrets names  --store ./secrets --as rowan
  nova-secrets check  --store ./secrets --as rowan --key ~/.config/nova-secrets/rowan.key --sops /opt/homebrew/bin/sops
  nova-secrets exec   --store ./secrets --as rowan --key ~/.config/nova-secrets/rowan.key --sops /opt/homebrew/bin/sops --only GH_TOKEN --require GH_TOKEN -- gh api user
  nova-secrets place  --store ./secrets --as rowan --key ~/.config/nova-secrets/rowan.key --sops /opt/homebrew/bin/sops --machine mini --secret DEEPSEEK_API_KEY --machines ./fleet.tsv
  nova-secrets placed --machine mini
`

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

	case "keygen":
		runKeygenCLI(os.Args[2:])

	case "place":
		runPlaceCLI(os.Args[2:])

	case "placed":
		runPlacedCLI(os.Args[2:])

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

	okLine, ruleLines, noteLine, err := secrets.RunKeygen(*asFlag, *keyFlag, *ageKeygenFlag, *storeFlag)
	if err != nil {
		fmt.Fprintf(os.Stderr, "SECRETS REFUSED: %s\n", oneline.Err(err))
		os.Exit(2)
	}

	fmt.Println(okLine)
	for _, l := range ruleLines {
		fmt.Println(l)
	}
	if noteLine != "" {
		fmt.Println(noteLine)
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
