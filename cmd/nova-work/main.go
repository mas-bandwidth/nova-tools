// nova-work is both the thin client of the resident work session (docs/SPEC-WORK.md,
// "The engine and its client") and the kernel's side of the work description language
// (docs/SPEC-WORKLANG.md) and the job graph of docs/SPEC-JOBS.md.
//
// The client sends one request over the Unix socket --session names and prints the
// session's answer byte for byte: OK, ROW, NOTE and MORE to stdout and exit 0, FAIL,
// RACED and REFUSED to stderr and exit 1. The wire is the spec's versioned,
// length-prefixed JSON protocol when the session answers in frames -- the only wire a
// listing verb's many ROW/NOTE/MORE lines can come home on (nova-tools#1696) -- and the
// S1 one-line wire when it does not. What cannot run at all -- no verb, no --session, no
// socket that answers -- is one WORK REFUSED line on stderr, exit 2, ending "run:
// nova-work help". The values travel as the caller spelled them and the session
// validates every one.
//
// The bounded reader: `plan check` reads one plan under --max-bytes, --max-depth and
// --max-nodes, refuses a `#.` dispatch macro at the byte offset that owes it, refuses any
// input past a bound whole rather than truncated, and refuses an unknown `:kind` by field
// name. It then closes the plan's needs/blocks graph at load: `:needs` is the
// reference edge and `:blocks` its inverse, an absent need is refused naming the field
// and the id, and a `:needs` cycle is refused by validator rule 3 before publication. A
// well-formed plan prints one line; a refusal is exit 2 with one remedy line.
//
// The job graph: typed needs and blocks edges, refused acyclic at seed by validator rule
// 3, and the mechanical ready set launch reads. A node is ready only when every need is
// terminal accepted. Every row that cannot proceed prints its exact blocker and its
// resolver; a card whose need is an open PR is never on a slot. This slice owns the
// in-process graph and the ready reading only: no Redis, no network, no launch, no lease.
//
// Every path comes from a flag. There is no default file and no discovery: a missing
// flag is a refusal, never a guess. Output is one line per verb. Exit 0 ran and passed;
// exit 2 could not run -- a missing flag, an unreadable graph or plan, a :deps cycle, an
// unknown node, a refusal.
//
// nova-work is also the work layer's event bridge. This binary's shipped verb, events,
// turns the cards:done stream and the gh fallback poll into the pub/sub messages the
// merge layer reacts to (docs/SPEC-JOBS.md, "Events, not ticks"). It makes no model call
// and writes no record: every message is a signal, and git stays the record. It announces
// a card's END only: every other transition on cards:done (queued, a turn, a decide
// event, ...) is acked and not re-announced, because the fold reads the stream itself.
//
// The card-result record is the fold of cards:done (internal/events, `nova-pulse fold`).
// The record and results verbs that wrote it into the card_results table are retired
// with that table (nova-tools #2623).
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/buildinfo"
	"github.com/mas-bandwidth/nova-tools/internal/ci"
	"github.com/mas-bandwidth/nova-tools/internal/jobs"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/workclient"
	"github.com/mas-bandwidth/nova-tools/internal/worklang"
)

// version is empty in ordinary builds and is filled only by a release stamp.
var version string

// usage is what help prints and what docs/CLI.md's nova-work section carries byte for byte.
// The session verb lines are the spec's own verbs block (docs/SPEC-WORK.md) and the graph
// and plan verb lines are docs/SPEC-JOBS.md and docs/SPEC-WORKLANG.md.
const usage = `nova-work: the thin client, the job graph and the bounded .work reader (see docs/SPEC-WORK.md, docs/SPEC-JOBS.md, docs/SPEC-WORKLANG.md)

usage:
  nova-work session start  --session <path> --as <name> --file <path-in-repo> --journal <path> --cache <path> --repo <path> --remote <name> --branch <name>
                           --max-bytes <n> --max-depth <n> --max-nodes <n> --every <duration> --skew <duration> --clip-every <duration> --clip-after <n> --retain <duration>
                           --savepoint-every <duration> --savepoint-after <n> --max-frame-bytes <n> --silence-ping <duration>
                           --index-cache <n> --page-bytes <n> --page-records <n> [--closed-window <duration>] [--render-root <root-id>=<owner/name>:<directory> ...]
                           [--resolver <scheme>=<command> ...] --git-timeout <seconds> [--attempts <n>] [--repair] [--foreground] [--max <n>] [--now <stamp>]
  nova-work session export (--session <path> | --journal <path> --max-bytes <n> --max-depth <n> --max-nodes <n>) --into <path>
  nova-work session export (--session <path> | --snapshot <path> --cache <path>) --state --at <revision> --closed-history <none|all|range> [--from <stamp> --to <stamp>] --into <new-directory> --max-bytes <n> --max-depth <n> --max-nodes <n> --max-output-bytes <n>   (a long operation under --session; one finite process under --snapshot)
  nova-work session replay --session <path> --from <path> --as <name> [--max <n>]
  nova-work session status --session <path>
  nova-work session stop   --session <path> --git-timeout <seconds> [--attempts <n>] [--no-clip]
  nova-work session handoff --session <path> --to <name> --git-timeout <seconds> [--attempts <n>]
  nova-work operation status  --session <path> --id <id>
  nova-work operation list    --session <path> [--max <n>]
  nova-work operation wait    --session <path> --id <id> --timeout <duration> [--after <cursor>]
  nova-work operation cancel  --session <path> <write flags> --id <id> --reason <text>
  nova-work savepoint list     --session <path> [--max <n>]
  nova-work savepoint create   --session <path> --as <name> --reason <text>
  nova-work savepoint verify   --session <path> --id <id>
  nova-work savepoint compare  --savepoint <path> --against (--session <path> | --snapshot <path> --max-bytes <n> --max-depth <n> --max-nodes <n>) [--max <n>]
  nova-work undo-plan      --session <path> --request <id> [--max <n>]
  nova-work undo           --session <path> <write flags> --request-of <id> --reason <text>
  nova-work redo-plan      --session <path> --request <id> [--max <n>]
  nova-work redo           --session <path> <write flags> --request-of <id> --reason <text>
  nova-work friend         --session <path> <write flags> (--register <name> | --retire <name> | --role <name>=<role>[:<scope>] | --participation <name>=<yes|no|withdrawn> | --capability <name>=<capability-id> --group <child|swarm|local|one-shot> --limit <n> | --limit <name>=<n>) --reason <text>
  nova-work config         --session <path> (--request <name> --base <hash|-> | --export <name> --into <path> | --intake --from <path> <write flags>) [--max <n>]
  nova-work model          --session <path> <write flags> (--register <id> --provider <name> --route <text> --billing <metered|subscription|local|unknown> | --rate <id>=<pricing-id> --effective <stamp> --source <pointer> | --evidence <id> --task-class <label> --result <pointer> --samples <n>) --reason <text>
  nova-work observe        --session <path> <write flags> --friend <name> (--state <awake|resting|unavailable|unconfirmed> --source <pointer> | --attempt <id> --observed-model <id> --bench <name> --usage <pointer>) --reason <text>
  nova-work goal set       --session <path> <write flags> --expect <rev> [--scope <scope>] (--goal <node-id> | --clear) --reason <text>   (--expect required here; --scope defaults to the caller's --as)
  nova-work goal show      (--session <path> | --snapshot <path> --max-bytes <n> --max-depth <n> --max-nodes <n> --cache <path>) --as <name> [--scope <scope>] --max <n>
  nova-work goal update    --session <path> <write flags> --expect <rev> [--scope <scope>] (--progress <text> [--evidence <pointer> --criterion <id> --against <sha>] | --blocked-by <node-id> --reason <text> | --stop --reason <text>)   (writes on the current goal node of the scope and on no other node)
  nova-work machine        --session <path> <write flags> (--register <id> --name <text> --owner <name> --connect <ref> --role <build|test|profile> ... | --retire <id> | --permit <id>=<kind> | --exclude <id>=<kind> | --limit <id> <key>=<n|n,n,...> | --fact <id> <key>=<value> --declared-by <name>) --reason <text>
  nova-work route          --session <path> <write flags> (--register <id> --provider <name> --endpoint <url> --key-location (:path "<path>"|:env "<name>") --plan <flat|metered|free|local> [--cost-per-mtok <n>] --capabilities <text=yes|no,code=yes|no,tool-calls=yes|no> --owner <name> | --retire <id> | --probe <id> --card <pointer> --pass <true|false|absent> [--wall <duration> --usd <amount>] --source <pointer>) --reason <text>
  nova-work offer          --session <path> <write flags> --node <id> --offer <offer-id> --to <name> --profile <capability-id>@<config-revision> --attempt <attempt-id> --generation <n> --request-ref <opaque-id> --payload <pointer> --payload-sha256 <hex> --reserve <slots> --until <stamp> [--requested-model <model-id>] [--predecessor-offer <offer-id> --predecessor-attempt <attempt-id>] [--reason <text>]
  nova-work profile        --session <path> <write flags> (--write <name> --model <id> --harness <id> --work-type <label> --pointer <path> --policy <revision> [--evidence <pointer>] [--expiry <stamp>] [--owner <name>] | --edit <name> (--pointer <path> | --policy <revision> | --evidence <pointer> | --expiry <stamp> | --owner <name>)) --reason <text>   (an edit re-pins the digest when it changes --pointer; a manager session selects one by name at start and never swaps it mid-session)
  nova-work acknowledge    --session <path> <write flags> --offer <offer-id> --reply <receipt-id> --stage <received|accepted> --provenance <pointer> --provenance-sha256 <hex> [--by <duration|stamp> --default <release|extend-once|escalate:<name>>] [--observed-model <model-id>] [--bench <name>] [--execution <handle>] [--reason <text>]   (--stage accepted: --by and --default, required, create-if-needed; --stage received: both exit 2)
  nova-work decline        --session <path> <write flags> --offer <offer-id> --reply <receipt-id> --provenance <pointer> --provenance-sha256 <hex> [--reason <text>]
  nova-work execution pause     --session <path> <write flags> (--node <id> | --repo <owner/name> | --all) --reason <text>
  nova-work execution stop      --session <path> <write flags> (--node <id> | --repo <owner/name> | --all) --reason <text>
  nova-work execution resume    --session <path> <write flags> --control <id> --action <release-hold|resume-workers> --reason <text>
  nova-work execution correct   --session <path> <write flags> --node <id> --instructions <pointer> --sha256 <hex> --reason <text>
  nova-work execution reconcile --session <path> <write flags> --control <id> --from <manifest-id> --reason <text>   (a content identity, never a local path)
  nova-work execution status    --session <path> --control <id> [--max <n>]
  nova-work check          (--session <path> | --snapshot <path> --max-bytes <n> --max-depth <n> --max-nodes <n> --cache <path>) [--max <n>]
  nova-work verify         --session <path> (--offline | --max-fetch <n> --fetch-timeout <seconds>) [--node <id>] [--max <n>]
  nova-work query          (--session <path> | --snapshot <path> --max-bytes <n> --max-depth <n> --max-nodes <n> --cache <path>) --ask <kind> --branch <open|closed|root>
                           (--ask is one of: done, remaining, who, percent, size, stream, under, stale, handoffs, roadmap, friends, models, ready, fleet, routes, reports)
                           [--node <id>] [--repo <o/n>] [--owner <name>] [--category <label>] [--axis <member>] [--for <workload-kind>] [--class <card-class>]
                           [--since <revision>] [--at <revision>] [--from <stamp>] [--to <stamp>] [--after <cursor>] [--page-budget <n>] [--max <n>] [--order <discovery|priority>]
                           (who and stale: --window <duration>, required; percent: --axis <member>, required on a matrix and refused on a zero- or one-axis roadmap;
                            ready: --order, optional, discovery by default; --order priority on any other ask is exit 2;
                            --branch closed and --branch root: --from and --to, required, and refused under --branch open;
                            who, stale, handoffs and reports: --branch open only, the other two exit 2; reports: --since <revision>, required (SPEC-AHEAD: #854);
                            fleet: --for optional, --node names a machine id, and --for with --node on a member that excludes the kind is refused;
                             routes: --class optional, the card class whose ordered route list the projection emits, cheapest first; without it the whole registry is listed)
  nova-work render         --session <path> --view <roadmap-id> (--chat [--projection <id> | --row-axis <id> --column-axis <id> --fixed <axis-id>=<member-id> ...] | --projection <id> (--file | --check)) [--at <revision>]
  nova-work node add       --session <path> <write flags> --id <id> --type <work-set|epic|feature|task> (--under <parent-id> | --under-root open --repo <owner/name>) [--title <text>] [--category <label>] [--required <true|false>] [--acceptance <id:kind:subject:predicate> ...] [--link <text> ... | --links-empty | --clear-links] [--private <true|false>] [--version <text>] --reason <text>   (--type roadmap is exit 2 naming 'roadmap create')
  nova-work node edit      --session <path> <write flags> --node <id> (--title <text> | --clear-title | --category <label> | --clear-category | --link <text> ... | --links-empty | --clear-links | --private <true|false> | --clear-private | --version <text> | --clear-version) ... --reason <text>
  nova-work node move      --session <path> <write flags> --node <id> --from <parent-id> --under <parent-id> --reason <text>
  nova-work node remove    --session <path> <write flags> --node <id> --reason <text>
  nova-work node require   --session <path> <write flags> --node <id> --to <true|false> --reason <text>
  nova-work decompose      --session <path> <write flags> --node <id> --into <id,...> --acceptance <child-id:id:kind:subject:predicate> ... --reason <text>
  nova-work accept         --session <path> <write flags> --node <id> (--add <id:kind:subject:predicate> | --remove <id>) --reason <text>
  nova-work source         --session <path> <write flags> --node <id> --to <sha> --reason <text>
  nova-work dep            --session <path> <write flags> --node <id> (--add <id> | --remove <id>) --reason <text>
  nova-work axis           --session <path> <write flags> --roadmap <id> --axis <id> (--add <member> | --remove <member>) --reason <text>
  nova-work roadmap create --session <path> <write flags> --id <id> --under <parent-id> [--title <text>] --row-kind <feature|epic|work-set> --aggregation <required-members|all-members|leaves> --completion-policy all-required-features (--axes-none | --axis-id <id> ...) [--permit-root <root-id> ...] --reason <text>
  nova-work roadmap configure --session <path> <write flags> --roadmap <id> [--row-kind <kind>] [--aggregation <policy>] [--completion-policy all-required-features] [--axes-none | --axis-id <id> ...] [--permit-root <root-id> ... | --roots-empty] --reason <text>
  nova-work roadmap row    --session <path> <write flags> --roadmap <id> (--add <member> | --remove <member>) --reason <text>   (axisless roadmaps only)
  nova-work roadmap projection --session <path> <write flags> --roadmap <id> (--add <id> --root <root-id> --repo <owner/name> --path <relative-path> --start <marker> --end <marker> --policy markdown-table [--row-axis <id> --column-axis <id> --fixed <axis-id>=<member-id> ...] | --remove <id>) --reason <text>
  nova-work prioritise     --session <path> <write flags> --node <id> (--set <rank> | --clear) [--context <self|subtree>] --reason <text>
  nova-work cell           --session <path> <write flags> --roadmap <id> --coord <member,member> (--ref <id|-> | --out-of-scope | --in-scope) --reason <text>   (--ref - clears the mapping)
  nova-work responsible    --session <path> <write flags> --node <id> --to <name> --reason <text>
  nova-work take           --session <path> <write flags> --node <id> --by <duration|stamp> --default <release|extend-once|escalate:<name>>
  nova-work heartbeat      --session <path> <write flags> --node <id> --evidence <pointer>
  nova-work release        --session <path> <write flags> --node <id> [--handed <name> --by <duration|stamp> --default <release|extend-once|escalate:<name>>]
  nova-work heartbeat      --session <path> <write flags> --allocation <id> --generation <n>   (allocation heartbeat: --allocation names the allocation id returned by take, --generation is the machine generation)
  nova-work release        --session <path> <write flags> --allocation <id> --generation <n> [--handed <name>]   (allocation release: --allocation names exactly one allocation, --generation is the machine generation; frees that allocation's slot only)
  nova-work attest         --session <path> <write flags> --node <id> --criterion <id> --result <pointer> --against <sha>
  nova-work attempt        --session <path> <write flags> --node <id> --model <name> --bench <name> --result <pointer> [--usage <pointer>]
  nova-work evidence       --session <path> <write flags> --node <id> --pointer <pointer> --criterion <id> --against <sha> [--attempt <id>]
  nova-work state          --session <path> <write flags> --node <id> --to <state> (--evidence <event-id> ... | --reason <text>) [--blocked-by <id>]
  nova-work correct        --session <path> <write flags> --node <id> --reason <text>
  nova-work event          --session <path> <write flags> --kind <baseline|discovery|defer|cancel|reopen|supersede> --node <id> --reason <text> [--member <id,...>] [--superseded-by <id>] [--evidence <pointer>] (baseline and discovery: --member, required, and --kind discovery on a :roadmap is exit 2 naming 'axis --add'; supersede: --superseded-by, required; cancel: --evidence <pointer>, required, and a note: pointer IS admitted here, because it evidences a stopped worker and never a done; --member on any other kind is exit 2)
  nova-work report         --session <path> <write flags> --act <launched|stopped|other> --subject <node|machine|friend|route|offer|external>:<text> --what <text> --acted-at <stamp> --instead-of <text|-> --reason <text>   (SPEC-AHEAD: #854; records a hand act whose effect lies outside the tree and changes no tree state)
  nova-work version        print this build identity (--version also accepted)
  nova-work help
  nova-work dependencies --graph <file> [--node <id> --needs <id>[,<id>...]]
  nova-work ready --node X --graph <file>
  nova-work clip --worktree <dir> --branch <name> --base <ref> --harvest <dir> [--result <file>] [--message <text>]
  nova-work plan check --file <path.work> [--max-bytes <n>] [--max-depth <n>] [--max-nodes <n>]
  nova-work plan expand --file <path.work> --out <dir> [--max-bytes <n>] [--max-depth <n>] [--max-nodes <n>]
  nova-work set check --file <path.lisp> [--minds <file>] [--lanes <file.tsv>] [--done <id>[,<id>...]] [--ready]
                      [--evaluate] [--base <branch>] [--cache <dir>] [--write-status]
                      [--max-bytes <n>] [--max-depth <n>] [--max-nodes <n>]
  nova-work attempt record --file <path.lisp> --unit <id> --by <mind> --outcome ok|failed|uncertain [--proof <path|sha|url>]
                      [--rung <name>] [--usage <file.tsv>] [--pr <n>] [--started <stamp>]
  nova-work attempt list   --file <path.lisp> --unit <id>
  nova-work next      --file <path.lisp> --for <mind> --lanes <file.tsv> [--machines <registry>] [--done <id>[,<id>...]]
                      [--kind <kind>] [--floor <0..1>] [--jev | --no-jev] [--usage <file.tsv>] [--log <file>]
                      [--take [--by <mind>] [--started <stamp>]]
  nova-work ask  --owner <friend> --unit <id> --units <file> --bus <dir> --as <name>
                 [--deadline <stamp>] [--kind work|read] [--cc <names>] [--record <file.json>]
                 [--reply-branch <name>] [--remote <name>] [--branch <name>]
                 [--nova-bus <path>] [--attempts <n>] [--timeout <duration>] [--max-bytes <n>] [--now <stamp>]
  nova-work asks (--units <file> | --bus <dir> --as <name>) [--owner <friend>] [--max <n>] [--max-notes <n>]
                 [--max-bytes <n>] [--now <stamp>]
  nova-work events --redis <addr> [--repo <owner>/<name>] [--base <branch>] [--gh-poll 60s] [--bench <name>] [--log <path>] (--once | --deadline <duration>)
  nova-work push --stream <kind> --lane <red|green|small|next> --card <file> (--redis <addr> | --dir <root>) [--priority <n>] [--needs <id>[,<id>...]]

wire:
  one line in, one line out over the Unix socket --session names. The request
  line is the verb and its flags in the order above, each as --name <value>,
  values escaped through internal/oneline's field form (one token per value:
  a space is \x20, an equals is \x3d), bools as --name true, the whole line
  newline-terminated. The reply is the session's own answer line, printed byte
  for byte: OK, ROW, NOTE and MORE to stdout, exit 0; FAIL, RACED and REFUSED
  to stderr, exit 1. What cannot run at all is one WORK REFUSED line on
  stderr, exit 2, ending "run: nova-work help". Values travel as given: the
  session validates every one and refuses with its own naming. An exchange is
  bounded by a 30-second default; a declared wait keeps a bounded 30-second
  transport allowance, an explicit --deadline caps the bound, and a deadline
  already past refuses before anything is dialled.

verbs:
  nova-work dependencies   owns the graph (:deps, refused acyclic at seed by validator rule 3)
  nova-work ready --node X is the ready set
  nova-work clip           commits the card's branch, harvests its result, resets the worktree to base
  nova-work plan check     reads a .work plan as data and closes its needs/blocks graph, never as a program
  nova-work plan expand    writes one card directory per hand-written :node, refusing a cycle or an absent need
  nova-work set check      reads the (work-set ...) form a coordinator writes and validates it whole
  nova-work attempt record files ONE attempt on ONE unit and moves its :state (A3, A4)
  nova-work attempt list   the unit's attempts, in order, with their termination proofs
  nova-work next           the ONE unit this mind does next: ready, owned, admitted, routed
  nova-work ask            delivers ONE unit to the FRIEND who owns it, as a bus note
  nova-work asks           the open asks, oldest first, with their age and their deadline
  nova-work events         bridges the events, not ticks (cards:done stream + gh fallback poll)

THE MACHINERY ROUTES TO FRIENDS (Glenn, 2026-09-18). A bench pulls cards; a friend pulls
asks. A unit whose owner is a friend is therefore never cut as a card: ask renders it as
ONE note in the house shape -- To, Cc, Subject, the unit, its lane, its needs, its
acceptance, the deadline and the branch to reply on -- and sends it through nova-bus's
OWN send path. An ask that could not be sent records nothing.

--units reads EITHER form, read from the file's first byte rather than its name: the
JSON shape this tool writes, or the SPEC-WORKLANG work set a coordinator writes by hand,
through the same bounded reader plan check uses. A JSON work set has the ask written
back onto its unit. A SPEC-WORKLANG one is a person's document and is NEVER written back:
the ask goes to --record when one is named, and otherwise the note on the bus is the
record -- which is what asks --bus --as reads. The bus is the source of truth for what
went out, and where a row appears in both, the bus's wins.

A unit with no :acceptance is asked, not refused: not one unit of the real work set
carries one, so the title stands as the acceptance, the note says "Acceptance: as titled"
and one ASK NOTE line on stderr says the unit carried none. A deadline is still never
guessed: --deadline, or the unit's own :deadline, and a unit with neither is refused.
The sender is on the Cc line of every ask it sends, because a broadcast includes self.

A node is ready only when every need is terminal accepted, and every row that cannot
proceed prints its exact blocker and its resolver. A :deps cycle is refused before
publication, so the ready set is finite and the graph can never deadlock.

A plan is read as data, never as a program: a ` + "`#.`" + ` dispatch macro anywhere in code
position is refused at exit 2 naming its byte offset, string and comment text is opaque,
and an unknown :kind is refused naming the field. :needs is the reference edge and
:blocks its inverse, so the kernel derives whichever a node did not give; an absent
need is refused naming the field and the id, and a :needs cycle is refused by validator
rule 3, both at load before the graph is published. A hand-written :node owes :kind,
:output and :budget before it can expand: :output must name a :branch, and :budget must
name :minutes, :tokens and :model-floor; a node owing several is refused naming every
field it owes in one run, never one round trip per field.

set check reads the OTHER top form of the same language: not ` + "`(:plan ...)`" + `, the
expander's, but ` + "`(work-set \"id\" ... :units ((unit ...)))`" + `, the one a coordinator
writes. It is read by the SAME bounded reader -- three bounds, no eval, a dispatch macro
refused at the byte that owes it -- and a key this reader does not know is KEPT, never
refused: the work set is a person's document and a unit with a :pr or a :budget is still
a unit with an owner. What set check then validates is the CONTENT, and the two exit
codes say different things. Exit 2 is a refusal: this file could not be read at all.
Exit 1 is findings: it was read whole and its content is wrong -- a duplicate id, a
:needs naming a unit nobody defined, a cycle, an :owner no --minds registry names, a
:lane no --lanes file names, a :deadline that is not an instant. Every rule runs over
every unit in ONE pass, one SET line per finding, because a checker that stopped at the
first would cost one round trip per defect. The SET OK line prints either way, and
units = ready + blocked + done closes its arithmetic; one SET DONE done=<n> percent=<p>
line follows it, percent rounded down, so x/y z% comes from the tool and not from awk.

--ready is the mechanical ready set, derived from the language rather than maintained by
hand: a unit is done when it says so (:done, or a :status of closed, done, landed or
merged) or when --done names it, and ready when it is not done and every need is done.
Without --minds and without --lanes those two rules are OFF rather than run against a
guessed file: there is no default registry and no discovery.

attempt and next are the WRITE side of SPEC-WORKLANG's amendment. A3 made an attempt a
RECORD with a termination proof and A4 made uncertain a state that keeps its reservation,
and both landed as readers: the only way a real work set could grow an :attempts list was
a person typing s-expressions into their own document by hand.

attempt record files one attempt on one unit and edits the file IN PLACE by splicing
bytes: every byte outside the edited unit comes back identical, and inside the unit every
byte outside the edited key does too. A work set is a person's document -- its comments,
its blank lines and the column its keys line up at are the document -- so the writer never
re-renders what it is not touching. An outcome that claims to have ended without proving
it is refused NAMING THE WORD to write instead (uncertain), and a unit already closed,
refused or abandoned takes no further attempt: a reopened piece of work is a new id
carrying :was (A2). The state machine is A4's own closed set and gets no second vocabulary
beside it -- green closes the unit, red leaves it OPEN because the ladder is the retry
policy, refused and abandoned are themselves, and uncertain keeps the reservation.

A try that started and then ended is ONE try. Where the unit's last attempt is still open
-- an :outcome :uncertain with no :proof, taken by this same mind -- record CLOSES that
record rather than appending beside it, keeping its :n, its :rung and the instant it
actually began. An open attempt under ANOTHER mind is refused, never appended beside:
its state and its reservation stand until its own owner records the outcome.

next is the what-do-I-do-next verb, and the first place all three halves of the work
language answer one question together: the graph says whose needs are closed, the kernel
(internal/jobs) says whose resources are free, and nova-decide's ladder says which mind
does it. Four gates, each a reading rather than a judgment -- ready, owned, free, routed
-- and ONE line out: NEXT unit= lane= rung= conf= take= reason=, or NEXT NONE naming the
gate that emptied the set. Everything already :live or :uncertain holds its reservation
BEFORE anything is admitted against what is left, which is what A4 means: the clock never
frees capacity, only an outcome does. And a unit the ladder is waiting on is never
dispatched (Stella's lease rule: a rung that may still be running is not a rung to step
off).

--evaluate derives done from each unit's :acceptance instead of its :status, through gh
(nova-tools #2664). Two criteria are evaluable: (:kind :landed :subject "pr:<o/r>#<n>"
:predicate :merged-or-closed-in-base) holds when the PR is merged, OR closed with its
content in the base by the lander's rule -- git merge-tree --write-tree <base> <head>
yields the base's own tree, so merging it changes nothing, which holds for a PR the lander
combined with another -- and :subject "commit:<sha>" holds when the commit is reachable
from the base; (:kind :merged ... :predicate :merged-at) holds only when the PR is merged.
A subject pinned as pr:<o/r>#<n>@<sha> asks about that head only: a pin the PR's last
head does not match was superseded and is not landed. The merge runs in one blobless
bare repository per repo under --cache. The base is
--base, else the set's :base, and one is required. Each evaluable criterion prints one
SET EVAL line with holds=yes|no|unknown and a why=; a criterion gh or git could not
answer is unknown and counts as not done. A unit is decided by its criteria when every
one is evaluable or one fails; a unit naming only :test, :job or :attested criteria keeps
its :status. Each question is asked once per run.
--write-status (implies --evaluate) then rewrites :status "open" to "landed" for each unit
whose criteria all hold, one SET WROTE line per unit, and changes no other byte.

events publishes the family's three event channels from two sources: a card's end on
the cards:done stream (consumer group events) becomes card-done, and a poll of gh every
--gh-poll becomes pr-checks-done on a changed check-suite conclusion and dev-moved on a
changed base head. Only an ok or a fail entry is a card's end (or an entry with no event
field, written before the field existed); every other transition on the stream -- queued,
a turn, a decide event -- is acked and not re-announced. The poll is the fallback
heartbeat until the forge pushes a webhook; a quiet poll publishes nothing. Without
--repo only the stream is bridged.

--once reads the stream and polls the forge once, then exits. The loop form requires
--deadline and returns when it is reached.

Every event events publishes is also written as one structured JSON line (SPEC-LOGS.md
Part 2): the same five labels on every line -- source=nova-work, verb=events, bench, the
event kind (start, card-done, pr-checks-done, dev-moved, done) and level -- plus the
fixed fields ts, guid, card, pr, msg, dur_ms and err. The line goes to stderr, which
under systemd is the unit's journal and so a source Alloy already reads, or to the file
--log names, which Alloy tails on every bench. A secret value never reaches the line:
the emitter redacts anything credential-shaped before it leaves the process. The stdout
EVENTS OK line is unchanged; the JSON line is written beside it, never instead of it.

flags:
  --graph <file>  the node graph, as JSON: {"nodes":[{"id":"a","needs":["b"]}, ...]}
                  Required on both graph verbs; there is no default and no discovery.
  --node <id>     dependencies: the node to write a needs edge to, creating it when the
                  graph does not hold it yet. ready: the one node to evaluate; without
                  it, ready prints one row per node in seed order.
  --needs <ids>   a comma-separated list of needs for --node. --needs needs --node;
                  --node alone creates a node needing nothing.
  --file <path>   plan check and plan expand: the plan to read. set check: the work set.
                  Required, always: there is no default file and no discovery from the
                  working directory.
  --minds <file>  set check: the registry an :owner must name, as the decide lane's
                  ladder ({"minds":[{"name":"emma"}...]}), the bus roster
                  ({"participants":[{"name":"Emma"}...]}) or a plain list, one name per
                  line. The shape is READ, not guessed at from the name, and the match
                  folds case. Without it no owner is checked.
  --lanes <file>  set check: the lanes file a :lane must name, <name>\t<path prefixes>
                  per line. Without it no lane is checked.
  --done <ids>    set check: comma-separated unit ids that are done, beside what the
                  file's own :done and :status say.
  --ready         set check: also print one SET READY line per unit of the ready set,
                  each carrying its admission verdict (admit=go, or admit=held with the
                  dimension or path that held it and the unit holding it).
  --unit <id>     attempt record and attempt list: the unit, by the stable id A2 mints.
  --by <mind>     attempt record: the mind the attempt is filed under. next --take: the
                  mind the opened attempt is filed under; --for when absent.
  --outcome <o>   attempt record: ok | failed | uncertain, and the grammar's own green,
                  red, refused and abandoned. Every outcome but uncertain owes --proof.
  --proof <p>     attempt record: the termination proof (A3). A url, a sha or a path,
                  and which of the three is READ off the value rather than asked for.
  --pr <n>        attempt record: the PR this attempt produced, written onto the record.
  --for <mind>    next: the mind asking. It is the OWNER the unit must belong to, not
                  the rung: rung= on the NEXT line is the ladder's answer, a different
                  axis, and the line carries both.
  --machines <f>  next: the registry of minds the ladder routes over; the embedded one
                  when absent, exactly as nova-decide route reads it.
  --kind <kind>   next: the decide kind a unit carrying no :kind of its own is routed
                  as. The default is new-verb: a unit of a pit-stop set is a verb to
                  build unless its author says otherwise.
  --jev/--no-jev  next: ask Jev among the eligible rungs, or answer by the rules alone
                  with no key and no network. Asking requires --usage and --log, because
                  a call nobody can account for is refused rather than made: both are
                  probed before the first call, every decision is appended to --log
                  and every provider call's spend to --usage.
  --take          next: open the attempt on the unit chosen -- :state :live, the lane
                  and the writes charged to it from that instant -- under the set's own
                  lock, so the unit a mind is told to do and the unit it is recorded as
                  doing are one decision.
  --evaluate      set check: derive done from :acceptance through gh (:landed, :merged).
  --base <branch> set check: the branch :landed means; default the set's :base.
  --cache <dir>   set check: where --evaluate keeps one blobless bare repository per
                  repo for the merge; default <user cache dir>/nova-work/landed.
  --write-status  set check: rewrite :status "open" to "landed" where every criterion
                  holds, in place, one line per unit; implies --evaluate.
  --out <dir>     plan expand: the directory to write one card per node into. Required;
                  a card already there is left byte-identical, so a re-expansion appends
                  only the new card and mints no id.
  --max-bytes <n> plan check: the byte ceiling (default 65536). A file past it is
                  refused before a byte is parsed, never truncated.
  --max-depth <n> plan check: the nesting ceiling (default 64). A form past it is
                  refused at its opening byte.
  --max-nodes <n> plan check: the atom ceiling (default 4096). A plan past it is refused
                  at the atom's byte.
  --units <file>  ask and asks: the work set, in either form and read as data. JSON:
                  {"units":[{"id":"u1","title":"...","owner":"Emma","lane":"work",
                  "needs":[...],"acceptance":[...],"deadline":"...","branch":"..."}]}.
                  SPEC-WORKLANG: (work-set "id" ... :units ((unit "id" :owner "Stella"
                  :lane "work" :needs (...) :deadline "2026-09-18T18:00Z" :title "..."))).
                  Required on ask; there is no default and no discovery.
  --record <file> ask: where the ask is recorded when --units is SPEC-WORKLANG, which is
                  never rewritten. Without it the bus note is the only record, and one
                  ASK NOTE line says so.
  --owner <name>  ask: the friend the unit belongs to, spelled the way the bus's roster
                  spells it. asks: show only that friend's asks.
  --deadline <t>  ask: when the answer is owed, as 2026-09-18T18:00:00Z or the shorter
                  2026-09-18T18:00Z a person writes. The unit's own :deadline stands when
                  this is absent; a unit with neither is refused, and a deadline that is
                  not after --now is refused before anything is sent.
  --bus <dir>     ask: the bus checkout the note is sent on. asks: the bus to READ the
                  sent notes from, which needs --as and is the source of truth.
  --now <stamp>   ask and asks: the instant deadlines and ages are measured against;
                  the default is this run's clock and an unparsable one is a refusal
                  rather than a silent fall back to it.
  --bench <name>  events: the fleet name of this machine, the bench label on every
                  structured line. Without it, $NOVA_BENCH, else the short hostname.
  --log <path>    events: append the structured JSON lines to this file instead of
                  stderr. The file is the one Alloy tails; a path that cannot be opened
                  is refused naming --log, never a silent run with no log.

exit codes: 0 ran and passed; 1 set check read the file whole and found something wrong
with its content, one SET line per finding; 2 could not run (bad invocation, an
unreadable graph, plan or work set, a :deps cycle, an unknown node, a refusal).

example:
  nova-work dependencies --graph ./deps.json --node b
  nova-work dependencies --graph ./deps.json --node a --needs b
  nova-work ready --node a --graph ./deps.json
  nova-work plan check --file ./work.work --max-bytes 65536
  nova-work set check --file ./work-set.lisp --ready
  nova-work next --file ./units.lisp --for rowan-child --lanes ./lanes.tsv --no-jev --take
  nova-work attempt record --file ./units.lisp --unit certify:verb --by rowan-child --outcome ok --proof 8a132e77 --pr 1369
  nova-work events --redis 127.0.0.1:6379 --once
`

// deps is the seam the tests replace: the store and consumer factories and the clock.

// refused is what could not run at all costs: ONE line on stderr naming what was wrong
// and the door to the usage, exit 2 -- the client spec's own remedy spelling.
func refused(stderr io.Writer, what string) int {
	fmt.Fprintf(stderr, "WORK REFUSED: %s; run: nova-work help\n", oneline.Escape(what))
	return 2
}

// legacyVerbs are the in-process graph and plan verbs, dispatched without a switch so
// that the verb switch a reader (and TestHelpListsEveryVerbTheSwitchAccepts) walks holds
// exactly the socket verbs.
//
// `attempt` COLLIDES: the spec's `attempt --session <path> ... --result <pointer>` is a
// write the resident session answers, and `attempt record`/`attempt list` are this
// binary's own local verbs over a work set file. Same word, two verbs, so `attempt` is
// dispatched by its second token (below, beside `record`/`results`) instead of living in
// this table: the bare word and every other second token still reach the socket switch.
var legacyVerbs = map[string]func([]string, io.Writer, io.Writer) int{
	"dependencies": cmdDependencies,
	"ready":        cmdReady,
	"clip":         cmdClip,
	"plan":         cmdPlan,
	"push":         cmdPushNow,
	"set":          cmdSet,
	"next":         cmdNext,
	"ask":          cmdAsk,
	"asks":         cmdAsks,
	"proving-run":  cmdProvingRun,
	"dogfood":      cmdDogfood,
}

// Deps is everything this binary reaches outside itself, injected so the tests drive a
// miniredis and a fake forge, and reach no network.
type Deps struct {
	Now   func() time.Time
	Dial  func(addr string) *redis.Client
	Forge func(repo, base string, timeout time.Duration) ci.Forge
}

func production() Deps {
	return Deps{
		Now:  func() time.Time { return time.Now().UTC() },
		Dial: func(addr string) *redis.Client { return redis.NewClient(&redis.Options{Addr: addr}) },
		Forge: func(repo, base string, timeout time.Duration) ci.Forge {
			return ci.NewGHForge(repo, base, timeout)
		},
	}
}

// absorbDecision is the decision record of nova-tools#2090, in the issue's own
// terms, carried as the one line this binary answers `absorb` with. It is a
// record for later, not a promise: nothing in it is work to start, and the
// three E09-F04 criteria it names stay unverified on purpose until one of the
// named triggers reopens the issue. Before it lived here the caller who asked
// was told `unknown verb`, a sentence that says nobody decided -- which is
// false: the decision is made, it is just NO, and Glenn's rulings of 2026-09-20
// (link mode today; absorb only if radically cheaper or a second tracker
// arrives, never to answer sync pain with a cleverer sync; and if one side
// must be primary, "the internal lisp data structure representation would
// win") are the reasoning this line keeps from having to be reconstructed.
const absorbDecision = "absorb is not scheduled (the decision record of nova-tools#2090, E09-F04): " +
	"link is the intake mode today and GitHub stays the source of truth for issues; " +
	"it reopens only on one of three triggers -- a radical saving in tokens and wall clock shown by the dogfood tables (#2089), " +
	"a second issue tracker beside GitHub, or sync pain, drift that needs a person or a decision made on a stale copy; " +
	"which side wins is already decided: if one side must be primary it is nova-work's Lisp data structure, " +
	"and the GitHub copy is what stops being maintained; " +
	"and before it is built all three E09-F04 criteria must be verified: " +
	"absorb separate from the default link with selected scope and authority, " +
	"source identity, provenance and content archived before removal with the deletion outcome receipt appended, " +
	"and deletion left pending on missing content, a source change or an uncertain network result"

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, production(), version)) }

// run takes the version stamp (a string, for the version verb's tests) and the injected
// Deps (for the events verb's tests) as trailing options, so the socket
// client's stamp and every outside edge reach the one entry point.
func run(args []string, stdout, stderr io.Writer, opts ...any) int {
	stamp := version
	deps := production()
	for _, opt := range opts {
		if s, ok := opt.(string); ok {
			stamp = s
			continue
		}
		if d, ok := opt.(Deps); ok {
			deps = d
		}
	}
	// The framed wire's hello names the build identity it is; the empty stamp
	// of a test run leaves the package's own floor.
	if stamp != "" {
		workclient.ClientIdentity = stamp
	}
	if len(args) == 0 {
		return refused(stderr, "a verb is required")
	}
	if h, ok := legacyVerbs[args[0]]; ok {
		return h(args[1:], stdout, stderr)
	}
	// accept is also a resident-session socket verb (accept --session ...). The
	// in-process graph form is the one that names --graph, and only that form is
	// taken here; every other accept line falls through to the socket verb table.
	if args[0] == "accept" && jobs.NamesGraphFlag(args[1:]) {
		return cmdAccept(args[1:], stdout, stderr)
	}
	if args[0] == "events" {
		return cmdEvents(args[1:], stdout, stderr, deps)
	}
	verb, rest := args[0], args[1:]
	// attempt COLLIDES with the socket verb of the same name (see legacyVerbs): only
	// its two known second tokens are this binary's own local verb; the bare word and
	// anything else falls through to the socket switch below, which refuses rather
	// than guesses.
	if verb == "attempt" && len(rest) > 0 && (rest[0] == "record" || rest[0] == "list") {
		return cmdAttempt(rest, stdout, stderr)
	}
	// visualize reads one record the caller names and reaches nothing outside the
	// process, so it dispatches the same way, outside the socket-verb switch.
	if verb == "visualize" {
		return cmdVisualize(rest, stdout, stderr)
	}
	switch verb {
	case "help", "--help", "-h":
		if len(rest) > 0 && (rest[0] == "--help" || rest[0] == "-h") {
			return printVerbHelp(stderr, "help")
		}
		if len(rest) != 0 {
			return refused(stderr, "help takes no arguments")
		}
		fmt.Fprint(stdout, usage)
		return 0
	case "version", "--version":
		if len(rest) > 0 && (rest[0] == "--help" || rest[0] == "-h") {
			return printVerbHelp(stderr, "version")
		}
		if len(rest) != 0 {
			return refused(stderr, "version takes no arguments")
		}
		fmt.Fprintln(stdout, oneline.Escape(buildinfo.Line("nova-work", stamp)))
		return 0
	case "query":
		return queryVerb(rest, stdout, stderr)
	}
	// absorb is the one name answered with a decision record rather than a
	// verb or an "unknown verb": the roadmap holds it as E09-F04 and the
	// 2026-09-20 ruling left it not scheduled (nova-tools#2090), so the caller
	// who asks is owed the record, said in one line.
	if verb == "absorb" {
		return refused(stderr, absorbDecision)
	}
	// Every other verb the spec addresses to a session is a row of verbFlags
	// and no case of its own: see socketverbs.go. The block's lines this
	// client does not send are named BEFORE the resolver, because `state
	// load` would otherwise resolve as the `state` mutation verb carrying a
	// stray positional and be refused for the wrong reason.
	if len(rest) > 0 {
		if why, ok := notCarried[verb+" "+rest[0]]; ok {
			return refused(stderr, why)
		}
	}
	if why, ok := notCarried[verb]; ok {
		return refused(stderr, why)
	}
	if v, args, ok := resolveSocketVerb(verb, rest); ok {
		return sessionVerb(v, args, stdout, stderr)
	}
	if subs, ok := verbFamilies[verb]; ok {
		if len(rest) > 0 && (rest[0] == "--help" || rest[0] == "-h") {
			return printVerbHelp(stderr, verb)
		}
		if len(rest) == 0 {
			return refused(stderr, verb+" needs one of "+orList(subs))
		}
		return refused(stderr, fmt.Sprintf("unknown %s verb %q; it is one of %s", oneline.Field(verb), rest[0], oneline.Escape(orList(subs))))
	}
	return refused(stderr, fmt.Sprintf("unknown verb %q", verb))
}

// orList spells a closed list the way a refusal reads it aloud: "a, b or c".
func orList(items []string) string {
	switch len(items) {
	case 0:
		return ""
	case 1:
		return items[0]
	default:
		return strings.Join(items[:len(items)-1], ", ") + " or " + items[len(items)-1]
	}
}

// refuse is what an unusable invocation costs: one line naming what was wrong and the door
// to the usage, never the banner.
func refuse(stderr io.Writer, where, what string) int {
	fmt.Fprintf(stderr, "nova-work%s: %s; run: nova-work help\n", oneline.Escape(where), oneline.Escape(what))
	return 2
}

func cmdVersion(args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 {
		return refuse(stderr, " version", fmt.Sprintf("takes no flags and no arguments, got %d", len(args)))
	}
	// buildinfo.Line already renders each field through oneline.Field; Escape here is
	// the source-level tripwire's proof that this print site is escaped, and it leaves
	// the line's deliberate spaces between fields intact.
	fmt.Fprintln(stdout, oneline.Escape(buildinfo.Line("nova-work", version)))
	return 0
}

func cmdDependencies(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("dependencies", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	graph := fs.String("graph", "", "the :deps graph file (required)")
	node := fs.String("node", "", "the node to write a needs edge to")
	needs := fs.String("needs", "", "comma-separated needs for --node")
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp { return printVerbHelp(stderr, "dependencies") }
		return refuse(stderr, " dependencies", oneline.Cap(err.Error(), oneline.TailBytes))
	}
	if fs.NArg() > 0 {
		return refuse(stderr, " dependencies", fmt.Sprintf("unexpected argument %q", fs.Arg(0)))
	}
	if strings.TrimSpace(*graph) == "" {
		return refuse(stderr, " dependencies", "--graph is required; refusing to guess")
	}
	if *node == "" && *needs != "" {
		return refuse(stderr, " dependencies", "--needs names the needs of a --node; give both or neither")
	}

	add := strings.TrimSpace(*node) != ""
	var nodes []jobs.Node
	raw, err := os.ReadFile(*graph)
	switch {
	case err == nil:
		nodes, err = jobs.ParseNodes(raw)
		if err != nil {
			return refuse(stderr, " dependencies", err.Error())
		}
	case !add:
		return refuse(stderr, " dependencies", oneline.Err(err))
	}
	if add {
		nodes = addNeeds(nodes, strings.TrimSpace(*node), splitNeeds(*needs))
	}
	// rule 3 refuses the cycle BEFORE anything is written: a refused graph is never
	// published.
	g, err := jobs.Seed(nodes)
	if err != nil {
		return refuse(stderr, " dependencies", err.Error())
	}
	if add {
		out, err := jobs.MarshalNodes(nodes)
		if err != nil {
			return refuse(stderr, " dependencies", err.Error())
		}
		if err := os.WriteFile(*graph, out, 0o644); err != nil {
			return refuse(stderr, " dependencies", oneline.Err(err))
		}
	}
	fmt.Fprintf(stdout, "DEPENDENCIES OK nodes=%d edges=%d\n", g.Len(), g.Edges())
	return 0
}

// addNeeds writes a needs edge to the named node, creating the node when the graph does
// not hold it yet. Needs are a set (#1788): a need named twice in one --needs list, a
// need re-sent by a re-run and a copy the file already holds are each written once, so a
// re-run leaves the file as it found it rather than a copy longer. The blocks edge is the
// same insert's inverse, so it is never written separately.
func addNeeds(nodes []jobs.Node, id string, needs []string) []jobs.Node {
	for i := range nodes {
		if nodes[i].ID == id {
			nodes[i].Needs = needsOnce(nodes[i].Needs, needs)
			return nodes
		}
	}
	return append(nodes, jobs.Node{ID: id, Needs: needsOnce(nil, needs)})
}

// needsOnce returns the needs in held and then want with every id once, first occurrence
// first. The file the verb writes holds each edge once, because the ready set counts
// unmet needs and a duplicated edge is never decremented to zero.
func needsOnce(held, want []string) []string {
	seen := make(map[string]bool, len(held)+len(want))
	var out []string
	for _, dep := range append(append([]string(nil), held...), want...) {
		if seen[dep] {
			continue
		}
		seen[dep] = true
		out = append(out, dep)
	}
	return out
}

func splitNeeds(list string) []string {
	var out []string
	for _, part := range strings.Split(list, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

func cmdReady(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("ready", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	graph := fs.String("graph", "", "the :deps graph file (required)")
	node := fs.String("node", "", "one node to evaluate; default all nodes")
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp { return printVerbHelp(stderr, "ready") }
		return refuse(stderr, " ready", oneline.Cap(err.Error(), oneline.TailBytes))
	}
	if fs.NArg() > 0 {
		return refuse(stderr, " ready", fmt.Sprintf("unexpected argument %q", fs.Arg(0)))
	}
	if strings.TrimSpace(*graph) == "" {
		return refuse(stderr, " ready", "--graph is required; refusing to guess")
	}
	raw, err := os.ReadFile(*graph)
	if err != nil {
		return refuse(stderr, " ready", oneline.Err(err))
	}
	g, err := jobs.ParseSeed(raw)
	if err != nil {
		return refuse(stderr, " ready", err.Error())
	}
	if strings.TrimSpace(*node) != "" {
		id := strings.TrimSpace(*node)
		n, ok := g.Node(id)
		if !ok {
			return refuse(stderr, " ready", fmt.Sprintf("no such node %q; refusing to guess", id))
		}
		writeRow(stdout, n, g)
		return 0
	}
	for _, id := range g.Order() {
		n, _ := g.Node(id)
		writeRow(stdout, n, g)
	}
	return 0
}

// writeRow prints one ready row: the node, whether it may proceed, and -- when it
// cannot -- its exact blocker and its resolver. A node that has already merged and gone
// green cannot begin because it is done, not because a need blocks it.
func writeRow(stdout io.Writer, n jobs.Node, g *jobs.Graph) {
	ready, blocker := g.Ready(n.ID)
	fmt.Fprintf(stdout, "READY node=%s ready=%t", oneline.Field(n.ID), ready)
	if blocker != nil {
		fmt.Fprintf(stdout, " blocker=%s state=%s resolver=%s",
			oneline.Field(blocker.Need), oneline.Field(blocker.State), oneline.Quote(blocker.Resolver))
	} else if !ready {
		fmt.Fprintf(stdout, " state=%s blocker=- resolver=-", oneline.Field(n.State()))
	}
	fmt.Fprintln(stdout)
}

// cmdAccept is the in-process graph form of accept. jobs.AcceptArgs parses the line
// and accepts the node, so the tested argv boundary is the one this verb ships.
func cmdAccept(args []string, stdout, stderr io.Writer) int {
	id, err := jobs.AcceptArgs(args)
	if err != nil {
		return refuse(stderr, " accept", oneline.Cap(err.Error(), oneline.TailBytes))
	}
	fmt.Fprintf(stdout, "ACCEPT OK node=%s\n", oneline.Field(id))
	return 0
}

func cmdPlan(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || (args[0] != "check" && args[0] != "expand") {
		return refuse(stderr, " plan", "the verbs are plan check --file <path.work> and plan expand --file <path.work> --out <dir>")
	}
	if args[0] == "expand" {
		return cmdPlanExpand(args[1:], stdout, stderr)
	}
	fs := flag.NewFlagSet("plan check", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	file := fs.String("file", "", "the plan to read (required)")
	def := worklang.DefaultLimits()
	maxBytes := fs.Int("max-bytes", def.MaxBytes, "byte ceiling")
	maxDepth := fs.Int("max-depth", def.MaxDepth, "nesting depth ceiling")
	maxNodes := fs.Int("max-nodes", def.MaxNodes, "atom ceiling")
	if err := fs.Parse(args[1:]); err != nil {
		if err == flag.ErrHelp { return printVerbHelp(stderr, "plan check") }
		return refuse(stderr, " plan check", oneline.Cap(err.Error(), oneline.TailBytes))
	}
	if fs.NArg() > 0 {
		return refuse(stderr, " plan check", fmt.Sprintf("unexpected argument %q", fs.Arg(0)))
	}
	if *file == "" {
		return refuse(stderr, " plan check", "--file is required; refusing to guess")
	}
	limits := worklang.Limits{MaxBytes: *maxBytes, MaxDepth: *maxDepth, MaxNodes: *maxNodes}
	data, err := os.ReadFile(*file)
	if err != nil {
		return refuse(stderr, " plan check", oneline.Err(err))
	}
	plan, err := worklang.ParsePlan(*file, data, limits)
	if err != nil {
		return refuse(stderr, " plan check", oneline.Err(err))
	}
	// Closing the graph at load refuses an absent need and a :needs cycle before
	// anything is published: a plan whose graph cannot be built is not a plan.
	graph, err := plan.Graph()
	if err != nil {
		return refuse(stderr, " plan check", oneline.Err(err))
	}
	fmt.Fprintf(stdout, "PLAN OK file=%s bytes=%d version=%d nodes=%d edges=%d\n",
		oneline.Field(*file), len(data), plan.Version, len(plan.Nodes), graph.Edges())
	return 0
}

// cmdPlanExpand is the smallest first slice of SPEC-WORKLANG's expander: it
// reads hand-written :nodes, builds the needs/blocks graph, refuses a cycle or
// an absent need, and writes one card directory per node under --out. Output is
// one line; a refusal is exit 2 with one remedy line.
func cmdPlanExpand(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("plan expand", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	file := fs.String("file", "", "the plan to expand (required)")
	out := fs.String("out", "", "the directory to write one card per node into (required)")
	def := worklang.DefaultLimits()
	maxBytes := fs.Int("max-bytes", def.MaxBytes, "byte ceiling")
	maxDepth := fs.Int("max-depth", def.MaxDepth, "nesting depth ceiling")
	maxNodes := fs.Int("max-nodes", def.MaxNodes, "atom ceiling")
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp { return printVerbHelp(stderr, "plan expand") }
		return refuse(stderr, " plan expand", oneline.Cap(err.Error(), oneline.TailBytes))
	}
	if fs.NArg() > 0 {
		return refuse(stderr, " plan expand", fmt.Sprintf("unexpected argument %q", fs.Arg(0)))
	}
	if *file == "" {
		return refuse(stderr, " plan expand", "--file is required; refusing to guess")
	}
	if *out == "" {
		return refuse(stderr, " plan expand", "--out is required; refusing to guess")
	}
	limits := worklang.Limits{MaxBytes: *maxBytes, MaxDepth: *maxDepth, MaxNodes: *maxNodes}
	data, err := os.ReadFile(*file)
	if err != nil {
		return refuse(stderr, " plan expand", oneline.Err(err))
	}
	plan, err := worklang.ParsePlan(*file, data, limits)
	if err != nil {
		return refuse(stderr, " plan expand", oneline.Err(err))
	}
	cards, err := worklang.ExpandPlan(plan)
	if err != nil {
		return refuse(stderr, " plan expand", oneline.Err(err))
	}
	written, err := worklang.ExpandDir(*out, cards)
	if err != nil {
		return refuse(stderr, " plan expand", oneline.Err(err))
	}
	fmt.Fprintf(stdout, "PLAN EXPANDED file=%s out=%s nodes=%d cards=%d\n",
		oneline.Field(*file), oneline.Field(*out), len(cards), written)
	return 0
}

// flagSpec is one flag of one socket verb. A bool flag is a switch serialized
// as --name true when set; a multi flag is repeatable and serializes one
// --name per value in the order the caller gave them; every other value
// travels as the caller spelled it, a string, because the session validates
// and a thin client never second-guesses the engine's bounds.
type flagSpec struct {
	name  string
	multi bool
	bool  bool
}

// verbFlags is each socket verb's flag surface and its canonical order, the
// spec's verbs block in the same order it prints them, so the request line a
// session reads is spelled the way its own spec names the flags.
var verbFlags = map[string][]flagSpec{
	"session start": {
		{name: "session"},
		{name: "as"},
		{name: "file"},
		{name: "journal"},
		{name: "cache"},
		{name: "repo"},
		{name: "remote"},
		{name: "branch"},
		{name: "max-bytes"},
		{name: "max-depth"},
		{name: "max-nodes"},
		{name: "every"},
		{name: "skew"},
		{name: "clip-every"},
		{name: "clip-after"},
		{name: "retain"},
		{name: "savepoint-every"},
		{name: "savepoint-after"},
		{name: "max-frame-bytes"},
		{name: "silence-ping"},
		{name: "index-cache"},
		{name: "page-bytes"},
		{name: "page-records"},
		{name: "closed-window"},
		{name: "render-root", multi: true},
		{name: "resolver", multi: true},
		{name: "git-timeout"},
		{name: "attempts"},
		{name: "repair", bool: true},
		{name: "foreground", bool: true},
		{name: "max"},
		{name: "now"},
	},
	// session status is a verb of the session's one request schema,
	// *REQUEST-SCHEMA* in lisp/nova-work/src/request-line.lisp (E08-F01-01):
	// this row is its flags, name for name, and
	// lisp/nova-work/tests/criterion-e08-f01-01.lisp reads it from here and sends
	// every flag this client admits or refuses over a real socket, so a row that
	// drifts from the schema, or a wire that disagrees with it, is a red test.
	"session status": {
		{name: "session"},
	},
	"session stop": {
		{name: "session"},
		{name: "git-timeout"},
		{name: "attempts"},
		{name: "no-clip", bool: true},
	},
}

// repeatFlag is a --flag that may be given more than once.
type repeatFlag []string

func (r *repeatFlag) String() string { return strings.Join(*r, ",") }
func (r *repeatFlag) Set(v string) error {
	*r = append(*r, v)
	return nil
}

func sessionVerb(verb string, args []string, stdout, stderr io.Writer) int {
	specs := verbFlags[verb]
	f := flag.NewFlagSet(verb, flag.ContinueOnError)
	f.SetOutput(io.Discard)
	strs := map[string]*string{}
	bools := map[string]*bool{}
	mults := map[string]*repeatFlag{}
	for _, s := range specs {
		switch {
		case s.bool:
			bools[s.name] = f.Bool(s.name, false, "")
		case s.multi:
			m := &repeatFlag{}
			f.Var(m, s.name, "")
			mults[s.name] = m
		default:
			strs[s.name] = f.String(s.name, "", "")
		}
	}
	if err := f.Parse(args); err != nil {
		if err == flag.ErrHelp { return printVerbHelp(stderr, verb) }
		return refused(stderr, verb+": "+err.Error())
	}
	if f.NArg() != 0 {
		return refused(stderr, verb+" takes no positional arguments (got "+oneline.Quote(f.Arg(0))+")")
	}
	socket := *strs["session"]
	if socket == "" {
		return refused(stderr, "--session is required; refusing to guess (the socket has no default path)")
	}
	deadline, timeout, gitTimeout := "", "", ""
	if p, ok := strs["deadline"]; ok {
		deadline = *p
	}
	if p, ok := strs["timeout"]; ok {
		timeout = *p
	}
	if p, ok := strs["git-timeout"]; ok {
		gitTimeout = *p
	}
	// Presence is separate from the value, and it comes from the FlagSet and
	// from nothing else: `--deadline=`, `-deadline=` and `--deadline ""` all
	// leave the string "", and a scan of args for the literal "--deadline"
	// would disagree with the parser on at least one of them. Visit walks only
	// the flags the caller actually set, where VisitAll walks every defined
	// flag. The two empty states need opposite answers -- a supplied empty cap
	// is invalid RFC3339 and refuses below, while a true omission keeps the
	// ordinary finite default -- so presence is handed to deadlineParse rather
	// than folded back into the empty string.
	deadlineGiven := false
	f.Visit(func(fl *flag.Flag) {
		if fl.Name == "deadline" {
			deadlineGiven = true
		}
	})
	// A supplied-but-empty --deadline is PRESENT and invalid RFC3339, not an
	// omitted flag: it must not silently fall back to the ordinary default. It
	// is refused here, before guard (a) and before anything is dialled. A true
	// omission is deadlineAbsent and keeps the ordinary default below. The
	// client sizes its own socket bound from --deadline, so this is the one
	// flag it PARSES AND USES rather than merely forwards; it must not act on a
	// value it could not read, and an unreadable value is not an absent one.
	_, state := deadlineParse(deadline, deadlineGiven)
	if state == deadlineEmptyPresent {
		return refused(stderr, "--deadline was given with no value; an explicit cap is invalid RFC3339, not an omitted flag")
	}
	if state == deadlineMalformed {
		return refused(stderr, fmt.Sprintf("the deadline %s is not an instant; the client sizes its own bound from --deadline and will not send a request it cannot bound",
			oneline.Field(deadline)))
	}
	// Guard (a): a deadline already past is refused before anything is dialled.
	// The measuring instant is the real clock, never --now, which is the
	// engine's instant for fencing and receipts. A malformed explicit deadline
	// was already refused above; here only a well-formed past one refuses.
	if at, ok := deadlineStamp(deadline); ok && !at.After(time.Now()) {
		return refused(stderr, fmt.Sprintf("the deadline %s is not after %s; an ask that is late before it is sent is not an ask",
			oneline.Field(at.UTC().Format(time.RFC3339)), oneline.Field(time.Now().UTC().Format(time.RFC3339))))
	}
	// Guard (b): the belt. The derivation can go non-positive on a pathological
	// declared wait, and workclient's budget() reads a non-positive bound as NO
	// DEADLINE AT ALL, so it never reaches the wire.
	within := derivedBound(deadline, timeout, gitTimeout)
	if within <= 0 {
		within = askTimeout
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s", oneline.Escape(verb))
	for _, s := range specs {
		switch {
		case s.bool:
			if *bools[s.name] {
				fmt.Fprintf(&b, " --%s true", oneline.Field(s.name))
			}
		case s.multi:
			for _, v := range *mults[s.name] {
				fmt.Fprintf(&b, " --%s %s", oneline.Field(s.name), oneline.Field(v))
			}
		default:
			if v := *strs[s.name]; v != "" {
				fmt.Fprintf(&b, " --%s %s", oneline.Field(s.name), oneline.Field(v))
			}
		}
	}
	if verb == "session start" {
		if journal := *strs["journal"]; journal != "" {
			if pid, held := checkJournalHeld(journal); held {
				fmt.Fprintf(stderr, "SESSION REFUSED session=%s: journal held by pid %s on %s\n", oneline.Field(socket), oneline.Field(pid), oneline.Field(journal))
				return 1
			}
		}
		if pid, held := checkSocketLocked(socket); held {
			fmt.Fprintf(stderr, "SESSION FAIL session=%s: socket held by pid %s\n", oneline.Field(socket), oneline.Field(pid))
			return 1
		}
	}
	return ask(socket, b.String(), frameFor(verb, specs, strs, bools, mults), within, stdout, stderr)
}

// checkJournalHeld reports the pid a journal's .lock file names, raw; the caller
// escapes it at the print site.
func checkJournalHeld(journal string) (pid string, held bool) {
	return lockPid(journal + ".lock")
}

// checkSocketLocked reports the pid a socket's .lock file names, raw; the caller
// escapes it at the print site.
func checkSocketLocked(socket string) (pid string, held bool) {
	return lockPid(socket + ".lock")
}

// lockPid returns the first pid= value in a lock file (the first space-separated
// token after "pid="), or held=false when the file is absent or names no pid.
func lockPid(path string) (pid string, held bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	for _, line := range strings.Split(string(data), "\n") {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), "pid="); ok {
			pid, _, _ = strings.Cut(rest, " ")
			return pid, true
		}
	}
	return "", false
}

// askTimeout is the wall-clock bound one exchange may spend. It is a variable
// only so the tests can shorten it: a wedged session is a thirty-second wait
// by design and a test suite cannot afford one per case.
var askTimeout = workclient.DefaultTimeout

// frameFor spells the same request the v1 framed wire carries, built from the
// same parsed flags the request line is spelled from, so the two wires can
// never disagree about what was asked. The verb is the op; the envelope fields
// the spec names -- request, as, expect, now, max, deadline -- are hoisted out
// of the flags that spell them; --session is the endpoint the frame travels
// over and never an argument of it; and every other set flag travels in args
// as the caller spelled it: a string, an array for a repeated flag, true for a
// switch, and never a JSON number, which the protocol forbids.
func frameFor(verb string, specs []flagSpec, strs map[string]*string, bools map[string]*bool, mults map[string]*repeatFlag) workclient.Frame {
	frame := workclient.Frame{Op: verb, Args: map[string]any{}}
	for _, s := range specs {
		switch {
		case s.bool:
			if *bools[s.name] {
				frame.Args[s.name] = true
			}
		case s.multi:
			if vs := *mults[s.name]; len(vs) > 0 {
				arr := make([]any, len(vs))
				for i, v := range vs {
					arr[i] = v
				}
				frame.Args[s.name] = arr
			}
		default:
			v := *strs[s.name]
			if v == "" {
				continue
			}
			switch s.name {
			case "session":
				// the endpoint the frame travels over, not an argument of it
			case "request":
				frame.Request = v
			case "as":
				frame.As = v
			case "expect":
				frame.Expect = v
			case "now":
				frame.Now = v
			case "max":
				frame.Max = v
			case "deadline":
				frame.Deadline = v
			default:
				frame.Args[s.name] = v
			}
		}
	}
	if frame.Request == "" {
		// The v1 wire correlates every response by its request id, so a verb
		// whose flags draw none has this run draw one.
		frame.Request = fmt.Sprintf("nova-work-%d-%d", os.Getpid(), time.Now().UnixNano())
	}
	return frame
}

// ask is the whole wire: the request line out on the S1 wire, and -- when the
// session proves it speaks v1 frames by answering the line's bytes with the
// framed refusal the spec pins for them -- the same request again as one v1
// frame, so a listing verb's many answer lines reach the caller
// (nova-tools#1696). The dial and the reads live in internal/workclient so
// that this package keeps a single print path; here only the reply is
// classified.
//
// Three ways there is no reply line, and they are three different refusals,
// because they send a person to three different places. A socket nothing is
// listening on is no such session. A session that accepts and then says
// nothing inside the bound is a session to go and look at — and, if the
// request was a mutation, one whose work may still have been accepted, since a
// client that stopped waiting is no more a rollback than a disconnect is
// (docs/SPEC-WORK.md, "The engine and its client"). A reply past the wire's cap
// is a session speaking a shape this wire does not carry.
func ask(socket, request string, frame workclient.Frame, within time.Duration, stdout, stderr io.Writer) int {
	answer, err := workclient.ExchangeWireWithin(socket, request, frame, within)
	switch {
	case err == nil && answer.Framed:
		return printReplyLines(answer.Reply, stdout, stderr)
	case err == nil:
		return printReply(answer.Line, stdout, stderr)
	case errors.Is(err, workclient.ErrSilent):
		return refused(stderr, "the session at "+socket+" accepted the request and did not answer: "+err.Error()+"; a mutation may still have been accepted -- ask the session rather than retrying blind")
	case errors.Is(err, workclient.ErrTooLong):
		return refused(stderr, "the session at "+socket+" answered past the wire's bound: "+err.Error())
	case errors.Is(err, workclient.ErrFrameVersion), errors.Is(err, workclient.ErrFrameMalformed),
		errors.Is(err, workclient.ErrFrameCorrelation), errors.Is(err, workclient.ErrFrameTooLong):
		return refused(stderr, "the session at "+socket+" answered in v1 frames and the framed exchange failed: "+err.Error())
	default:
		return refused(stderr, "no such session: cannot reach "+socket+": "+err.Error())
	}
}

// printReply splits the session's line by the second token and by nothing
// else, the spec's own client rule: OK, ROW, NOTE and MORE to stdout; FAIL,
// RACED and REFUSED -- the session answered, and what it answered was no, the
// spec's exit 1 -- to stderr the same way. Anything else is not a line the
// grammar spells, and the client refuses rather than guessing a verdict. The
// line is rendered through oneline.Escape, which is the identity on a
// well-formed one-line reply and so keeps the byte-for-byte promise.
func printReply(line string, stdout, stderr io.Writer) int {
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return refused(stderr, "the session's reply is not an answer line: "+line)
	}
	switch fields[1] {
	case "OK", "ROW", "NOTE", "MORE":
		fmt.Fprintln(stdout, oneline.Escape(line))
		return 0
	case "FAIL", "RACED", "REFUSED":
		fmt.Fprintln(stderr, oneline.Escape(line))
		return 1
	default:
		return refused(stderr, "the session's reply carries no verdict the grammar spells: "+line)
	}
}

// printReplyLines is printReply for the v1 wire's ordered answer: the spec's
// one client rule -- "the client splits them by the second token and by
// nothing else" -- applied to every line of the response's array, and the
// response's own exit as the run's verdict, the field the one-line wire has
// no place for (nova-tools#1696). OK, ROW, NOTE and MORE go to stdout, FAIL,
// RACED and REFUSED to stderr, each rendered through oneline.Escape, the
// identity on a well-formed grammar line, so the byte-for-byte promise holds
// per line. decodeReply has already read the exit as one of the three codes.
func printReplyLines(reply workclient.Reply, stdout, stderr io.Writer) int {
	code := 2
	switch reply.Exit {
	case "0":
		code = 0
	case "1":
		code = 1
	}
	for _, line := range reply.Lines {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			return refused(stderr, "the session's reply is not an answer line: "+line)
		}
		switch fields[1] {
		case "OK", "ROW", "NOTE", "MORE":
			fmt.Fprintln(stdout, oneline.Escape(line))
		case "FAIL", "RACED", "REFUSED":
			fmt.Fprintln(stderr, oneline.Escape(line))
		default:
			return refused(stderr, "the session's reply carries no verdict the grammar spells: "+line)
		}
	}
	return code
}

// cmdEvents is the events verb. Its flags are parsed with flag's usage dump discarded, so
// a bad value is one refusal line and not a banner.
func cmdEvents(args []string, stdout, stderr io.Writer, deps Deps) int {
	fs := flag.NewFlagSet("events", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	addr := fs.String("redis", "", "")
	repo := fs.String("repo", "", "")
	base := fs.String("base", "dev", "")
	ghPoll := fs.String("gh-poll", "60s", "")
	deadline := fs.String("deadline", "", "")
	consumer := fs.String("consumer", "", "")
	bench := fs.String("bench", "", "")
	logPath := fs.String("log", "", "")
	once := fs.Bool("once", false, "")
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp { return printVerbHelp(stderr, "events") }
		fmt.Fprintf(stderr, "nova-work events: %s; run: nova-work help\n", oneline.Escape(oneline.Cap(err.Error(), oneline.TailBytes)))
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "nova-work events: takes no positional arguments, got %d; run: nova-work help\n", fs.NArg())
		return 2
	}
	if *addr == "" {
		fmt.Fprintf(stderr, "nova-work events: --redis is required; it wants the address of the pub/sub instance; run: nova-work help\n")
		return 2
	}
	poll, err := time.ParseDuration(*ghPoll)
	if err != nil || poll <= 0 {
		fmt.Fprintf(stderr, "nova-work events: --gh-poll is a positive duration such as 60s, got %q; run: nova-work help\n", oneline.Escape(*ghPoll))
		return 2
	}
	var bound time.Duration
	if *deadline != "" {
		if bound, err = time.ParseDuration(*deadline); err != nil || bound <= 0 {
			fmt.Fprintf(stderr, "nova-work events: --deadline is a positive duration, got %q; run: nova-work help\n", oneline.Escape(*deadline))
			return 2
		}
	}
	if !*once && bound <= 0 {
		fmt.Fprintf(stderr, "nova-work events: the loop form requires --deadline; a loop with no deadline is a process nobody can tell from a stuck one; run: nova-work help\n")
		return 2
	}

	// The structured sink of SPEC-LOGS.md Part 2. Its default is stderr, which under
	// systemd is the unit's journal and so a source Alloy already reads without a new
	// agent; --log names the file Alloy tails instead, for a bench whose supervisor is
	// not systemd. A path that cannot be opened is a refusal here and not a silent run
	// with no log: a bench whose lines never reach Loki must say why, at the start.
	events := stderr
	if *logPath != "" {
		f, err := os.OpenFile(*logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			fmt.Fprintf(stderr, "nova-work events: --log %s cannot be opened for append: %s; run: nova-work help\n",
				oneline.Field(*logPath), oneline.Err(err))
			return 2
		}
		defer f.Close()
		events = f
	}

	rdb := deps.Dial(*addr)
	defer rdb.Close()

	var forge ci.Forge
	if *repo != "" {
		forge = deps.Forge(*repo, *base, poll)
	}
	p := ci.NewProducer(rdb, forge, *consumer, stderr)
	p.Events = events
	p.Bench = benchName(*bench)
	if deps.Now != nil {
		p.Clock = deps.Now
	}

	started := time.Now()
	p.Announce(ci.EventStart, fmt.Sprintf("events: bridging %s with gh-poll %s",
		oneline.Field(ci.StreamCardsDone), oneline.Field(poll.String())), 0, nil)

	ctx := context.Background()
	if bound > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, bound)
		defer cancel()
	}

	if *once {
		cards, err := p.PublishCardsDone(ctx)
		if err != nil {
			fmt.Fprintf(stderr, "nova-work events: %s\n", oneline.Err(err))
			p.Announce(ci.EventRefuse, "events: the stream could not be read", time.Since(started), err)
			return 1
		}
		polls := 0
		if forge != nil {
			polls, err = p.PollOnce(ctx)
			if err != nil {
				fmt.Fprintf(stderr, "nova-work events: %s\n", oneline.Err(err))
				p.Announce(ci.EventRefuse, "events: the forge could not be polled", time.Since(started), err)
				return 1
			}
		}
		fmt.Fprintf(stdout, "EVENTS OK once=true card-done=%d published=%d\n", cards, polls)
		p.Announce(ci.EventDone, fmt.Sprintf("events: one pass, card-done %d, published %d", cards, polls), time.Since(started), nil)
		return 0
	}
	if err := p.Run(ctx, poll); err != nil && err != context.DeadlineExceeded {
		fmt.Fprintf(stderr, "nova-work events: %s\n", oneline.Err(err))
		p.Announce(ci.EventRefuse, "events: the bridge stopped before its deadline", time.Since(started), err)
		return 1
	}
	fmt.Fprintf(stdout, "EVENTS OK once=false deadline=%s\n", oneline.Field(bound.String()))
	p.Announce(ci.EventDone, fmt.Sprintf("events: the bridge reached its deadline %s",
		oneline.Field(bound.String())), time.Since(started), nil)
	return 0
}

// benchName is the bench label on every structured line: the flag when given, else
// $NOVA_BENCH, else the short hostname. It is the fleet's name for this machine, which is
// what a LogQL query selects on, and it is read here rather than in internal/ci so a test
// of the producer injects it and never reads the environment.
func benchName(flagValue string) string {
	if s := strings.TrimSpace(flagValue); s != "" {
		return s
	}
	if s := strings.TrimSpace(os.Getenv("NOVA_BENCH")); s != "" {
		return s
	}
	h, err := os.Hostname()
	if err != nil {
		return ""
	}
	if i := strings.Index(h, "."); i > 0 {
		h = h[:i]
	}
	return strings.TrimSpace(h)
}
