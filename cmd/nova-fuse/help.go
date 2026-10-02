package main

import (
	"fmt"
	"io"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// Verb help describes the existing effects and exit meanings of docs/SPEC.md's
// ingestion fuse. It never runs a verb or reads a box.
type fuseHelp struct {
	usage, effect, exits string
}

var fuseHelps = map[string]fuseHelp{
	"init": {
		"init --box <path>",
		"Local write: create an empty box only where nothing exists, then verify it by rereading. An existing box is never replaced.",
		"0 created and verified; 1 already exists, write failed, or verification failed; 2 bad invocation.",
	},
	"status": {
		"status --box <path> [--max <n>]",
		"Inspection: report lockdown and quarantines. A readable blown box still exits 0; status is not a permission gate.",
		"0 reported a readable box; 2 bad invocation, absent box, or unreadable box.",
	},
	"check": {
		"check --box <path> [--] [surface]",
		"Inspection and permission gate: act only on exit 0. Without a surface, checks lockdown only; no quarantine is checked.",
		"0 verified clear for what was checked; 1 lockdown or the named surface's quarantine is blown; 2 cannot prove clear (bad invocation, absent box, or unreadable box).",
	},
	"lockdown": {
		"lockdown --box <path> [--] <reason>",
		"Local write: blow the hard fuse and verify it. Every untrusted read and surface-driven act stops; authored outbound life continues. An absent or broken box is replaced with a blown box; unreadable bytes are backed up when possible.",
		"0 blown and verified; 1 write or verification failed; 2 bad invocation. Every failure remains no permission to read.",
	},
	"quarantine": {
		"quarantine --box <path> [--] <surface> <reason>",
		"Local write: stop reading one surface and verify it. Surface spellings match after normalization; the reason is recorded. Requires a readable existing box.",
		"0 quarantined and verified; 1 write or verification failed; 2 bad invocation, absent box, or unreadable box.",
	},
	"lift quarantine": {
		"lift quarantine --box <path> [--] <surface>",
		"Local write: rescind your quarantine under every equivalent spelling, announce it, and verify it. Any lockdown still blocks reads.",
		"0 lifted and verified; 1 no quarantine to lift, write failed, or verification failed; 2 bad invocation, absent box, or unreadable box.",
	},
	"lift lockdown": {
		"lift lockdown",
		"Refused by design before flags or files are read. Replacement is a deliberate hand-edit of the box agreed in a live conversation with your person, preserving the quarantines that still stand; this tool supplies no reset or override.",
		"2 always refused; no arguments or flags can change this.",
	},
	"path": {
		"path --box <path>",
		"Inspection of the invocation: echo the supplied path, without reading or writing the box. Its exit is not permission to read a surface.",
		"0 path printed; 2 bad invocation.",
	},
	"version": {
		"version",
		"Inspection: print this build identity; reads no box and changes nothing. Takes no flags or arguments.",
		"0 build identity printed; 2 unexpected flags or arguments.",
	},
}

func cmdHelp(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stdout, usage)
		return 0
	}
	name := strings.Join(args, " ")
	if name == "lift" {
		fmt.Fprintln(stdout, "usage: nova-fuse lift quarantine --box <path> <surface> | lift lockdown\nChoose help lift quarantine or help lift lockdown; their effects differ.\nexit codes: 0 quarantine lifted and verified; 1 no quarantine to lift, write failed, or verification failed; 2 bad invocation or lockdown lift refused.")
		return 0
	}
	h, ok := fuseHelps[name]
	if !ok {
		return refuse(stderr, " help", fmt.Sprintf("unknown verb %q; verbs: init, status, check, lockdown, quarantine, lift quarantine, lift lockdown, path, version", name))
	}
	fmt.Fprintf(stdout, "usage: nova-fuse %s\n\n%s\n\n", oneline.Escape(h.usage), oneline.Escape(h.effect))
	if name != "version" && name != "lift lockdown" {
		fmt.Fprintln(stdout, "flags:\n  --box <path>  required JSON box path; no default or environment variable\n  --            end flags; following words are literal surfaces or reasons\nFlags precede positional arguments; each flag is given once.")
	}
	if name == "status" {
		fmt.Fprintln(stdout, "  --max <n>    quarantine line limit (default 20); 0 lists all; totals stay uncapped")
	}
	fmt.Fprintf(stdout, "\nexit codes: %s\nHelp: nova-fuse help %s; -h after a verb is refused at exit 2.\n", oneline.Escape(h.exits), oneline.Escape(name))
	return 0
}
