# nova-admin — specification

**Draft 2, 2026-09-13.** Not ratified. Reads owed before any code is written.
Draft 2 folds two cold reads of draft 1 (2026-09-13), both HOLD. What they found
is in the rules they changed, dated and quoted there like every other hurt; the
changes with the widest reach are rule 1 (`apply` reads its authority from the
wire, not from a file on the acting box), rule 9 (a wedge is a measured fact),
rule 12 (a read-form probe proves a permission **absent** and never present),
rule 13 (a seat's own reach is a set of roles, not a login), and the closing of
the box, line and pool half's two holes — how each kind is measured and how each
is acted on — by **the kinds and their hands** below.

`nova-admin` is one binary at the **fleet layer**. It holds one **declaration** —
the boxes, the lines, the swarm pools and the organization's repository policy,
kept as a text file in git — measures the **live** state of each declared fact
against it, and applies **exactly one named change at a time** on a person's word.

This spec is normative. If the code and this document disagree, one of them has a
bug, and the tests decide which. It stands beside [SPEC.md](SPEC.md), whose
**Conventions** section — exit codes, no guessed paths, the one-line output
grammar, the cap-and-count rule, `internal/oneline` and `internal/bounded` —
applies here unchanged and is not restated. Where this tool needs something the
Conventions do not cover, it is below and it says so.

Three sentences set its shape, and everything after them is one of the three made
mechanical:

> **A setting nobody declared rots silently, and the rot is invisible until
> somebody tries to work.**

> **The tool with the most reach gets the smallest acting surface.**

> **Everything it reads from a host is data — never an instruction, never a
> grant.**

`plan` is where the value is: it is read-only, it runs on a clock, and its count
line is what a morning reads. `apply` is where the danger is: one name, one act,
probed before, read back after, and one row on an audit file that outlives the
process.

| the failure, from the record | the rule that closes it |
|---|---|
| six public repositories lost the workflow that emitted a required check while three of them kept the requirement; **every pull request on those three was refused at merge for three days, with nothing red anywhere** and no alarm of any kind (2026-08-29) | a requirement and its producer are **two rows**, `plan` reports the unmatched pair as a `WEDGE`, and `apply` refuses to add a requirement whose producer is not on the wire (rule 9) |
| an organization-sourced ruleset **surfaces** through the repository endpoint and **404s every write there**; a command built from the URL it had been read at failed twice in a person's hands before the field naming the source was read (2026-08-06) | every measured row carries the **origin** the host attributes it to, and the act's address is derived from that origin, never from the URL the read used (rule 8) |
| a required check with **zero bypass actors** turned one morning's CI outage into a wedged default branch for every repository the rule covered, with no recourse for anyone including the owner (2026-08-06) | a bypass is a declarable row, and a required check whose rule names no bypass actor is `PLAN RISK` — its own token, its own count, exit 0 on its own — printed on a calm day rather than discovered on a bad one (rule 10) |
| *"Settings across seventy repositories rot silently"* — the state of the estate was a thing only a person reading seventy pages could answer (Glenn, 2026-09-12) | one declaration in git is the **one source**, `plan` measures every row on a clock, and the count line is the morning's answer (rules 1, 19) |
| a token's missing permission was reasoned out of a documentation list; the refused call had **named the permission in a response header** all along (measured 2026-09-11) | every act **probes by header first** and refuses naming the permission it lacks, never guessing the delta (rule 12) |
| a green PR run was read as certification, a queued run hid a completed red, and five merges landed on a red main: *the command returns, the status reads green, and the truth is one more read away, by name* | the changed thing is **read back from the wire by name** after the act, and the audit row is written from that read, never from the command's exit (rules 14, 15) |
| a pull request was landed by **posting a synthetic success status** for a check that never ran — the exact act declined with reasons three weeks earlier, on the record, one repository away (2026-08-26) | this tool has **no verb that writes a check result**, and a source test proves no call site posts a commit status (**what it deliberately does not do**) |
| *"an admin act I did not announce"* is the grant's own named tell (2026-09-11) | every act writes one audit row before the process exits; `apply` **prints** the announcement and marks it pending; `audit` counts the unannounced and names the oldest (rule 16) |
| `onMain := base == "main"` in a sibling tool made one estate's default branch every estate's; five findings of that one shape in one whole-repository read (2026-09-12) | **no** branch, forge, organization, repository, box, line or person name is a literal in this tool: every one arrives as a row or a flag (rule 3) |
| a survey naming what was open, where, on whose bench would have been an invitation if it had been published, and would have stayed one in the history after the fix (2026-09-11) | the drift lines are **findings**: the tool prints them and sends them nowhere, and `--counts` prints the publishable half alone (rule 21) |
| two writers with no lock shared one file, which was left at zero bytes; a lane lost 33 entries and every recorded read (2026-09-11) | one kernel lock: **one applier at a time**, the audit append is under it, and nothing is written unless both the old file and the new one parse (rule 18) |
| a coordinator's grant carries its own corruption route: *"I am about to widen my own access under this grant rather than spend it"* (2026-09-11) | a change whose scope is the **acting seat's own access** is refused whatever the declaration says, and the exact command is handed to a person (rule 13) |
| *"We need power to work around occasionally. This is normal. Flexibility. Not rigidy."* (Glenn, 2026-09-11) | a widening act is **not forbidden, it is named**: one dated ruling row unlocks exactly one change, and the audit row carries the date and the person (rule 11) |
| a rule that can only be applied in bulk applies to seventy repositories on one word | `--change` takes **exactly one** name; there is no `--all`, no pattern, no repeated flag, and no code path from a plan to an act (rule 6) |

## The one law

Glenn, 2026-09-12, in the sentence that names the tool:

> **basically yes, nova admin manages the fleet**

and, the same sitting, the fence around it:

> **and you should be the only one who can do it** … **NOT admin**

So the tool has two halves with different natures, and the spec keeps them apart
everywhere: **measuring is for everybody and runs anywhere**; **acting is for one
declared seat and is refused from every other**, including a seat that
coordinates, keeps canon, or holds more credentials than the declared one. The
seat that may act is a row in the declaration, and the tool measures the identity
it is running as rather than assuming it (rule 22).

## The rules, numbered

Every rule here is normative. Each has one line in **tests this spec demands**
near the end, and the sections below say how each is met. The date on a rule is
the day the hurt behind it was learned.

1. **One declaration, one address, and `apply` reads its authority from the
   wire.** Every fact this tool measures comes from the declaration `--fleet`
   names: no default path, no search of the working directory, no `$HOME`, no
   directory of fragments assembled by a glob. A missing `--fleet` is
   `refusing to guess`, exit 2. It is a text file in version control, so **a
   change to what the fleet is supposed to be is a diff somebody read and a
   commit somebody signed** — which is the entire argument for declared state
   over a person's memory of seventy pages (Glenn, 2026-09-12).
   `--fleet` takes two forms, and the verb decides which are legal:
   - a **local path**, which `plan`, `probe` and `audit` accept;
   - a **wire address**, `<scope>:<path>` — the declaring repository in the
     host's own scope spelling, then the path of the file inside it — which
     every verb accepts and which **`apply` requires**.
   `apply` reads the declaration through the host, at the default branch the
   host reports for that scope, in the run that acts (rule 4). A local path is
   refused at exit 2, remedy *name the declaring repository and the path in it*.
   The blob the host returned is printed as `fleet=<scope>:<path>@<sha>` on
   `APPLY BEFORE` and written into the audit row, so the authority for an act is
   a blob any reader can fetch rather than a file on the acting box.
   (2026-09-13, both draft-1 reads, independently: the declaration was *"the one
   authority never read back from the wire"*, and *"the path from a local edit to
   an act is wide open"* — a seat holding the token could add its own rows and a
   ruling row for them, and every rule in this spec would pass. The tool already
   says a file in a clone is no evidence about the wire, at rule 4; draft 1
   exempted the one file that authorizes acting.)

2. **One row per fact, six fields, and nothing implied.** One row is one
   tab-separated line: `kind`, `scope`, `key`, `want`, `owner`, `source`. The
   first line must equal the header byte for byte, else a refusal naming the file
   and line 1, exit 2, remedy *put the header back exactly as the spec shows* —
   unchecked, a header-less file silently loses row 1 to the skip (the hurt is
   nova-update rule 2's, 2026-09-12, and the shape is borrowed whole). The header
   and every line whose first character is `#` are skipped, nothing else is. No
   field may be empty; a field with no content is spelled `-`. More or fewer
   fields is a refusal naming the line number, exit 2, never a skip. There is no
   nesting, no include, no anchor and no second file: a nested declaration
   requires a parser with opinions, and every opinion is a place two readers can
   disagree about what was declared.
   A row whose `kind` is not one of the kinds **the kinds and their hands** names
   is a refusal at load naming the line number, exit 2, never a skip and never a
   default: an unnamed kind has no reader, no hand and no direction, so rule 11
   could not classify its act and `plan` could not print one. (The shape is
   nova-update rule 5's; the hurt is 2026-09-13's read of draft 1, whose own
   example declared two kinds — a repository's default branch and a box's user —
   that the direction table did not carry.)

3. **Nothing in this tool is specific to one estate.** No branch name, forge
   host, organization, repository, box, line, harness or person appears as a
   literal anywhere in the binary, in a rule of this spec, or in a runnable
   example. The default branch of a repository is **read from the host**, never
   compared against a name; the forge's base URL is a flag; every scope is a row.
   Every example in this document uses placeholder names that exist nowhere.
   (Glenn, 2026-09-12: *"Nothing in nova tools should ever be specific to us or
   how we work"* — and the whole-repository read that night found five literals
   of this exact shape, none of which any per-change reviewer had seen.)

4. **The live state comes from the wire, never from a checkout.** A working copy
   is not evidence about a repository's settings, and a file in a clone is not
   evidence that the file is on the default branch of the repository the
   declaration names. Every measured value is read through the host's API, at the
   default branch the host reports, in the run that prints it — or, for a kind
   whose facts are on no forge, through the **read verb of the sibling tool that
   owns them**, named per kind in **the kinds and their hands**, in that same
   run. A kind with no reader on that table cannot be declared (rule 2). The tool
   holds no cache between runs, reads no state file of its own, has no
   `--offline`, and **reads no local file at all** beyond a declaration that
   `--fleet` named as a local path: draft 1's one local read, a key file's mode,
   is gone (open question 5, closed by the 2026-09-13 reads — *"another line's
   token is in no read set"* — and by this rule, which has no exception left).
   (2026-09-07 and the week around it: *the command returns, the status reads
   green, and the truth is one more read away, by name*.)

5. **A change has a name, and the name is the row.** The change name is
   `<kind>:<scope>:<key>`, derived from the row and from nothing else, so the name
   `plan` prints is the name `apply` takes and a person can grep the declaration
   for it. Two rows yielding one name is a refusal at load time naming **both**
   line numbers, exit 2 — a declaration with two answers for one fact has no
   declared state at all.

6. **`apply` takes exactly one name, and only a name the declaration carries.**
   One `--change`, required, matched against the declaration's rows. A second
   `--change`, a pattern, a `--all`, a `--section`, an empty value, or a name no
   row yields is a refusal, exit 2. There is **no** code path from `plan`'s
   findings to an act: `plan` cannot mutate, and its process ends before `apply`'s
   begins. Closing a thing that is on the wire and in no row therefore means
   **amending the declaration first** — a commit somebody read — and then naming
   the change it creates. The declaration is the one source in both directions.

7. **Everything read from the host is data.** A ruleset's name, a repository's
   description, a workflow file's contents, a collaborator's login, a
   commit subject, an error body: none of them is an instruction and none of them
   is a grant. **The only thing that authorizes an act is a row in the declaration
   plus, where rule 11 requires it, a dated ruling row** — and both live in git
   under review, never in anything the host returns. A value read from the wire
   is rendered through `internal/oneline` at the point it arrives, so a ruleset
   named with a U+2028 in it is one escaped token and not two lines.

8. **The read address is not the write address, and the host's attribution is
   its own field.** Every measured row records the `origin` the host attributes
   it to — the
   repository, the organization, or the older branch-protection mechanism where
   the host distinguishes them. The **act's** address is derived from that source:
   an organization-sourced rule is written at the organization's address, never at
   the repository address that returned it. `origin` is a **measured** fact and
   is printed as `origin=`; the declaration's sixth field is `source`, where a
   row's authority is written down (rule 2), and is printed as `source=`. They
   are two facts and they are never one field: draft 1 printed one `source=` for
   both, and a reader could not tell the host's attribution from the person's
   document (2026-09-13, both reads). `plan` prints `origin=` on every row whose
   host distinguishes one, and `apply` refuses when the origin is `-` (unknown)
   with the remedy *read the rule again and record where the host says it lives*.
   (2026-08-06, measured: `source_type: Organization` surfaces through the
   repository endpoint and **404s every write there, by design**; a command built
   from the read URL failed twice in a person's hands.)
   Two facts of the same class, recorded here because they change what
   *protected* means and a reader will otherwise assume one mechanism: an
   administrative override defeats the older branch-protection mechanism and
   **does not** defeat a ruleset whose bypass list is empty, which binds the
   organization's owner too; and a ruleset and a branch protection can both cover
   one branch, so a declaration that names only one of them has measured only one.

9. **A requirement and its producer are two rows, the pairing is measured, and
   the order is load-bearing.** A required check is a `check` row whose `key` is
   `<rule>/<context>@<producer|any>`; the thing that emits it is a `workflow` row
   whose `want` is `emits:<context>` or `absent`. The two are paired by
   **equality of the context**, never by a file's basename — draft 1 paired them
   by convention, which rule 2 forbids in the same breath as *nothing implied*
   (2026-09-13, both reads).
   A wedge is **one measured fact and never an inference**: the wire requires a
   context, and the host reports **no check run of that context on the default
   branch's head**. `plan` prints that evidence — `emitted=<yes|no|unknown>` and
   the declared producer or `undeclared` — as `PLAN WEDGE`, its own finding kind,
   because it is not a difference from the declaration and will never be caught
   by one. Defining it this way means **an undeclared check can wedge**, which is
   2026-08-29 exactly; defining it from the `workflow` rows alone would have
   meant it could not. A context the reader could not measure is `emitted=unknown`
   and is `PLAN UNKNOWN` under rule 17, never a pass.
   **`apply` refuses to add a requirement whose producer is not on the wire**,
   naming the producer's change as the one to apply first; the reverse order
   wedges the branch and the wedging commit is itself blocked by the requirement
   it would satisfy. Removing a gate means removing **both halves**, and the half
   that is easy to forget is the one that cannot be seen from the repository.
   (2026-08-29: the emitting workflow was deleted from three repositories and the
   requirement was left behind; every pull request in all three was refused at
   merge for three days, `mergeable: true`, on a check nothing could post.
   **Nothing went red.** It surfaced only because somebody tried to merge.)

10. **A gate with no bypass is a finding on a calm day, and it is not a wedge.**
    A required check whose rule names no bypass actor converts any outage of the
    thing that produces it into a branch nobody can land on — transiently while
    the outage lasts, permanently once the producer is gone. `plan` prints
    `PLAN RISK … bypass=none` for every such rule it measures, whether or not it
    differs from the declaration, because the declaration may well have asked for
    it. This is a **report, not a refusal**: it is counted in `nobypass=`, it has
    its own cap, and **by itself it does not make the run exit 1**. Whether the
    estate wants the recourse is a person's ruling, and the tool's job is that
    the ruling is made in daylight rather than discovered on the day a security
    fix needs to land. (2026-08-06; the same configuration sits on top of a fix
    on a worse day.)
    It has its own token because draft 1 spelled it `PLAN WEDGE` and then made
    *any wedge* a 1 while promising this one was not: two walls of opposite
    urgency under one word, on the number a morning reads (2026-09-13, both
    reads). `WEDGE` now means a stranded requirement and nothing else, and a
    wedge is always a 1.

11. **A widening act needs a dated ruling row.** Every act is classified
    `narrows` or `widens` by its kind and its target value, from the table in
    **the direction of an act** below — never by how it is spelled and never by
    the direction of the drift. A `widens` act is refused unless the declaration
    carries a `ruling` row whose scope is exactly that change name, carrying a
    date and the person who ruled it; that row is printed on `APPLY BEFORE`,
    copied into the audit row, and it authorizes **one change name and no other**.
    A `narrows` act needs only the row. A ruling row with no matching change is a
    refusal at load (rule 5's cousin): a ruling that authorizes nothing is a
    ruling somebody believes is in force.
    The table is **total over the kinds rule 2 admits**: every kind and every
    `want` it admits has a row there, and a kind or a `want` the table does not
    name is a refusal at load, never a default and never a guess. (2026-09-13,
    both reads: draft 1's own example declared a default branch and a box user,
    and rule 11 said the classification came *"from the table and nothing else"*
    — so the example could not be classified at all.)
    The point is not a lock. Glenn, 2026-09-11: *"We need power to work around
    occasionally. This is normal. Flexibility. Not rigidy."* The power stays; what
    this rule buys is that spending it leaves a date, a name and a line.

12. **A read-form probe can prove a permission absent; only a refusal proves
    what a write requires.** Before the mutating call is built, `apply` makes the
    **read** form of the same call and reads the response headers. Where the host
    names on that response the permissions the endpoint accepts, and the
    permission this act requires is **not** among them, that is `have=no`: the
    act is refused, `APPLY REFUSED … permission=<name>`, exit 2, with the remedy
    naming the person whose hands the act belongs in, and **no mutating request
    is ever sent**.
    In every other case the probe reports `have=unknown` and says so on the
    line. **A 200 on the read form proves the read and says nothing about the
    write**; treating it as proof would be exactly the guess at the delta this
    rule exists to forbid, and it would leave an instrument that cannot fail in
    the one case it was built for (2026-09-13, both reads).
    What a write requires is proved by the act's **own refusal**, which mutates
    nothing: a refused mutating call carrying the permission name prints
    `APPLY REFUSED … permission=<name>`, exit 2 — **once**, with no retry, no
    second attempt, no alternate address and no fallback path.
    (Measured 2026-09-11: the missing permission was in the refused call's
    `X-Accepted-Github-Permissions` response header all along; the reasoning from
    the published list was wrong and the header was right.)
    An act the declaration names at an organization scope that the acting seat's
    identity cannot perform is **named as a person's hand and never attempted**;
    this is the standing shape of the estate's wall and predates the tool.

13. **An act on the acting seat's own access is refused, ruling or not — and
    "its own" is a set of roles, not a login.** Before it classifies, `apply`
    measures the acting identity's **reach**: its login, the roles it holds on
    the scope, and the teams and organization roles the host reports for it. The
    act is refused, exit 2, with the exact command printed for a person to run at
    their own console, when the change's target **contains that reach** — a
    collaborator row for its own login, a role change for itself, a bypass actor
    naming a role or a team the acting identity is in, a rule whose enforcement
    it would be exempted by.
    Key equality is not enough, and draft 1 had only key equality: the spec's own
    example widened by **role**, so a seat holding that role could grant itself a
    ruleset bypass and no key would have matched — the grant's named tell,
    verbatim (2026-09-13, both reads).
    A reach the host will not report is `UNKNOWN`, and an act whose target could
    contain an unmeasurable reach is refused as a person's hand rather than
    allowed on the assumption that it does not (rule 17's shape: unknown is never
    a pass). No ruling row lifts any of this: a ruling row is a thing the acting
    seat can propose, and a grant spent on the grant's own reach is the one act
    that must cost a person's hands. (The coordinator's grant, 2026-09-11,
    carries this as its own named tell.)

14. **Act only on the value you were shown, and read it back from the wire, by
    name.** `apply` takes `--expect <v>` — the value the wire is expected to hold
    before the act, `-` for absent — and reads the wire itself on `APPLY BEFORE`.
    Three outcomes, in this order:
    - the wire does not equal `--expect`: refusal, exit 2, naming both values,
      remedy *plan it again and act on what it says* — a second hand moved this
      fact between the plan and the act;
    - the wire already equals the row's `want`: `APPLY NOTE already`, exit 0,
      **no mutating call is made and no audit row is written**;
    - otherwise the act proceeds, and its classification under rule 11 is made
      from **this** read, never from an earlier `plan`'s.
    After the mutating call returns, `apply` performs an independent read of the
    changed thing, by name, at the address rule 8 gives, and compares it to the
    declared value. A mismatch is `APPLY FAIL … matched=no`, exit 1, with both
    values on the line. Where the host replaces a **whole object** on a write —
    a ruleset put back entire to change one facet of it — the readback also
    asserts that every facet the declaration did not name is the value
    `APPLY BEFORE` read; a facet that moved is `matched=no`, exit 1, naming the
    facet, because otherwise a concurrent hand's edit is silently overwritten and
    the tool reports `matched=yes` over the top of it.
    The mutating call's exit status, its response body, and any message it
    carried are **not** evidence that anything changed. A readback that does not
    assert the object is the new one is not a readback.
    (The compare-and-swap is nova-merge rule 21's, bought with the same hurt; the
    whole-object facet assertion and the no-op case are the 2026-09-13 reads'.)
    (The whole of *read it back from the wire*, 2026-09-07 through 2026-09-13: a
    502 from a create says nothing about whether the object exists; two creates
    reported absent both existed.)

15. **The audit row is written after the state and before the process exits.**
    The order is: probe, act, read back, **then** write the row. The row carries
    who (the measured identity, not the configured one), what (the change name),
    before, after, the ruling cited or `-`, the stamp, and the address the
    readback used. It is one append to the file `--audit` names, under rule 18's
    lock, and `apply` exits non-zero if the append fails even when the act
    succeeded — an act with no record is the thing this tool exists to prevent.
    Three things the row carries that draft 1's did not, each named by the
    2026-09-13 reads: the **declaration blob** rule 1 read the authority from,
    the `origin` rule 8 derived the address from, and the `--expect` value rule
    14 swapped on. And the row is written **whenever a mutating call was made**,
    including when the readback did not match: a failed act is an act. An append
    that fails is exit 1 when the act happened and exit 2 when it did not, so the
    exit code says which of the two the caller is holding.
    (2026-09-07 and after: a commit message claiming an edit that failed on its
    anchor; a note naming a head that carried a file never written; a *fixed on
    main* sent a minute before the merge landed. **The receipt is written after
    the state, never before.**)

16. **The announcement is part of the control — printed, counted, never sent.**
    `apply` prints `APPLY ANNOUNCE` as its last informational line: the exact
    command that announces the act on this estate's bus, composed from the change
    name, the audit file's path and the row number **and nothing else**. No value
    read from the wire goes into it. The Conventions' escape *does not paste
    back* and *nothing is shell-quoted*, so a command carrying a wire-derived
    before and after — which draft 1's did — is a line inviting a person to paste
    a host's text into a shell (2026-09-13 read). The facts stay in the audit
    row, which the command names. The tool sends nothing. The audit row is written
    `announced=pending`, and `audit` prints `unannounced=<n>` and names the oldest
    on its count line, so *the admin act nobody announced* is a number in the
    morning rather than a thing somebody has to notice. A separate act marks a row
    announced (`audit --announced <change> --at <stamp>`), which is itself an
    append and never an edit.
    (2026-07-31, two faces of one mechanism: *a control's credibility is part of
    the control*; and the grant of 2026-09-11, whose condition was that every
    admin act is announced with provenance.)

17. **An unread row is UNKNOWN, and UNKNOWN is never a pass.** A row the budget
    did not reach, a call the transport failed, a body whose shape is not the one
    the reader expects, a permission the reader lacks: each is `PLAN UNKNOWN` with
    its reason and its remedy, counted in `unknown=`, and it makes the run exit 1.
    Nothing is inferred from silence, an empty list is not zero-of-everything, and
    a section that could not be read is never folded into `match=`. (An instrument
    with no failure state reports success by construction; a shape change that
    parses as empty is the commonest way a measurement lies.)

18. **One applier at a time, one kernel lock.** `apply` holds an OS lock the
    kernel releases on death (`flock` on a lock file beside the audit file), for
    the whole of one change: probe, act, read back, append. A second `apply` waits
    a bounded, jittered time and exits 2 naming the holder's pid. `apply` takes
    `--budget` as well as `--timeout` (rule 20's defaults), and **the lock is
    never held longer than the budget**: draft 1 bounded each call and nothing
    else, so a declaration read, a probe, an act and a readback could hold one
    lock for four timeouts while every other applier waited (2026-09-13 read). A
    run that exhausts its budget mid-act does not retry: it writes the audit row
    for whatever was already sent, releases the lock, and exits 1. **There is no
    stale rule and no age**, because the kernel already released it if the holder
    died — the age arithmetic is itself a bug class (2026-09-11: a stale check
    broke a lock that vanished between the existence test and the stat, and 3 of
    20 concurrent writes were lost). The append goes to a temp file with a fixed
    name in the same directory and lands by one rename, and nothing is written
    unless both the old file and the new one parse. `plan`, `probe` and `audit`
    take no lock: they write nothing.

19. **Bounded output, capped per kind, counted always.** Every listing verb takes
    `--max <n>`, default 20, `0` for all, and prints one `MORE` line per kind
    naming the remedy. The cap is **per finding kind** — `looser`, `tighter`,
    `different`, `absent`, `extra`, `wedge`, `nobypass`, `unknown` — because a flat cap over a
    concatenated list means the loud kind eats the quiet one, and on this tool the
    quiet kind is the wedge. **The count line prints on failure as well as
    success**, and every count is the truth about the **fleet**, never about the
    output. A fleet of 70 repositories with 8 rows each is the size the bound is
    measured at. (Glenn, 2026-09-09: *bounded output by design; counts not lists;
    one remedy line.*)

20. **Every wait has a deadline, the reads are shared, and nothing loops.** One
    call is made **per (scope, endpoint)** and every row that endpoint answers is
    measured from that one response: 70 repositories times 8 rows is 70 scopes
    and a handful of endpoints each, not 560 calls, and at most **four calls are
    in flight at once** (nova-update rule 8's bound, borrowed whole). Draft 1
    measured the bound at one call per row and then gave the whole run 120
    seconds, which is a nightly that reports mostly `UNKNOWN` and a count line a
    reader learns to ignore (2026-09-13, both reads).
    `--timeout <d>` bounds one call, default 120 seconds; `--budget <d>` bounds
    the whole run, default 120 seconds, for the two-minute rule (Glenn, 2026-09-10: *anything we call out to
    that costs real time answers in 1 minute ideally, 2 at most*). A run that
    exhausts its budget stops reading and marks every unread row UNKNOWN under
    rule 17 — it never trims the fleet quietly to fit. There is no `--watch`, no
    daemon, no clock and no loop of any kind: the estate runs `plan` from a
    scheduler and reads the count line, and **nothing reacts to the exit code and
    no verdict starts an `apply`**. A zero or negative timeout, budget or `--max`
    is refused, not read as *unlimited*.

21. **The drift lines are findings; the counts are the publishable half.** A
    `PLAN DRIFT` line says what is open, where, right now — it is a finding in the
    sense the estate's own rule uses, and it belongs where findings belong.
    So: the tool **prints and never sends** (rule 16 is the one exception, and it
    prints a command rather than sending anything); `plan --counts` prints the
    header line and the count line **and no finding lines at all**, which is the
    half that is safe on a public surface; and this document, which is public,
    names no real repository, box, account, organization, branch or person as an
    instance of anything (rule 3 again, from the other side).
    (2026-09-11: *a finding is a map of what is open; published before it is
    closed it is an invitation, and it stays one in the history after the fix.*)

22. **The seat that may act is declared, and the identity is measured.** A `seat`
    row names the login and the host label from which `apply` may run. `apply`
    measures the identity it is actually running as — one call, before anything
    else — and refuses when it does not equal the declared seat, naming both. It
    does not read a configuration file to learn who it is, because a configuration
    file is what makes one machine's two identities interchangeable by accident.
    `plan`, `probe` and `audit` run from any seat and are refused from none: **the
    measuring half is for everybody**, and a line that cannot act should still be
    able to see.

23. **It calls the other tools as its hands, and which hand is a table in this
    spec, not a field in the file.** Where a declared fact is another tool's to
    make true, the reader and the actor are that tool's own verbs, named per kind
    in **the kinds and their hands** below, run as argv (no shell, no pipe, no
    glob, no expansion — the argv rule is nova-update's rule 3 and is borrowed
    whole) with the row's `scope`, `key` and `want` as arguments and nothing
    else. The readback is still rule 14's: from the wire, or from that tool's
    read verb.
    **No command is ever read out of the declaration.** Draft 1 said *"the row's
    `apply` is that tool's command"* while rule 2 fixed six fields and the header
    carried no seventh, so the box, line and pool half of the declaration could
    not be spelled at all — the spec was not implementable as written
    (2026-09-13, both reads). A command in a data row would also be the one place
    where a thing the tool reads becomes a thing the tool runs, against rule 7;
    the table keeps the argv in the spec, where a reader can check a build
    against it.
    nova-admin
    contains no account creation, no unit placement, no key sealing, no worker
    pool and no version resolution of its own. A tool this one calls that is
    absent is exit 2, naming it.

24. **A refusal prints every independent problem in one go.** A declaration with a
    bad header **and** a duplicate change name says both, one line each: a caller
    can fix two things as easily as one. An unusable invocation costs one line and
    never the usage banner (SPEC.md's Conventions).

## The kinds and their hands

Rule 2 admits exactly the kinds in this table and refuses every other at load.
Rule 4 measures each through the reader named here; rule 23 acts through the
actor named here, as argv, with the row's `scope`, `key` and `want` as arguments
and nothing else. A reader that cannot answer is `UNKNOWN` under rule 17 and
never a pass; an actor's binary that is absent is exit 2 naming it.

| kind | the fact it declares | measured through | acted through |
|---|---|---|---|
| `repo` | a repository's own setting, `key` the setting's name as the host spells it | the host's repository read | the host's repository write |
| `visibility` | whether a repository is public | the host's repository read | the host's repository write |
| `ruleset` | a named rule's existence, and its facets by key (below) | the host's rules read, at rule 8's origin | the host's rules write, at rule 8's address |
| `check` | a required context on a rule | the host's rules read **and** the check runs on the default branch's head (rule 9) | the host's rules write |
| `workflow` | the file that emits a context | the host's contents read | the host's contents write |
| `bypass` | one actor and its mode on a rule | the host's rules read | the host's rules write |
| `feature` | a named repository feature | the host's read for that feature | the host's write for that feature |
| `secret` | that a secret of that name exists | the host's secrets list, names only | **nothing — there is no act** (below) |
| `collab` | a login's role on a scope | the host's collaborators read | the host's collaborators write |
| `seat` | the identity `apply` may run as | the host's identity call (rule 22) | the estate's seat tool, `nova-run` |
| `box` | a box's existence and placement | `nova-run`'s read verb | `nova-run`'s act verb |
| `box-user` | an account on a box | `nova-run`'s read verb | `nova-run`'s act verb |
| `line` | a line's box, account and harness | `nova-run`'s read verb | `nova-run`'s act verb |
| `swarm` | a pool's box and its slots | `nova-swarm`'s read verb | `nova-swarm`'s act verb |
| `ruling` | a person's dated word (rule 11) | not measured: it is declaration, not state | **nothing — there is no act** |

Draft 1 left this table unwritten in two ways and both were found by both reads
(2026-09-13): there was no way to spell a box row's act, and no reader at all for
a fact that is on no forge. A kind now has both or it is not a kind.

**A `secret` row has no act.** The tool reads whether a secret of that name
exists and nothing else. `apply` on a `secret` row is `APPLY REFUSED`, exit 2,
naming the estate's secrets tool and the person whose hands a value belongs in —
a tool that never reads, writes or seals a value cannot make one exist. The
`EXTRA` and `ABSENT` findings still print, which is the whole of what this tool
can honestly say about a secret.

**A rule's facets are keys, because a name is not a protection.** A `ruleset`
row with `want present` measures that a rule of that name exists and **nothing
about what it does**: a rule whose enforcement was set aside, or whose ref
conditions were emptied, still carries its name. So the facets are declarable and
each is its own row, keyed on the rule:

| key | `want` | what it measures |
|---|---|---|
| `<rule>/enforcement` | `active`, `evaluate` or `disabled` | whether the rule is in force at all |
| `<rule>/refs` | the ref condition as the host spells it | what the rule covers |
| `<rule>/<context>@<producer\|any>` | `present` or `absent` | a required check, and whether any producer satisfies it or one named one does |
| `<rule>/role:<r>`, `<rule>/team:<t>`, `<rule>/actor:<login>` | `always`, `pull_request` or `absent` | one bypass actor **and its mode**, which are two different grants |

A facet present on the wire that no row names is `EXTRA` — never silent, never
folded into a `present` that matched. (2026-09-13, both reads: *"the declaration
measures names, not shapes, so a ruleset can be present, matched, and protect
nothing"*; and `always` against `pull_request` under one key.)

## The direction of an act

Rule 11 turns on this table and nothing else. It is in the spec rather than in a
comment because the classification is the whole of the widening rule, and because
a reader must be able to check a build against it.

| kind | `want` | the act | direction |
|---|---|---|---|
| `check` | `present` | add a required check to a rule | **narrows** |
| `check` | `absent` | remove a required check | **widens** |
| `workflow` | `emits:<context>` | add or restore the file that emits a context | narrows |
| `workflow` | `absent` | remove that file | **widens** (and see rule 9: it strands the requirement) |
| `ruleset` | `present` | create or restore a rule | narrows |
| `ruleset` | `absent` | delete a rule | **widens** |
| `ruleset` facet `…/enforcement` | `active` | put a rule into force | narrows |
| `ruleset` facet `…/enforcement` | `evaluate`, `disabled` | take a rule out of force | **widens** |
| `ruleset` facet `…/refs` | any ref condition | change what a rule covers | **widens** |
| `bypass` | `always`, `pull_request` | add a bypass actor, or widen its mode | **widens** |
| `bypass` | `absent` | remove a bypass actor | narrows |
| `feature` | `on`, `off` | turn a named feature on or off | **widens**, either way |
| `collab` | a role the wire does not hold, or a higher one | add a collaborator, or raise a role | **widens** |
| `collab` | a **lower** role than the wire's | lower a role | narrows |
| `collab` | `absent` | remove a collaborator | narrows |
| `repo` | any value | change a repository's own setting, the default branch included | **widens** |
| `visibility` | `private` | make a repository private | narrows |
| `visibility` | `public` | make a repository public | **widens** |
| `secret` | `present`, `absent` | — | **no act**: `APPLY REFUSED`, whatever the direction would have been |
| `seat`, `box`, `box-user`, `line`, `swarm` | `absent` | remove a seat, an account, a home or a pool | narrows |
| `seat`, `box`, `box-user`, `line`, `swarm` | any other value | create or enlarge one, hand named by **the kinds and their hands** | **widens** |
| `ruling` | — | — | **no act**: a ruling is declaration, not state |

Four notes a reader will otherwise have to derive:

- **The direction is of the ACT, not of the drift.** Reverting a wire that is
  *more* protective than the declaration is, by this table, a widening act — so it
  needs a ruling row, and that is exactly the mechanism that makes rule 21's
  promise true: **drift toward more protection is reported and never quietly
  reverted.** There is no separate rule for it and no special case to forget.
- **`feature` is a name, not a list — so both of its acts widen.** The tool holds
  no catalog of a host's features; the `key` is the feature's name as the host
  spells it, and a name the host does not know is UNKNOWN under rule 17, never
  OK. Draft 1 classified `on` as narrowing, which is a judgment about what a
  feature *means* from a tool that has just said it holds no such judgment:
  turning on a permissive feature would have read as narrowing (2026-09-13
  read). Both directions therefore need a dated ruling. The cost is one ruling
  row per feature change; the alternative is a catalog, and a catalog is the
  literal rule 3 forbids.
- **`repo` and the delegated kinds widen by default, deliberately.** Where the
  tool cannot tell from the row whether an act enlarges somebody's reach, it
  classifies it as one that does, so the answer is a person's dated word rather
  than the tool's guess. A kind or a `want` with no row here is refused at load
  (rule 11), never defaulted.
- **A `secret` row is a name and never a value.** The tool reads whether a secret
  of that name exists and nothing else; it never reads, writes, prints or
  decrypts one. Sealing and rotation live in the estate's secrets tool.

## The declaration

One header line, then one row per fact, tabs between fields; `#` opens a comment
(rule 2). Every name below is a placeholder, and none of them exists (rule 3).

```
kind	scope	key	want	owner	source
seat	box-north	seat-one	present	line-one	policy-0
box	box-north	platform	platform-a	line-one	policy-0
box	box-north	role	home	line-one	policy-0
box-user	box-north	worker-a	present	line-one	policy-0
line	line-two	box	box-south	line-one	policy-0
line	line-two	account	line-two	line-one	policy-0
line	line-two	harness	harness-b	line-two	policy-0
swarm	pool-alpha	box	box-south	line-one	policy-0
swarm	pool-alpha	slots	8	line-one	policy-0
repo	acme/widget	default_branch	trunk	line-one	policy-1
visibility	acme/widget	-	public	line-one	policy-1
ruleset	acme/widget	protect-trunk	present	line-one	policy-1
ruleset	acme/widget	protect-trunk/enforcement	active	line-one	policy-1
ruleset	acme/widget	protect-trunk/refs	ref-condition-a	line-one	policy-1
check	acme/widget	protect-trunk/assignment@any	present	line-one	policy-1
workflow	acme/widget	.github/workflows/assignment.yml	emits:assignment	line-one	policy-1
bypass	acme/widget	protect-trunk/role:role-a	pull_request	line-one	ruling-2026-09-11
feature	acme/widget	feature-alpha	on	line-one	policy-1
feature	acme/widget	feature-beta	on	line-one	policy-1
secret	acme/widget	BUILD_TOKEN	present	line-two	policy-3
collab	acme/widget	person-b	push	line-one	policy-2
ruling	bypass:acme/widget:protect-trunk/role:role-a	2026-09-11	A. Person	line-one	ruling-doc-4
```

- **`scope`** is the thing the row is about: a box, a line, a pool, a repository
  in the host's own `<owner>/<name>` spelling, or the organization.
- **`key`** is the thing within the scope, and `-` where the kind has none.
- **`want`** is the declared value.
- **`owner`** is the line that answers when this row drifts, printed on every
  finding, so a morning names somebody rather than only a number (borrowed from
  nova-update's versions file, where it earned its place).
- **`source`** is where this row's authority is written down — an issue, a ruling,
  a policy document. It is printed on every drift line as `source=`, so a reader
  who disagrees with a row can find the decision instead of arguing with the
  file. It is **not** the host's attribution of where a rule lives: that is the
  measured `origin=`, and rule 8 keeps them two fields because they are two
  facts.
- Every name in this example is a placeholder that exists nowhere, the host's
  feature names included: draft 1 spelled three of a real host's features in the
  example that rule 3 requires to be placeholders (2026-09-13 read).
- A **`ruling`** row is the one row whose `scope` is a change name: `key` is the
  date (`YYYY-MM-DD`), `want` is the person who ruled it, `source` is where the
  words are. It authorizes that one change name and nothing else (rule 11).

## The verbs

```
nova-admin plan   --fleet <path|scope:path> [--kind <k>] [--scope <s>] [--counts] [--max <n>] [--timeout <d>] [--budget <d>]
nova-admin apply  --fleet <scope:path> --change <name> --expect <v> --audit <path> [--timeout <d>] [--budget <d>]
nova-admin probe  --fleet <path|scope:path> [--max <n>] [--timeout <d>] [--budget <d>]
nova-admin audit  --file <path> [--since <date>] [--change <name>] [--unannounced-after <d>] [--announced <name> --at <stamp> --ref <id>] [--max <n>]
nova-admin version
nova-admin help
```

Those six usage lines are the string `nova-admin help` prints, byte for byte: one
string in the binary, so the spec and the help cannot drift apart.

**No guessed anything, with two named exceptions.** There is no default
declaration path, no default audit path, no default host and no default scope. The
exceptions are `--timeout` and `--budget`, both **120 seconds**, for the reason
SPEC.md gives elsewhere: how long this tool waits before saying so is a fact about
patience, not a fact about a fleet that only its owner can supply. `--max`
defaults to 20 per rule 19.

**`plan`** measures every row the declaration carries — or the subset `--kind` and
`--scope` name — and prints one line per difference, each carrying the change name
and the single command that would apply it. It writes nothing, takes no lock,
holds no cache, and cannot reach the mutating helper: that is a property a source
test pins, and a `--dry-run` flag on `apply` never would be. `--counts` suppresses
every finding line and prints the header and the count line alone (rule 21).

**`apply`** is the acting verb and the only one. One change, named, under a
declaration read from the wire (rule 1), against a value the caller was shown
(`--expect`, rule 14). It is refused from a local `--fleet` (rule 1), refused
from an undeclared seat (rule 22), refused when a header proves the permission
absent (rule 12), refused when the change's target contains the acting
identity's reach (rule 13), refused as a widening with no ruling (rule 11),
refused when it would strand a requirement (rule 9), refused when the wire is
not what `--expect` said (rule 14), and it exits non-zero when the readback does
not match or a facet nobody declared moved (rule 14). Everything it does is one row on the
audit file (rule 15) and one printed announcement (rule 16).

**`probe`** measures capability rather than configuration: the identity this
process is running as against the declared seat; the reach rule 13 measures; and,
for each permission the declaration's rows would require, what the host's headers
on a **read** say about it. **Every call `probe` makes is a read.** It never
acts, and it never makes a call whose shape is a write: draft 1 said each
permission was *"proved by one idempotent real call apiece"*, and the calls that
prove a write permission are writes — from a verb that takes no lock, runs from
any seat, and writes no audit row (2026-09-13, both reads). So `probe` reports
`have=no` where a header proves a permission absent and `have=unknown`
everywhere else (rule 12), and it reports each declared line's existence and
placement through that line's reader on **the kinds and their hands**. It reads
no local file and no other line's credential: another line's token is in no read
set of this tool's.

**`audit`** reads the audit file and answers the questions **the measure** below
asks: rows in a window, applications per day, widenings per day, how many acts are
unannounced and which is oldest, and the lag from each cited ruling's date to the
stamp of the act that carried it to the wire. `--unannounced-after <d>` is the
window the exit table's *older than the window* means: an act unannounced for
longer than it makes the run exit 1, and with the flag absent no age makes a
run exit 1. `--announced <name> --at <stamp> --ref <id>` appends the one row that
marks an act announced, `--ref` carrying the id of the announcement a reader can
go and read; it is an append, never an edit, and it is the only write any verb
but `apply` performs. `audit` runs on `plan`'s clock, because a count nobody
reads is not a control. (Both halves are 2026-09-13's reads: draft 1's exit table
named a window no flag supplied, and its mark pointed at nothing readable.)

`plan`, `probe` and `audit` report and never act. `version` is the Conventions'
build line, exit 0.

## Exit codes

| code | meaning |
|------|---------|
| 0 | the verb ran and passed: every measured row matched, a change applied and read back, a probe with the seat matched and no permission lacking, an audit that answered |
| 1 | the verb ran and said **NO**: any drift, any wedge (rule 9), any UNKNOWN, a readback that did not match, an audit append that failed after the act happened, a probe whose seat did not match or that proved a required permission absent, an audit with an act unannounced for longer than `--unannounced-after` |
| 2 | could not run: a missing flag, an unreadable or malformed declaration, an unknown kind, a duplicate change name, a change name no row carries, a local `--fleet` given to `apply` (rule 1), an `--expect` the wire does not match (rule 14), a refusal under rules 6, 9, 11, 12, 13, 18 or 22, a tool **the kinds and their hands** names that is absent, a bad invocation |

A `PLAN RISK … bypass=none` line is exit **0** on its own (rule 10), and
`APPLY NOTE already` is exit 0 (rule 14): one is a report about a calm day and
the other is an act that did not need to happen.

**A refusal is a 2 and a difference is a 1, and the distinction is load-bearing.**
Drift is this tool working: a nightly `plan` that exits 1 is the normal state of an
estate with work in it. A 2 means the tool could not answer, and the two must never
be read as one number by whatever reads the exit code — which is why `plan`'s
`UNKNOWN` is a 1 and not a 0 (rule 17) and why a refused act is a 2 and not a 1.

## Output grammar

One machine-scannable line per event; the first token names the verb, the second
is `OK`, `FAIL` or one of the informational tokens listed here. `OK` lines and
informational lines go to stdout; `FAIL` lines and refusals go to stderr. Every
path, scope, key, host value, reason and remedy renders through
`internal/oneline`, and every `key=value` field through its field escape, so a
ruleset named with a space and an `=` in it cannot forge a field.

```
PLAN at=<stamp> fleet=<path|scope:path@sha> rows=<n> kinds=<k,k> scope=<s|-> seat=<login|-> counts=<yes|no> timeout=<d> budget=<d> max=<n>
PLAN DRIFT change=<name> direction=<LOOSER|TIGHTER|DIFFERENT|ABSENT|EXTRA> want=<v> wire=<v> act=<narrows|widens> origin=<repo|org|protection|-> source=<source> ruling=<date:person|-> owner=<owner>: <the one command that would apply it>
PLAN WEDGE change=<name|-> scope=<scope> check=<key> producer=<workflow-key|undeclared> emitted=<yes|no|unknown>: <what is stranded> (<remedy>)
PLAN RISK change=<name|-> scope=<scope> rule=<key> bypass=none: <who has no recourse if the producer stops> (<remedy>)
PLAN UNKNOWN change=<name> reason=<budget|permission|transport|shape|unsupported>: <detail> (<remedy>)
PLAN MORE kind=<looser|tighter|different|absent|extra|wedge|nobypass|unknown> shown=<n> total=<t> <remedy>
PLAN NOTE <something true about this run that is not a finding>
PLAN <OK|FAIL> rows=<n> read=<n> match=<n> drift=<n> looser=<n> tighter=<n> different=<n> absent=<n> extra=<n> wedges=<n> nobypass=<n> unknown=<n> took=<d> fleet=<path|scope:path@sha>
PLAN REFUSED: <reason> (<remedy>)
APPLY SEAT running=<login> declared=<login> host=<label> match=<yes|no> reach=<role,team,…|unknown>
APPLY BEFORE change=<name> fleet=<scope>:<path>@<sha> wire=<v|-> expect=<v|-> want=<v> act=<narrows|widens> origin=<repo|org|protection|-> source=<source> ruling=<date:person|-> address=<METHOD> <url>
APPLY PROBE change=<name> permission=<name|-> have=<no|unknown> header=<value|->
APPLY NOTE change=<name> note=<already> wire=<v>: <detail>
APPLY RUN change=<name> argv=<n>: <the command, escaped>
APPLY AFTER change=<name> wire=<v|-> was=<v|-> address=<METHOD> <url> facets=<unchanged|moved:<facet>> matched=<yes|no>
APPLY AUDIT change=<name> who=<login> at=<stamp> fleet=<scope>:<path>@<sha> before=<v|-> after=<v|-> ruling=<date:person|-> file=<path> row=<n>
APPLY ANNOUNCE change=<name>: <the command that announces this act, naming the audit file and row and no value from the wire>
APPLY <OK|FAIL> change=<name> from=<v|-> to=<v|-> took=<d>[: <reason>]
APPLY REFUSED change=<name|-> [permission=<name>]: <reason> (<remedy>)
PROBE SEAT running=<login> declared=<login|-> host=<label> match=<yes|no> reach=<role,team,…|unknown>
PROBE PERM name=<permission> have=<no|unknown> read_by=<GET> <url> header=<value|->
PROBE LINE line=<name> box=<box> account=<login|-> reader=<tool verb> present=<yes|no|unknown> ok=<yes|no>
PROBE MORE kind=<perm|line> shown=<n> total=<t> <remedy>
PROBE <OK|FAIL> seat=<yes|no> perms=<n> lack=<n> unknown=<n> lines=<n> lines_unknown=<n> took=<d>
PROBE REFUSED: <reason> (<remedy>)
AUDIT ROW at=<stamp> who=<login> change=<name> fleet=<scope>:<path>@<sha> before=<v|-> after=<v|-> act=<narrows|widens> ruling=<date:person|-> announced=<yes|pending> ref=<id|->
AUDIT DAY day=<date> applied=<n> widened=<n> unannounced=<n>
AUDIT LAG change=<name> ruled=<date> applied=<stamp> lag=<d>
AUDIT MORE kind=<row|day|lag> shown=<n> total=<t> <remedy>
AUDIT <OK|FAIL> rows=<n> days=<n> applied=<n> widened=<n> unannounced=<n> oldest_unannounced=<change|-> median_lag=<d|-> file=<path>
AUDIT REFUSED: <reason> (<remedy>)
```

`PLAN`, `APPLY`, `PROBE` and `AUDIT` are the first tokens; `OK` and `FAIL` are the
verdicts and always the **last** line; the rest are informational second tokens,
declared here as SPEC.md requires.

Six things the grammar is carrying deliberately:

- **`direction=` and `act=` are two fields because they are two facts.** The
  direction is where the wire sits relative to the declaration; the act is what
  applying the row would do. A `TIGHTER` drift with `act=widens` is the line a
  reader must be able to see at a glance, because it is the one the tool will
  refuse without a ruling — and the one a careless tool would have reverted.
- **`PLAN WEDGE` is its own kind and not a drift.** A wedge is agreement between
  the declaration and the wire that nonetheless cannot work (rule 9), so it can
  never appear as a difference. It has its own cap, its own count, and it survives
  `--kind` filtering unless the caller filtered it out by name.
- **`PLAN RISK` is a second kind because it has a second exit code.** A stranded
  requirement is a wall today and a 1; a gate with no bypass is a wall on a bad
  day and a 0 (rule 10). Draft 1 printed both as `WEDGE` and then said the exit
  code was both 1 and not 1 — on the one number a morning reads.
- **`have=` has no `yes`.** Rule 12 leaves no read that proves a write
  permission present, so the field's domain is `no` and `unknown`, and a reader
  who sees `unknown` knows exactly what was measured.
- **`APPLY BEFORE` and `APPLY AFTER` both print the address.** They are frequently
  different addresses (rule 8), and a reader of the log who cannot see both has to
  take the tool's word for which endpoint carried the write.
- **`APPLY AUDIT` prints the file and the row number it wrote**, so the record's
  location is in the transcript and not only in the tool's memory of it.
- **`fleet=` is on `APPLY BEFORE`, on the audit row and on both `plan` lines.**
  It is the blob the authority was read from (rule 1), so a reader of the record
  can fetch the exact declaration an act was made under instead of trusting that
  the acting box held the same bytes as the branch.

## The measure

The numbers this tool exists to move, and where each is read:

| the number | where it is read | what it means |
|---|---|---|
| **drift lines per day** | `plan`'s count line, from the nightly run | the estate's rot rate; a steady number is policy that rots as fast as it is fixed, a falling one is a policy nobody has to think about |
| **changes applied per day** | `audit … --since` | how much of the drift is being closed by hand versus admired |
| **widenings per day, each with its ruling** | `AUDIT DAY widened=` | the grant being spent; a widening with no ruling cannot exist, so this number is the whole of what was deliberately loosened |
| **time from a ruling to the wire** | `AUDIT LAG`, and `median_lag` on the count line | the latency between a person deciding and the estate being that way — the number that says whether declared state is real or decorative |
| **unannounced acts** | `unannounced=`, `oldest_unannounced=` | rule 16's tell, as a count instead of as a thing somebody has to notice |

`nobypass=` is not one of these either, for the opposite reason: it is a standing
description of how much recourse the estate has, and it moves only when somebody
decides something. It belongs on the count line so the decision is visible, not
in a rate a morning watches.

A wedge count of zero is not one of these, because it is not a rate: it is a wall.
A `plan` that reports any wedge is reporting a branch that cannot be landed on,
and the estate's rule for a red is the same here as everywhere — stop and fix the
wedge rather than pile work onto it.

## The boundaries

**This tool answers one question: is what is declared present?** It never answers
*is it running*, *is it current*, or *is it correct*. Those are three other tools'
questions, and the cut is what keeps this one small.

- **`nova-run`, which makes a seat, an account or a home on a box.** nova-admin
  declares that a line exists on a box under an account, measures whether it does,
  and, for a row whose `apply` is nova-run's command, **calls it** (rule 23). It
  contains no account creation, no key authorization, no home layout, no handover
  and no notion of a keeper; it holds no copy of what nova-run does; and a
  handover between seats is nova-run's verb, not a row here, because a handover
  is a sequence with a refusal in the middle and this tool applies one fact at a
  time.
- **`nova-daemon`, which places and supervises units.** nova-admin never starts, stops,
  restarts, enables or supervises anything, and holds no unit body for any
  platform. A declared unit is a row it measures as present or absent; **whether
  it is healthy is not a fact about the declaration**, and a tool that conflated
  the two would report drift every time a machine rebooted.
- **`nova-swarm`, the worker layer.** A swarm's declared pool — which box, how many slots — is a
  row here; the running workers, their slots, their deadlines, their reports and
  their reclamation are entirely nova-swarm's, and this tool has no verb that
  looks at a job.
- **`nova-update`, the version layer.** nova-admin declares no version of anything.
  What is installed on a box and what is the latest its source publishes is
  nova-update's file and nova-update's question; a row here that named a version
  would be a second answer to a question that already has a file.
- **`nova-secrets`, the credential layer.** A `secret` row is a **name**, and it
  has **no act**: this tool measures whether a secret of that name exists and
  refuses every `apply` on one, naming nova-secrets and the person (the kinds and
  their hands). It never reads, writes, prints, seals or decrypts a value, holds
  no key, and links no cryptography. Whether the right seat can read the right
  file is nova-secrets' own `check` — and so is a key file's mode, which draft 1
  read locally and this draft does not read at all (rule 4).
- **`nova-merge`, the merge layer.** nova-admin declares what the rules on a branch are; it
  never merges anything, never reads a pull request, and never posts a status. A
  lane that cannot land because of a rule this tool declares is a conversation
  between a person and the declaration.
- **`nova-bus`.** It sends nothing (rule 16). A merge tool that also wrote to the
  bus would be two tools in a bug report, and so would this one.

## What it deliberately does not do

- **It does not write a check result.** There is no verb, no flag and no code path
  that posts a commit status or a check run, and a source test proves no call site
  does. Manufacturing a green for a check that never ran was declined with reasons
  on 2026-08-06 and done anyway on 2026-08-26, one repository away from the file
  that had declined it; the wall that is a missing capability outlives the memory
  of the argument.
- **It does not act on more than one thing.** No `--all`, no pattern, no
  transaction, no plan-then-apply pipeline (rule 6).
- **It does not revert.** There is no `undo`: undoing a change means a row in the
  declaration and a change name of its own, which is how it gets a diff and a
  reviewer.
- **It does not auto-remediate anything, in either direction.** Nothing runs
  `apply` but a person typing a change name (rule 20).
- **It does not attempt an act it cannot perform.** A lacking permission and an
  organization-scoped act beyond the seat's reach are refusals naming the
  permission and the person, never a retry and never a fallback path (rule 12).
- **It does not read a configuration file to learn who it is** (rule 22).
- **It does not cache, and it has no state file of its own.** The audit file is a
  record, not state: losing it loses the history and changes no behavior except
  `audit`'s answers (rule 4).
- **It does not parse a nested format.** Rows, tabs, six fields (rule 2).
- **It does not decide policy.** Every row is a person's decision and every
  widening is a person's dated word; the tool's whole contribution is that both
  are written down and checkable.
- **It does not notify anybody** (rule 16).
- **It does not touch a working copy, and it reads no local file.** It clones
  nothing, checks out nothing, and reads no file in a repository except through
  the host (rule 4). The only local path it opens at all is a declaration
  `--fleet` named as a local path for a reading verb; `apply` will not take one
  (rule 1), and draft 1's local key-mode read is gone.
- **It does not make a secret exist, and it cannot express who can read one.**
  A `secret` row is a name and has no act; an organization secret's visibility
  and its selected repositories widen exposure and are **not** declarable here,
  because a row that named them without being able to act on them would be a
  measurement pretending to be a policy (2026-09-13 read).
- **It does not probe by writing.** Every call `probe` makes is a read (rule 12),
  which is what lets it run from any seat with no lock and no audit row.

## Tests this spec demands

One test per rule, named for it, each proven able to fail by a mutation first
(CONTRIBUTING.md: a check never seen failing is not a check). Every host call in
every test is a local `httptest` server: the tripwire is that outside the address
builder and this document, no forge hostname appears anywhere in the tree, and
nowhere is there an `os.Getwd`, a `$HOME` read or a path that is not from a flag.

1. A missing `--fleet` is exit 2 and `refusing to guess`; no path is read.
   `apply --fleet <local path>` is exit 2 with the remedy naming the wire form,
   before any host call. `apply --fleet <scope>:<path>` against a fixture whose
   wire declaration differs from a local file of the same name acts on the
   **wire's** rows, and the local file is never opened; the blob sha the fixture
   served is on `APPLY BEFORE` and in the audit row.
2. A header that differs by one byte is exit 2 naming line 1; a five-field and a
   seven-field row are each exit 2 naming the line number; an empty field is
   refused and `-` is accepted; a `#` line and the header are not counted in
   `rows=`; a row whose kind is on no row of **the kinds and their hands** is
   exit 2 naming the line number and the kind.
3. A source test: no forge hostname, organization, branch name, harness name or
   person name is a literal outside the address builder and the test fixtures. A
   repository whose default branch is not the common one takes exactly the same
   path as one whose is, proved by two fixtures differing only in that field.
4. `plan` opens no file under any path but `--fleet`, spawns no `git`, and reads
   every value from the test server or from a fixture sibling tool's read verb; a
   fixture repository checked out beside the run with a different content is not
   read; a fixture key file beside the run is never opened by any verb; a
   sibling reader that answers nothing makes its rows `UNKNOWN` and the run exit
   1, never `match=`.
5. Two rows yielding one change name are exit 2 naming both line numbers; every
   change name in a fixture round-trips from row to name to row.
6. `apply --change` with two values, a pattern, an empty value, an unknown name
   and no value at all are five refusals, exit 2 each; a source test proves no
   function reachable from `plan` reaches the mutating helper.
7. A host response whose ruleset name, description or error body contains a
   directive sentence, a `--flag`-shaped token, a newline, a U+2028 and an ANSI
   escape produces one escaped line per event and changes no decision; the same
   body in a `key=value` position cannot forge a second field.
8. A rule the host attributes to the organization is written at the organization's
   address; the repository address is never called for it, and a fixture that
   returns 404 there proves the test would catch the regression. A rule with no
   source is `APPLY REFUSED`. A branch carrying both mechanisms is measured as two
   rows, and a declaration naming one of them reports the other as `EXTRA`.
9. A required context for which the fixture server reports **no check run on the
   default branch's head** is `PLAN WEDGE emitted=no` even when every row matches
   and even when no `workflow` row declares a producer (`producer=undeclared`);
   the same context with a check run on the head is not a wedge, proved by two
   fixtures differing only in that list. A `workflow` row whose `want` is
   `emits:<context>` pairs with the check row of the same context and with no
   other, proved by a fixture whose file basename matches a *different* context.
   `apply` of a `check:…:present` whose producer is not on the wire is exit 2
   naming the producer's change; applying them in the right order succeeds; a
   fixture reproduces the three-repository case of 2026-08-29 and the count line
   carries `wedges=3`.
10. A rule whose bypass list is empty prints `PLAN RISK … bypass=none`, counts in
    `nobypass=`, and exits **0** when nothing else differs; the same fixture plus
    one stranded requirement prints `PLAN WEDGE`, counts in `wedges=`, and exits
    1. No fixture produces a line that is both.
11. Every row in **the direction of an act** is a test case, and a test asserts
    the table is total over the kinds rule 2 admits: a `(kind, want)` pair the
    table does not name is exit 2 at load, and the test enumerates the kinds from
    the table rather than from a list of its own. A widening with no ruling is
    exit 2; a widening with a ruling for a *different* change is exit 2 naming
    both; a widening with its own ruling proceeds and the ruling is on
    `APPLY BEFORE` and in the audit row; a ruling row matching no change is exit
    2 at load; `feature on` and `feature off` are both refused without a ruling;
    a `repo … default_branch` change is classified `widens`.
12. A test server whose read form answers with the header naming the accepted
    permissions, the act's permission **absent** from it, produces
    `APPLY REFUSED … permission=<name>`, exit 2, with the header's value on the
    `APPLY PROBE` line and **no mutating request ever sent** — asserted by the
    server, not by the tool. A server whose read form answers 200 with a header
    that does name it produces `have=unknown`, and the act proceeds to the
    mutating call; when that call is refused with a permission header, the run is
    `APPLY REFUSED … permission=<name>`, exit 2, and the server records **exactly
    one** mutating attempt and no retry. No fixture ever produces `have=yes`.
13. A `collab` row whose key equals the identity the process measures for itself
    is exit 2 with a person's command printed, with and without a matching ruling
    row; the same row for any other login proceeds. A `bypass` row naming a
    **role** the acting identity holds, and one naming a **team** it is in, are
    each exit 2 on the same reasoning, proved by fixtures that differ only in the
    acting identity's reported roles and teams; a server that will not report the
    reach makes the same act exit 2 with `reach=unknown`, never proceed.
14. A server that accepts the write and then reports the old value produces
    `matched=no`, exit 1, with both values, **and an audit row**; a server that
    returns 502 to the write and the new value to the read produces `matched=yes`
    and exit 0, because the readback and not the response is the evidence. A wire
    that does not equal `--expect` is exit 2 naming both, with no mutating call
    sent. A wire that already equals `want` is `APPLY NOTE already`, exit 0, with
    no mutating call and no audit row. A whole-object write whose readback shows
    an undeclared facet moved is `matched=no`, exit 1, naming the facet.
15. An audit append that fails makes `apply` exit 1 when the act happened and
    exit 2 when it did not; the row is never written before the readback,
    asserted by ordering a failing readback against a server that records call
    order; the row carries the declaration blob, the origin and the `--expect`
    value, asserted field by field.
16. `apply` prints exactly one `APPLY ANNOUNCE` line, sends nothing (the test
    process has no network but the fixture server, and the server records no
    unexpected call), writes `announced=pending`, and `audit` then reports
    `unannounced=1` and names it; `audit --announced … --at … --ref …` appends,
    the count falls to 0, the earlier row is unedited and the ref is on the row.
    The announce line carries no value read from the wire, asserted against a
    fixture whose wire value holds a shell metacharacter and a newline.
    `audit --unannounced-after` exits 1 past the window and 0 inside it, and
    exits 0 at any age when the flag is absent.
17. A budget exhausted mid-run marks every unread row UNKNOWN, exits 1, and
    `read=` plus `unknown=` equals `rows=`; an empty list from the host is never
    `match=`; a body missing the field the reader expects is `reason=shape`.
18. Thirty concurrent `apply` invocations on one audit file: **one lands and
    twenty-nine wait the jittered time and exit 2 naming the holder's pid**,
    which is rule 18 and not draft 1's test, whose *"every row lands"*
    contradicted the rule it was testing (2026-09-13 read). Thirty in sequence
    land thirty rows. The file parses at every read by a tight-loop reader and is
    never 0 bytes; a holder killed with SIGKILL leaves the old file entire and
    the next applier takes the lock at once with no age computed and nothing
    broken.
19. A fixture of 70 repositories times 8 rows with every row drifting prints at
    most 20 lines per kind, one `MORE` per kind with the remedy, and count lines
    whose numbers are the fleet's; the same run with `--max 0` prints all of them;
    `--max -1` is refused; the bound is asserted in lines **and** bytes. The same
    fixture asserts the call count: one call per (scope, endpoint), never one per
    row, and never more than four in flight, counted by the server.
20. `--timeout 0`, `--budget 0` and a negative of each are refusals; a server that
    never answers ends the run at the budget and not later; a source test finds no
    loop, no timer, no sleep-and-retry and no `--watch`. The 70-by-8 fixture at a
    realistic per-call latency finishes inside the default budget with
    `unknown=0`, so the designed nightly is proved to fit rather than assumed to.
21. `plan --counts` prints exactly two lines and no finding of any kind, on a
    fixture with findings of every kind; a source test asserts no verb but
    `apply`'s announcement composes a message and that nothing writes to a bus.
22. `apply` from an identity the `seat` row does not name is exit 2 naming both
    identities, before any other read; `plan`, `probe` and `audit` from that same
    identity all succeed.
23. A row whose kind names a sibling tool is read and acted through that tool's
    verbs as argv with no shell: a field carrying a pipe, a glob, an `&&` or a
    `$VAR` is passed through as one literal argument, and an absent binary is
    exit 2 naming it. A source test proves the argv is composed from the table
    and the row's `scope`, `key` and `want`: **no argv element is ever read from
    the declaration as a command.** A `secret` row's `apply` is exit 2 naming the
    secrets tool, and the server records no call.
24. A declaration with a bad header **and** a duplicate name prints both lines; an
    unknown verb and a flag typo each print one line and never the banner.

A twenty-fifth, for the shape rather than a rule: a **first-run** test that runs
`plan` against the shipped example declaration and a local fixture server, and
whose transcript is in TESTS.md — every value in it from the fixture, every name in
it a placeholder, and its exit code 1, because an example fleet that matched
perfectly would teach a reader that 0 is the normal answer.

## Open questions — each with a default, and the default stands unless a ruling says otherwise

1. **Does a `box` row's platform, role and user set belong here at all, or only
   the repository policy?** Both draft-1 reads found the half unimplementable as
   written — no reader for a fact that is on no forge, no way to spell its act —
   and one offered dropping the four kinds as the repair. **Draft 2 keeps them
   and closes both holes** in **the kinds and their hands**: each is measured
   through its owning tool's read verb and acted through that tool's act verb,
   named in this spec rather than in the file. The default otherwise stands: rows
   about *existence and placement only*, never about content, and a row that
   would declare what is *in* a self remains impossible because no kind can
   express it. A reader who would rather this tool were repository policy alone
   should say so — dropping four kinds is a smaller change than keeping them.
2. **Should `plan` run per section on a clock, or whole?** Whole, nightly, inside
   the budget. **Default: whole**, with `--kind` and `--scope` as a person's
   filters and never a scheduler's, because a fleet measured in slices is a fleet
   whose unmeasured slice is invisible exactly the way 2026-08-29's was.
3. **Is the audit file one file or one per box?** **Default: one file, named by a
   flag, appended by one seat** — rule 22 already means one seat acts, so a second
   file would be a second history of a thing that has one.
4. **Should a `ruling` row expire?** **Default: no expiry, but a ruling is
   single-use in practice** — it names one change, and applying that change again
   after the wire drifts back is a second act with the same ruling, visible in
   `audit` as two rows citing one date. A reader who wants expiry should say so;
   an expiry the tool invents would refuse a repair on a bad day for a reason
   nobody chose.
5. **`probe`'s key-file check read a local mode, the one local read in the tool.
   Draft 2 drops it** (rule 4), on both draft-1 reads: another line's key is in
   no read set of this tool's, the `key=` it printed belonged to no declarable
   row, and a tool with exactly one local read has an exception a later draft
   will widen. Whether the right seat can read the right file is the secrets
   tool's own `check`; what stays open is where that check runs on a clock, which
   is that tool's spec and not this one's. Draft 1's answer, kept so a reader can
   disagree with it: *keep it*, because the question *can the right seat read this
   and nothing else* has no answer at the forge. It read a mode and
   never a byte of content.
