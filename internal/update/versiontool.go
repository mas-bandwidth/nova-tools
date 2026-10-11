package update

import (
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/tool"
)

// VersionTool is nova-version on pkg/tool: its verbs are this package's
// moved, snapshot and diff (SPEC-VERSION), and report and send, which share
// nova-update's report body. Run("nova-version", ...) is this tool's Run, so
// the package's tests reach it by that name.
func VersionTool(stamp string, env Environment) *tool.Tool {
	if env.Now == nil {
		env.Now = time.Now
	}
	manifest := "--file <manifest>"
	return &tool.Tool{
		Name:  "nova-version",
		What:  "which version of each tool is installed, recorded and compared",
		Stamp: stamp,
		How: `report reads each tool's installed version; snapshot records a directory's binaries;
diff compares two snapshots; moved writes the note of what two commits' binaries changed.
It is one of two binaries sharing the manifest and report; latest and installing are nova-update's.
THE MANIFEST is the file --file names, written by hand; report -h states its six rules.
first run: the binary alone; the example lines write a one-tool manifest and read it.`,
		NoJSON:    "report and send write the note body the bus carries, so they take no --json; report's first line is `REPORT OK checked=1 known=1 unknown=0 changed=- sent=-`",
		UsageNote: "THE MANIFEST is the file --file names: " + manifestShape + "; report -h states its six rules.",
		ExitTable: "0 done, 2 usage or could not run, for every verb; by verb:\n" +
			"  report: 0 every entry answered; 1 an entry is UNKNOWN; 2 usage, or a\n" +
			"    manifest that did not read\n" +
			"  send: 0 nova-bus took the note; 1 an entry is UNKNOWN, or the send was\n" +
			"    refused or unconfirmed; 2 usage, or a manifest that did not read\n" +
			"  snapshot: 0 every tool answers; 1 a tool is UNKNOWN; 2 usage, or a\n" +
			"    manifest or directory that did not read\n" +
			"  diff: 0 the snapshots compared; 2 usage, or a snapshot that did not read\n" +
			"  moved: 0 the note written; 2 usage, or a revision or build that did not run",
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
					// The two shapes carry two real defaults: --bin reads one
					// binary under snapshotChildTimeout (SPEC-VERSION rule 11)
					// and --file probes one adopted tool under
					// snapshotAdoptedTimeout (SPEC-UPDATE) unless --timeout is
					// given, so the help names both rather than one.
					f.Duration("timeout", snapshotChildTimeout, "one binary's read deadline: 30s with --bin, 5s with --file unless --timeout is given")
					f.Duration("budget", snapshotBudget, "whole run deadline")
					f.Bool("dry-run", false, "read every binary and list the rows; write no --out")
					// Neither path is guessed: both are the caller's to name
					// (SPEC-UPDATE rule 1), unless --file asks the manifest shape.
					// The refusal names both shapes, so either is the next call.
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
				Run: func(c *tool.Call) *tool.Out { return snapshotVerb(c, env) },
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
				Usage:  "send " + manifest + " --as <friend> --to <who,who> [--snapshot <path>] [--host <label>]",
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
// verbs Print until nova-update is on pkg/tool.
func reportFlags(f *tool.Flags, report bool) {
	f.Prints()
	f.String("file", "", "manifest (required): "+manifestShape)
	f.String("host", "", "execution bench label")
	f.String("snapshot", "", "state file of what was observed and confirmed sent: an unchanged report is not sent again")
	f.Bool("draft", false, "print the note only")
	if report {
		f.Bool("send", false, "explicit delivery")
	}
	f.String("as", "", "the sender the note is from")
	f.String("to", "", "the recipients, comma-separated")
	f.Int("max", 20, "per-kind output cap; 0 is all")
	f.Duration("timeout", 5*time.Second, "one read deadline")
	f.Duration("budget", 60*time.Second, "whole run deadline")
	f.Var(new(kindFlags), "kind", "kind filter; repeat to select kinds")
}

func reportVerb(c *tool.Call, send bool, env Environment) *tool.Out {
	o := options{
		file: c.Str("file"), host: c.Str("host"), snapshot: c.Str("snapshot"),
		as: c.Str("as"), to: c.Str("to"),
		max: c.Int("max"), timeout: c.Dur("timeout"), budget: c.Dur("budget"),
		kinds: *c.Get("kind").(*kindFlags), draft: c.Bool("draft"), send: send || (c.Given("send") && c.Bool("send")),
	}
	verb := "report"
	if send {
		verb = "send"
	}
	return tool.Exit(emit(checked("nova-version", verb, o, nil, env), false, o.max, c.Stdout, c.Stderr))
}
