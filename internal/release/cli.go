package release

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/buildinfo"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// Verbs is the usage block `nova-release help` prints, and the same six lines
// docs/SPEC-UPDATE.md carries. Every path is a flag and no flag has a
// default path: a path guessed from the cwd or from `$HOME` makes a release cut
// from a laptop and a release cut from a bench mean different things, so the
// same command is the same release on either host. The one exception is
// --receipts, and internal/release/dogfoodgate.go says at length why the gate
// in front of the definition of done is worth it.
const Verbs = `nova-release cut --repo <owner/name> --from <branch> --version <v> --changelog <path> [--sums <file>] [--security-read <id|url>] [--local-diff <checkout> [--paths-from <file>] | --paths-from <file>] [--cli <file>] [--receipts <dir>] [--no-dogfood-gate --reason <why>] [--dry-run] [--timeout <d>]
nova-release build --version <v> --out <dir> --source <dir> [--platform <goos-goarch>,...] [--incremental] [--cli <file>] [--receipts <dir>] [--no-dogfood-gate --reason <why> | --gate report --reason <why>] [--dry-run] [--timeout <d>]
nova-release install --from <dir> --version <v> --bin <dir> [--retire <dir>] [--platform <goos-goarch>] [--dry-run] [--timeout <d>]
nova-release adopt [--version <v>] --machines <file> --ssh <path> --from <dir|host:dir> --bin <dir> --dest <dir> [--stage <dir> --repo <owner/name> | --stage <dir> --expect-sums <sha256> | --stage <dir> --expect-sums-from <file>] [--retire <dir>] [--platform <goos-goarch>] (--certify <machines.tsv> --certs <file> --standard <file> | --no-certify) [--dry-run] [--timeout <d>]
nova-release pull --version <v> --out <dir> --changelog <path> [--machines <file> --ssh <path> --dest <dir>] [--reason <text>] [--platform <goos-goarch>] [--dry-run] [--timeout <d>]
nova-release cycle --version <v> --source <dir> --out <dir> --inventory <file> --benches <a,b,...> --reason <why> --ansible <path> [--receipts <dir>] [--dry-run] [--timeout <d>]`

// ReleaseOpening opens the banner with its three answers: what the tool does
// (line 1, the README's sentence), how it works, and the first run
// (ONBOARDING.md point 6). The verbs that build and install ask for real
// paths and a real checkout, so a first run of this binary reads rather than
// builds, and the opening says so: even a --dry-run names its paths.
const ReleaseOpening = `nova-release: cut, build, install, adopt and pull a repository's releases

how it works: one green commit becomes a version, stamped binaries and the
same binaries answering for themselves on every machine that runs them:
cut tags it and runs the gates, build compiles every cmd/* binary for each
platform and writes one SHA256SUMS per platform, install puts one platform's
set in place by rename, adopt fans out over ssh and pull withdraws.
first run: the binary alone; the lines under example: answer with help, a
verb's own help and this binary's version line; they build nothing.`

// BuildEffect and InstallEffect are the effect line of `build -h` and
// `install -h` (STANDARD §3: a verb states what it writes; the tool-answers
// class rule reads this line).
const (
	BuildEffect   = "effect: local write: compiles every cmd/nova-* of --source into <out>/<version>/<goos-goarch>/ and writes its SHA256SUMS there; --dry-run prints the plan and writes nothing"
	InstallEffect = "effect: local write: replaces the release's binaries in --bin, optionally clears --retire, and prunes older releases under --from; --dry-run verifies the release and writes nothing"
)

// CutNote is the gate in front of a tag, said where a person will meet it.
// It is a var rather than a const because it names the list, and the list has
// ONE home: composing this from SensitivePaths is why the help cannot fall
// behind the gate.
var CutNote = "cut classifies the range since the previous tag against the sensitive path list in internal/release/sensitive.go and docs/SPEC-RELEASE.md " +
	"(" + SensitiveShape + "). A range that touches one of them REFUSES until --security-read names the security reader's read -- a note id or the url of the comment -- " +
	"and the cut then prints `RELEASE CUT SENSITIVE paths=<n> read=<id>` above its receipt. " +
	"A range TOO BIG FOR THE FORGE TO LIST is a different refusal and --security-read does not get past it: a read of a list that may be short is a read of a prefix of the truth. " +
	"Classify such a range from a complete local list instead -- `--local-diff <checkout>` runs `git diff --name-only <previous tag>...<head>` in that checkout, and `--paths-from <file>` writes the answer there for a later cut to read back. " +
	"The tag is annotated, and the annotation carries `sums=<sha256 of SHA256SUMS>` when --sums names the built checksum file, which is the digest `adopt --repo` reads back. " +
	"`build` writes one SHA256SUMS per platform, under <out>/<version>/<goos-goarch>/, and --sums takes one of them: the tag and the CHANGELOG section carry THAT platform's digest, and `adopt --repo` verifies that platform only. " +
	"Every other platform the release built is adopted with --expect-sums-from <out>/<version>/<goos-goarch>/" + DigestFile + " on the host that built it, or --expect-sums <sha256> from the sums= field of its `RELEASE BUILT` line."

// AdoptNote is what a person needs before their first adopt, and every sentence
// of it is something the first dogfood pass had to find out by failing.
const AdoptNote = "adopt runs FROM the host that has ssh to every machine and fans out from there; it never needs the machines to reach each other. " +
	"When the release was built elsewhere, --from may name that machine as host:dir and --stage <dir> says where to fetch it first. " +
	"Such a fetch is verified against a digest that did NOT travel with the bits: --repo <owner/name> reads it off the annotated tag the cut wrote, --expect-sums <sha256> names it outright, or --expect-sums-from <file> reads it out of the " + DigestFile + " this host's own `release build` wrote. " +
	"A dev build has no tag, which is why the third exists; the file must be a LOCAL one, because a digest computed on the machine holding the bits is that machine vouching for itself. " +
	"Install the release on this host before adopting it: the nova-release running the fan-out is the one here, and a coordinator older than the release it is adopting refuses and says so. " +
	"--machines is " + MachinesShape + ". " + RemotePathsNote + ". " +
	"--retire <dir> removes this release's own nova-* files from a second directory nobody should still be running from (~/go/bin); it refuses to be --bin or the live stamp. " +
	"--bin, --dest and --retire must be absolute or ~/-rooted and free of shell metacharacters; they are validated before any remote command is composed."

// Deps are the seams. A zero Deps is the production one: the forge is gh, the
// remote is ssh, the compiler is go, the clock is the machine's. A test fills in
// what it needs and nothing it fills in can reach the network.
type Deps struct {
	Forge     Forge
	SSH       SSH
	Toolchain Toolchain
	// Git is the local checkout `cut --local-diff` reads the complete path
	// list out of when the forge's compare is at its ceiling.
	Git Git
	// Dogfood is the definition-of-done gate `cut` and `build` run first. A
	// nil Dogfood is ReadDogfood, which reads the command reference and the
	// receipts off disk and reaches nothing else.
	Dogfood Dogfood
	Now     func() time.Time
	// Self answers what the nova-release RUNNING THIS is stamped with. It is a
	// seam rather than a constant because this package is a library and the
	// stamp lives in main; a nil Self means `adopt` cannot compare its own
	// version with the release's and does not pretend to.
	Self func() string
	// VersionOf answers what the binary at path reports for itself. It is a
	// seam because `install`'s skip decision is the one place this package
	// runs a binary it is about to replace.
	VersionOf func(ctx context.Context, path string) (string, error)
	// Source is the checkout `build` records and `--incremental` diffs
	// (incremental.go); nil is ExecSource.
	Source Source
	// Ansible runs the tools play for `cycle` (cycle.go); nil is
	// ExecAnsible with --ansible.
	Ansible Ansible
}

// options are every flag the five verbs take, in one struct, because they share
// --version, --from and --timeout and a reader should see that once.
type options struct {
	repo, from, version, changelog, out, source, bin, machines, ssh, dest, platform string
	stage, retire, expectSums, expectSumsFrom, sums, securityRead, reason           string
	pathsFrom, localDiff                                                            string
	// cli and receipts are the dogfood gate's two inputs, and noDogfood is
	// the way past it. --reason is shared with `pull`, which already had one:
	// both are somebody saying, in the record, why a release did something
	// out of the ordinary.
	cli, receipts string
	noDogfood     bool
	// gate is build's --gate: "refuse" (the default, and the only way cut
	// runs it) or "report", which prints the open edges and builds.
	gate        string
	incremental bool
	// cycle's own: the inventory, the benches, the ansible-playbook binary.
	inventory, benches, ansible string
	platforms                   platformList
	dryRun                      bool
	timeout                     time.Duration
	// The three that turn on certification after an adopt. They are named together or
	// not at all: a certificates file with no registry names no machine's roles, and a
	// registry with no standard has no hash to write.
	certify, certs, standard string
	noCertify                bool
}

// namedHalf is the flags that WERE given, as a refusal spells them: the complement of the
// missing list, so a person who passed two of three is told which two to drop.
func namedHalf(missing []string) []string {
	gone := map[string]bool{}
	for _, m := range missing {
		gone[m] = true
	}
	var out []string
	for _, f := range []string{"--certify", "--certs", "--standard"} {
		if !gone[f] {
			out = append(out, f)
		}
	}
	return out
}

// platformList is a repeatable, comma-separated --platform. The fleet is three
// platforms wide, and four invocations differing only in --platform are four
// chances for one of them to carry a different --version -- which is a release
// whose linux half and darwin half are not the same release.
type platformList []string

func (p *platformList) String() string { return strings.Join(*p, ",") }
func (p *platformList) Set(v string) error {
	for _, one := range strings.Split(v, ",") {
		if one = strings.TrimSpace(one); one != "" {
			*p = append(*p, one)
		}
	}
	return nil
}

// VerbUsage is the one usage line for one release verb, so that `--help` on a
// verb answers about THAT verb. A person who asked about `adopt` did not ask to
// re-read `cut`.
func VerbUsage(verb string) string {
	for _, line := range strings.Split(Verbs, "\n") {
		if strings.HasPrefix(line, "nova-release "+verb+" ") {
			return line
		}
	}
	return Verbs
}

// refusal is the one refusal line (STANDARD §2): what was wrong and what the
// input wants, then the command a reader runs next, the help of the verb that
// refused (token is that verb, upper-case) or of the tool as a whole.
func refusal(w io.Writer, token string, err error) int {
	run := "nova-release " + strings.ToLower(token) + " -h"
	if token == "RELEASE" {
		run = "nova-release help"
	}
	fmt.Fprintf(w, "%s REFUSED: %s; run: %s\n", token, oneline.Err(err), run)
	return 2
}

// verbNames are the verbs, as a refusal lists them.
const verbNames = "cut, build, install, adopt, pull, cycle, version"

// ExitCodes is the release verbs' exit-code line, which each verb's -h prints.
const ExitCodes = "exit codes: 0 the verb did what its line says (a --dry-run printed its plan and changed nothing); " +
	"1 it ran and a step failed partway, the FAIL or REFUSED line naming what was done and what to do next; " +
	"2 it refused before acting, naming the command to run."

// progress is the stderr voice. A program says what it is doing for any step
// over about a tenth of a second, and every step in this package -- a forge
// read, a compile, a copy over ssh -- is well over that.
func progress(w io.Writer, format string, a ...any) {
	fmt.Fprintf(w, "release: "+format+"\n", a...)
}

// Main is the production entry: the verb with every seam at its default, and
// the one thing a seam cannot default to -- what THIS binary is stamped with,
// which lives in main and is handed down. `adopt` is the reader: a coordinator
// older than the release it is fanning out cannot run that release's install,
// and a coordinator far enough behind cannot adopt at all.
func Main(name string, args []string, stamp string, out, errs io.Writer) int {
	return Run(name, args, out, errs, Deps{Self: func() string { return stamp }})
}

// versionVerb prints the one version line every binary answers (the version
// class rule, TestEveryToolPrintsTheOneVersionLine): `<name> <identity>
// <goos>/<goarch> <go version>`, the identity from the stamp main hands down
// through Deps.Self.
func versionVerb(name string, deps Deps, args []string, out, errs io.Writer) int {
	// version takes no flag but -h, so the arguments are read here rather
	// than through a flag set: the one argument that is not a refusal is a
	// request for help, and asking is not an error.
	if len(args) > 0 {
		switch arg := args[0]; {
		case len(args) == 1 && (arg == "-h" || arg == "-help" || arg == "--help"):
			fmt.Fprintln(out, name+" version")
			fmt.Fprintln(out, "flags:")
			fmt.Fprintln(out, "  -h  this help")
			fmt.Fprintln(out, "effect: inspection: prints this binary's version line, writes nothing")
			fmt.Fprintln(out, ExitCodes)
			return 0
		case strings.HasPrefix(arg, "-"):
			return refusal(errs, "VERSION", fmt.Errorf("unknown flag --%s; version takes no flags but -h", strings.TrimLeft(arg, "-")))
		default:
			return refusal(errs, "VERSION", fmt.Errorf("version takes no positional arguments, got %q", arg))
		}
	}
	stamp := ""
	if deps.Self != nil {
		stamp = deps.Self()
	}
	fmt.Fprintln(out, buildinfo.Line(name, stamp))
	return 0
}

// Run is nova-release's verb dispatch. Each verb validates its own flags, so
// a missing flag is named by the verb that wanted it rather than by a shared
// check that knows about all of them.
func Run(name string, args []string, out, errs io.Writer, deps Deps) int {
	if deps.Now == nil {
		deps.Now = time.Now
	}
	if len(args) == 0 {
		return refusal(errs, "RELEASE", fmt.Errorf("a verb is required; the verbs are %s", verbNames))
	}
	verb := args[0]
	args = args[1:]
	switch verb {
	case "cut", "build", "install", "adopt", "pull", "cycle":
	case "version", "--version":
		return versionVerb(name, deps, args, out, errs)
	case "help", "--help", "-h":
		// The banner answers the three questions a stranger brings
		// (ONBOARDING.md point 6) before the usage lines, and the notes the
		// verbs carry follow the usage they belong to.
		fmt.Fprintf(out, "%s\n\n", ReleaseOpening)
		fmt.Fprintf(out, "usage:\n%s\nnova-release help\nnova-release version (or --version)\n", Verbs)
		fmt.Fprintln(out, ExitCodes+" `nova-release <verb> -h` lists a verb's flags.")
		fmt.Fprintln(out, CutNote)
		fmt.Fprintln(out, DogfoodNote)
		fmt.Fprintln(out, IncrementalNote)
		fmt.Fprintln(out, AdoptNote)
		fmt.Fprintln(out, PullNote)
		fmt.Fprintln(out, CycleNote)
		fmt.Fprint(out, "\nexample:\n  nova-release help\n  nova-release cut -h\n  nova-release version\n")
		return 0
	default:
		return refusal(errs, "RELEASE", fmt.Errorf("unknown verb %q; the verbs are %s", verb, verbNames))
	}
	o := options{timeout: 10 * time.Minute}
	f := flag.NewFlagSet("release "+verb, flag.ContinueOnError)
	f.SetOutput(io.Discard)
	f.DurationVar(&o.timeout, "timeout", o.timeout, "the whole run's deadline, such as 10m")
	f.StringVar(&o.version, "version", "", "the release version, such as 1.2.0")
	// Every verb's own flags, declared per verb rather than all at once, so
	// that `release cut --bin x` is an unknown flag rather than a silent
	// no-op -- a flag the verb ignores is a flag somebody thinks worked.
	var required []string
	switch verb {
	case "cut":
		f.StringVar(&o.repo, "repo", "", "the forge repository to tag, owner/name")
		f.StringVar(&o.from, "from", "", "the branch whose head is cut")
		f.StringVar(&o.changelog, "changelog", "", "the CHANGELOG.md the release's section is written to")
		f.BoolVar(&o.dryRun, "dry-run", false, "decide and print, write nothing")
		f.StringVar(&o.sums, "sums", "", "one platform's built SHA256SUMS, <out>/<version>/<goos-goarch>/SHA256SUMS; the section and the tag record its digest, and adopt --repo verifies that platform")
		f.StringVar(&o.securityRead, "security-read", "", "the note id or comment url of the security reader's read, required when the range touches a sensitive path")
		f.StringVar(&o.localDiff, "local-diff", "", "a checkout to run `git diff --name-only <previous>...<head>` in, when the forge's compare is at its ceiling")
		f.StringVar(&o.pathsFrom, "paths-from", "", "the path list to classify: written by --local-diff, read back without it")
		addDogfoodFlags(f, &o, "docs/CLI.md beside --changelog")
		required = []string{"repo", "from", "version", "changelog"}
	case "build":
		f.StringVar(&o.out, "out", "", "the artifact root the release is written under, as <out>/<version>/<goos-goarch>/")
		f.StringVar(&o.source, "source", "", "the checkout to build")
		f.Var(&o.platforms, "platform", "goos-goarch, repeatable and comma-separated (default: this host)")
		addDogfoodFlags(f, &o, "<--source>/docs/CLI.md")
		f.BoolVar(&o.dryRun, "dry-run", false, "resolve the platforms and the tools and print the plan, compile and write nothing")
		f.StringVar(&o.gate, "gate", "refuse", "refuse: an open dogfood edge refuses the build; report: the edges are printed and the build goes on, --reason <why> required (a machinery install during a sprint; cut always refuses)")
		f.BoolVar(&o.incremental, "incremental", false, "compile only the tools whose packages changed since the newest clean build recorded under --out, and copy the rest from it, verified")
		required = []string{"version", "out", "source"}
	case "cycle":
		// A cycle is a build and two plays: ten minutes is a cold build alone.
		o.timeout = 30 * time.Minute
		f.StringVar(&o.source, "source", "", "the nova-tools checkout to build; its fleet/tools.yml is the play")
		f.StringVar(&o.out, "out", "", "the artifact root the build writes under (the play's nova_release_out)")
		f.StringVar(&o.inventory, "inventory", "", "the inventory the play reads, the nova-inventory script")
		f.StringVar(&o.benches, "benches", "", "the machines to install on, comma-separated, as the inventory names them")
		f.StringVar(&o.reason, "reason", "", "why this install is happening; the dogfood gate reports under it and the build record keeps it")
		f.StringVar(&o.receipts, "receipts", "", "the dogfood receipts directory (default: ~/"+DefaultReceiptsDir+" when it exists)")
		f.StringVar(&o.ansible, "ansible", "", "the ansible-playbook binary")
		f.BoolVar(&o.dryRun, "dry-run", false, "run the play with --check only: build nothing, install nothing")
		required = []string{"version", "source", "out", "inventory", "benches", "reason", "ansible"}
	case "install":
		f.StringVar(&o.from, "from", "", "the artifact root a release build wrote (its --out)")
		f.StringVar(&o.bin, "bin", "", "the directory the binaries are installed into")
		f.StringVar(&o.retire, "retire", "", "a second directory to clear of this release's tools")
		f.StringVar(&o.platform, "platform", "", "goos-goarch (default: this host)")
		f.BoolVar(&o.dryRun, "dry-run", false, "verify the release and print what would be installed, write nothing")
		required = []string{"version", "from", "bin"}
	case "adopt":
		f.StringVar(&o.from, "from", "", "the artifact root a release build wrote, here or as host:dir on another machine")
		f.StringVar(&o.bin, "bin", "", "the install directory on each machine")
		f.StringVar(&o.machines, "machines", "", MachinesShape)
		f.StringVar(&o.ssh, "ssh", "", "the ssh binary that reaches each machine")
		f.StringVar(&o.dest, "dest", "", "the artifact root on each machine")
		f.StringVar(&o.stage, "stage", "", "where to fetch a host:dir --from to")
		f.StringVar(&o.expectSums, "expect-sums", "", "sha256 of SHA256SUMS, as the cut recorded it")
		f.StringVar(&o.expectSumsFrom, "expect-sums-from", "", "a LOCAL "+DigestFile+" this host's own `release build` wrote; a release with no tag has no other digest")
		f.StringVar(&o.repo, "repo", "", "owner/name, to read that digest off the annotated tag instead")
		f.StringVar(&o.retire, "retire", "", "second directory on each machine to clear")
		f.StringVar(&o.platform, "platform", "", "goos-goarch (default: this host)")
		f.BoolVar(&o.dryRun, "dry-run", false, "probe every machine and stream nothing")
		f.StringVar(&o.certify, "certify", "", "the machines registry; turns on certification after each install")
		f.StringVar(&o.certs, "certs", "", "the certificates file the rows are appended to")
		f.StringVar(&o.standard, "standard", "", "the provisioning standard file the hash is taken over")
		f.BoolVar(&o.noCertify, "no-certify", false, "adopt without certifying, and say so on the line")
		// --version is NOT required: a --from root usually holds exactly one
		// release, and adopt reads it rather than making somebody retype what
		// the directory already says. Two releases there is the case where a
		// guess would be wrong, and it refuses naming both.
		required = []string{"from", "bin", "machines", "ssh", "dest"}
	case "pull":
		f.StringVar(&o.out, "out", "", "the artifact root holding the release to withdraw")
		f.StringVar(&o.changelog, "changelog", "", "the CHANGELOG.md whose section for the release is marked pulled")
		f.StringVar(&o.machines, "machines", "", MachinesShape)
		f.StringVar(&o.ssh, "ssh", "", "the ssh binary that reaches each machine")
		f.StringVar(&o.dest, "dest", "", "the artifact root on each machine")
		f.StringVar(&o.reason, "reason", "", "why it was withdrawn; it goes in the changelog")
		f.StringVar(&o.platform, "platform", "", "goos-goarch (default: this host)")
		f.BoolVar(&o.dryRun, "dry-run", false, "say what would be deleted and delete nothing")
		// --machines is OPTIONAL and the three fleet flags go together: a
		// release that never left this host is pulled from this host alone,
		// and the verb should not demand a machine list to say so.
		required = []string{"version", "out", "changelog"}
	}
	token := strings.ToUpper(verb)
	if err := f.Parse(args); err != nil {
		// `--help` on a verb is a REASONABLE QUESTION, not a parse failure.
		// The flag package answers it with the sentinel flag.ErrHelp, and
		// printing that gave a person who asked for help the words `flag: help
		// requested` -- the package's own internals, leaked. It is answered
		// here with that verb's usage, and exit 0, because asking is not an
		// error.
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprintln(out, VerbUsage(verb))
			fmt.Fprintln(out, "flags:")
			f.VisitAll(func(fl *flag.Flag) {
				kind, wants := flag.UnquoteUsage(fl)
				if kind != "" {
					kind = " <" + kind + ">"
				}
				fmt.Fprintf(out, "  --%s%s  %s\n", fl.Name, kind, wants)
			})
			fmt.Fprintln(out, ExitCodes)
			switch verb {
			case "cut":
				fmt.Fprintln(out, CutNote)
				fmt.Fprintln(out, DogfoodNote)
			case "build":
				fmt.Fprintln(out, DogfoodNote)
				fmt.Fprintln(out, IncrementalNote)
				fmt.Fprintln(out, BuildEffect)
			case "install":
				fmt.Fprintln(out, InstallEffect)
			case "cycle":
				fmt.Fprintln(out, CycleNote)
			case "adopt":
				fmt.Fprintln(out, AdoptNote)
			case "pull":
				fmt.Fprintln(out, PullNote)
			}
			return 0
		}
		if flagName, ok := strings.CutPrefix(err.Error(), "flag provided but not defined: -"); ok {
			// An unknown flag is named with every flag the verb takes
			// (STANDARD §3.2), so the next call is a paste.
			var names []string
			f.VisitAll(func(fl *flag.Flag) { names = append(names, "--"+fl.Name) })
			err = fmt.Errorf("unknown flag --%s; the flags are %s", strings.TrimLeft(flagName, "-"), strings.Join(names, ", "))
		}
		return refusal(errs, token, err)
	}
	if len(f.Args()) != 0 {
		return refusal(errs, token, fmt.Errorf("%s takes no positional arguments, got %q", verb, f.Arg(0)))
	}
	// EVERY missing flag at once. A refusal that names one of four sends
	// somebody round the loop four times.
	var missing []string
	for _, flagName := range required {
		if f.Lookup(flagName).Value.String() == "" {
			missing = append(missing, "--"+flagName)
		}
	}
	if len(missing) > 0 {
		return refusal(errs, token, fmt.Errorf("missing %s; refusing to guess (supply each named flag)", strings.Join(missing, ", ")))
	}
	if o.timeout <= 0 {
		return refusal(errs, token, fmt.Errorf("--timeout wants a positive duration"))
	}
	// adopt may infer its version from --from; every other verb must be told.
	if o.version != "" || verb != "adopt" {
		if err := ValidVersion(o.version); err != nil {
			return refusal(errs, token, err)
		}
	}
	// CERTIFICATION AFTER AN ADOPT IS ON BY DEFAULT. An adopt changes the build
	// on every machine it touches and so invalidates every certificate those
	// machines held; leaving the renewal to whoever remembers is how a fleet
	// spends an afternoon uncertified.
	//
	// "On by default" cannot mean guessed paths (a guessed path makes two
	// runs of one command mean different things), so it means this: an adopt
	// that names none of the three and does not waive it is REFUSED, with both
	// roads on the line. Waiving is `--no-certify`, and it is said out loud on
	// the verdict.
	if verb == "adopt" {
		var half []string
		for _, x := range []struct{ n, v string }{{"certify", o.certify}, {"certs", o.certs}, {"standard", o.standard}} {
			if x.v == "" {
				half = append(half, "--"+x.n)
			}
		}
		switch {
		case o.noCertify && len(half) < 3:
			return refusal(errs, token, fmt.Errorf(
				"--no-certify waives certification and %s asks for it; pass one",
				strings.Join(namedHalf(half), ", ")))
		case !o.noCertify && len(half) == 3:
			return refusal(errs, token, fmt.Errorf(
				"an adopt certifies the machines it changes: pass --certify <machines registry> --certs <file> --standard <file>, or waive it with --no-certify"))
		case !o.noCertify && len(half) > 0:
			return refusal(errs, token, fmt.Errorf(
				"missing %s; refusing to guess (certification after an adopt wants the machines registry, the certificates file and the provisioning standard together)",
				strings.Join(half, ", ")))
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), o.timeout)
	defer cancel()
	switch verb {
	case "cut":
		return cut(ctx, o, deps, out, errs)
	case "build":
		return build(ctx, o, deps, out, errs)
	case "install":
		return install(ctx, o, deps, out, errs)
	case "pull":
		return pull(ctx, o, deps, out, errs)
	case "cycle":
		return cycle(ctx, o, deps, out, errs)
	default:
		return adopt(ctx, o, deps, out, errs)
	}
}
