package update

import (
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/tool"
)

// VersionTool is nova-version on internal/tool: its verbs are this package's
// moved, snapshot and diff (SPEC-VERSION), and report and send, which share
// nova-update's report body. Run("nova-version", ...) is this tool's Run, so
// the package's tests reach it by that name.
func VersionTool(stamp string, env Environment) *tool.Tool {
	if env.Now == nil {
		env.Now = time.Now
	}
	manifest := "--file <manifest: " + manifestShape + ">"
	return &tool.Tool{
		Name:  "nova-version",
		What:  "report which version of each tool is installed, record it, and compare two records or two revisions",
		Stamp: stamp,
		How: `report reads installed versions; snapshot records a manifest or directory.
diff compares two recorded inventories; moved builds two revisions and compares their help.
It shares manifests and report with nova-update, which checks for and installs new releases.
The manifest is the file --file names, written by hand; report -h states its six rules.
first run: Go on PATH; the example lines write a one-tool manifest and read its version.`,
		ExitTable: "0 the verb ran and passed: a report whose every entry answered (under send, whose note nova-bus took), a snapshot whose tools all answer, a diff, a moved note written; 1 the tool said NO (a report or a snapshot with an UNKNOWN tool, a send that was refused or unconfirmed); 2 could not run (a refusal naming the remedy).",
		Verbs: []tool.Verb{
			{
				Name:    "example",
				Usage:   "example [--out <path>]",
				Example: "example --out versions.tsv",
				Effect:  tool.LocalWrite + " with --out, never over another file; without it, inspection: prints the example manifest",
				Flags: func(f *tool.Flags) {
					f.String("out", "", "write the example manifest to this path (an existing file is never overwritten); without it, print the manifest")
				},
				Run: func(c *tool.Call) *tool.Out { return exampleVerb("nova-version", c.Str("out")) },
			},
			{
				Name:   "moved",
				Usage:  "moved --from <sha> --to <sha> --repo <dir> --out <path> [--timeout <d>] [--budget <d>] [--dry-run]",
				Effect: tool.LocalWrite + "; with --dry-run, the note is printed and nothing is written but the builds' scratch",
				// No banner example: it needs a checkout and builds every cmd/* twice.
				Detail: "example, in a checkout containing both revisions (requires Go and Git; raise --budget if the builds need longer):\n" +
					"  nova-version moved --from HEAD~1 --to HEAD --repo . --out moved.txt --dry-run\n",
				Flags: func(f *tool.Flags) {
					f.Required("from", "the revision to compare from")
					f.Required("to", "the revision to compare to")
					f.Required("repo", "the checkout holding both revisions")
					f.Required("out", "the path of the note to write")
					f.Duration("timeout", movedChildTimeout, "one child's deadline")
					f.Duration("budget", movedBudget, "whole run deadline")
					f.Bool("dry-run", false, "build and read both revisions and print the note; write no --out")
					f.Check(positiveBounds)
				},
				Run: func(c *tool.Call) *tool.Out { return movedVerb(c, env) },
			},
			{
				Name:    "snapshot",
				Usage:   "snapshot " + manifest + "\nsnapshot --bin <dir> --out <file.tsv> [--timeout <d>] [--budget <d>] [--max <n>] [--dry-run]",
				Example: "snapshot --file versions.tsv",
				Effect:  tool.LocalWrite + "; with --file or --dry-run, inspection: writes nothing",
				Flags: func(f *tool.Flags) {
					f.String("file", "", "manifest of adopted tools: count how many answer")
					f.String("bin", "", "directory holding the binaries")
					f.String("out", "", "TSV snapshot to write")
					f.Duration("timeout", snapshotChildTimeout, "one binary's read deadline")
					f.Duration("budget", snapshotBudget, "whole run deadline")
					f.Bool("dry-run", false, "read every binary and list the rows; write no --out")
					// The caller names either a manifest or both inventory paths.
					// The refusal explains both shapes (SPEC-UPDATE, no guessed paths).
					f.Check(func(c *tool.Call) {
						var missing []string
						for _, n := range []string{"bin", "out"} {
							if c.Str(n) == "" {
								missing = append(missing, "--"+n)
							}
						}
						switch {
						case c.Given("file") && len(missing) < 2:
							c.Problem("--file counts a manifest's adopted tools and --bin/--out inventory a directory; give one shape, not both")
						case !c.Given("file") && len(missing) > 0:
							c.Problem("missing " + strings.Join(missing, ", ") + "; refusing to guess (--bin wants the directory holding the binaries, for example ./bin, and --out the TSV snapshot to write, for example ./before.tsv; or give --file <manifest> alone to count which adopted tools answer)")
						}
					})
					f.Check(positiveBounds)
					f.Max()
				},
				Run: snapshotVerb,
			},
			{
				Name:   "diff",
				Usage:  "diff --from <a.tsv> --to <b.tsv>",
				Effect: tool.Inspection,
				Flags: func(f *tool.Flags) {
					f.Required("from", "the snapshot to compare from")
					f.Required("to", "the snapshot to compare to")
				},
				Run: diffVerb,
			},
			{
				Name:  "report",
				Usage: "report " + manifest + " [--host <label>] [--snapshot <path>] [--draft --as <friend> --to <who,who>] [--max <n>] [--timeout <d>] [--budget <d>] [--kind <k>]",
				// The second example line is the version verb's: the banner's
				// examples are its verbs' in order, and version is last.
				Example: "report --file versions.tsv\nversion",
				// The strongest effect a flag gives it: --send delivers the note and
				// writes the --snapshot state file; without --send it only reads.
				Effect: tool.Delivery + "; only with --send, which also writes the --snapshot state file; " +
					"without --send, report reads and writes nothing (--draft prints the note)",
				Detail: manifestHelp("nova-version"),
				Flags:  func(f *tool.Flags) { reportFlags(f, true) },
				Run:    func(c *tool.Call) *tool.Out { return reportVerb(c, false, env) },
			},
			{
				Name:   "send",
				Usage:  "send " + manifest + " --as <friend> --to <who,who> --bus <path> --remote <r> --branch <b> [--snapshot <path>] [--host <label>]",
				Effect: tool.Delivery + "; --snapshot writes its state file",
				Detail: manifestHelp("nova-version"),
				Flags:  func(f *tool.Flags) { reportFlags(f, false) },
				Run:    func(c *tool.Call) *tool.Out { return reportVerb(c, true, env) },
			},
		},
	}
}

// positiveBounds is the rule on --timeout and --budget: a bound is positive.
func positiveBounds(c *tool.Call) {
	if c.Dur("timeout") <= 0 || c.Dur("budget") <= 0 {
		c.Problem("invalid bound (use positive --timeout/--budget)")
	}
}

// reportFlags are report's flags; send is report with delivery implied.
// Report's body prints its own lines because nova-update shares it, so these
// verbs Print until nova-update is on internal/tool.
func reportFlags(f *tool.Flags, report bool) {
	f.Prints()
	f.String("file", "", "manifest (required): "+manifestShape)
	f.String("host", "", "execution bench label")
	f.String("snapshot", "", "state file for delivery recovery across processes: retry the saved artifact, never prepare again while pending")
	f.Bool("draft", false, "print the note only")
	if report {
		f.Bool("send", false, "explicit delivery")
	}
	f.String("as", "", "the sender the note is from")
	f.String("to", "", "the recipients, comma-separated")
	f.String("bus", "", "the bus checkout that delivers the note")
	f.String("remote", "", "the bus remote")
	f.String("branch", "", "the bus branch")
	f.Int("max", 20, "per-kind output cap; 0 is all")
	f.Duration("timeout", 5*time.Second, "one read deadline")
	f.Duration("budget", 60*time.Second, "whole run deadline")
	f.Var(new(kindFlags), "kind", "kind filter; repeat to select kinds")
}

func reportVerb(c *tool.Call, send bool, env Environment) *tool.Out {
	o := options{
		file: c.Str("file"), host: c.Str("host"), snapshot: c.Str("snapshot"),
		as: c.Str("as"), to: c.Str("to"), bus: c.Str("bus"), remote: c.Str("remote"), branch: c.Str("branch"),
		max: c.Int("max"), timeout: c.Dur("timeout"), budget: c.Dur("budget"),
		kinds: *c.Get("kind").(*kindFlags), draft: c.Bool("draft"), send: send || (c.Given("send") && c.Bool("send")),
	}
	verb := "report"
	if send {
		verb = "send"
	}
	return tool.Exit(emit(checked("nova-version", verb, o, nil, env), false, o.max, c.Stdout, c.Stderr))
}
