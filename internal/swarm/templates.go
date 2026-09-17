package swarm

import (
	"fmt"
	"sort"
	"strings"
)

// THE TEMPLATES ARE THE SINGLE HIGHEST-VALUE THING IN THIS SPEC.
//
// A task is a text, and a bare text produces a bare answer. The conditions below are what
// turned one worker from 62 of 67 accurate with 5 wrong and 25 duplicate (batch 1) into 17
// of 17 with 0 wrong and 0 duplicate (batch 2), and a file budget turned 0 of 3 complete
// into 2 of 3 (batch 3). In the prototype they lived in a task text a person retyped, which
// means they were sometimes retyped and sometimes forgotten. Here they are text in the
// binary, printable, and a caller may write their own file instead: the tool has no list of
// blessed task shapes.
//
// A TEMPLATE IS TEXT AND NOTHING ELSE. It is not code, it does not execute, and nothing in
// this package reads a worker's RESULT.md and acts on it.

const templateReadPR = `read-pr — read one pull request against the rules

1. READ THE PR BODY'S OWED LIST FIRST, before reading any code, and for every
   finding you report, say whether it is already on that list. A finding that
   is already owed is marked ` + "`dup:`" + ` and is not a new finding.
   [batch 1: 25 of 67 findings were duplicates of the owed list]
2. QUOTE EVERY RULE VERBATIM, with ` + "`file:line`" + `. Never paraphrase a rule from
   memory, and never assert a rule you did not open.
   [batch 1: 5 of 67 findings were wrong, each a paraphrase]
3. APPEND EACH FINDING TO RESULT.md THE MOMENT IT EXISTS. Not at the end.
   You may be killed at your deadline; what is on disk is what you found.
4. A FILE BUDGET: read at most <n> files (the task's --files). When the budget
   is spent, write what you have and stop. Say in RESULT.md which files you
   did not open.
   [batch 3: with a budget, 2 of 3 tasks complete; without, 0 of 3]
5. A RESULT.md CONTAINING ONLY A PLAN IS A FAILED TASK. The plan belongs at
   the top, before the work; the findings are the work. A finished read that
   found nothing is NOT a failed task: write the ` + "`## Head`" + ` with ` + "`findings: 0`" + `.
   Never report a finding to have something to report.
6. If a board was supplied, check it before reporting: a card that already names
   this is a ` + "`dup:`" + `. Do not search for an unspecified board.
7. A SEVERITY FLOOR: emit only findings at or above ` + "`HIGH`" + `. A finding below the
   floor is not emitted at all. State the floor in RESULT.md's ` + "`## Head`" + `
   paragraph as ` + "`floor: HIGH`" + `, and mark each emitted finding with its
   severity. The floor decides which findings are emitted, not how they are
   written: every emitted finding still quotes its rule verbatim with ` + "`file:line`" + `.

Keep RESULT.md concise: omit progress narration, praise, repeated task text, and a
separate summary. Each finding keeps its proof in compact form: severity, ` + "`file:line`" + `,
the exact quoted rule, the fix, and ` + "`dup:`" + ` status when applicable. Retain every valid
finding, its context and evidence, and any coverage limitation; do not drop context or
evidence by default. Brevity is a soft target: never hard-truncate findings or proof; if
the report overflows, preserve the proof and say so. Preserve the complete RESULT.md
shape and its mandatory ` + "`## Head`" + `, ` + "`## Findings`" + `, ` + "`## Per item`" + `, ` + "`## Gates`" + `,
` + "`## Left owed`" + `, and ` + "`## One line`" + ` sections.
In Gates, distinguish source checks from tests and report-writing commands.
Mark only checks actually performed as pass; no tests run does not mean no commands run.
`

const templateProbeRow = `probe-row — make one claim true or false

1. Name the claim in one sentence at the top of RESULT.md before probing it.
2. The probe is a command, a file:line, or a measurement — never an opinion.
   Paste the command and its tail into RESULT.md.
3. Append the result the moment you have it.
4. A file budget: read at most <n> files (the task's --files). When the budget
   is spent, write what you have and stop.
5. A probe that could not be run is a RESULT with ` + "`not done`" + ` and the reason.
   That is a complete task; a guess is not.
`

const templateFixCard = `fix-card — take one card and land the fix

1. Read the card, and the board, before touching anything: a card already taken
   is a ` + "`dup:`" + ` and you stop.
2. Work only inside the job directory. The clone is yours; nothing outside it
   is yours.
3. Quote the rule the fix serves, verbatim, with file:line.
4. Write the gate you ran and its result into RESULT.md's Gates table. A fix
   with no gate is ` + "`not done`" + `.
5. A file budget: read at most <n> files (the task's --files). When the budget
   is spent, write what you have and stop.
6. Leave what you did not do under ` + "`Left owed`" + `, named so the next worker can
   pick it up with no other context.
`

// templateResult is the ONE shape a report has, so the fold is mechanical and a person
// reads counts. The parser in result.go parses exactly this and nothing else.
const templateResult = "# <task>\n" + `
## Head
findings: <n>
notes read: <n>
repo: <owner>/<name>
rev: <sha>
<one paragraph: what was asked, what the state is now, and the single most
important fact.>

## Findings
- <one finding, appended the moment it exists: what was found, the rule it rests
  on quoted verbatim between backticks, and the file:line it is beside. A finding
  already on the owed list begins ` + "`dup:`" + `.>

## Per item
| item | state | evidence |
| --- | --- | --- |
| <the item as it was handed to me> | red / green / not done | <file:line, gate name, PR #, or the command and its tail> |

## Gates
| name | result | seconds |
| --- | --- | --- |
| <gate or command> | pass / fail / not run | <n> |

## Left owed
- <the item nobody did, named so the next worker can pick it up with no other
  context.>

## One line
<one sentence a coordinator can paste into the board.>
`

// templateWorker is the ONE FILE A FIRST RUN CANNOT START WITHOUT, and it was the one with
// no template: the audit guessed `"env"`, met a good refusal listing the fields, and had to
// read a refusal to learn a schema (S1 and S2, 2026-09-11). Every value in angle brackets
// is a thing only the caller knows; everything else is the shape this tool reads.
const templateWorker = `{
  "name": "<what this worker is called on a RUN POOL line>",
  "provider": "<the provider id the harness config declares, such as deepseek>",
  "model": "<the model id>",
  "env_var": "<the NAME of the variable the provider reads>",
  "key_file": "<the path of a file holding one line, mode 0600, OUTSIDE worker_dir>",
  "usage": "opencode",
  "harness": "<the harness command on PATH>",
  "harness_args": ["run", "--model", "{model}", "--", "{prompt}"],
  "worker_dir": "<the home copy of this worker's own directory>",
  "deadline": "20m",
  "board": "<owner/repo#issue, or omit>"
}
`

// templateSetup is the PER-FRIEND SAFETY-SETUP AGREEMENT (#184): one form per friend,
// reviewed and agreed BEFORE any staged security implementation is built, because a
// blanket restrictive setup prevents useful work and ignores each friend's chosen harness,
// while a blanket permissive one hands every friend every other friend's secrets. It is
// the issue's near-term endpoint and nothing beyond it: the generic configuration examples
// and the agreement/evidence template, published with placeholder values only. No secret,
// no private path and no live bench layout is in it -- the private configuration it stands
// for stays on the friend's own bench -- and an agreed form supplies no account access.
// It is a document and not a task's conditions, so WrapTemplate refuses it as it refuses
// `result`.
const templateSetup = `setup — one friend's safety setup, proposed, reviewed, agreed (#184)

One form per friend, agreed before it is enforced, because a blanket restrictive
setup prevents useful work and ignores each friend's chosen harness, while a
blanket permissive one hands every friend every other friend's secrets. Print
it, fill it with the friend who would run under it, and paste the FILLED form
where the review happened; the private configuration it describes is never
pasted anywhere. Names, models, harnesses and bench layouts are not constants
of this form: every value below is a placeholder, and a friend's own choices
fill their own copy. A friend may propose an alternative, decline, or stay
silent, and missing feedback is pending, never assent. An agreed form is an
agreement and nothing more: it supplies no account access, and implementation,
credential migration and deployment are separate staged work with their own
authorization.

## The proposal (written with the friend)

friend: <name>
bench: <the machine or hosted runner this friend works on>
harness: <the harness this friend chose, and its version>
model: <the model this friend chose; never this form's business>
proposal by: <who wrote this form, and where the review is recorded>
reviewed with: <the friend's own read of this form, or pending>

read scope: <the shared inputs this friend needs to read, named once>
write scope: <this friend's own directories, and nothing above them>
execution boundaries: <one task, one process tree, one deadline, or the
  friend's own boundary and who holds it>
secret use: <the ONE seat file that holds this friend's keys, and the ONE
  variable name the harness reads; a value is never written here>
destructive controls: <what a delete, a force-push or a repository
  destruction must be unable to reach, and which ruleset forbids it>
recoverability: <what is pushed where on every exit, so a delete is a
  re-clone>
unresolved concerns: <what this friend has not agreed to, in their own words>

## The agreement (the friend's own half)

status: agree | alternative | decline | pending
alternative proposed: <the friend's own setup, in their own words, or ->
declined because: <the reason, kept honestly, or ->
pending since: <the date feedback was asked for>

## The guarantee table (filled together, one row per guarantee)

| guarantee | who enforces it | supported here | evidence |
| --- | --- | --- | --- |
| a read outside the named lists is denied | the OS wall | yes / no | nova-sandbox probe |
| a write outside the write set is denied | the OS wall | yes / no | probe step write_outside |
| no credential file is readable inside the wall | the OS wall and the caller's placement | yes / no | probe --secret <path> |
| no agent socket or agent address reaches the child | the OS wall and the environment scrub | yes / no | SPEC-SANDBOX rules 7 and 9 |
| a push from inside the job fails | the OS wall | yes / no | the four mechanisms of SPEC-SANDBOX test 27 |
| the harness asks before an outside path | a cooperating harness | yes / no / unproven | the fence example below |
| the friend's work survives a delete | the launcher, outside the wall | yes / no | the push on exit, a re-clone recovers |
| the friend's secrets stay the friend's | the seat file's own recipients | yes / no | nova-secrets check |

A row the OS wall enforces is a fact the kernel keeps on the machine this form
names. A row a cooperating harness enforces is a row the wall must not be
asked to prove: mark it unproven until the friend's harness build is shown to
honour it, and call a row supported only on the machine and the build this
form names.

## The generic examples (placeholder values only, never a private one)

the wall, one job, its lists written down in one place and never guessed:
  HOME=<data home> nova-sandbox --read <the shared reference checkout>
    --read <the worker home> --write <the job directory>
    --write <the data home> -- <the friend's harness> <args...>

the fence, the harness's own permission block, allow or deny, never ask:
  {"permission": {"external_directory": "deny", "webfetch": "<the friend's choice>"}}

the seat, one file per friend, sealed to that friend's bench key alone:
  nova-secrets exec --store <the store's working copy> --as <this friend>
    --key <the key path> --sops <the sops binary>
    --only <ONE variable name> --require <ONE variable name> -- <launcher>

the launcher, outside the wall, the friend's own lists:
  sets HOME inside a --write, passes the credential by environment read as
  data before the wrap, and pushes the friend's directories to their remote
  on every exit, clean or not.

## What is never in this form

No secret, no key, no token and no private path is ever written into this
form, a task card, a bus note, an issue or a token ledger: a name or a path
is not a secret, but a value is, and this form carries values for nobody.
Before any staged implementation is built on an agreed form, validate it on
synthetic secrets and disposable repositories and record both runs:
a denied destructive operation and successful permitted work.
`

// templateCapacity is the OFFERED-CAPACITY AND ROUTING-LOG FORM (#176): a manual
// census of one friend's bounded, expiring capacity offer, plus the coordinator's
// manual routing log that matches dependency-ready work to compatible offers
// without double-counting shared pools. It is the issue's near-term endpoint and
// nothing beyond it: a form a friend and a coordinator fill together, reviewed
// before any automatic scheduler is built. Five capability kinds are kept apart
// (coordinator, direct worker, one-shot, swarm and local) because model slots
// are not interchangeable throughput units. The offer's named fields (friend,
// instance, bench, model identity and basis, harness, supported task types,
// demonstrated strengths and limits, permitted scope, current availability,
// concurrency, expected queue/latency and shared-limit pool references) are
// placeholders, never a friend's own live values, and an expiry bounds the offer.
// The routing log names the four acceptance rows the issue asks for, in words:
// an idle compatible pool receiving ready work, an incompatible offer being
// skipped, shared capacity counted once, and a stale offer excluded. Missing
// contact is unknown; stale capacity is not proof of failure and not proof of
// consent. No key, no token, and no private host detail is ever written here --
// credentials stay on the friend's own bench -- and a form is a form, never a
// task's conditions, so WrapTemplate refuses it the way `result` and `setup`
// are refused.
const templateCapacity = `capacity — one friend's offered capacity and the manual routing log (#176)

One offer per friend, bounded and expiring, published before any automatic
scheduler is built, because an idle pool receiving ready work, an incompatible
offer being skipped, shared capacity counted once, and a stale offer excluded
are four separate things a coordinator has to do by hand first, in a form a
friend fills and a coordinator reads. Print the offer half, fill it with the
friend whose capacity is being advertised, and paste the FILLED offer where the
review happened; the private configuration it stands for stays on the friend's
own bench. Print the routing log half, fill it with the ready work and the
offers considered, and record the matching decision in writing so the next
review can compare the count against the offers' own quotas. Names, instances,
benches, models, harnesses and pool layouts are not constants of this form:
every value below is a placeholder, and a friend's own choices fill their own
copy. A friend may propose an alternative, decline, or stay silent, and missing
feedback is pending, never consent. A filled form supplies no account access,
and an automatic scheduler is separate staged work with its own authorization.
A form carries one capacity kind at a time from the five the SPEC-WORK friend
section distinguishes — coordinator, direct worker, one-shot, swarm and local —
because model slots are not interchangeable throughput units and a swarm
worker, a one-shot, and a local model run on different evidence and different
shared-limit pools.

## The offer (the friend's own half)

offered by: <who wrote this offer, and where the review is recorded>
reviewed with: <the friend and a coordinator, or pending>
expires: <the stamp this offer stops being an offer, never blank>

friend: <name>
instance: <the worker home or container this friend runs under>
bench: <the machine or hosted runner this friend works on>
model identity: <the provider's id and the resolved model id>
basis: <the per-token cost class — zero|flat|metered — and its pricing reference, or local>
harness: <the harness this friend chose, and its version>
supported task types: <read, text, code, replay; one or more, a comma list>
demonstrated strengths: <what the friend has been shown to do well, never an inferred claim>
demonstrated limits: <what the friend has been shown unable to do, never an inferred claim>
permitted scope: <the repositories and paths this offer may read and write>
current availability: <awake | resting | credit-limited | rate-limited | unknown — never idle because a recent message did not arrive>
concurrency: <the maximum parallel slots this offer reserves>
expected queue/latency: <the queue depth and the latency a scheduler can expect, bounded>
shared-limit pools: <the named pools whose quota this offer shares, or ` + "`[]`" + ` for none>

## The routing log (the coordinator's half)

ready work: <the dependency-ready task list being matched this cycle>
compatible offers: <the offers whose supported task types and permitted scope admit the ready work>
incompatible offers: <the offers skipped this cycle, with one reason each — wrong task type, scope mismatch, basis mismatch, capacity kind, anything but a name>
shared pool share: <the share of the named shared-limit pools, counted ONCE per pool across all offers naming it>
stale offers excluded: <the offers whose expires stamp has passed or whose contact stamp is past the silent-ping window, named and never counted>
utilization denominator: <the explicit pool-specific denominator this cycle's utilization would be reported against — a coordinator, direct worker, one-shot, swarm and local each have their own>

## The four rows acceptance evidence demands (one row each, when they occurred this cycle)

| observed | row to write |
| --- | --- |
| an idle compatible pool receiving ready work | offer=<name> task=<id> routed=true admit-gate=<gates that passed> |
| an incompatible offer being skipped | offer=<name> task=<id> reason=<what rules it out> |
| shared capacity counted once | pooled-as=<pool> reservations=<n> offers-with-that-pool=<n> shared-share=<n> |
| a stale offer excluded | offer=<name> expires=<stamp> contact=<stamp or NONE> reason=<expired or unconfirmed> |

missing contact is unknown; **stale capacity is not proof of failure and not proof of consent**, so an offer nobody answered since the silent-ping
window is excluded, not favoured and not penalised, and reported as
` + "`reason=unconfirmed`" + ` alongside any expired offer reported as
` + "`reason=expired`" + `. Each capacity kind from the SPEC-WORK friend section
gets its own row when the offer names it — **coordinator capacity** is its
own row, **direct worker capacity** is its own row, **one-shot capacity** is
its own row, **swarm capacity** is its own row, and **local capacity** is its
own row — because model slots are not interchangeable throughput units, and
sharing a quota across those kinds is the double-count the form exists to
prevent.

## What is never in this form

no key, no token, and no private host detail is ever written here, a task
card, a bus note, an issue or a token ledger: a name or a path is not a
secret, but a value is, and this form carries values for nobody. A shared
account limit is named by its pool, never by the credential that holds it.
An offered capacity is not a purchase, a permission, or a promise to run;
it is the standing under which a coordinator may propose ready work, and a
friend chooses offers, reserves, and rest, not a scheduler that maximises
occupation beyond that offer.
`

// Template returns one template by name.
func Template(name string) (string, error) {
	switch name {
	case "read-pr":
		return templateReadPR, nil
	case "probe-row":
		return templateProbeRow, nil
	case "fix-card":
		return templateFixCard, nil
	case "result":
		return templateResult, nil
	case "worker":
		return templateWorker, nil
	case "setup":
		return templateSetup, nil
	case "capacity":
		return templateCapacity, nil
	}
	return "", fmt.Errorf("--name wants one of %s, got %q", strings.Join(TemplateNames(), ", "), name)
}

// TemplateNames is every name Template answers to, in a fixed order.
func TemplateNames() []string {
	names := []string{"read-pr", "probe-row", "fix-card", "result", "worker", "setup", "capacity"}
	sort.Strings(names)
	return names
}

// WrapTemplate puts a task's own text under its template's conditions, with the file budget
// written into the condition that names it, so the number in the prompt is the number the
// machinery will hold the worker to.
func WrapTemplate(name string, files int, text []byte) ([]byte, error) {
	body, err := Template(name)
	if err != nil {
		return nil, err
	}
	if name == "result" {
		return nil, fmt.Errorf("--template wants a task template (read-pr, probe-row, fix-card); `result` is the report's shape, printed by `template --name result`")
	}
	if name == "setup" {
		return nil, fmt.Errorf("--template wants a task template (read-pr, probe-row, fix-card); `setup` is the per-friend agreement form of issue #184, printed by `template --name setup`")
	}
	if name == "capacity" {
		return nil, fmt.Errorf("--template wants a task template (read-pr, probe-row, fix-card); `capacity` is the per-friend offer and routing-log form of issue #176, printed by `template --name capacity`")
	}
	body = strings.ReplaceAll(body, "<n> files", fmt.Sprintf("%d files", files))
	var b strings.Builder
	b.WriteString("## The conditions (they are worth more than the model)\n\n")
	b.WriteString(body)
	b.WriteString("\n## The task\n\n")
	b.Write(text)
	if len(text) > 0 && text[len(text)-1] != '\n' {
		b.WriteString("\n")
	}
	return []byte(b.String()), nil
}
