# nova-admin — specification

**Draft 3, 2026-09-13.** Not ratified. Reads owed before any code is written.
Draft 3 folds two cold reads of draft 2 (2026-09-13), both HOLD. What they found
is in the rules they changed, dated and quoted there like every other hurt. The
changes with the widest reach are all **deletions**:

- the four delegated kinds — a box, a box's user, a line and a swarm pool — are
  **gone**, and with them rule 23's argv: no sibling tool in this tree answers
  *which box holds this pool* or *does this account exist*, so by rule 2 those
  kinds had no reader and could not be declared. The tool now runs no
  subprocess at all (rule 23);
- a `workflow` row has **no act**, like a `secret` row: `emits:<context>` is not
  a file body, and a contents write is a direct commit to the default branch
  that the very rules this declaration declares are there to refuse (rule 9);
- `APPLY ANNOUNCE` prints **facts and no command**, so no literal naming an
  estate's bus survives rule 3 and nothing invites a paste;
- `--expect` carries a **digest**, not a host's text, so no wire value reaches a
  shell (rule 14).

The three repairs that are not deletions are rule 9 (a wedge is measured across
the heads a gate actually runs on, and a gate that has not run yet is not a
wedge), rule 12 (a read-form header names what the **endpoint** accepts and
therefore proves nothing about a token), and the third direction, `unjudged`,
which keeps `widened=` a security number.

`nova-admin` is one binary at the **fleet layer**. It holds one **declaration** —
the organization's repository policy, kept as a text file in git — measures the
**live** state of each declared fact against it, and applies **exactly one named
change at a time** on a person's word.

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
| six public repositories lost the workflow that emitted a required check while three of them kept the requirement; **every pull request on those three was refused at merge for three days, with nothing red anywhere** and no alarm of any kind (2026-08-29) | a requirement with no producer anywhere the host reports one is a `WEDGE` — measured across the heads a gate actually runs on, so a gate that has merely not run yet is not one — and `apply` refuses to add a requirement whose producer is absent from the wire (rule 9) |
| an organization-sourced ruleset **surfaces** through the repository endpoint and **404s every write there**; a command built from the URL it had been read at failed twice in a person's hands before the field naming the source was read (2026-08-06) | every measured row carries the **origin** the host attributes it to, and the act's address is derived from that origin, never from the URL the read used (rule 8) |
| a required check with **zero bypass actors** turned one morning's CI outage into a wedged default branch for every repository the rule covered, with no recourse for anyone including the owner (2026-08-06) | a bypass is a declarable row, and a required check whose rule names no bypass actor is `PLAN RISK` — its own token, its own count, exit 0 on its own — printed on a calm day rather than discovered on a bad one (rule 10) |
| *"Settings across seventy repositories rot silently"* — the state of the estate was a thing only a person reading seventy pages could answer (Glenn, 2026-09-12) | one declaration in git is the **one source**, `plan` measures every row on a clock, and the count line is the morning's answer (rules 1, 19) |
| a token's missing permission was reasoned out of a documentation list; the refused call had **named the permission in a response header** all along (measured 2026-09-11) | the permission is read from the host's own header on the **refused act**, and never guessed from a list — and a header that names what an *endpoint* accepts is not a measurement of what a *token* holds (rule 12) |
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
   **The threat this trades, named rather than left for a reader to find.** Rule
   7 says everything the host returns is data; rule 1 now reads the one thing
   that *authorizes* from that same host. The trade is deliberate and it is
   between two different attacks: a box the acting seat can edit is edited by
   one hand with no record, while the declaring repository is edited by a commit
   with a diff, an author and a history. The trade is only worth making while
   the declaring repository is itself protected, so:
   - the scope `--fleet` names **must itself be a declared scope** carrying at
     least one `ruleset` row in the declaration it is reading. A declaration
     that does not declare its own protection is `APPLY REFUSED`, exit 2,
     remedy *declare a rule on the repository this file lives in*;
   - `APPLY BEFORE` prints what the host says about the commit the blob came
     from — `commit=<sha> signed=<yes|no|unknown> reviewed=<yes|no|unknown>` —
     so the claim *a diff somebody read and a commit somebody signed* is a
     measurement on the line rather than a sentence in this document. An
     `unknown` on either is printed and does not by itself refuse: what the host
     will tell a reader about a commit is the host's business, and rule 17's
     floor is for measured state, not for provenance the host declines to give.
   (2026-09-13 read of draft 2: *"rule 1 moved the authority into the channel
   rule 7 distrusts, and nothing gives it a floor … a seat with write there
   commits a `seat` row, a `collab` row and a `ruling` row for it, and every rule
   in this spec passes."*)

2. **One row per fact, six fields, and nothing implied.** One row is one
   tab-separated line: `kind`, `scope`, `key`, `want`, `owner`, `source`. The
   first line must equal the header byte for byte, else a refusal naming the file
   and line 1, exit 2, remedy *put the header back exactly as the spec shows* —
   unchecked, a header-less file silently loses row 1 to the skip (the hurt is
   nova-update rule 2's, 2026-09-12, and the shape is borrowed whole). The header
   and every line whose first character is `#` are skipped, nothing else is. No
   field may be empty; a field with no content is spelled `-`. More or fewer
   fields is a refusal naming the line number, exit 2, never a skip. Exactly one
   `seat` row: a second is a refusal naming both line numbers, exit 2 (rule 22).
   There is no
   nesting, no include, no anchor and no second file: a nested declaration
   requires a parser with opinions, and every opinion is a place two readers can
   disagree about what was declared.
   A row whose `kind` is not one of the kinds **the kinds** names
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
   default branch the host reports, in the run that prints it. **Every kind this
   tool admits is a fact the host can answer**, which is what rule 2's *a kind
   with no reader cannot be declared* comes to after the 2026-09-13 read of
   draft 2: the four kinds draft 2 delegated to sibling tools had no reader in
   this tree at all, and they are gone (**the kinds**, below). The tool
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
   the host distinguishes them. The **act's** address is derived from that
   `origin`: an organization-sourced rule is written at the organization's
   address, never at
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
   administrative override defeats the older branch-protection mechanism
   **wherever that mechanism's own setting leaves administrators unenforced**,
   and it does **not** defeat a ruleset whose bypass list is empty, which binds
   the organization's owner too — so *protected* is two different promises and
   the tool prints which one it measured (2026-09-13 read: the override claim
   holds only with enforcement of administrators off, and draft 2 stated it
   flat); and a ruleset and a branch protection can both cover
   one branch, so a declaration that names only one of them has measured only one.

9. **A requirement and its producer are two rows, the pairing is measured, and
   the measurement is taken where a gate actually runs.** A required check is a
   `check` row whose `key` is `<rule>/<context>@<producer|any>`; the thing that
   emits it is a `workflow` row whose `want` is `emits:<context>` or `absent`.
   The two are paired by **equality of the context**, never by a file's basename
   — draft 1 paired them by convention, which rule 2 forbids in the same breath
   as *nothing implied* (2026-09-13, both reads).
   **`emitted=` is measured over the heads the host reports, not over one head.**
   A context counts as emitted when the host reports a **check run or a commit
   status** carrying that context on at least one of:
   - the head of the default branch; and
   - the head of each open pull request into the default branch, newest first,
     up to the budget rule 20 gives the scope.
   `plan` prints which list and which head the evidence came from —
   `emitted=<yes|no|unknown> from=<check-run|status|-> head=<default|pull|->`.
   Both halves of that sentence are draft 2's two errors, found by both reads of
   it (2026-09-13). **A workflow triggered on pull requests posts its runs on
   pull request heads and never on the default branch's head**, so draft 2's
   *no check run of that context on the default branch's head* flagged every
   correctly configured gate of that shape, every night, on the one number a
   morning reads — the false wall rule 19 exists to prevent. And **a required
   context can be satisfied by a commit status rather than a check run**: they
   are two lists at the host, the 2026-08-26 synthetic green was a status, and a
   reader of one list alone reports a working gate as stranded.
   **A wedge is a stranded requirement: `emitted=no` and no producer.** The wire
   requires the context, nothing emitted it on any measured head, **and** the
   thing that would emit it is not there — the declared `workflow` row's file is
   absent from the wire, or no row declares a producer at all. That is
   `PLAN WEDGE`, its own finding kind, exit 1, because it is not a difference
   from the declaration and will never be caught by one. Defining it so that an
   **undeclared** check can wedge is 2026-08-29 exactly; defining it from the
   `workflow` rows alone would have meant it could not.
   **A gate that has not run yet is not a wedge.** `emitted=no` with the
   declared producer present on the wire is `PLAN NOTE … never-run`, exit 0: it
   is every gate's first day, and a tool that called it a wall would make a new
   gate unaddable (2026-09-13 read of draft 2). A context the reader could not
   measure is `emitted=unknown`, `PLAN UNKNOWN` under rule 17, never a pass.
   **`apply` refuses to add a requirement whose producer is absent from the
   wire**, naming the producer as the thing to put there first. *Absent from the
   wire* is the producer-presence measurement of the paragraph above and **not**
   `emitted=`: a producer that exists and has not yet run satisfies this refusal,
   or a new gate could never be added in either order. Where no row declares a
   producer and nothing has emitted the context anywhere, the refusal stands and
   names the missing `workflow` row. Removing a gate means removing **both
   halves**, and the half that is easy to forget is the one that cannot be seen
   from the repository; the producer half has no act here (**the kinds**), so
   removing it is a person's commit and this tool only measures that it went.
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

11. **An act that is not narrowing needs a dated ruling row, and there are two
    ways not to be narrowing.** Every act is classified `narrows`, `widens` or
    `unjudged` by its kind, its target value and — where the key carries one —
    the wire's value in the same position, from the table in **the direction of
    an act** below: never by how it is spelled and never by the direction of the
    drift. Both `widens` and `unjudged` are refused unless the declaration
    carries a `ruling` row whose scope is exactly that change name, carrying a
    date and the person who ruled it; that row is printed on `APPLY BEFORE`,
    copied into the audit row, and it authorizes **one change name and no other**.
    A `narrows` act needs only the row. A ruling row with no matching change is a
    refusal at load (rule 5's cousin): a ruling that authorizes nothing is a
    ruling somebody believes is in force.
    **`widens` and `unjudged` cost a person the same and count differently.**
    `widens` means *the tool can see that this enlarges somebody's reach*;
    `unjudged` means *the tool cannot tell, and says so rather than guessing*.
    They are one refusal and two numbers, because `AUDIT DAY widened=` is called
    *"the whole of what was deliberately loosened"* in **the measure**, and draft
    2 put a repository setting, a feature toggle and a bypass grant under that
    one word — *"a box created and a bypass added are one number"* (2026-09-13
    read). A security number that counts unjudged acts is not a security number.
    The table is **total over the kinds rule 2 admits**: every kind and every
    `want` it admits has a row there, and a kind or a `want` the table does not
    name is a refusal at load, never a default and never a guess. (2026-09-13,
    both reads: draft 1's own example declared a default branch and a box user,
    and rule 11 said the classification came *"from the table and nothing else"*
    — so the example could not be classified at all.)
    The point is not a lock. Glenn, 2026-09-11: *"We need power to work around
    occasionally. This is normal. Flexibility. Not rigidy."* The power stays; what
    this rule buys is that spending it leaves a date, a name and a line.

12. **Measure a permission the way the host answers it, or do not claim to have
    measured it.** Before the mutating call is built, `apply` makes the **read**
    form of the same call and records the permission headers the response
    carries. That record is a **measurement and not a verdict**: it is printed as
    `have=unknown` with the header's value beside it, and **no act is ever
    refused on it**.
    The reason is the whole of this repair. A header that names *the permissions
    an endpoint accepts* is a fact about that endpoint and not about the token:
    the read endpoint of a pair accepts a read permission, the write endpoint of
    the same pair requires a write one, and the token is not mentioned in either
    answer. Draft 2 read the first as the second, so *the permission this act
    requires is not among them* was true for **every** act of that shape with
    **every** token — a refusal, exit 2, always, and a test fixture that pinned
    the wrong behavior (2026-09-13 read, measured live against a host: a rules
    read answered `metadata=read` while the matching write is documented under an
    administration write).
    **One header shape may prove a permission absent: one that names the grants
    the presented token itself holds** (the classic scope-list shape, which
    enumerates what was granted rather than what is accepted here). Where a host
    returns such a header and the permission this act requires is not in it, that
    is `have=no`: `APPLY REFUSED … permission=<name>`, exit 2, remedy naming the
    person whose hands the act belongs in, and **no mutating request is ever
    sent**. A host that returns no such header leaves `have=unknown`, which is
    the expected steady state on a host that reports only endpoint acceptance.
    In every other case the act proceeds to the mutating call, and what that call
    requires is proved by its **own refusal**, which mutates nothing: a refused
    mutating call carrying the permission name prints
    `APPLY REFUSED … permission=<name>`, exit 2 — **once**, with no retry, no
    second attempt, no alternate address and no fallback path.
    **A 200 on the read form proves the read and says nothing about the write**;
    treating it as proof would be exactly the guess at the delta this rule exists
    to forbid, and it would leave an instrument that cannot fail in the one case
    it was built for (2026-09-13, both reads of draft 1).
    (Measured 2026-09-11: the missing permission was in the refused call's
    response header all along; the reasoning from the published list was wrong
    and the header was right. That measurement was taken on a **refused act**,
    which is the only place draft 3 reads it.)
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
    The reach includes the **organization role** the host reports for the
    identity, because a rule's bypass list can name an organization's
    administrators as an actor type of their own: a seat that is an owner is
    inside such an actor and no login, team or repository role would have said so
    (2026-09-13 read of draft 2).
    A reach the host will not report is `UNKNOWN`, and an act whose target could
    contain an unmeasurable reach is refused as a person's hand rather than
    allowed on the assumption that it does not (rule 17's shape: unknown is never
    a pass). **On a token scoped to repositories this is the expected steady
    state, not a fault**: such a token is commonly refused the organization
    membership read, so `reach=unknown` and every `bypass` and `collab` act
    becomes a person's hand. That is the answer this rule wants — the alternative
    is a tool that grants itself access whenever it cannot see itself — and it is
    written here so a first run does not read as a bug (2026-09-13 read,
    measured live).
    No ruling row lifts any of this: a ruling row is a thing the acting
    seat can propose, and a grant spent on the grant's own reach is the one act
    that must cost a person's hands. (The coordinator's grant, 2026-09-11,
    carries this as its own named tell.)

14. **Act only on the value you were shown, and read it back from the wire, by
    name.** `apply` takes `--expect <d>` — **a digest, never a host's text**:
    either `-` for absent, or `sha256:<the first twelve hex digits>` of the
    canonical form of the value the wire held when the caller was shown it.
    `plan` prints that digest on every finding line; `apply` recomputes it from
    its own `APPLY BEFORE` read and compares digests. The canonical form is the
    tool's, written down once and the same on both sides, so *equal* has one
    spelling for a string, a list and a nested object alike.
    The digest is not decoration. The one command a `plan` line prints must be
    pasteable, and the Conventions' escape *does not paste back* and *nothing is
    shell-quoted*; a wire value in that command can be a JSON object with quotes
    and braces in it, which is the shape rule 16 has just finished removing from
    the announcement for that exact reason. **No value read from the wire appears
    in any command this tool prints** (2026-09-13 read).
    Three outcomes, in this order:
    - the wire's digest does not equal `--expect`: refusal, exit 2, naming both
      digests and printing the wire's value as a field, remedy *plan it again and
      act on what it says* — a second hand moved this fact between the plan and
      the act;
    - the wire already equals the row's `want`: `APPLY NOTE already`, exit 0,
      **no mutating call is made and no audit row is written**;
    - otherwise the act proceeds, and its classification under rule 11 is made
      from **this** read, never from an earlier `plan`'s.
    **Where the host offers a precondition, send it; where it does not, say so.**
    If the host will take a conditional write — an entity tag, a version, any
    server-side compare — `apply` sends the one `APPLY BEFORE` read returned, and
    a refused precondition is exit 2 with the same remedy as a digest mismatch.
    **Where the host offers none, this is a client re-read and not a
    compare-and-swap**, and the difference is stated rather than borrowed: the
    window between `APPLY BEFORE` and the write is open, it is at most one
    `--timeout` wide, and a hand that moves inside it is caught by the readback
    below and never before the act. Draft 2 cited nova-merge rule 21, whose
    compare-and-swap is a **server** precondition; this one is not, and a floor a
    spec claims and does not have is worse than the floor it has (2026-09-13,
    both reads).
    **The request body is `BEFORE`'s object with the one declared facet
    changed.** Where the host replaces a whole object on a write — a rule put
    back entire to change one facet of it — the body is composed from the object
    `APPLY BEFORE` read, with exactly the declared facet replaced and every other
    facet carried across byte for byte. An implementer who composes the body from
    the declaration instead drops every facet nobody declared and learns it from
    the readback, which is after the fact and after the `no undo` (2026-09-13
    read). The readback then also asserts that every facet the declaration did
    not name is the value `APPLY BEFORE` read; a facet that moved is
    `matched=no`, exit 1, naming the facet.
    After the mutating call returns, `apply` performs an independent read of the
    changed thing, by name, at the address rule 8 gives, and compares it to the
    declared value. A mismatch is `APPLY FAIL … matched=no`, exit 1, with both
    values on the line.
    The mutating call's exit status, its response body, and any message it
    carried are **not** evidence that anything changed. A readback that does not
    assert the object is the new one is not a readback.
    (The no-op case, the whole-object facet assertion, the digest and the named
    gap are the 2026-09-13 reads'.)
    (The whole of *read it back from the wire*, 2026-09-07 through 2026-09-13: a
    502 from a create says nothing about whether the object exists; two creates
    reported absent both existed.)

15. **The audit row is written after the state and before the process exits.**
    The order is: **read** (`APPLY BEFORE`, rule 14), probe (rule 12), act, read
    back, **then** write the row — the `BEFORE` read first, because the
    classification, the digest compare and the write body all come out of it, and
    because draft 2's canonical order omitted it (2026-09-13 read). The row carries
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

16. **The announcement is part of the control — printed, counted, never sent,
    and it is not a command.** `apply` prints `APPLY ANNOUNCE` as its last
    informational line: the change name, the audit file's path and the row
    number, **and nothing else**. It is three facts and no argv.
    Draft 1 printed a command carrying a wire-derived before and after, which is
    a line inviting a person to paste a host's text into a shell — the
    Conventions' escape *does not paste back* and *nothing is shell-quoted* — and
    draft 2 fixed that by composing the command from the tool's own facts. But a
    command must name a program, an estate's announcement program is that
    estate's, and rule 3 admits no such literal: draft 2 left one standing
    (2026-09-13 read). There is no third thing to compose. **The line carries the
    facts an announcement needs and the estate announces the way it announces.**
    The tool sends nothing. The audit row is written
    `announced=pending`, and `audit` prints `unannounced=<n>` and names the oldest
    on its count line, so *the admin act nobody announced* is a number in the
    morning rather than a thing somebody has to notice. A separate act marks a row
    announced (`audit --announced <change> --at <stamp> --ref <id>`), which is
    itself an append and never an edit, and which is **an acting invocation**:
    it takes rule 18's lock and it is refused from any seat but the declared one
    (rule 22). Draft 2 called it *"the only write any verb but `apply`
    performs"* two hundred lines after saying `audit` writes nothing and takes no
    lock: an unlocked append racing `apply`'s locked one is 2026-09-11's two
    writers and zero bytes, on the file rule 18 exists for, and an unseated one
    lets any line drive `unannounced=` to zero without announcing anything —
    the grant's own condition cleared by the half of the tool that was for
    everybody (2026-09-13 read).
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
    unless both the old file and the new one parse.
    **Every write to the audit file is under this lock, and there is exactly one
    of them besides `apply`'s**: `audit --announced`, which appends the mark rule
    16 describes, takes the same lock in the same way and is refused from any
    seat but the declared one. `plan`, `probe` and every reading form of `audit`
    take no lock, because they write nothing — which is now a true sentence
    (2026-09-13 read of draft 2, where it was not).

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
    Rule 9's heads are inside this bound and not beside it: **one call lists the
    open pull requests of a scope, and the check runs and statuses of a head are
    one call each per head**, newest first, stopping at the budget. A context the
    run stopped before reaching is `emitted=unknown` under rule 17 and never a
    wedge — an instrument that runs out of time says so rather than reporting a
    wall.
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
    row names the login and the host label from which `apply` may run. **The
    declaration carries exactly one**: a second `seat` row is a refusal at load,
    exit 2, naming both line numbers, because *you should be the only one who can
    do it* (Glenn, 2026-09-12) has no second answer and draft 2 left the case
    unspecified (2026-09-13 read). `apply`
    measures the identity it is actually running as — one call, before anything
    else — and refuses when it does not equal the declared seat, naming both. It
    does not read a configuration file to learn who it is, because a configuration
    file is what makes one machine's two identities interchangeable by accident.
    `plan`, `probe` and every **reading** form of `audit` run from any seat and
    are refused from none: **the measuring half is for everybody**, and a line
    that cannot act should still be able to see. The one acting form,
    `audit --announced`, is refused from every seat but the declared one, on the
    same reasoning as `apply` and for the hurt rule 16 names.

23. **It has no hands: it runs no other program.** Every kind this tool admits
    is a fact the **host** answers and, where it has an act at all, a fact the
    host writes. There is no `exec`, no shell, no pipe, no argv composed from
    anything, and no sibling tool's binary on any path this tool takes.
    This rule is the third and last shape of one question. Draft 1 put the
    command in a seventh field of a data row, which rule 2's six fields could not
    even spell. Draft 2 moved the argv into a table in this spec, which was
    implementable — and then the read of draft 2 asked what the table's two named
    programs actually were: one does not exist anywhere in this tree, and the
    other has no verb that answers the question its row asks (2026-09-13). A hand
    that is not there is not a hand. **Deleting the four kinds that needed one
    deletes the mechanism**, and what is left is a tool whose whole surface is one
    host's API — which is also the smallest acting surface the second of the three
    opening sentences asks for.
    So: nova-admin creates no account, places no unit, seals no key, runs no
    worker pool, resolves no version, and **starts no process**. Where a declared
    fact is another tool's to make true, this tool does not declare it.

24. **A refusal prints every independent problem in one go.** A declaration with a
    bad header **and** a duplicate change name says both, one line each: a caller
    can fix two things as easily as one. An unusable invocation costs one line and
    never the usage banner (SPEC.md's Conventions).

## The kinds

Rule 2 admits exactly the kinds in this table and refuses every other at load.
Rule 4 measures each through the reader named here. **Every reader and every
actor is the host**, per rule 23. A reader that cannot answer is `UNKNOWN` under
rule 17 and never a pass.

| kind | the fact it declares | measured through | acted through |
|---|---|---|---|
| `repo` | a repository's own setting, `key` the setting's name as the host spells it | the host's repository read | the host's repository write |
| `visibility` | whether a repository is public | the host's repository read | the host's repository write |
| `ruleset` | a named rule's existence, and its enforcement and ref facets by key (below) | the host's rules read, at rule 8's origin | the host's rules write, at rule 8's address |
| `check` | a required context on a rule | the host's rules read **and** the check runs and commit statuses on the heads rule 9 names | the host's rules write |
| `workflow` | the file that emits a context | the host's contents read, and the check run → check suite → workflow run path (below) | **nothing — there is no act** (below) |
| `bypass` | one bypass actor and its mode on a rule | the host's rules read | the host's rules write |
| `feature` | a named repository feature | the host's read for that feature | the host's write for that feature |
| `secret` | that a secret of that name exists | the host's secrets list, names only | **nothing — there is no act** (below) |
| `collab` | a login's role on a scope | the host's collaborators read | the host's collaborators write |
| `seat` | the identity `apply` may run as (rule 22) | the host's identity call | **nothing — there is no act** (below) |
| `ruling` | a person's dated word (rule 11) | not measured: it is declaration, not state | **nothing — there is no act** |

**Four kinds are gone, and the deletion is the repair.** Draft 1 declared a box,
an account on a box, a line and a swarm pool with no reader and no way to spell
an act. Draft 2 gave each a reader and an actor by naming a sibling tool's verbs
in this table. The read of draft 2 checked those names against the tree
(2026-09-13): one of the two programs **exists nowhere in it** — no command, no
spec, two mentions as a future idea — and the other **has no verb that answers
the question the row asks**, which is *which box holds this pool*. By rule 2, *a
kind with no reader cannot be declared*; these four had none, so draft 3 does not
declare them. The cost is real and is named in the open questions: a box's
existence is no longer declared state anywhere. The alternative was four kinds
that could not run, and account creation inside the tool with the most reach.

**A `seat` row has no act.** It names the identity `apply` may run as, which is a
fact about this tool's own authority: the tool measures it (rule 22) and never
makes it true. `apply` on a `seat` row is `APPLY REFUSED`, exit 2, naming the
person. Making a seat exist on a box was the last thing in draft 2 that needed a
hand, and rule 23 no longer has one.

**A `secret` row has no act.** The tool reads whether a secret of that name
exists and nothing else. `apply` on a `secret` row is `APPLY REFUSED`, exit 2,
naming the estate's secrets tool and the person whose hands a value belongs in —
a tool that never reads, writes or seals a value cannot make one exist. The
`EXTRA` and `ABSENT` findings still print, which is the whole of what this tool
can honestly say about a secret.

**A `workflow` row has no act either, and that is new in draft 3.** Draft 2 said
the act of `emits:<context>` was to *"add or restore the file that emits a
context"* through the host's contents write, with the row's `scope`, `key` and
`want` as the arguments. Two things are wrong with that and both are fatal
(2026-09-13 read):

- **there are no bytes.** `emits:<context>` is a context name, not a file body,
  and rule 3 forbids the tool from holding a catalog of file bodies — the same
  reasoning that makes a `feature` unjudgeable;
- **a contents write is a direct commit to the default branch**, which is
  precisely what the rules this same declaration declares exist to refuse. A
  tool that must spend a bypass on the estate's own gate to satisfy the estate's
  own declaration is not enforcing the declaration.

So: `apply` on a `workflow` row is `APPLY REFUSED`, exit 2, naming the person and
the repository — in **either** direction, `emits:` and `absent` alike, because a
delete is the same direct commit. Putting a workflow file there or taking it away
is a pull request somebody reviews, which is what the gate is for. The row stays,
because the **measurement** is what 2026-08-29 needed: rule 9's wedge turns on
whether the producer is on the wire.

**How `emits:` is measured, mechanically.** Never by parsing the file: a workflow
file's job names can come from a matrix, a display name can override the context,
and a parser with opinions is two readers disagreeing about what was declared
(rule 2). Instead the tool follows the host's own link **backwards from a run**:
a check run of the context names its check suite, the suite names the workflow
run, and the workflow run names the **path** of the file that produced it.
`emits:<context>` matches when that path equals the row's `key`. A context with
no run anywhere the tool looked is `emitted=no` and the pairing is `unknown`, not
false — which is exactly the case rule 9 splits into a wedge and a gate that has
not run yet.

**A rule's facets are keys, because a name is not a protection.** A `ruleset`
row with `want present` measures that a rule of that name exists and **nothing
about what it does**: a rule whose enforcement was set aside, or whose ref
conditions were emptied, still carries its name. So the facets are declarable and
each is its own row, keyed on the rule — and the **kind** column is load-bearing,
because draft 2's table presented all four as `ruleset` rows while its own
example spelled two of them as kinds `check` and `bypass`. The direction table
names no `(ruleset, present)` pair for a check context, so one of those two
spellings would not have loaded at all, and the other was two change names for
one wire fact, which is rule 5's own hurt (2026-09-13 read):

| key | the row's `kind` | `want` | what it measures |
|---|---|---|---|
| `<rule>/enforcement` | `ruleset` | `active`, `evaluate` or `disabled` as the host spells them | whether the rule is in force at all |
| `<rule>/refs` | `ruleset` | the ref condition as the host spells it | what the rule covers |
| `<rule>/<context>@<producer\|any>` | `check` | `present` or `absent` | a required check, and whether any producer satisfies it or one named one does |
| `<rule>/actor:<type>:<id>` | `bypass` | a bypass mode as the host spells it, or `absent` | one bypass actor **and its mode**, which are two different grants |

**A bypass key spells the host's own actor, and a bypass mode is the host's own
word.** The key's `<type>` and `<id>` are the actor type and actor identifier the
host returns, copied and not translated; the `want` is a mode the host returns,
or `absent`. Draft 2 admitted three key shapes and two modes of its own choosing,
which was a partial catalog of somebody else's domain: the host's actor types
also include an integration, an organization's administrators and a deploy key —
each of which would have been `EXTRA` forever, undeclarable and unremovable — and
its mode domain has a third value draft 2 would have refused at load. Worse,
`role:<name>` needed a table mapping the host's numeric role identifiers to
names, which is the literal rule 3 forbids (2026-09-13 read, measured live). A
mode the tool has never seen needs no catalog under the direction table below,
which classifies **every** non-`absent` bypass mode the same way.
An enforcement value the host will not accept — one it offers only on some plans
— is the host's refusal, printed under rule 12's *once, no retry* and not
second-guessed here.

A facet present on the wire that no row names is `EXTRA` — never silent, never
folded into a `present` that matched. (2026-09-13, both reads: *"the declaration
measures names, not shapes, so a ruleset can be present, matched, and protect
nothing"*; and two different modes under one key.)

## The direction of an act

Rule 11 turns on this table and nothing else. It is in the spec rather than in a
comment because the classification is the whole of the widening rule, and because
a reader must be able to check a build against it.

| kind | `want`, and the wire where the key carries one | the act | direction |
|---|---|---|---|
| `check` | `present`, key `@<a named producer>`, wire absent or `@any` | add a required check, pinned to one producer | narrows |
| `check` | `present`, key `@any`, wire absent | add a required check any producer satisfies | narrows |
| `check` | `present`, key `@any` or a **different** producer, wire pinned to a named producer | change which producer satisfies the gate | **widens** |
| `check` | `absent` | remove a required check | **widens** |
| `workflow` | `emits:<context>`, `absent` | — | **no act**: `APPLY REFUSED` (**the kinds**), whatever the direction would have been |
| `ruleset` | `present` | create or restore a rule | narrows |
| `ruleset` | `absent` | delete a rule | **widens** |
| `ruleset` facet `…/enforcement` | the host's in-force value | put a rule into force | narrows |
| `ruleset` facet `…/enforcement` | any other value the host offers | take a rule out of force | **widens** |
| `ruleset` facet `…/refs` | any ref condition | change what a rule covers | **widens** |
| `bypass` | any mode the host spells | add a bypass actor, or change its mode | **widens** |
| `bypass` | `absent` | remove a bypass actor | narrows |
| `feature` | any value | turn a named feature on or off | **unjudged**, either way |
| `collab` | `absent` | remove a collaborator | narrows |
| `collab` | any role | add a collaborator, or change a role | **unjudged** |
| `repo` | any value | change a repository's own setting, the default branch included | **unjudged** |
| `visibility` | the host's private value | make a repository private | narrows |
| `visibility` | any other value | make a repository public or otherwise wider | **widens** |
| `secret` | `present`, `absent` | — | **no act**: `APPLY REFUSED`, whatever the direction would have been |
| `seat` | any value | — | **no act**: `APPLY REFUSED` (**the kinds**) |
| `ruling` | — | — | **no act**: a ruling is declaration, not state |

Five notes a reader will otherwise have to derive:

- **The direction is of the ACT, not of the drift.** Reverting a wire that is
  *more* protective than the declaration is, by this table, a widening act — so it
  needs a ruling row, and that is exactly the mechanism that makes rule 21's
  promise true: **drift toward more protection is reported and never quietly
  reverted.** There is no separate rule for it and no special case to forget.
- **The producer half of a `check` key is part of the classification.** A gate
  the wire pins to one named producer and a row spells `@any` is a gate anything
  holding the write scope can satisfy — and draft 2 classified it `narrows`, so
  it would have landed with no ruling and no line in `widened=`. That is the
  2026-08-26 route surviving the wall built against it: the tool still writes no
  check result, and after that act it does not need to (2026-09-13 read).
  Direction therefore reads `want` **and** the wire's value in the same position
  of the key, which is why the table's second column names both.
- **`feature` and `collab` are `unjudged`, not `widens`.** The tool holds no
  catalog of a host's features, so it cannot say what turning one on *means*;
  and it holds no ordering of a host's roles, so it cannot say that one is
  *higher* than another — a custom role has no rank at all, and draft 2's
  *"a **lower** role than the wire's"* would have classified a raise to an
  unranked role as narrowing and landed it with no ruling (2026-09-13 read).
  Both need the same dated ruling a widening needs. What they do not do is count
  in `widened=`, because that number is read as *what was deliberately loosened*.
  The alternative in both cases is a catalog, and a catalog is the literal rule 3
  forbids.
- **`repo` is `unjudged` for the same reason, deliberately.** Where the tool
  cannot tell from the row whether an act enlarges somebody's reach, it refuses
  without a person's dated word rather than guessing — and it says which of the
  two it is refusing for. A kind or a `want` with no row here is refused at load
  (rule 11), never defaulted.
- **A `secret` row is a name and never a value.** The tool reads whether a secret
  of that name exists and nothing else; it never reads, writes, prints or
  decrypts one. Sealing and rotation live in the estate's secrets tool.

## The declaration

One header line, then one row per fact, tabs between fields; `#` opens a comment
(rule 2). Every name below is a placeholder, and none of them exists (rule 3).

```
kind	scope	key	want	owner	source
seat	acme/widget	seat-one	present	line-one	policy-0
repo	acme/widget	default-branch	trunk	line-one	policy-1
visibility	acme/widget	-	public	line-one	policy-1
ruleset	acme/policy	protect-the-declaration	present	line-one	policy-0
ruleset	acme/widget	protect-trunk	present	line-one	policy-1
ruleset	acme/widget	protect-trunk/enforcement	enforcement-a	line-one	policy-1
ruleset	acme/widget	protect-trunk/refs	ref-condition-a	line-one	policy-1
check	acme/widget	protect-trunk/assignment@producer-a	present	line-one	policy-1
workflow	acme/widget	workflow-dir/assignment-file	emits:assignment	line-one	policy-1
bypass	acme/widget	protect-trunk/actor:actor-type-a:actor-id-a	mode-a	line-one	ruling-2026-09-11
feature	acme/widget	feature-alpha	value-a	line-one	policy-1
feature	acme/widget	feature-beta	value-a	line-one	policy-1
secret	acme/widget	SECRET-NAME-A	present	line-two	policy-3
collab	acme/widget	person-b	role-a	line-one	policy-2
ruling	bypass:acme/widget:protect-trunk/actor:actor-type-a:actor-id-a	2026-09-11	A. Person	line-one	ruling-doc-4
```

- **`scope`** is the thing the row is about: a repository in the host's own
  `<owner>/<name>` spelling, or the organization.
- The `acme/policy` row is the declaration's **own** repository, which rule 1
  requires it to declare a rule on: a declaration that does not protect the place
  it lives in is a declaration one hand can rewrite.
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
- Every name in this example is a placeholder that exists nowhere — the host's
  feature names, its enforcement values, its bypass actor types and modes, its
  collaborator roles, its setting names and the **path** of a workflow file
  included. Draft 1 spelled three of a real host's features here; draft 2 fixed
  those and left a host's conventional workflow directory, its enforcement
  words, its bypass modes and a setting's exact spelling standing (2026-09-13
  read). A reader who wants to know what a host actually calls these things
  reads the host's documentation, which is where that fact lives.
- A **`ruling`** row is the one row whose `scope` is a change name: `key` is the
  date (`YYYY-MM-DD`), `want` is the person who ruled it, `source` is where the
  words are. It authorizes that one change name and nothing else (rule 11).

## The verbs

```
nova-admin plan   --fleet <path|scope:path> [--kind <k>] [--scope <s>] [--counts] [--max <n>] [--timeout <d>] [--budget <d>]
nova-admin apply  --fleet <scope:path> --change <name> --expect <digest|-> --audit <path> [--timeout <d>] [--budget <d>]
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
`--scope` name — and prints one line per difference, each carrying the change
name, the digest `--expect` will take, and the single command that would apply it.
It writes nothing, takes no lock,
holds no cache, and cannot reach the mutating helper: that is a property a source
test pins, and a `--dry-run` flag on `apply` never would be. `--counts` suppresses
every finding line and prints the header and the count line alone (rule 21).
**A `plan` run from a local `--fleet` prints no command.** `apply` will not take
a local path (rule 1), so the command a local `plan` could print would be one
that is refused; instead the line ends in the remedy *plan it again naming the
declaring repository and the path in it*, with the change name and the digest
still on the line. (2026-09-13 read: draft 2 promised *the one command that would
apply it* on every line, and on a local run there is no such command.)

**`apply`** is the acting verb and the only one. One change, named, under a
declaration read from the wire (rule 1), against a value the caller was shown
(`--expect`, rule 14). It is refused from a local `--fleet` (rule 1), refused
when the declaration does not declare a rule on its own repository (rule 1),
refused
from an undeclared seat (rule 22), refused when the host's own grant header
proves the permission absent (rule 12), refused when the change's target contains
the acting
identity's reach (rule 13), refused as a widening or an unjudged act with no
ruling (rule 11),
refused when it would strand a requirement (rule 9), refused on a kind that has
no act — `workflow`, `secret`, `seat`, `ruling` (**the kinds**) — refused when
the wire's digest is
not what `--expect` said (rule 14), and it exits non-zero when the readback does
not match or a facet nobody declared moved (rule 14). Everything it does is one row on the
audit file (rule 15) and one printed announcement (rule 16).

**`probe`** measures capability rather than configuration: the identity this
process is running as against the declared seat; the reach rule 13 measures; and,
for each permission the declaration's rows would require, whatever the host's
headers on a **read** say about it. **Every call `probe` makes is a read.** It
never
acts, and it never makes a call whose shape is a write: draft 1 said each
permission was *"proved by one idempotent real call apiece"*, and the calls that
prove a write permission are writes — from a verb that takes no lock, runs from
any seat, and writes no audit row (2026-09-13, both reads). So `probe` reports
`have=no` only where a header naming the **token's own grants** shows the
permission missing, and `have=unknown` everywhere else — including everywhere the
host reports what an endpoint accepts rather than what the token holds, which on
many hosts is everywhere (rule 12). A `probe` that reports `have=unknown` for
every permission is a `probe` that worked. It reads
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
go and read; it is an append, never an edit, and it is **the one acting
invocation outside `apply`**, so it takes rule 18's lock and it is refused from
any seat but the declared one (rules 16, 22). `audit` runs on `plan`'s clock,
because a count nobody
reads is not a control. (The window and the ref are draft-1 reads'; the lock and
the seat are draft-2's, where an unlocked append from any seat could zero the
one number the coordinator's grant is conditioned on.)

`plan`, `probe` and every reading form of `audit` report and never act.
`version` is the Conventions' build line, exit 0.

## Exit codes

| code | meaning |
|------|---------|
| 0 | the verb ran and passed: every measured row matched, a change applied and read back, a probe with the seat matched and no permission lacking, an audit that answered |
| 1 | the verb ran and said **NO**: any drift, any wedge (rule 9), any UNKNOWN, a readback that did not match, an audit append that failed after the act happened, a probe whose seat did not match or whose grant header proved a required permission absent, an audit with an act unannounced for longer than `--unannounced-after` |
| 2 | could not run: a missing flag, an unreadable or malformed declaration, an unknown kind, a duplicate change name, a second `seat` row, a change name no row carries, a local `--fleet` given to `apply` (rule 1), a declaration that declares no rule on its own repository (rule 1), an `--expect` digest the wire does not match (rule 14), a change on a kind with no act (**the kinds**), a refusal under rules 6, 9, 11, 12, 13, 18 or 22, a bad invocation |

A `PLAN RISK … bypass=none` line is exit **0** on its own (rule 10), a
`PLAN NOTE … never-run` line is exit 0 (rule 9), and `APPLY NOTE already` is
exit 0 (rule 14): the first is a report about a calm day, the second is a gate on
its first day, and the third is an act that did not need to happen.

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
PLAN DRIFT change=<name> direction=<LOOSER|TIGHTER|DIFFERENT|ABSENT|EXTRA> want=<v> wire=<v> expect=<sha256:<12 hex>|-> act=<narrows|widens|unjudged> origin=<repo|org|protection|-> source=<source> ruling=<date:person|-> owner=<owner>: <the one command that would apply it, or the remedy where --fleet was local>
PLAN WEDGE change=<name|-> scope=<scope> check=<key> producer=<workflow-key|undeclared> present=<no|unknown> emitted=no from=- heads=<n>: <what is stranded> (<remedy>)
PLAN RISK change=<name|-> scope=<scope> rule=<key> bypass=none: <who has no recourse if the producer stops> (<remedy>)
PLAN NOTE change=<name|-> scope=<scope> check=<key> producer=<workflow-key> present=yes emitted=no note=never-run: <detail>
PLAN NOTE <something true about this run that is not a finding>
PLAN UNKNOWN change=<name> reason=<budget|permission|transport|shape|unsupported> reach=<role,team,org-role,…|unknown>: <detail> (<remedy>)
PLAN MORE kind=<looser|tighter|different|absent|extra|wedge|nobypass|unknown> shown=<n> total=<t> <remedy>
PLAN <OK|FAIL> rows=<n> read=<n> match=<n> drift=<n> looser=<n> tighter=<n> different=<n> absent=<n> extra=<n> wedges=<n> nobypass=<n> unknown=<n> took=<d> fleet=<path|scope:path@sha>
PLAN REFUSED: <reason> (<remedy>)
APPLY SEAT running=<login> declared=<login> host=<label> match=<yes|no> reach=<role,team,org-role,…|unknown>
APPLY BEFORE change=<name> fleet=<scope>:<path>@<sha> commit=<sha> signed=<yes|no|unknown> reviewed=<yes|no|unknown> wire=<v|-> digest=<sha256:<12 hex>|-> expect=<sha256:<12 hex>|-> want=<v> act=<narrows|widens|unjudged> origin=<repo|org|protection|-> source=<source> ruling=<date:person|-> precondition=<sent:<token>|none> address=<METHOD> <url>
APPLY PROBE change=<name> permission=<name|-> have=<no|unknown> header=<value|->
APPLY NOTE change=<name> note=<already> wire=<v>: <detail>
APPLY AFTER change=<name> wire=<v|-> was=<v|-> address=<METHOD> <url> facets=<unchanged|moved:<facet>> matched=<yes|no>
APPLY AUDIT change=<name> who=<login> at=<stamp> fleet=<scope>:<path>@<sha> before=<v|-> after=<v|-> ruling=<date:person|-> file=<path> row=<n>
APPLY ANNOUNCE change=<name> file=<path> row=<n>
APPLY <OK|FAIL> change=<name> from=<v|-> to=<v|-> took=<d>[: <reason>]
APPLY REFUSED change=<name|-> [permission=<name>]: <reason> (<remedy>)
PROBE SEAT running=<login> declared=<login|-> host=<label> match=<yes|no> reach=<role,team,org-role,…|unknown>
PROBE PERM name=<permission> have=<no|unknown> read_by=<GET> <url> header=<value|-> header_names=<endpoint|token|->
PROBE MORE kind=<perm> shown=<n> total=<t> <remedy>
PROBE <OK|FAIL> seat=<yes|no> perms=<n> lack=<n> unknown=<n> took=<d>
PROBE REFUSED: <reason> (<remedy>)
AUDIT ROW at=<stamp> who=<login> change=<name> fleet=<scope>:<path>@<sha> before=<v|-> after=<v|-> act=<narrows|widens|unjudged> ruling=<date:person|-> announced=<yes|pending> ref=<id|->
AUDIT DAY day=<date> applied=<n> widened=<n> unjudged=<n> unannounced=<n>
AUDIT LAG change=<name> ruled=<date> applied=<stamp> lag=<d>
AUDIT MORE kind=<row|day|lag> shown=<n> total=<t> <remedy>
AUDIT <OK|FAIL> rows=<n> days=<n> applied=<n> widened=<n> unjudged=<n> unannounced=<n> oldest_unannounced=<change|-> median_lag=<d|-> file=<path>
AUDIT REFUSED: <reason> (<remedy>)
```

`PLAN`, `APPLY`, `PROBE` and `AUDIT` are the first tokens; `OK` and `FAIL` are the
verdicts and always the **last** line; the rest are informational second tokens,
declared here as SPEC.md requires.

Twelve things the grammar is carrying deliberately:

- **`direction=` and `act=` are two fields because they are two facts.** The
  direction is where the wire sits relative to the declaration; the act is what
  applying the row would do. A `TIGHTER` drift with `act=widens` is the line a
  reader must be able to see at a glance, because it is the one the tool will
  refuse without a ruling — and the one a careless tool would have reverted.
- **`act=` has three values and `widened=` counts one of them.** `unjudged` is
  refused exactly as `widens` is and counted apart from it, so the number a
  reader calls *what was deliberately loosened* means that (rule 11).
- **`expect=` is a digest and `wire=` is a value.** The digest goes in commands;
  the value goes in fields, escaped, where nobody pastes it (rule 14).
- **`APPLY BEFORE` carries the declaring commit's provenance.** `commit=`,
  `signed=` and `reviewed=` are what the host says about the commit the authority
  was read from (rule 1), so *a diff somebody read* is on the line as a
  measurement.
- **`precondition=` says whether the host took one.** `sent:<token>` where the
  host offered a conditional write and `none` where it did not, because rule 14's
  window is only closed in the first case and a reader of the record is entitled
  to know which one they are looking at.
- **`APPLY ANNOUNCE` is three fields and no command.** Rule 16: the facts an
  announcement needs, and no program name, no wire value, nothing to paste.
- **`PLAN WEDGE` is its own kind and not a drift.** A wedge is agreement between
  the declaration and the wire that nonetheless cannot work (rule 9), so it can
  never appear as a difference. It has its own cap, its own count, and it survives
  `--kind` filtering unless the caller filtered it out by name.
- **`PLAN RISK` is a second kind because it has a second exit code.** A stranded
  requirement is a wall today and a 1; a gate with no bypass is a wall on a bad
  day and a 0 (rule 10). Draft 1 printed both as `WEDGE` and then said the exit
  code was both 1 and not 1 — on the one number a morning reads.
- **`have=` has no `yes`, and `header_names=` says what the header was about.**
  Rule 12 leaves no read that proves a write permission present, so the field's
  domain is `no` and `unknown`. `header_names=endpoint` means the host answered
  what that endpoint accepts — a fact about the endpoint, never about the token,
  and never a `have=no`; `header_names=token` means the host answered what the
  presented token holds, which is the one shape that can prove absence. Draft 2
  read the first as the second, which refused every act of that shape with every
  token (2026-09-13 read).
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
| **widenings per day, each with its ruling** | `AUDIT DAY widened=` | the grant being spent on something the tool could see was a loosening; a widening with no ruling cannot exist, so this number is the whole of what was deliberately loosened |
| **unjudged acts per day, each with its ruling** | `AUDIT DAY unjudged=` | the grant being spent on something the tool could not classify — a feature, a role, a repository setting. It costs a person the same ruling and is a different number, because a security number that counts these is not a security number (rule 11) |
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

- **Whatever makes a seat, an account or a home on a box.** nova-admin does not
  declare that a box exists, that an account exists on one, that a line lives
  there, or that a pool has slots. Draft 2 declared all four and named two
  sibling tools as their hands; the 2026-09-13 read of draft 2 found that one of
  those programs is in no part of this tree and the other has no verb answering
  the question, so the rows could not be measured and the acts could not be made.
  **Draft 3 deletes the four kinds and rule 23's whole mechanism with them**: the
  tool starts no process. It contains no account creation, no key authorization,
  no home layout, no handover and no notion of a keeper. A `seat` row remains,
  because rule 22 must know which identity may act, and it has **no act**: making
  a seat exist is somebody else's, and measuring which identity is running is
  one call to the host.
- **`nova-daemon`, which places and supervises units.** nova-admin never starts, stops,
  restarts, enables or supervises anything, and holds no unit body for any
  platform. A declared unit is a row it measures as present or absent; **whether
  it is healthy is not a fact about the declaration**, and a tool that conflated
  the two would report drift every time a machine rebooted.
- **`nova-swarm`, the worker layer.** A pool is **not** a row here any more, for
  the reason above: nothing in that tool answers *which box holds this pool*, and
  a kind with no reader cannot be declared (rule 2). The running workers, their
  slots, their deadlines, their reports and their reclamation were always
  entirely nova-swarm's, and this tool still has no verb that looks at a job.
- **`nova-update`, the version layer.** nova-admin declares no version of anything.
  What is installed on a box and what is the latest its source publishes is
  nova-update's file and nova-update's question; a row here that named a version
  would be a second answer to a question that already has a file.
- **`nova-secrets`, the credential layer.** A `secret` row is a **name**, and it
  has **no act**: this tool measures whether a secret of that name exists and
  refuses every `apply` on one, naming nova-secrets and the person (**the
  kinds**). It never reads, writes, prints, seals or decrypts a value, holds
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
- **It does not run another program.** No `exec`, no shell, no argv, no sibling
  tool's binary on any path it takes (rule 23). Everything it reads and
  everything it writes is one host's API.
- **It does not write a file into a repository.** A `workflow` row is measured
  and has no act, in either direction, because a contents write is a direct
  commit to the default branch that the rules this declaration declares exist to
  refuse — and a tool that spends a bypass on the estate's own gate to satisfy
  the estate's own declaration is not enforcing anything (**the kinds**).
- **It does not declare a box, an account, a line or a worker pool.** Those were
  rows in drafts 1 and 2 and have no reader anywhere in this tree; a kind with no
  reader cannot be declared (rule 2), and what is left of this tool is one host's
  repository policy.
- **It does not print a command that carries a host's text.** `--expect` takes a
  digest and `APPLY ANNOUNCE` takes no command at all (rules 14, 16).

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
   served is on `APPLY BEFORE` and in the audit row, with the commit's `signed=`
   and `reviewed=` from the fixture beside it. A declaration carrying no
   `ruleset` row for its own declaring scope is `APPLY REFUSED`, exit 2, and the
   same declaration with one proceeds, proved by two fixtures differing in that
   row alone.
2. A header that differs by one byte is exit 2 naming line 1; a five-field and a
   seven-field row are each exit 2 naming the line number; an empty field is
   refused and `-` is accepted; a `#` line and the header are not counted in
   `rows=`; a row whose kind is on no row of **the kinds** is
   exit 2 naming the line number and the kind, and the four kinds drafts 1 and 2
   admitted (a box, an account on a box, a line, a pool) are each exit 2 by that
   same path, proved by a fixture declaring one of each. Two `seat` rows are exit
   2 naming both line numbers.
3. A source test: no forge hostname, organization, branch name, harness name or
   person name is a literal outside the address builder and the test fixtures. A
   repository whose default branch is not the common one takes exactly the same
   path as one whose is, proved by two fixtures differing only in that field.
4. `plan` opens no file under any path but `--fleet`, spawns no process at all,
   and reads every value from the test server; a
   fixture repository checked out beside the run with a different content is not
   read; a fixture key file beside the run is never opened by any verb; a host
   that answers nothing makes its rows `UNKNOWN` and the run exit
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
   returns 404 there proves the test would catch the regression. A rule whose
   `origin` the host does not give is `APPLY REFUSED`. A branch carrying both mechanisms is measured as two
   rows, and a declaration naming one of them reports the other as `EXTRA`.
9. Five fixtures, each differing from the next in one list, and no two of them
   producing the same verdict:
   - a required context with **no run and no status on any head the server
     reports**, and the declared producer's file **absent**, is `PLAN WEDGE
     emitted=no present=no`, exit 1 — even when every row matches;
   - the same with **no `workflow` row at all** is `PLAN WEDGE …
     producer=undeclared`, which is 2026-08-29 and is the case a declaration
     alone could never have caught;
   - the same with the declared producer's file **present** is
     `PLAN NOTE … never-run`, exit 0, and is **not** counted in `wedges=`: a
     gate on its first day is not a wall;
   - a context whose only evidence is a check run on the head of an **open pull
     request** — none on the default branch's head — is not a wedge and prints
     `emitted=yes from=check-run head=pull`. This fixture fails against draft 2's
     rule and is the test that pins the repair;
   - a context whose only evidence is a **commit status** and not a check run is
     not a wedge and prints `from=status`. A server that answers the two lists
     from one handler cannot pass this test.
   A `workflow` row whose `want` is
   `emits:<context>` pairs with the check row of the same context and with no
   other, resolved through the run's workflow **path** and never a basename,
   proved by a fixture whose file basename matches a *different* context and
   whose run path matches this one.
   `apply` of a `check:…:present` whose producer's file is absent from the wire
   is exit 2 naming the producer; the same act with the producer present and
   `emitted=no` **succeeds**, which is the new-gate case draft 2 made impossible;
   a fixture reproduces the three-repository case of 2026-08-29 and the count line
   carries `wedges=3`.
   `apply` on any `workflow` row, in either direction, is `APPLY REFUSED`, exit
   2, and the server records **no contents write of any kind**.
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
    2 at load; both `feature` values are refused without a ruling and are
    `act=unjudged`, not `act=widens`; a `repo` change to the default branch is
    `act=unjudged`; a `collab` row to any role is `act=unjudged` and a `collab
    absent` is `act=narrows`, proved without the test holding any ordering of
    roles.
    **The producer half decides a `check` direction**, proved by three fixtures
    that differ only in the wire's producer: a row `@<named>` against a wire with
    no requirement is `narrows`; a row `@any` against a wire with no requirement
    is `narrows`; a row `@any` against a wire **pinned to a named producer** is
    `widens` and is refused without a ruling. The third fixture fails against
    draft 2 and is the test that pins the repair.
    An `unjudged` act lands in `AUDIT DAY unjudged=` and **not** in `widened=`,
    asserted on a fixture carrying one of each on one day.
12. A test server whose read form answers with a header naming **what that
    endpoint accepts**, the act's permission absent from it, produces
    `have=unknown header_names=endpoint` and the act **proceeds** — this is the
    ordinary case on a real host, it is what draft 2 refused every time, and the
    fixture is the one that fails against draft 2. The mutating call is then
    refused with a permission header and the run is
    `APPLY REFUSED … permission=<name>`, exit 2, with the server recording
    **exactly one** mutating attempt and no retry.
    A second server answers a header naming **the grants the presented token
    holds**, the act's permission absent from it: `have=no
    header_names=token`, `APPLY REFUSED … permission=<name>`, exit 2, and the
    server asserts **no mutating request was ever sent**.
    A third server returns no permission header at all: `have=unknown`, the act
    proceeds. No fixture ever produces `have=yes`, and no fixture produces a
    `have=no` from an endpoint-acceptance header.
13. A `collab` row whose key equals the identity the process measures for itself
    is exit 2 with a person's command printed, with and without a matching ruling
    row; the same row for any other login proceeds. A `bypass` row naming a
    **repository role** the acting identity holds, one naming a **team** it is
    in, and one naming the **organization's administrators** while the identity
    is an owner, are each exit 2 on the same reasoning, proved by fixtures that
    differ only in the acting identity's reported roles, teams and organization
    role; a server that will not report the
    reach makes the same act exit 2 with `reach=unknown`, never proceed — and a
    fixture that answers the team read and refuses the organization read is that
    case, which is the steady state on a repository-scoped token.
14. A server that accepts the write and then reports the old value produces
    `matched=no`, exit 1, with both values, **and an audit row**; a server that
    returns 502 to the write and the new value to the read produces `matched=yes`
    and exit 0, because the readback and not the response is the evidence. A wire
    whose digest does not equal `--expect` is exit 2 naming both digests, with no
    mutating call
    sent. A wire that already equals `want` is `APPLY NOTE already`, exit 0, with
    no mutating call and no audit row. A whole-object write whose readback shows
    an undeclared facet moved is `matched=no`, exit 1, naming the facet.
    **The request body is `BEFORE`'s object with one facet changed**, asserted by
    a server that records the body it received: a fixture object carrying three
    facets nobody declared comes back with all three byte for byte, and a build
    that composes the body from the declaration fails this test.
    **No command the tool prints carries a wire value**, asserted on a fixture
    whose wire value is a nested object containing quotes, braces, a newline and a
    shell metacharacter: every printed command carries `sha256:` and twelve hex
    digits in the `--expect` position and nothing else.
    A server that offers a conditional write receives the precondition the
    `BEFORE` read returned and the line says `precondition=sent:<token>`; a server
    that offers none produces `precondition=none` and the act proceeds.
15. An audit append that fails makes `apply` exit 1 when the act happened and
    exit 2 when it did not; the row is never written before the readback,
    asserted by ordering a failing readback against a server that records call
    order; the row carries the declaration blob, the origin and the `--expect`
    value, asserted field by field.
16. `apply` prints exactly one `APPLY ANNOUNCE` line, carrying the change name,
    the audit path and the row number **and no other token** — no program name,
    no value read from the wire — asserted field by field against a fixture whose
    wire value holds a shell metacharacter and a newline. It sends nothing (the
    test process has no network but the fixture server, and the server records no
    unexpected call), writes `announced=pending`, and `audit` then reports
    `unannounced=1` and names it; `audit --announced … --at … --ref …` appends,
    the count falls to 0, the earlier row is unedited and the ref is on the row.
    **`audit --announced` is an acting invocation**: run from an identity the
    `seat` row does not name it is exit 2 naming both identities and the file is
    unchanged; run while another process holds the lock it waits the jittered
    time and exits 2 naming the holder's pid; run concurrently thirty times
    against one file, one lands and the file parses at every read (test 18's
    assertions, on this verb).
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
23. A source test finds **no process execution anywhere in the tree**: no
    `os/exec`, no shell, no argv builder, no sibling tool's name as a program.
    A field carrying a pipe, a glob, an `&&` or a `$VAR` reaches no interpreter,
    because there is none. A `secret` row's `apply` is exit 2 naming the
    secrets tool, a `workflow` row's is exit 2 naming the person and the
    repository, a `seat` row's is exit 2 naming the person, a `ruling` row's is
    exit 2 — and in all four the server records **no call of any kind**.
24. A declaration with a bad header **and** a duplicate name prints both lines; an
    unknown verb and a flag typo each print one line and never the banner.

A twenty-fifth, for the shape rather than a rule: a **first-run** test that runs
`plan` against the shipped example declaration and a local fixture server, and
whose transcript is in TESTS.md — every value in it from the fixture, every name in
it a placeholder, and its exit code 1, because an example fleet that matched
perfectly would teach a reader that 0 is the normal answer.

## Open questions — each with a default, and the default stands unless a ruling says otherwise

1. **CLOSED by deletion: this tool is repository policy and nothing else.** Draft
   1 declared a box, an account on a box, a line and a swarm pool with no reader
   and no way to spell an act; both draft-1 reads found the half unimplementable.
   Draft 2 kept the four and named a sibling tool's verbs as each one's reader and
   actor. The read of draft 2 checked those names against the tree (2026-09-13):
   one program **exists nowhere in it**, and the other **has no verb** that
   answers *which box holds this pool*. A hand that is not there is not a hand,
   so by rule 2 — *a kind with no reader cannot be declared* — draft 3 deletes
   all four, along with rule 23's argv mechanism and the tool's ability to run any
   program at all.
   **What the deletion costs, stated so nobody discovers it later.** A box's
   existence, an account on a box, a line's home and a pool's slots are now
   declared state **nowhere**. That is a real gap and it is the same class of rot
   Glenn named on 2026-09-12, one layer down. The gap is preferable to four kinds
   that cannot run, and preferable to putting account creation inside the tool
   with the most reach. The way back is not a row here: it is a read verb on the
   tool that owns the fact, and a spec for it, at which point a kind could be
   added to this table with a reader that exists. A reader who wants the four
   kinds back should say **which tool's which verb** answers each one.
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
