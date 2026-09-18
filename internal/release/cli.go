package release

import (
	"context"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// Verbs is the usage block `nova-update help` prints for this verb, and the same
// four lines docs/SPEC-UPDATE.md carries. Every path is a flag and no flag has a
// default path: SPEC-UPDATE rule 1 (no search of the cwd, no $HOME) is why a
// release cut from a laptop and a release cut from a bench are the same release.
const Verbs = `nova-update release cut --repo <owner/name> --from <branch> --version <v> --changelog <path> [--sums <file>] [--dry-run] [--timeout <d>]
nova-update release build --version <v> --out <dir> --source <dir> [--platform <goos-goarch>] [--timeout <d>]
nova-update release install --from <dir> --version <v> --bin <dir> [--retire <dir>] [--platform <goos-goarch>] [--timeout <d>]
nova-update release adopt --version <v> --machines <file> --ssh <path> --from <dir|host:dir> --bin <dir> --dest <dir> [--stage <dir> --expect-sums <sha256>] [--retire <dir>] [--platform <goos-goarch>] [--timeout <d>]`

// AdoptNote is what a person needs before their first adopt, and every sentence
// of it is something the first dogfood pass had to find out by failing.
const AdoptNote = "adopt runs FROM the host that has ssh to every machine and fans out from there; it never needs the machines to reach each other. " +
	"When the release was built elsewhere, --from may name that machine as host:dir, --stage <dir> says where to fetch it first, and --expect-sums <sha256> is the digest the cut recorded -- a release fetched from a machine is never verified by the checksum file that came with it. " +
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
	Now       func() time.Time
	// VersionOf answers what the binary at path reports for itself. It is a
	// seam because `install`'s skip decision is the one place this package
	// runs a binary it is about to replace.
	VersionOf func(ctx context.Context, path string) (string, error)
}

// options are every flag the four verbs take, in one struct, because the four
// share --version, --from and --timeout and a reader should see that once.
type options struct {
	repo, from, version, changelog, out, source, bin, machines, ssh, dest, platform string
	stage, retire, expectSums, sums                                                 string
	dryRun                                                                          bool
	timeout                                                                         time.Duration
}

func refusal(w io.Writer, token string, err error) int {
	fmt.Fprintf(w, "%s REFUSED: %s\n", token, oneline.Err(err))
	return 2
}

// progress is the stderr voice. Glenn, 2026-09-17: a program says what it is
// doing for any step over about a tenth of a second, and every step in this
// package -- a forge read, a compile, a copy over ssh -- is well over that.
func progress(w io.Writer, format string, a ...any) {
	fmt.Fprintf(w, "release: "+format+"\n", a...)
}

// Main is the production entry: the verb with every seam at its default.
func Main(name string, args []string, out, errs io.Writer) int {
	return Run(name, args, out, errs, Deps{})
}

// Run is `nova-update release <verb>`. The verb is dispatched here and each of
// the four validates its own flags, so a missing flag is named by the verb that
// wanted it rather than by a shared check that knows about all of them.
func Run(name string, args []string, out, errs io.Writer, deps Deps) int {
	if deps.Now == nil {
		deps.Now = time.Now
	}
	if len(args) == 0 {
		return refusal(errs, "RELEASE", fmt.Errorf("a release verb is required: cut, build, install or adopt (run %s help)", name))
	}
	verb := args[0]
	args = args[1:]
	switch verb {
	case "cut", "build", "install", "adopt":
	case "help", "--help", "-h":
		fmt.Fprintln(out, Verbs)
		fmt.Fprintln(out, AdoptNote)
		return 0
	default:
		return refusal(errs, "RELEASE", fmt.Errorf("unknown release verb %s (use cut, build, install or adopt)", verb))
	}
	o := options{timeout: 10 * time.Minute}
	f := flag.NewFlagSet("release "+verb, flag.ContinueOnError)
	f.SetOutput(io.Discard)
	f.DurationVar(&o.timeout, "timeout", o.timeout, "whole-run deadline")
	f.StringVar(&o.version, "version", "", "release version")
	// Every verb's own flags, declared per verb rather than all at once, so
	// that `release cut --bin x` is an unknown flag rather than a silent
	// no-op -- a flag the verb ignores is a flag somebody thinks worked.
	var required []string
	switch verb {
	case "cut":
		f.StringVar(&o.repo, "repo", "", "owner/name")
		f.StringVar(&o.from, "from", "", "branch")
		f.StringVar(&o.changelog, "changelog", "", "CHANGELOG.md path")
		f.BoolVar(&o.dryRun, "dry-run", false, "decide and print, write nothing")
		f.StringVar(&o.sums, "sums", "", "a built SHA256SUMS whose digest the section records")
		required = []string{"repo", "from", "version", "changelog"}
	case "build":
		f.StringVar(&o.out, "out", "", "artifact root")
		f.StringVar(&o.source, "source", "", "the checkout to build")
		f.StringVar(&o.platform, "platform", "", "goos-goarch (default: this host)")
		required = []string{"version", "out", "source"}
	case "install":
		f.StringVar(&o.from, "from", "", "artifact root")
		f.StringVar(&o.bin, "bin", "", "install directory")
		f.StringVar(&o.retire, "retire", "", "second directory to clear of this release's tools")
		f.StringVar(&o.platform, "platform", "", "goos-goarch (default: this host)")
		required = []string{"version", "from", "bin"}
	case "adopt":
		f.StringVar(&o.from, "from", "", "artifact root, or host:dir on another machine")
		f.StringVar(&o.bin, "bin", "", "install directory on each machine")
		f.StringVar(&o.machines, "machines", "", MachinesShape)
		f.StringVar(&o.ssh, "ssh", "", "the ssh binary")
		f.StringVar(&o.dest, "dest", "", "artifact root on each machine")
		f.StringVar(&o.stage, "stage", "", "where to fetch a host:dir --from to")
		f.StringVar(&o.expectSums, "expect-sums", "", "sha256 of SHA256SUMS, as the cut recorded it")
		f.StringVar(&o.retire, "retire", "", "second directory on each machine to clear")
		f.StringVar(&o.platform, "platform", "", "goos-goarch (default: this host)")
		required = []string{"version", "from", "bin", "machines", "ssh", "dest"}
	}
	token := strings.ToUpper(verb)
	if err := f.Parse(args); err != nil {
		return refusal(errs, token, fmt.Errorf("%s (run %s help)", err, name))
	}
	if len(f.Args()) != 0 {
		return refusal(errs, token, fmt.Errorf("release %s takes no positional arguments (run %s help)", verb, name))
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
		return refusal(errs, token, fmt.Errorf("missing %s; refusing to guess (supply each named flag; run: %s help)", strings.Join(missing, ", "), name))
	}
	if o.timeout <= 0 {
		return refusal(errs, token, fmt.Errorf("invalid bound (use a positive --timeout)"))
	}
	if err := ValidVersion(o.version); err != nil {
		return refusal(errs, token, err)
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
	default:
		return adopt(ctx, o, deps, out, errs)
	}
}
