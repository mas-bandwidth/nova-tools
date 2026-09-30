# Deprecated tools: command reference

## nova-work

`nova-work` is both the thin client of the resident work session ([docs/SPEC-WORK.md](SPEC-WORK.md), "The engine and its client") and the in-process reader of the job graph and bounded `.work` plans ([SPEC-JOBS.md](SPEC-JOBS.md), [SPEC-WORKLANG.md](SPEC-WORKLANG.md)). As a client it sends one request line over the Unix socket `--session` names and prints the session's one answer line, byte for byte; the session is the engine and owns every fact, so the client refuses to guess and second-guesses nothing. The graph and plan verbs read files as data, never as programs.

```
nova-work: the thin client, the job graph and the bounded .work reader (see docs/SPEC-WORK.md, docs/SPEC-JOBS.md, docs/SPEC-WORKLANG.md)

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
  nova-work verification --sexp <path> --repo <dir> (--check | --write) [--timeout <duration>]

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
  nova-work verification   runs the suite at HEAD, lists STALE and PROPOSE criteria, --write rewrites :verification

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

A plan is read as data, never as a program: a `#.` dispatch macro anywhere in code
position is refused at exit 2 naming its byte offset, string and comment text is opaque,
and an unknown :kind is refused naming the field. :needs is the reference edge and
:blocks its inverse, so the kernel derives whichever a node did not give; an absent
need is refused naming the field and the id, and a :needs cycle is refused by validator
rule 3, both at load before the graph is published. A hand-written :node owes :kind,
:output and :budget before it can expand: :output must name a :branch, and :budget must
name :minutes, :tokens and :model-floor; a node owing several is refused naming every
field it owes in one run, never one round trip per field.

set check reads the OTHER top form of the same language: not `(:plan ...)`, the
expander's, but `(work-set "id" ... :units ((unit ...)))`, the one a coordinator
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
bare repository per repo under --cache, fetched --depth 50 and deepened (the depth
doubled) only while the PR's window start or merge base lies past it; a window the
clone could not reach is unknown, never no (#3404). The base is
--base, else the set's :base, and one is required. Each evaluable criterion prints one
SET EVAL line with holds=yes|no|unknown and a why=; a criterion gh or git could not
answer is unknown and counts as not done. A unit is decided by its criteria when every
one is evaluable or one fails; a unit naming only :test, :job or :attested criteria keeps
its :status. Each question is asked once per run, and every PR the criteria name is read
before any is evaluated, in one gh GraphQL call per repo (100 PRs to a call; #3460); a PR
that call did not answer is read alone by REST.
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
```

The session verbs `session start`, `session status` and `session stop` speak the socket protocol; `SESSION OK` is one shape printed by all three alike. A missing `--session` (the socket has no default path) or a socket nothing answers is one `WORK REFUSED` line on stderr, exit 2, ending `run: nova-work help`. The session's own refusals — `FAIL`, `RACED`, `REFUSED` — reach stderr and exit 1. The graph and plan verbs read the JSON dependency graph and the bounded `.work` plan as data: `plan check` and `plan expand` require a plan path and default to 65,536 bytes, 64 levels of nesting and 4,096 atoms (`--max-bytes`, `--max-depth`, `--max-nodes`); unknown kinds, absent dependencies and dependency cycles refuse. `plan expand` writes card directories for explicit `:node` entries and does not launch them; existing cards are left unchanged when expanding again. `dependencies --graph <file>` reads the graph and `--node <id> --needs <id,id>` writes dependency edges; `ready` prints whether each requested node's dependencies are terminal and accepted without acquiring a lease or reserving a slot. `set check` reads the other top form of the same language — `(work-set "id" … :units ((unit …)))`, the one a coordinator writes by hand — through that same bounded reader, and validates its content: a duplicate id, a `:needs` naming a unit nobody defined, a cycle, an `:owner` no `--minds` registry names, a `:lane` no `--lanes` file names, a `:deadline` that is not an instant. Every rule runs over every unit in one pass and each finding is one `SET` line, so a defective set costs one run rather than one run per defect. The two exit codes stay apart: exit 2 is a file that could not be read at all, exit 1 is a file read whole whose content is wrong, and the `SET OK units=… ready=… blocked=… owned=…` summary prints either way. `--ready` adds the mechanical ready set — a unit is done when it says so (`:done`, or a `:status` of closed, done, landed or merged) or when `--done` names it, and ready when it is not done and every need is done — so what can be pulled is derived from the language rather than maintained by hand. `nova-work help` also describes `clip`, which commits and harvests a worker's result before resetting its worktree; use that mutating workflow only with the intended worktree, branch, base and harvest destination.

Readiness is only half the question, so each SET READY line carries the other half: the
ADMISSION verdict from internal/jobs (SPEC-JOBS section 9, SPEC-WORKLANG A5 to A9).
`ready` is whether a unit's needs are closed, which the language answers; `admit` is
whether its resource vector is free, which the kernel answers. The ready units are run
through one admission in written order, so the ones marked `admit=go` are a set that may
run TOGETHER -- one live unit per lane (A6), no two intersecting :writes (A7) -- rather
than a list each of which could run if the others did not. A held unit prints what held
it and who holds it:

  SET READY unit=certify:verb owner=rowan-child lane=pulse deadline=- admit=go on=- by=-
  SET READY unit=harvest:bench owner=rowan-child lane=pulse deadline=- admit=held on=lane:pulse by=certify:verb

A held unit never holds the ones after it: A9 says a unit goes when its OWN needs are
closed and its OWN vector is free, so the pass neither stops nor waits at a refusal. The
authority here counts lanes and writes only -- set check reads a file and knows no bench
-- so a unit naming cpu or memory is reported as held on that dimension rather than
silently granted against a capacity nobody counted.

