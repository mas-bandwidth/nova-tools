package update

import (
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
		What:  "installed tool identities: report, snapshot, diff, and the TOOLS MOVED note (see docs/SPEC-VERSION.md)",
		Stamp: stamp,
		How: `Report reads installed identities with no bus or network; --draft prints a note, send delivers it.
Defaults: --max 20 (0 = all), --timeout 5s (snapshot 30s: a new binary's first run is assessed), --budget 60s.
Delivery recovery across processes needs --snapshot; retry the saved artifact rather than prepare again.
latest=local:<path> runs that binary (or argv) here; installed is a version, a command on PATH, or an argv.
The examples run from a nova-tools checkout.`,
		ExitTable: "0 the verb did what it said (a report whose every entry answered, a snapshot, a diff, a note written); 1 the tool said NO (a report with an UNKNOWN, a send that was refused or unconfirmed, a snapshot --file with a tool that did not answer); 2 could not run (a refusal naming the remedy).",
		Verbs: []tool.Verb{
			{
				Name:   "moved",
				Usage:  "moved --from <sha> --to <sha> --repo <dir> --out <path>",
				Effect: tool.LocalWrite,
				Flags: func(f *tool.Flags) {
					f.Required("from", "the revision to compare from")
					f.Required("to", "the revision to compare to")
					f.Required("repo", "the checkout holding both revisions")
					f.Required("out", "the path of the note to write")
					f.Duration("timeout", movedChildTimeout, "one child's deadline")
					f.Duration("budget", movedBudget, "whole run deadline")
					f.Check(positiveBounds)
				},
				Run: func(c *tool.Call) *tool.Out { return movedVerb(c, env) },
			},
			{
				Name:   "snapshot",
				Usage:  "snapshot " + manifest + "\nsnapshot --bin <dir> --out <file.tsv> [--timeout <d>] [--budget <d>]",
				Effect: tool.LocalWrite + "; with --file, inspection",
				Flags: func(f *tool.Flags) {
					f.String("file", "", "manifest of adopted tools: count how many answer")
					f.String("bin", "", "directory holding the binaries")
					f.String("out", "", "TSV snapshot to write")
					f.Duration("timeout", snapshotChildTimeout, "one binary's read deadline")
					f.Duration("budget", snapshotBudget, "whole run deadline")
					// Neither path is guessed: both are the caller's to name
					// (SPEC-UPDATE rule 1), unless --file asks the manifest shape.
					f.Check(func(c *tool.Call) {
						if !c.Given("file") {
							c.Want("bin", "the directory holding the binaries, for example ./bin")
							c.Want("out", "the TSV snapshot to write, for example ./before.tsv")
						}
					})
					f.Check(positiveBounds)
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
				Example: "report --file cmd/nova-version/testdata/example.tsv\nversion",
				Effect:  tool.Inspection + "; --draft prints a note, --send delivers it",
				Flags:   func(f *tool.Flags) { reportFlags(f, true) },
				Run:     func(c *tool.Call) *tool.Out { return reportVerb(c, false, env) },
			},
			{
				Name:   "send",
				Usage:  "send " + manifest + " --as <friend> --to <who,who> --bus <path> --remote <r> --branch <b> [--snapshot <path>] [--host <label>]",
				Effect: tool.Delivery,
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
	f.String("snapshot", "", "explicit state file")
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
	return tool.Exit(checked("nova-version", "report", "REPORT", o, nil, c.Stdout, c.Stderr, env))
}
