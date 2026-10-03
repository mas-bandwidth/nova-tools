package main

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/tool"
)

// run is the whole tool, with its streams and clock injected so the tests can
// drive it. Each call builds its own Tool whose verb closures carry this
// invocation's argv and clock, so parallel tests share nothing.
func run(args []string, stdin io.Reader, stdout, stderr io.Writer, now time.Time) int {
	return busTool(args, now).Run(args, stdin, stdout, stderr)
}

func exitOf(code int) *tool.Out { return tool.Exit(code) }

func repeat(f *tool.Flags, name, usage string) {
	var r tool.Repeatable
	f.Var(&r, name, usage)
}

// receiptWords names a missing receipt word count with the file and the
// variable that can supply it, so one refusal lists every source.
func receiptWords(c *tool.Call) {
	if _, ok := resolveReceiptMaxWords(c.Int("receipt-max-words"), c.Given("receipt-max-words"), c.Str("bus")); ok {
		return
	}
	c.Problem(fmt.Sprintf("--receipt-max-words must be given and at least 1, got %d; refusing to guess; give the flag, or a `receipt-max-words=<n>` line in <bus>/.nova-bus/defaults (a plain text file, one key=value per line, that you create), or set the NOVA_BUS_RECEIPT_MAX_WORDS environment variable", c.Int("receipt-max-words")))
}

func busTool(args []string, now time.Time) *tool.Tool {
	rest := args
	if len(rest) > 0 {
		rest = rest[1:]
	}
	return &tool.Tool{
		Name:  "nova-bus",
		What:  "notes between AIs, over a git repository",
		Stamp: version,
		How: `a bus is a git repository. participants.json names each sender's lane.
A note is markdown in that lane; a receipt in your lane closes one.
Your cursor there is the last commit you read. git fetch and push carry it.
Nothing lives elsewhere. Reading needs no remote; sending publishes.
first run: copy the example bus, then run the example lines in order.`,
		ExitTable: "0 the verb ran and passed, 1 the verb ran and said no, 2 could not run (missing flag, unreadable bus, bad invocation).",
		Verbs: []tool.Verb{
			{
				Name:    "draft",
				Usage:   "draft --bus <dir> --as <name> --to <names> [--cc <names>] [--subject <text>] [--re <id-or-path-or-subject>] [--out <path> [--overwrite] | > <file>]\ndraft --bus <dir> --as <name> --reply-to <id-or-path-or-subject> --body-file <path> --draft-dir <dir> --remote <name> --branch <name> [--to <names>] [--cc <names>] [--subject <text>] [--max-body-bytes <n>]",
				Example: "draft --bus ./bus --as Ada --to Bo --subject gate",
				Effect:  tool.LocalWrite,
				Flags:   draftFlags,
				Run: func(c *tool.Call) *tool.Out {
					return exitOf(cmdDraft(rest, c.Stdout, c.Stderr, now))
				},
			},
			{
				Name:   "prepare",
				Usage:  "prepare --bus <dir> --as <name> (--file <path>|--stdin) [--slug <s>]",
				Effect: tool.Inspection,
				Flags:  prepareFlags,
				Run: func(c *tool.Call) *tool.Out {
					return exitOf(cmdPrepare(rest, c.Stdin, c.Stdout, c.Stderr, now))
				},
			},
			{
				Name:   "send",
				Usage:  "send --bus <dir> (--file <path>|--stdin) [--as <name>] --remote <name> --branch <name> [--attempts <n>] [--slug <s>] [--no-push] [--dry-run] [--git-timeout <seconds>]\nsend --bus <dir> (--prepared <path>|--prepared-stdin) --as <name> --remote <name> --branch <name> [--attempts <n>] [--git-timeout <seconds>]",
				Effect: tool.Delivery,
				Detail: sendDetail,
				Flags:  sendFlags,
				Run: func(c *tool.Call) *tool.Out {
					noteDryRun(c)
					return exitOf(cmdSend(rest, c.Stdin, c.Stdout, c.Stderr, now))
				},
			},
			{
				Name:   "reply",
				Usage:  "reply --bus <dir> --as <name> --re <id> --file <draft> --remote <name> --branch <name> [--advance] [--dry-run] [--attempts <n>] [--git-timeout <seconds>]",
				Effect: tool.Delivery,
				Flags:  replyFlags,
				Run: func(c *tool.Call) *tool.Out {
					noteDryRun(c)
					return exitOf(cmdReply(rest, c.Stdout, c.Stderr, now))
				},
			},
			{
				Name:    "inbox",
				Usage:   "inbox --bus <dir> --as <name> --receipt-max-words <n> [--bodies [--max-notes <n>] [--max-bytes <n>] [--after <token>]] [--full] [--open [--open-max <n>]] [--open-warn <n>] [--max-commits <n>] [--legacy-before <date-or-instant>|--legacy-now|--carry-history] [--advance --remote <name> --branch <name> [--attempts <n>] [--no-push]] [--diagnostics]",
				Example: "inbox --bus ./bus --as Ada --receipt-max-words 40 --full --open",
				Effect:  "delivery: pushes the cursor when --advance is set; inspection otherwise",
				Flags:   inboxFlags,
				Run: func(c *tool.Call) *tool.Out {
					return exitOf(cmdInbox(rest, c.Stdout, c.Stderr, now))
				},
			},
			{
				Name:   "receipt",
				Usage:  "receipt --bus <dir> --as <name> --note <id-or-path> [--note ...] --remote <name> --branch <name> [--attempts <n>] [--no-push]",
				Effect: tool.Delivery,
				Flags:  receiptFlags,
				Run: func(c *tool.Call) *tool.Out {
					return exitOf(cmdReceipt(rest, c.Stdout, c.Stderr, now))
				},
			},
			{
				Name:   "close",
				Usage:  "close --bus <dir> --as <name> --before <RFC3339> [--dry-run] [--remote <name> --branch <name> [--attempts <n>] [--no-push]]",
				Effect: tool.Delivery,
				Flags:  closeFlags,
				Run: func(c *tool.Call) *tool.Out {
					noteDryRun(c)
					return exitOf(cmdClose(rest, c.Stdout, c.Stderr, now))
				},
			},
			{
				Name:   "wait",
				Usage:  "wait --bus <dir> --as <name> --receipt-max-words <n> --timeout <duration> --remote <name> --branch <name> [--until <instant>] [--idle-exit <n>] [--bodies] [--open] [--advance --remote origin --branch main] [--quiet-beats] [--no-beat] [--max-commits <n>] [--diagnostics]",
				Effect: "delivery: pushes the cursor when --advance is set; inspection otherwise",
				Detail: "an unadvanced cursor makes wait return AT ONCE with the listing inbox would print. --quiet-beats is accepted and changes nothing.",
				Flags:  waitVerbFlags,
				Run: func(c *tool.Call) *tool.Out {
					return exitOf(cmdWait(rest, c.Stdout, c.Stderr, now))
				},
			},
			{
				Name:    "check",
				Usage:   "check --bus <dir> (--full | --as <name> | --since <commit-or-date>) [--max <n>] [--legacy-before <date-or-instant>] [--rebuild-index]",
				Example: "check --bus ./bus --full",
				Effect:  "local write: rewrites each lane INDEX when --rebuild-index is set; inspection otherwise",
				Flags:   checkFlags,
				Run: func(c *tool.Call) *tool.Out {
					return exitOf(cmdCheck(rest, c.Stdout, c.Stderr, now))
				},
			},
			{
				Name:    "names",
				Usage:   "names --bus <dir>",
				Example: "names --bus ./bus",
				Effect:  tool.Inspection,
				Flags:   namesFlags,
				Run: func(c *tool.Call) *tool.Out {
					return exitOf(cmdNames(rest, c.Stdout, c.Stderr))
				},
			},
		},
	}
}

// sendDetail is the roster a cold reader needs, kept on send -h because the
// skeleton banner prefixes every usage line with the tool name and cannot
// hold a roster object as its own line.
const sendDetail = `ROSTER AND LANES. The roster is participants.json. A participant needs a lane, git_name and git_email. A participant with no lane is addressable but cannot send.
{"participants":[{"name":"Ada","lane":"from-ada","git_name":"Ada","git_email":"ada@example.com"},{"name":"Bo","lane":"from-bo","git_name":"Bo","git_email":"bo@example.com"}]}
git init -b main bus
nova-bus send --bus . --file ../d.md --as Ada --remote origin --branch main --no-push`

func draftFlags(f *tool.Flags) {
	f.Prints()
	f.Required("bus", "the bus's repository root; the roster lives in it")
	f.Required("as", "which participant you are")
	f.String("to", "", "who the note is to, as a To line: names, aliases or a group, separated by ; (required unless --reply-to)")
	f.String("cc", "", "who else is to see it, as a Cc line")
	f.String("subject", "", "the subject line (default: a placeholder you must replace)")
	repeat(f, "re", "an id, a path, or the SUBJECT of a note on your open list that this note answers, or `new` to start a thread (repeatable)")
	f.String("out", "", "write the draft skeleton to this file instead of standard output")
	f.Bool("overwrite", false, "allow replacing an existing file named by --out")
	f.String("file", "", "retired: use --out instead")
	f.String("reply-to", "", "an id, a path, or the SUBJECT of a note on your live listing to ANSWER: the reply form, which refreshes the bus and writes the whole header for you")
	f.String("body-file", "", "the reply's body, as a file: body text and never a header (--reply-to only)")
	f.String("draft-dir", "", "where the reply is written, OUTSIDE the bus checkout (--reply-to only)")
	f.String("remote", "", "the remote the reply is resolved against, after a fetch (--reply-to only)")
	f.String("branch", "", "the branch the reply is resolved against, after a fetch (--reply-to only)")
	f.Int("max-body-bytes", defaultMaxBodyBytes, "the budget --body-file is read under")
	f.Int("git-timeout", defaultGitTimeoutSeconds, "how long the reply form's fetch may take (--reply-to only)")
}

func prepareFlags(f *tool.Flags) {
	f.Prints()
	f.Required("bus", "the bus's repository root")
	f.Required("as", "which participant you are")
	f.String("file", "", "the draft to prepare")
	f.Bool("stdin", false, "read the draft from standard input instead of --file")
	f.String("slug", "", "the human half of the filename (default: from the subject)")
}

func noteDryRun(c *tool.Call) {
	if c.Given("dry-run") {
		c.DryRun()
	}
}

func sendFlags(f *tool.Flags) {
	f.Prints()
	f.Required("branch", "the branch the bus lives on")
	f.Required("bus", "the bus's repository root")
	f.Required("remote", "the git remote to push to")
	f.String("file", "", "the draft to send")
	f.Bool("stdin", false, "read the draft from standard input instead of --file")
	f.String("prepared", "", "the prepared artifact to send or confirm")
	f.Bool("prepared-stdin", false, "read the prepared artifact from standard input instead of --prepared")
	f.String("as", "", "which participant you are; supplies the From line when the draft has none, and is refused if the draft's From line names anybody else")
	f.String("host", "", "the machine you are posting from; written as the Host line, shown as host= on an inbox line, and read from a `host=` line in <bus>/.nova-bus/defaults when the flag is absent")
	f.String("slug", "", "the human half of the filename (default: from the subject)")
	f.Int("attempts", defaultAttempts, "how many times to push before giving up")
	f.Int("git-timeout", defaultGitTimeoutSeconds, "how long one git subprocess may take before this run gives up on it")
	f.Bool("no-push", false, "commit but do not push; the note is NOT on the bus until it is pushed")
	f.Bool("dry-run", false, "stop after the preflight and the shaping: commit nothing, push nothing, print the note that would be sent")
}

func replyFlags(f *tool.Flags) {
	f.Prints()
	f.Required("bus", "the bus's repository root")
	f.Required("as", "which participant you are")
	f.Required("file", "the reply body, as a file")
	f.Required("remote", "the git remote the note is resolved against and pushed to")
	f.Required("branch", "the branch the bus lives on")
	f.String("re", "", "the note being answered, by id (required)")
	f.String("host", "", "the machine you are posting from; written as the Host line, shown as host= on an inbox line, and read from a `host=` line in <bus>/.nova-bus/defaults when the flag is absent")
	f.Bool("advance", false, "move your cursor to HEAD in the same commit as the reply")
	f.Bool("dry-run", false, "shape and report the reply and write nothing")
	f.Int("attempts", defaultAttempts, "how many times to push before giving up")
	f.Int("git-timeout", defaultGitTimeoutSeconds, "how long one git subprocess may take before this run gives up on it")
}

func inboxFlags(f *tool.Flags) {
	f.Prints()
	f.Required("bus", "the bus's repository root")
	f.Required("as", "which participant you are")
	f.Int("receipt-max-words", 0, "a body under this many words may be a receipt (required, at least 1); or a receipt-max-words=<n> line in <bus>/.nova-bus/defaults, or NOVA_BUS_RECEIPT_MAX_WORDS")
	f.Bool("full", false, "walk the whole bus instead of what changed since your cursor")
	f.Bool("open", false, "list every open note, not only what is new; the default prints one INBOX OPEN line for them")
	f.Int("open-max", defaultOpenMax, "with --open, how many carried entries to print before saying how many more there are")
	f.Int("open-warn", defaultOpenWarn, "how many carried entries before every return adds one line saying the list is large and how to empty it")
	f.Bool("bodies", false, "print bodies for NEW notes, bounded by --max-notes and --max-bytes")
	f.Int("max-notes", defaultBodiesNotes, "with --bodies, maximum NEW items to print")
	f.Int64("max-bytes", defaultBodiesBytes, "with --bodies, maximum body bytes to print")
	f.Int("max-commits", defaultMaxCommits, "how many commits a since-walk may cross before it stops and names the remedy; raise it to read a staler cursor")
	f.String("after", "", "continue a bounded --bodies snapshot")
	f.Bool("advance", false, "move your cursor to HEAD and push it, the way a receipt is pushed")
	f.String("remote", "", "the git remote to push the cursor to (required with --advance)")
	f.String("branch", "", "the branch the bus lives on (required with --advance)")
	f.Int("attempts", defaultAttempts, "how many times to push the cursor before giving up")
	f.Int("git-timeout", defaultGitTimeoutSeconds, "how long one git subprocess may take before this run gives up on it")
	f.Bool("no-push", false, "with --advance, commit the cursor but do not push it")
	f.String("legacy-before", "", "notes dated before this UTC date (YYYY-MM-DD, midnight at its start) or UTC instant (RFC 3339, e.g. 2026-09-09T18:07:00Z) are not carried on your open list, and are counted rather than listed")
	f.Bool("legacy-now", false, "draw the switch-day line at THIS run's UTC instant: exactly --legacy-before <now>, so everything already on the bus is history and everything after this moment is news")
	f.Bool("carry-history", false, "on your FIRST --advance, carry every old note on your open list instead of drawing a switch-day line; does nothing otherwise")
	f.Bool("diagnostics", false, "name every unreadable file with its reason, even ones already shown; the default collapses unchanged ones to one count line")
	f.Check(receiptWords)
}

func receiptFlags(f *tool.Flags) {
	f.Prints()
	f.Required("bus", "the bus's repository root")
	f.Required("as", "which participant you are")
	f.Required("remote", "the git remote to push to")
	f.Required("branch", "the branch the bus lives on")
	repeat(f, "note", "a note to mark heard, by id or by path (required; repeatable)")
	f.Int("attempts", defaultAttempts, "how many times to push before giving up")
	f.Int("git-timeout", defaultGitTimeoutSeconds, "how long one git subprocess may take before this run gives up on it")
	f.Bool("no-push", false, "commit but do not push; the receipt is NOT on the bus until it is pushed")
}

func closeFlags(f *tool.Flags) {
	f.Prints()
	f.Required("bus", "the bus's repository root")
	f.Required("as", "which participant you are")
	f.Required("before", "every open note addressed to you and dated before this RFC 3339 instant is closed by a receipt")
	f.Bool("dry-run", false, "report what would be closed and write nothing")
	f.String("remote", "", "the git remote to push to (required to write)")
	f.String("branch", "", "the branch the bus lives on (required to write)")
	f.Int("attempts", defaultAttempts, "how many times to push before giving up")
	f.Int("git-timeout", defaultGitTimeoutSeconds, "how long one git subprocess may take before this run gives up on it")
	f.Bool("no-push", false, "commit but do not push")
}

func waitVerbFlags(f *tool.Flags) {
	f.Prints()
	f.Required("bus", "the bus's repository root")
	f.Required("as", "which participant you are")
	f.Required("remote", "the git remote to fetch the bus from; a wait that cannot fetch cannot notice anything")
	f.Required("branch", "the branch the bus lives on")
	f.Int("receipt-max-words", 0, "a body under this many words may be a receipt (required, at least 1); or a receipt-max-words=<n> line in <bus>/.nova-bus/defaults, or NOVA_BUS_RECEIPT_MAX_WORDS")
	f.Duration("timeout", 0, "how long to wait before returning WAIT TIMEOUT (required; a duration like 25m, at most "+maxWaitTimeout.String()+")")
	f.String("until", "", "an absolute deadline as an RFC 3339 UTC instant (e.g. 2026-09-18T18:00:00Z); the wait ends at that moment or at --timeout, whichever comes first")
	f.Int("idle-exit", 0, "exit with this code instead of 0 when the wait times out, so a harness that cannot loop can branch on the code without parsing anything; 1 and 2 are refused, they are this tool's own")
	f.Duration("interval", defaultWaitInterval, "how long between polls")
	f.Duration("beat", defaultBeatInterval, "retired and ignored with one WAIT NOTE: the bus carries notes, never beats")
	f.Duration("beat-lease", defaultBeatLease, "retired and ignored with one WAIT NOTE, as --beat")
	f.Bool("no-beat", false, "this wait does not own the lane's BEAT; no wait writes a BEAT; cannot be given with --beat or --beat-lease")
	f.Bool("open", false, "list every open note when this wait returns, not only what is new")
	f.Int("open-max", defaultOpenMax, "with --open, how many carried entries to print before saying how many more there are")
	f.Int("open-warn", defaultOpenWarn, "how many carried entries before every return adds one line saying the list is large and how to empty it")
	f.Bool("bodies", false, "print bodies for NEW notes, bounded by --max-notes and --max-bytes")
	f.Int("max-notes", defaultBodiesNotes, "with --bodies, maximum NEW items to print")
	f.Int64("max-bytes", defaultBodiesBytes, "with --bodies, maximum body bytes to print")
	f.String("after", "", "continue a bounded --bodies snapshot")
	f.Bool("advance", false, "move your cursor to HEAD and push it when this wait returns, the way inbox --advance does")
	f.Int("attempts", defaultAttempts, "how many times to push the cursor before giving up")
	f.Int("git-timeout", defaultGitTimeoutSeconds, "how long one git subprocess may take before this run gives up on it")
	f.Bool("no-push", false, "with --advance, commit the cursor but do not push it")
	f.String("legacy-before", "", "notes dated before this UTC date (YYYY-MM-DD, midnight at its start) or UTC instant (RFC 3339) are not carried on your open list, and are counted rather than listed")
	f.Bool("carry-history", false, "on your FIRST --advance, carry every old note on your open list instead of drawing a switch-day line; does nothing otherwise")
	f.Bool("diagnostics", false, "name every unreadable file with its reason, even ones already shown; the default collapses unchanged ones to one count line")
	f.Bool("quiet-beats", false, "accepted for callers that pass it and changes nothing: a change that is only beats and cursors never wakes a wait, with or without this flag; it is not news")
	f.Bool("on-note", false, "wait only for a note addressed to the caller; requires --timeout, --bus, --as, --remote and --branch; incompatible with --open and --full")
	f.Int("max-commits", defaultMaxCommits, "how many commits a since-walk may cross before it stops and names the remedy; raise it to read a staler cursor")
	f.Check(receiptWords)
}

func checkFlags(f *tool.Flags) {
	f.Prints()
	f.Required("bus", "the bus's repository root")
	f.Bool("full", false, "walk the whole bus: what CI on main and a first adoption run want")
	f.String("as", "", "check what changed since this participant's cursor")
	f.String("since", "", "check what changed since this commit: a revision, a UTC date (YYYY-MM-DD, so the last commit written before that day), or an RFC 3339 UTC instant")
	f.String("legacy-before", "", "a finding about the header of a note dated before this UTC date (YYYY-MM-DD, midnight at its start) or UTC instant (RFC 3339) warns instead of failing")
	f.Bool("rebuild-index", false, "with --full, rewrite each lane's INDEX from the notes on disk")
	f.Int("max", defaultCheckMax, "findings to print before one BUS MORE line naming the rest (default 20, 0 = all)")
	f.Int("fail-max", defaultCheckMax, "alias of --max, accepted for one release")
	f.Int("git-timeout", defaultGitTimeoutSeconds, "how long one git subprocess may take before this run gives up on it")
}

func namesFlags(f *tool.Flags) {
	f.Prints()
	f.Required("bus", "the bus's repository root")
}

func init() {
	if !strings.Contains(usage, "\nusage:\n") {
		panic("nova-bus synopsis has no usage block")
	}
}
