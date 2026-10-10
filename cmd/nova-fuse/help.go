package main

import (
	"fmt"
	"io"
	"strings"

	"github.com/mas-bandwidth/nova-tools/pkg/oneline"
)

// fuseHelp is one verb's help: what `nova-fuse help <verb>` prints. A verb's
// -h is refused rather than answered (exit 0 is this tool's CLEAR), so this
// table is the only route to it. It never runs a verb or reads a box.
type fuseHelp struct {
	usage  string
	effect string // "inspection: ..." or "local write: ...", the line's prefix in the skeleton's grammar
	detail string
	exits  string
	flags  bool // takes --box and the positional rules
	dryRun bool // takes --dry-run
}

const (
	inspection = "inspection: reads, writes nothing"
	localWrite = "local write: writes the box file named by --box"
)

var fuseHelps = map[string]fuseHelp{
	"init": {
		"init --box <path> [--dry-run]",
		localWrite,
		"Create an empty box only where nothing exists, then verify it by rereading. An existing box is never replaced.",
		"0 created and verified; 1 already exists, write failed, or verification failed; 2 bad invocation.",
		true, true,
	},
	"status": {
		"status --box <path> [--max <n>]",
		inspection,
		"Report lockdown and quarantines. A readable blown box still exits 0; status is not a permission gate.",
		"0 reported a readable box; 2 bad invocation, absent box, or unreadable box.",
		true, false,
	},
	"check": {
		"check --box <path> [--] [surface]",
		inspection + "; the permission gate: act only on exit 0",
		"Without a surface, checks lockdown only; no quarantine is checked.",
		"0 verified clear for what was checked; 1 lockdown or the named surface's quarantine is blown; 2 cannot prove clear (bad invocation, absent box, or unreadable box).",
		true, false,
	},
	"lockdown": {
		"lockdown --box <path> [--dry-run] [--] <reason>",
		localWrite,
		"Blow the hard fuse and verify it. Every untrusted read and surface-driven act stops; authored outbound life continues. An absent or broken box is replaced with a blown box; unreadable bytes are backed up when possible.",
		"0 blown and verified; 1 write or verification failed; 2 bad invocation. Every failure remains no permission to read.",
		true, true,
	},
	"quarantine": {
		"quarantine --box <path> [--dry-run] [--] <surface> <reason>",
		localWrite,
		"Stop reading one surface and verify it. Surface spellings match after normalization; the reason is recorded. Requires a readable existing box.",
		"0 quarantined and verified; 1 write or verification failed; 2 bad invocation, absent box, or unreadable box.",
		true, true,
	},
	"lift quarantine": {
		"lift quarantine --box <path> [--dry-run] [--] <surface>",
		localWrite,
		"Rescind your quarantine under every equivalent spelling, announce it, and verify it. Any lockdown still blocks reads.",
		"0 lifted and verified; 1 no quarantine to lift, write failed, or verification failed; 2 bad invocation, absent box, or unreadable box.",
		true, true,
	},
	"lift lockdown": {
		"lift lockdown",
		"inspection: refused by design before flags or files are read; writes nothing",
		"Replacement is a deliberate hand-edit of the box agreed in a live conversation with the person you work with, preserving the quarantines that still stand; this tool supplies no reset or override.",
		"2 always refused; no arguments or flags can change this.",
		false, false,
	},
	"path": {
		"path --box <path>",
		"inspection: writes nothing and does not read the box",
		"Echo the supplied path, without reading or writing the box. Its exit is not permission to read a surface.",
		"0 path printed; 2 bad invocation.",
		true, false,
	},
	"version": {
		"version",
		inspection,
		"Print this build identity; reads no box and changes nothing. Takes no flags or arguments.",
		"0 build identity printed; 2 unexpected flags or arguments.",
		false, false,
	},
}

func cmdHelp(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stdout, usage)
		return 0
	}
	name := strings.Join(args, " ")
	if name == "lift" {
		fmt.Fprintln(stdout, "usage: nova-fuse lift <quarantine|lockdown> [flags]\n  nova-fuse lift quarantine --box <path> [--dry-run] [--] <surface>\n  nova-fuse lift lockdown\n"+
			"Choose help lift quarantine or help lift lockdown; their effects differ.\n"+
			"exit codes: 0 quarantine lifted and verified; 1 no quarantine to lift, write failed, or verification failed; 2 bad invocation or lockdown lift refused.")
		return 0
	}
	h, ok := fuseHelps[name]
	if !ok {
		return refuse(stderr, " help", fmt.Sprintf("unknown verb %q; the verbs are init, status, check, lockdown, quarantine, lift quarantine, lift lockdown, path, version", name))
	}
	fmt.Fprintf(stdout, "usage: nova-fuse %s\n\n%s\n\n", oneline.Escape(h.usage), oneline.Escape(h.detail))
	if h.flags {
		fmt.Fprintln(stdout, "flags:\n  --box <path>  required JSON box path; no default or environment variable")
		if name == "status" {
			fmt.Fprintln(stdout, "  --max <n>    quarantine line limit (default 20); 0 lists all; totals stay uncapped")
		}
		if h.dryRun {
			fmt.Fprintln(stdout, "  --dry-run    make every check the write would, print what it would do, write nothing")
		}
		fmt.Fprintln(stdout, "  --           end flags; following words are literal surfaces or reasons\nFlags precede positional arguments; each flag is given once.")
	}
	fmt.Fprintf(stdout, "\nexit codes: %s\neffect: %s\nHelp: nova-fuse help %s; -h after a verb is refused at exit 2.\n",
		oneline.Escape(h.exits), oneline.Escape(h.effect), oneline.Escape(name))
	return 0
}
