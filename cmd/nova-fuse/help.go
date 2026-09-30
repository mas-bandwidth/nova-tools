package main

import (
	"fmt"
	"io"
)

const initHelp = `nova-fuse init: create an empty box where none is

usage:
  nova-fuse init --box <path>

Create an empty box where none is; never replaces one.
This is the one way a box comes into being clear. A box already at the path,
blown or not, readable or not, is left as it is and the run exits 1, because
replacing a box is the lockdown reset this tool does not have.

flags:
  --box <path>   path to the fuse box JSON file (required; no default)

exit codes:
  0   empty box made and verified by re-reading
  1   something is already there, or write/verify failure
  2   could not run (missing flag, bad invocation)
`

const statusHelp = `nova-fuse status: what is blown, and since when

usage:
  nova-fuse status --box <path> [--max <n>]

Reports the current fuse state: lockdown status, quarantine count, and
quarantined surfaces. Never gate on the exit code of status; check is the gate.
Lists at most --max quarantines (default 20, and 0 means all) after its count
line, then one STATUS MORE kind=quarantine shown=<n> total=<t> line standing
for the rest. The count on the first line is never capped.

flags:
  --box <path>   path to the fuse box JSON file (required; no default)
  --max <n>      quarantine lines to list before one MORE line stands for the rest; 0 lists all (default 20)

exit codes:
  0   box read and status reported (blown or not)
  2   could not run (missing flag, unreadable or missing box)
`

const checkHelp = `nova-fuse check: may I read? -- the gate

usage:
  nova-fuse check --box <path> [surface]

This is the gate every ingestion path calls. Act only on exit 0.
Checks whether lockdown is blown, and if a surface is given, whether that
surface is quarantined. A check without a surface checks lockdown only.
Fails closed: an unreadable or missing box treats every fuse as blown (exit 2).

flags:
  --box <path>   path to the fuse box JSON file (required; no default)

exit codes:
  0   clear (permission to read)
  1   blown (lockdown is blown or the surface is quarantined)
  2   could not run (cannot prove clear; missing flag, unreadable or missing box)
`

const lockdownHelp = `nova-fuse lockdown: blow the one hard fuse

usage:
  nova-fuse lockdown --box <path> "<reason>"

Blow the one hard fuse: all untrusted reads and surface-driven acts stop,
while authored outbound life continues. Solo, instant, and needs no proof.
A blown lockdown is not reset -- it is REPLACED, and only in a live conversation
with your person. Works even when the box is unreadable or absent.

flags:
  --box <path>   path to the fuse box JSON file (required; no default)

exit codes:
  0   lockdown blown and verified by re-reading the box
  1   write or verification failure
  2   could not run (missing flag, missing reason)
`

const quarantineHelp = `nova-fuse quarantine: stop reading one surface (soft dial)

usage:
  nova-fuse quarantine --box <path> <surface> "<reason>" [--dry-run]

Records a quarantine for one surface (stored normalized), verified by
re-reading the box. Soft: yours to lift when the surface is safe again.
Refuses on an unreadable or absent box: an unreadable box already blocks
every surface, and writing a fresh box holding only this quarantine would
unblock the rest.

flags:
  --box <path>   path to the fuse box JSON file (required; no default)
  --dry-run      validate inputs and print the rule that would be added and the box path, write nothing

exit codes:
  0   quarantine recorded and verified (or plan printed with --dry-run)
  1   write or verification failure
  2   could not run (missing flag, missing input, unreadable or missing box)
`

const liftHelp = `nova-fuse lift: rescind a fuse

usage:
  nova-fuse lift quarantine --box <path> <surface>
  nova-fuse lift lockdown

Rescinds a fuse.
Quarantine is soft: your own dial in both directions, so rescinding one
succeeds -- out loud, verified, never silent.
Lockdown is hard: lift lockdown is REFUSED forever by design -- a blown fuse is
not reset, it is REPLACED, and only in a live conversation with your person.

flags:
  --box <path>   path to the fuse box JSON file (required for lift quarantine)

exit codes:
  0   quarantine lifted and verified
  1   nothing to lift, or write/verify failure
  2   could not run, missing argument, or refused by design (lift lockdown)
`

const liftQuarantineHelp = `nova-fuse lift quarantine: rescind your own quarantine (soft dial)

usage:
  nova-fuse lift quarantine --box <path> <surface>

Rescinds one quarantine: the soft dial, turned the other way. The write is
verified by re-reading the box, and the rescind is announced out loud.

flags:
  --box <path>   path to the fuse box JSON file (required; no default)

exit codes:
  0   quarantine lifted and verified
  1   nothing to lift, or write/verify failure
  2   could not run (missing flag, unreadable box, missing surface)
`

const liftLockdownHelp = `nova-fuse lift lockdown: REFUSED by design

usage:
  nova-fuse lift lockdown

REFUSED, forever, by design. A blown lockdown is not reset, it is REPLACED,
and only in a live conversation with your person. Nothing this tool is told
changes that. Always exits 2.

exit codes:
  2   refused by design
`

const pathHelp = `nova-fuse path: echo the box path

usage:
  nova-fuse path --box <path>

Echo the box path this invocation would use. With no default paths anywhere,
exists to verify plumbing: what one caller passes is what another sees.

flags:
  --box <path>   path to the fuse box JSON file (required; no default)

exit codes:
  0   box path echoed
  2   could not run (missing flag)
`

const versionHelp = `nova-fuse version: print this build identity

usage:
  nova-fuse version

Print this build identity (--version also accepted). Takes no flags and no arguments.

exit codes:
  0   version printed
  2   unexpected flags or arguments
`

const helpHelp = `nova-fuse help: print usage and help

usage:
  nova-fuse help [<verb>]

Print the general help banner, or usage and synopsis for a specific verb.

exit codes:
  0   help printed
  2   unknown verb
`

func cmdHelp(rest []string, stdout, stderr io.Writer) int {
	if len(rest) == 0 {
		fmt.Fprint(stdout, usage)
		return 0
	}
	verb := rest[0]
	switch verb {
	case "init":
		if len(rest) > 1 {
			return refuse(stderr, " help init", fmt.Sprintf("unexpected argument %q", rest[1]))
		}
		fmt.Fprint(stdout, initHelp)
		return 0
	case "status":
		if len(rest) > 1 {
			return refuse(stderr, " help status", fmt.Sprintf("unexpected argument %q", rest[1]))
		}
		fmt.Fprint(stdout, statusHelp)
		return 0
	case "check":
		if len(rest) > 1 {
			return refuse(stderr, " help check", fmt.Sprintf("unexpected argument %q", rest[1]))
		}
		fmt.Fprint(stdout, checkHelp)
		return 0
	case "lockdown":
		if len(rest) > 1 {
			return refuse(stderr, " help lockdown", fmt.Sprintf("unexpected argument %q", rest[1]))
		}
		fmt.Fprint(stdout, lockdownHelp)
		return 0
	case "quarantine":
		if len(rest) > 1 {
			return refuse(stderr, " help quarantine", fmt.Sprintf("unexpected argument %q", rest[1]))
		}
		fmt.Fprint(stdout, quarantineHelp)
		return 0
	case "lift":
		if len(rest) > 1 {
			switch rest[1] {
			case "quarantine":
				if len(rest) > 2 {
					return refuse(stderr, " help lift quarantine", fmt.Sprintf("unexpected argument %q", rest[2]))
				}
				fmt.Fprint(stdout, liftQuarantineHelp)
				return 0
			case "lockdown":
				if len(rest) > 2 {
					return refuse(stderr, " help lift lockdown", fmt.Sprintf("unexpected argument %q", rest[2]))
				}
				fmt.Fprint(stdout, liftLockdownHelp)
				return 0
			default:
				return refuse(stderr, " help lift", fmt.Sprintf("unknown power %q; lift takes 'quarantine' or 'lockdown'", rest[1]))
			}
		}
		fmt.Fprint(stdout, liftHelp)
		return 0
	case "path":
		if len(rest) > 1 {
			return refuse(stderr, " help path", fmt.Sprintf("unexpected argument %q", rest[1]))
		}
		fmt.Fprint(stdout, pathHelp)
		return 0
	case "version":
		if len(rest) > 1 {
			return refuse(stderr, " help version", fmt.Sprintf("unexpected argument %q", rest[1]))
		}
		fmt.Fprint(stdout, versionHelp)
		return 0
	case "help":
		if len(rest) > 1 {
			return refuse(stderr, " help help", fmt.Sprintf("unexpected argument %q", rest[1]))
		}
		fmt.Fprint(stdout, helpHelp)
		return 0
	case "-h", "--help":
		fmt.Fprint(stdout, usage)
		return 0
	default:
		return refuse(stderr, " help", fmt.Sprintf("unknown verb %q", verb))
	}
}
