package main

import (
	"fmt"
	"io"
	"strings"

	"github.com/mas-bandwidth/nova-tools/pkg/nsprint/verbflag"
)

// exitParagraph is the banner's exit codes, every verb's at once: `nova-swarm help` prints
// it whole, and a verb's -h prints that verb's own line in its place (verbExits; docs/
// STANDARD.md section 2, "Every verb's -h quotes the table, or the verb's own"; tool
// ledger X9). It is the usage const's own text, and a test holds the two to one string.
const exitParagraph = `exit codes: 0 the verb ran and passed; 1 the verb ran and said NO -- a verification that
failed, a lint that found a defect; 2 could not run: a missing flag, an unreadable worker
description, a key file that is absent or empty, a bad invocation; by verb:
  member: 3 its binary was replaced on disk (MEMBER STOP: its supervisor starts the new
    one; with children running it first takes no new card and stops when the last is
    reported)`

// verbExit is each verb's own exit codes.
var verbExit = map[string]string{
	"step": `exit codes: 0 every step run is ok, each on its STEP OK line (--remainder: the card printed);
  1 a step failed, on its STEP FAILED line, and the steps after it were not run; 2 could not
  run: a missing flag, a card that cannot be read, whose tree has a finding or that is not
  script steps only, no wall and no --no-wall`,
	"lint": `exit codes: 0 the card is clean (a NOTE line is advice and changes nothing); 1 a drift,
  each on its LINT DRIFT line; 2 could not run: a missing flag, a file that cannot be read, a
  bad invocation`,
	"verify": `exit codes: 0 the result holds its contract; 1 it does not (the line says why); 2 could
  not run: a missing flag, a file that cannot be read, a receipt that cannot be written`,
	"worker": `exit codes: 0 WORKER OK; 1 the description was read and drifts, each on its WORKER
  DRIFT line; 2 it cannot be read, or a bad invocation`,
	"worker check": `exit codes: 0 WORKER OK; 1 the description was read and drifts, each on its WORKER
  DRIFT line; 2 it cannot be read, or a bad invocation`,
	"member": `exit codes: 0 it stopped as asked (--once, --ticks); 2 could not run: a missing flag, a
  directory that cannot be made; 3 its binary was replaced on disk (MEMBER STOP: its supervisor
  starts the new one; with children running it first takes no new card and stops when the last
  is reported)`,
	"native": `exit codes: 0 the child exited 0 (the NATIVE line's OK, or INCOMPLETE and its why=, is
  the verdict); 1 the child was killed (its deadline, a TERM) or exited 255; any other code is
  the child's own; 2 could not run: a missing flag, a wall, a card or a worker description that
  is not there`,
	"slots take": `exit codes: 0 the leases are granted; 2 refused: the owner's share or the bench is
  full (SLOTS REFUSED names the holders), a missing flag, or a store that cannot be read`,
	"doctor": `exit codes: 0 the binaries agree, or there is one to read; 2 they drift, one shadows
  the other, or one cannot be read (the DOCTOR line says which)`,
	"disk-guard": `exit codes: 0 DISK-GUARD OK, everything it looked at done (a KEPT line is a refusal
  it means); 1 DISK-GUARD INCOMPLETE, something could not be read or removed (each on its NOTE
  line); 3 DISK-GUARD STOP, free disk under --stop-floor (stop the loops); 2 could not
  run: a bad flag`,
	"mirror": `exit codes: 0 every repository refreshed (MIRROR OK each); 1 a repository failed (MIRROR FAILED
  names it and the cause; the others are still refreshed); 2 could not run: a missing flag or a bad name`,
	"slots release": `exit codes: 0 the leases named are freed; 2 a lease's holder still runs (SLOTS
  KEPT; --force frees it), a missing flag or a store that cannot be read`,
}

// verbDetail is what `nova-swarm help` cut from a long usage parenthesis and moved into
// the verb's -h: the sentences the banner no longer carries, printed above the verb's
// flags so a cold reader still finds them (docs/STANDARD.md section 3, ONBOARDING point 6).
// The banner keeps what member, lint and disk-guard are, and names the verb's -h for the rest.
var verbDetail = map[string]string{
	"version":    "prints this build's identity: the version, the commit it was built from and the time it was stamped.",
	"mirror":     "keeps the bench's bare mirrors fresh: each --repos name is cloned from <base>/<name>.git into <dir>/<name>.git when absent, then every head and every pull-request head is fetched into it, so a card's git clone --reference finds the repository. It never prunes objects (the disk guard sweeps a mirror's temporary packs) and one repository's failure never stops the others; --every keeps refreshing until stopped.",
	"profile":    "reads the timeline.tsv of every job the glob names and prints one PROFILE line per job and one mean summary, so a slow card shows where its time went.",
	"slots init": "creates the slot store for an owner: the machine's capacity and the owner's share of it, which slots take then leases from.",
	"slots list": "prints the store's slots: each owner's capacity and share, and every lease held, with its label and when it expires.",
	"member": `run this machine as a sprint member; --server is the address of nova-sprint run --listen.
Each tick beats, reads the queue, reports ended children and takes cards to the fleet row's width.
A reader uses its machine's width; --width overrides it. This machine opens no store.
Each card runs as one native child with its frame and an allowlist environment.
Its packet supplies the model, budget and deadline; the flags fill missing route values.
--reader runs reads from the readers table; its flags override the read's route.
A flash card's first read asks a decide read using JEV_API_KEY in the reader environment.
The key stays with the reader process (docs/SPEC-SPRINT.md section 6).
A work member with JEV_API_KEY asks an attempt decision when a take ends; the finish carries it.
Native classifies a not-done child's red gate with the same key, with one bounded base test run.
Decisions are recorded and routed on the sprint row's gate bars; flaky failures rerun once
and pre-existing failures never count against the card (docs/SPEC-SPRINT.md sections 2 and 5).
The member pushes the child's commit and opens its requested PR outside the wall, never force-pushing.
Each finish is judged ok, failed or reaped (docs/SPEC-CARD-CONTRACT.md).
--pass names environment secrets to hand to children; a harness that needs one must receive it.
--identity names the pool's commit identity; otherwise the pool's identity.tsv supplies it.
Completed launches leave no checkout; each pool keeps its newest five failed launches.
No card starts below --disk-floor GiB free (default 10); --stage-wall bounds staging (default 120s).
--max-load refuses a local child above the configured one-minute host load; --warn-load warns at its threshold through that bound.
The shared Go build cache is held under --gocache-limit GiB (default 20), never an entry used in the last two hours.
A staging refusal reports why so the sprint can deal the card to another member.`,
	"lint": `a bare --card holds the card to nova-swarm's own card contract, the shape native runs, the same for every adopter: the RESULT line first and written last, numbered STEPs entering the repository, a test and its command, a deadline, the files named, scratch under a named root; --rules lists every check; an adopter's own rules go in --child-rules-file
--fleet lints a launcher script against the coordinator's /bin/bash 3.2: shebang, bash-4 builtins, unquoted expansions
--child-rules holds the card to the rules the coordinator gives a child: one rule-<name> per required sentence, one step-<what> per forbidden command; the sentences are the built-in general rules, or the lines of --child-rules-file, one required sentence per line; template --name card prints a card that passes the general ones
--member-injects lints the card as the member stages it, rules by reference: the rules are appended at stage time from the held file of the card's REPO: (fleet/child-rules.txt for nova-tools, fleet/child-rules.<repo>.txt for another), or --child-rules-file; a card need not carry them, and a line that contradicts them is still a finding
--decide asks the brief decision nova-sprint add asks (nova-decide's brief: p(converges), the minutes, the questions the card leaves open) through Jev with JEV_API_KEY, or from --decide-answers, and prints one LINT DECIDE line after the lint's own; it never changes the verdict, and a failing backend prints the verdict, then why, exit 2
--base-check adds the four checks of a coding card: its PATHS exist at the base sha in --repo (default the working directory), no STEP pushes or calls gh, its LEG is a line of --legs, its deadline is at least --p95's figure for its kind; evidence not given is reported missing, never passed
nova-sprint add holds a brief to the --child-rules tokens only, and to its model lines: rule-<name> for each rule of its set (the six general rules, or the file add --rules or init --rules names), the step-<what> scans (step-go-clean and step-go-test-timeout only when the file carries those rules), and rule-libraries-considered when the file carries [libraries-considered]; every other token --rules lists is this lint's alone`,
	"disk-guard": `one pass over this machine, run every few minutes by the disk-guard loop row fleet/loops.yml adds to every machine: every Go build cache (the login's, each root's cache/go-build, each --cache) held under --cache-max-gb, default 20 (a quarter of it while the tightest volume has under twice --disk-floor free, said on a PRESSURE line), by the member's trim, oldest entries first and never one used in the last two hours; a module cache over --modcache-max-gb, default 50, emptied while no go command runs; every loop log over --log-max-mb, default 50, copied to <log>.1 and emptied in place, --log-keep copies, default 3; the pool of a loop that stopped (no process names its root, nothing moved for --pool-idle, default 30m) swept as the member sweeps its own, a work launch whose checkout holds commits past its staged one kept; land clones unused for --clone-age, default 24h, removed; a mirror's temporary packs older than an hour removed while nothing fetches into it, never git prune; never anything with uncommitted work or a live process; one REMOVED, TRIMMED, CLEANED, ROTATED or KEPT line per action with freed=<bytes>, a DISK-GUARD WARN line under --disk-floor, default 10, and DISK-GUARD OK freed=<bytes> free=<bytes> at the end; --dry-run judges the same and removes nothing, each action said WOULD-REMOVE, WOULD-TRIM, WOULD-CLEAN or WOULD-ROTATE`,
}

// verbEffect is what running a verb does beyond printing, the last line of its -h, in the
// skeleton's words (pkg/tool Effect: inspection, local write or delivery; docs/
// STANDARD.md section 2, "Its effects are explicit").
var verbEffect = map[string]string{
	"version":      "inspection: reads, writes nothing",
	"doctor":       "inspection: reads, writes nothing",
	"lint":         "inspection: reads, writes nothing",
	"template":     "inspection: prints a template, writes nothing",
	"worker":       "inspection: reads, writes nothing",
	"worker check": "inspection: reads, writes nothing",
	"slots list":   "inspection: reads, writes nothing",
	"profile":      "inspection: reads the timeline.tsv of each job the glob names, writes nothing",
	"native":       "delivery: runs the card's harness, which calls the model's provider, and writes the job directory under --root",
	"member":       "delivery: joins a sprint's fleet through --server, runs its cards as native children, pushes their commits and opens their pull requests",
}

// verbExample is one worked invocation per verb, printed on the verb's -h as an `example:`
// line, for a verb whose help text carries none of its own.
var verbExample = map[string]string{
	"version":       "nova-swarm version",
	"doctor":        "nova-swarm doctor",
	"lint":          "nova-swarm lint --card card.md --child-rules",
	"profile":       "nova-swarm profile --jobs 'jobs/*'",
	"slots":         "nova-swarm slots list --store /srv/slots",
	"slots init":    "nova-swarm slots init --store /srv/slots --owner ada --capacity 8 --share 4",
	"slots list":    "nova-swarm slots list --store /srv/slots",
	"slots release": "nova-swarm slots release --store /srv/slots --owner ada --label card1",
	"slots take":    "nova-swarm slots take --store /srv/slots --owner ada --n 1 --for 30m --label card1",
	"template":      "nova-swarm template --name card",
	"verify":        "nova-swarm verify --result RESULT.md --contract 'RESULT: done' --label card1",
	"worker check":  "nova-swarm worker check worker.json",
}

// commonExit is the codes of every other verb.
const commonExit = "exit codes: 0 done; 2 could not run: a missing flag, a file that cannot be read, a bad invocation"

// verbExits is the exit-code paragraph a verb's -h prints.
func verbExits(name string) string {
	if e, ok := verbExit[name]; ok {
		return e
	}
	return commonExit
}

// recoverHelp is verbflag.RecoverWith with the verb's own exit codes in place of the
// banner's paragraph: a verb's -h (or help <verb>) prints its usage quoted from the
// banner, its example, its flags and its exit codes on stdout, exit 0. The verb's exit
// lines are handed to Print whole, so the by-verb indentation survives. It is deferred
// directly, as RecoverWith is.
func recoverHelp(out io.Writer, code *int) {
	r := recover()
	if r == nil {
		return
	}
	h, ok := r.(verbflag.Help)
	if !ok {
		panic(r)
	}
	name := verbflag.Verb("nova-swarm", h.FS)
	var b strings.Builder
	verbflag.Print(&b, "nova-swarm", usage, h.FS, strings.Split(verbExits(name), "\n")...)
	*code = 0
	lines := verbHelpLines(name)
	if e, ok := verbExample[name]; ok && !strings.Contains("\n"+lines+b.String(), "\nexample:") {
		lines += "example: " + e + "\n"
	}
	help := verbflag.Insert(b.String(), lines)
	if e, ok := verbEffect[name]; ok {
		help += "effect: " + e + "\n"
	}
	if _, err := fmt.Fprint(out, help); err != nil {
		// the help did not reach its reader (a closed stdout): the exit code says so
		*code = 1
	}
}
