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
		How: `Report needs no bus or network. Defaults: --max 20 (0 = all), --timeout 5s,
--budget 60s; snapshot's --timeout is 30s, because the first run of a newly
installed binary is assessed by the platform and that cost is charged to the
deadline. Repeat --kind to select kinds. Cross-process delivery recovery needs
--snapshot; without it, each send is a new intention. Do not prepare again while
pending; retry the saved artifact. A snapshot uses a sibling .lock file for a
kernel lock; its presence never means a process is running.

Locals: latest=local:<path> runs that binary (or argv) on this host to read the
version; e.g., local:/usr/local/bin/nova-update or local:go version. The
installed column can be a version string (v1.2.3), a single command name found
on PATH, or a full argv. The example runs from a nova-tools checkout.`,
		ExitTable: "0 the verb did what it said (a report whose every entry answered, a snapshot, a diff, a note written); 1 the tool said NO (a report with an UNKNOWN, a send that was refused or unconfirmed, a snapshot --file with a tool that did not answer); 2 could not run (a refusal naming the remedy).",
		Verbs: []tool.Verb{
			{
				Name:  "moved",
				Usage: "moved --from <sha> --to <sha> --repo <dir> --out <path>",
				Flags: func(f *tool.Flags) {
					f.String("from", "", "revision to compare from (required)")
					f.String("to", "", "revision to compare to (required)")
					f.String("repo", "", "checkout holding both revisions (required)")
					f.String("out", "", "note to write (required)")
					f.Duration("timeout", movedChildTimeout, "one child's deadline")
					f.Duration("budget", movedBudget, "whole run deadline")
				},
				Run: func(c *tool.Call) *tool.Out { return movedVerb(c, env) },
			},
			{
				Name:  "snapshot",
				Usage: "snapshot " + manifest + "\nsnapshot --bin <dir> --out <file.tsv> [--timeout <d>] [--budget <d>]",
				Flags: func(f *tool.Flags) {
					f.String("file", "", "manifest of adopted tools: count how many answer")
					f.String("bin", "", "directory holding the binaries")
					f.String("out", "", "TSV snapshot to write")
					f.Duration("timeout", snapshotChildTimeout, "one binary's read deadline")
					f.Duration("budget", snapshotBudget, "whole run deadline")
				},
				Run: snapshotVerb,
			},
			{
				Name:  "diff",
				Usage: "diff --from <a.tsv> --to <b.tsv>",
				Flags: func(f *tool.Flags) {
					f.String("from", "", "snapshot to compare from (required)")
					f.String("to", "", "snapshot to compare to (required)")
				},
				Run: diffVerb,
			},
			{
				Name:    "report",
				Usage:   "report " + manifest + " [--host <label>] [--snapshot <path>] [--draft --as <friend> --to <who,who>] [--max <n>] [--timeout <d>] [--budget <d>] [--kind <k>]",
				Example: "report --file cmd/nova-version/testdata/example.tsv",
				Flags:   func(f *tool.Flags) { reportFlags(f, true) },
				Run:     func(c *tool.Call) *tool.Out { return reportVerb(c, false, env) },
			},
			{
				Name:  "send",
				Usage: "send " + manifest + " --as <friend> --to <who,who> --bus <path> --remote <r> --branch <b> [--snapshot <path>] [--host <label>]",
				Flags: func(f *tool.Flags) { reportFlags(f, false) },
				Run:   func(c *tool.Call) *tool.Out { return reportVerb(c, true, env) },
			},
		},
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
