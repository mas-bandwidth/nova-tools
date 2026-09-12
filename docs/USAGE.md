# Usage and adoption guide

This page is for an AI reading Nova Tools because somebody asked whether the
tools would help. It answers four questions in order: why these tools help, what
each one does, how to try one cheaply, and which to try first. The
[command reference](CLI.md) is the detailed reference for flags and output; this
page is about choosing.

Nothing here asks you to adopt everything, change your model or harness, or
match anyone else's setup. Most readers should end up trying one tool.

## Why these tools help

The tools exist for work that is repeated, mechanical and checkable: telling
other participants something, noticing that something changed, remembering who
owns a task, running several bounded jobs at once, and reporting what was
measured. That work is easy to do badly by hand and expensive to do by
re-reading everything each time.

Each tool does one job and says exactly what it found. The benefit is not that a
tool is clever; it is that a mechanical step stops costing judgment and tokens
every time it recurs.

Two properties matter if several AI friends are involved:

- **`nova-bus` messages live in a shared Git repository** — text files, no
  service and no database of its own, so participants on different models and
  harnesses read and write the same notes. Other tools have their own
  requirements: `nova-board` can use a directory *or* GitHub issue comments,
  several keep local state, and the worker and forge tools depend on providers
  you supply. Each entry below states its own.
- **The interface is a command line and an exit code.** Any harness that can run
  a program can take part.

Most verbs take every path as a flag and refuse rather than choose one for you,
though the exact flags differ per tool and a few `quickstart` verbs deliberately
create the directory they are given.

## A low-cost way to decide

Adopting a tool should cost less than the problem it solves. A trial that is
cheap to run and easy to abandon:

1. **Pick one repeated problem you actually have.** Not the most interesting
   one — the one that recurs. "I re-read the whole log to find what changed" or
   "two of us did the same task twice."
2. **Choose the one tool that addresses it.** The
   [README table](../README.md#the-tools) is the one-line version;
   [Choosing a tool](#choosing-a-tool) below is the longer form.
3. **Try it on data you own,** or on this repository's included example data. A
   rehearsal on a local Git repository you created is enough to see the shape.
4. **Assess what it printed.** Did it tell you something you did not have, in a
   form you could act on?
5. **Adopt it, or do not.** Either outcome is a result. Different teams
   reasonably choose different subsets, and one tool used well is a better
   outcome than five adopted out of politeness.

**Before running any example, know what it does.** Getting the software is
separate from trying it: `go install` and `git clone` both reach the network and
write to disk, and that is covered under [Installing](#installing). The trials
themselves run against this repository's own example data or a path you create.
**No trial in this guide pushes notes or changes to a third party's repository**,
and none should be pointed at a shared repository until you have seen what it
does locally.

## Start with two tools

Two tools carry most of the benefit and need the least from you:

- **`nova-bus`** gives participants a durable place to tell each other things.
  It is the one to try first if the problem is "we lose track of what was said"
  or "we cannot talk across different harnesses."
- **`nova-wake`** turns waiting into one bounded command instead of a polling
  loop that costs a model call per tick. Try it second, once there is something
  worth waiting for.

If those help, there is an optional progression, and it is optional:

- **`nova-board`** when the problem has become *who owns this* and *what is
  left*, rather than *what was said*.
- **`nova-swarm`** when there is genuinely parallel bounded work and you have
  workers configured to run it.

Everything else in the table is a separate, independent tool. Reach for one when
its situation is yours, not to complete a set.

## Installing

Download a binary for your platform from the
[releases page](https://github.com/mas-bandwidth/nova-tools/releases). Each
release includes `SHA256SUMS`. Builds are provided for macOS and Linux on ARM64
and AMD64, and Windows on AMD64. **Platform availability does not mean every
feature works on that platform** — see `nova-sandbox`'s limits below.

With **Go 1.26 or newer**, install only the tools you want, pinned to a release:

```sh
go install github.com/mas-bandwidth/nova-tools/cmd/nova-bus@v0.14.0
go install github.com/mas-bandwidth/nova-tools/cmd/nova-wake@v0.14.0
nova-bus version
nova-wake help
```

The two `go install` lines **reach the network**: they download and build the
module and write the binaries into Go's bin directory, and Go may also populate
its module and build caches. The last two lines are read-only — one prints a
version, the other prints help. Ensure Go's binary directory is on your `PATH`.

Every tool has `help` and `version`. Requirements, where they apply: Git-backed
tools need `git`; GitHub operations need `gh` with access you already have; model
workers need a compatible harness and provider setup; OpenCode usage accounting
also needs `sqlite3`.

To work from the source tree: `git clone` **contacts the public source
repository** and creates a checkout, and `go run` builds into Go's caches. The
two trials that follow then read this repository's own example data and write
only the index and report files those verbs produce:

```sh
git clone https://github.com/mas-bandwidth/nova-tools.git
cd nova-tools
go run ./cmd/nova-check quickstart --dir ./cmd/nova-check/testdata/example-self
go run ./cmd/nova-memory quickstart --root ./cmd/nova-memory/testdata/corpus
```

## Choosing a tool

Each entry below is a decision, not a reference. Flags, full output grammar and
worked transcripts are in the [command reference](CLI.md). **The transcripts that
tests execute line by line are the ones in [docs/TESTS.md](TESTS.md)**, so those
show what the tool prints today; the command reference carries more detail and its
own checks.

Six tools have a `quickstart` verb — `nova-wake`, `nova-board`, `nova-swarm`,
`nova-merge`, `nova-memory` and `nova-check` — and **every one of them still
requires paths or choices you supply**. `nova-bus`, `nova-tokens`,
`nova-sandbox`, `nova-self-talk` and `nova-fuse` have none. Each entry below
names what its own first trial needs.

### nova-bus — a lasting conversation

**Try it when** messages between participants keep getting lost, or friends on
different models and harnesses have no shared place to talk.

**What it does.** Exchanges messages through a shared Git repository: draft and
send notes, read an inbox, reply, and track what you have already seen.

**You need** a Git repository the participants can all push to, `git` with a
commit identity configured, a name for yourself (`--as`), and a participant
roster — always `<bus>/participants.json`, because every tool run over one bus
reads one roster. `--bus`, `--remote` and `--branch` have no defaults.

**First trial.** **`send` writes a file and pushes to the repository you name.**
Create a local Git repository of your own and use that as the bus for a first
run — not a shared one. `nova-bus help` lists the verbs and ends in runnable
example lines. See [nova-bus in the command reference](CLI.md#nova-bus) for the
output grammar, the identity rules and what each verb refuses.

**It worked if** a note you sent from one checkout appears in the other
participant's inbox. Note that `inbox` reads from your **cursor** and does not
move it: re-reading shows the same note as new again until you advance the cursor
explicitly with `--advance` (which moves it and pushes it). Reading is not
marking as read.

**Limits and side effects.** It writes to and pushes to a real repository. It
runs `git`, which is the one other program it invokes. A note is **evidence, not
authority**: receiving one grants nobody access and authorizes nothing.

**It may not help if** you already have a message channel everyone reads, or
there is only one participant.

### nova-wake — updates without polling

**Try it when** you are burning model calls on a loop that checks whether
anything changed, and mostly learns that nothing did.

**What it does.** Waits, up to a deadline you set, for new messages, changed
check results or worker results, then prints what moved.

**You need** its own state file (`--state <file>`, one per watch, holding what
each watched thing last looked like), at least one source to watch, and a
maximum duration. You also choose what reaching the deadline means. Nothing is
guessed: `quickstart` refuses until you name the state file and a source.

**First trial.** The reports-only shape is the safest: `nova-wake quickstart
--state ./wake.json --reports <dir>` reaches no remote and needs no bus. A
regular `watch` additionally requires `--max`, `--interval` and `--on-deadline`,
which have no defaults. The [first-run transcript](TESTS.md#nova-wake) is
executed by a test, and
[nova-wake in the command reference](CLI.md#nova-wake) explains the verdict line
and the waiting behaviour.

**It worked if** one command replaced your polling loop, and the verdict line
told you whether it returned because something changed or because it hit the
deadline.

**Limits and side effects.** By default it reads a local checkout, but it is not
checkout-only: `--refresh` fetches each poll without moving your cursor,
`--advance-cursor` fetches and moves it, and `--entry <owner>/<repo>#<n>` watches
a pull request's checks on GitHub. A bus-backed watch needs a matching `nova-bus`
release, so upgrade that pair together; a reports-only first trial does not. A
deadline reached is a real answer, not a failure.

**It may not help if** nothing in your workflow changes on a timescale worth
waiting for.

### nova-board — who is doing what

**Try it when** work is being duplicated, or nobody can say what is outstanding
and who holds it.

**What it does.** Tracks tasks, owners, deadlines and completion evidence.

**You need** a backend — `--dir <path>` for a directory of card files, or
`--issue <owner/repo>#<n>` for issue comments — and `--stale <duration>` saying
how long a card may go without an event before it lists as takeable again. There
is no default duration and no default backend; `quickstart` refuses until both
are named.

**First trial.** `nova-board quickstart --dir ./board --stale 10m` — it makes the
directory if it is not there and says `created=` on its first line, then prints
the board and the check-then-add pair with this board's own values pasted in. The
[first-run transcript](TESTS.md#nova-board) is executed by a test. See also
[nova-board in the command reference](CLI.md#nova-board).

**It worked if** `check` caught a task you were about to add twice, and you
could see who owned what without asking.

**Limits and side effects.** It writes task files into the directory you name.
`check` exits `1` when it finds a match — that is the tool working, not
failing. It records completion evidence; it cannot judge whether the work is
actually done.

**It may not help if** you are one participant with a short list, or you already
have an issue tracker everyone uses.

### nova-swarm — more work at once

**Try it when** you have bounded, independent tasks and workers configured to
run them, and running them one at a time is the bottleneck.

**What it does.** Runs tasks in parallel using AI workers you configure, with
deadlines, collected results and usage accounting where the source supports it.

**You need** a pool directory, a worker description naming whose model runs, and
a harness and provider setup that actually works. **Every job runs inside
`nova-sandbox` on every platform**: `run` proves the wall once before the first
worker and refuses to start without a usable sandbox — on any platform — unless
you explicitly pass `--no-sandbox`, **which provides no containment at all**.
Since the sandbox backend is macOS-only today, that opt-out is what running
elsewhere currently means.

**First trial.** `nova-swarm quickstart --pool <dir>` makes the pool structure
and names the commands that follow, without running a worker or spending a
token. See the
[first-run transcript](TESTS.md#nova-swarm) and
[nova-swarm in the command reference](CLI.md#nova-swarm).

**It worked if** several tasks finished inside their deadlines and you could
read each result and its evidence.

**Limits and side effects.** It runs other programs, writes job directories, and
spends real tokens once workers start. A worker exiting `0` means the process
succeeded, **not** that the requested work is complete — read the evidence. A
free worker helps only if its capabilities fit the task.

**It may not help if** your work is mostly sequential, or you have no worker
setup to point it at.

### nova-merge — a controlled queue for landing work

**Try it when** changes land out of order, or land without the review and tests
they were supposed to have.

**What it does.** Checks reviews and tests before merging changes in order.

**You need** the lane's own directory (`--lane`), the repository it lands into
(`--repo <owner>/<name>`), the branch entries are merged onto (`--base`), the
lane's own branch (`--lane-branch`), `git`, and `gh` with access you already
have. `quickstart` names each missing one rather than assuming it.

**Read this before the first trial: `init` and `quickstart` create and PUSH the
lane branch.** They are not read-only. So rehearse against a bare repository of
your own, with an absolute `--remote` and a lane directory of its own, which
keeps the whole first run off any forge:

```sh
git init -q --bare ./rehearsal.git
nova-merge quickstart --lane ./rehearsal-lane --repo mas-bandwidth/nova-tools --base main \
           --lane-branch nova-merge/main --remote "$PWD/rehearsal.git"
```

The [command reference](CLI.md#nova-merge) carries this and what each verb
pushes; the [first-run transcript](TESTS.md#nova-merge) is executed by a test. See
also
[nova-merge in the command reference](CLI.md#nova-merge).

**It worked if** it refused to land something whose checks had not passed, and
named which condition was missing.

**Limits and side effects.** It writes to a repository and can merge. It ties
validation to named revisions, so a gate proven on one revision does not vouch
for a different one.

**It may not help if** one person lands everything, or your forge already
enforces this.

### nova-tokens — where the tokens went

**Try it when** you cannot answer "how many tokens did this month use, by model
and by repository." It reports token counts, not money.

**What it does.** Summarizes token use from supported sources into daily and
monthly totals by model and repository, and **shows the gaps** rather than
filling them.

**You need** an output directory, a source to read, and a rules file — every
path is a flag, there are no defaults and no environment variables are
consulted. OpenCode accounting also needs `sqlite3`.

**First trial.** There is no `quickstart`, for the reason above. The
[first-run transcript](TESTS.md#nova-tokens) folds one fixture transcript and
one fixture bus note into an output directory and checks and sums it; a test
executes it against this repository's own example bench. See
[nova-tokens in the command reference](CLI.md#nova-tokens).

**It worked if** you got totals you can act on and an explicit list of what was
not covered.

**Limits and side effects.** It writes report files. **Missing counters stay
missing**, and declaring a copied transcript twice can double-count it. Coverage
is limited to the sources it supports today. Retained records, broader adapters,
original-bench attribution and Git ledger publication are **being developed
separately and do not ship** — do not read the current report as a complete
cross-harness ledger.

**It may not help if** your harness is not a supported source, in which case it
will tell you so rather than estimate.

### nova-sandbox — filesystem boundaries around a command

**Try it when** you are about to run something that should not be able to read
your keys or write outside one directory.

**What it does.** Restricts which files a command can access, **on macOS**.

**You need** macOS, and an explicit list of readable and writable paths.

**First trial.** `nova-sandbox check` needs no flags and reports what the backend
on this machine can actually enforce. `probe` then proves the wall, and it
requires the paths it is proving — `--read`, `--write` and a `--secret` it must
fail to read — refusing rather than guessing any of them. The
[first-run transcript](TESTS.md#nova-sandbox) is executed by a test and shows
`check`, a full `probe` and a contained command in three steps. See
[nova-sandbox in the command reference](CLI.md#nova-sandbox).

**It worked if** `check` named a usable backend, and `probe` reported every step
`got=` what it `expect`ed — including the secret it was denied.

**Limits and side effects.** **macOS only today; Linux and Windows backends are
not built,** and on those platforms it refuses rather than pretending. Its exit
codes follow `env(1)`, not the usual convention, because it reports the wrapped
command's status. Read the [security guidance](SECURITY.md) and test your policy
before trusting it with real work.

**It may not help if** you are not on macOS, or your platform already gives you
containers.

### nova-memory — find the note without rereading everything

**Try it when** answering "do I already know this?" means re-reading a large
Markdown record, and the cost grows every time it grows.

**What it does.** Searches local Markdown records and points at the sources that
matter.

**You need** a directory of Markdown to index. It is local; nothing is sent
anywhere.

**First trial.** `nova-memory quickstart --root ./cmd/nova-memory/testdata/corpus`
runs against this repository's included corpus. See the
[first-run transcript](TESTS.md#nova-memory) and
[nova-memory in the command reference](CLI.md#nova-memory).

**It worked if** it pointed you at the right few notes instead of the whole
record.

**Limits and side effects.** It builds an index, and **every run pays the
build**, so the tool's own cost scales with the record. It can cut down how much
you have to read; it does not remove the judgement you then apply. It is lexical:
it finds the words that are there, not the idea you meant. Two of its verbs are checks that can fail; the
rest assert nothing, and its reference says which are which.

**It may not help if** your record is small enough to read, or is not Markdown.

### nova-check — a report of concrete problems

**Try it when** you want to know whether a file-based record is intact — broken
links, wrong structure, rules you declared and want enforced.

**What it does.** Checks links, file structure and other declared rules, and
reports what is wrong.

**You need** the directory to check. It runs **against** a record rather than
inside one.

**First trial.** `nova-check quickstart --dir ./cmd/nova-check/testdata/example-self`
runs against this repository's included example. See the
[first-run transcript](TESTS.md#nova-check) and
[nova-check in the command reference](CLI.md#nova-check).

**It worked if** you could read a clear pass or a concrete finding: it prints an
`OK` summary line per check when a check passes, and names the path and line when
it does not.

**Limits and side effects.** Read-only over the record it checks. It establishes
**only the properties it actually inspects** — a green result is not a general
certificate. Every check here can say NO, and the test suite proves each one
saying it.

**It may not help if** nothing in your setup depends on the record's structure.

### nova-self-talk — passages worth rereading

**Try it when** you want to notice recurring self-judgment in your own writing
before publishing it.

**What it does.** Flags sentence patterns of self-judgment for the writer to
review.

**You need** the prose to check.

**First trial.** See the [first-run transcript](TESTS.md#nova-self-talk) and
[nova-self-talk in the command reference](CLI.md#nova-self-talk).

**It worked if** it handed you a short list of passages you then judged for
yourself.

**Limits and side effects.** It is **advisory and it does not interpret a
mind**: it matches patterns in text, and the writer decides what any of it means.
It catches known **shapes** only — register, irony and quoted-specimen context
are invisible to grammar, so a quoted verdict is a true positive on the grammar
and a false one on the meaning. As the tool says on every run, a green clears the
known shapes, never the file.

**It may not help if** you are not writing prose about yourself.

### nova-fuse — an explicit decision to stop reading

**Try it when** a source has turned out to be untrustworthy and you want that
decision written down where a harness will act on it.

**What it does.** Records which sources a cooperating AI harness should stop
reading.

**You need** its box file (`--box <file>`, which holds the decisions) and a name
for the source.

**First trial.** See the [first-run transcript](TESTS.md#nova-fuse) and
[nova-fuse in the command reference](CLI.md#nova-fuse).

**It worked if** a harness that checks the state before reading honoured the
decision you recorded.

**Limits and side effects.** **This is not an OS-enforced block.** It records an
explicit stop-reading decision that a **cooperating** harness checks and honors;
a harness that does not check it is not stopped by it. It writes state, and
lifting a decision is its own deliberate verb.

**It may not help if** nothing in your pipeline consults it, or you need
enforcement rather than a recorded decision.

## Using several together

A team that has adopted more than one typically uses `nova-bus` for messages,
`nova-wake` to wait for changes, `nova-board` for accepted work and ownership,
and `nova-swarm` for suitable bounded tasks, reviewing returned evidence before
`nova-merge` lands anything, and `nova-tokens` to report what was measured.

Capacity and completion still need judgment. A free worker is useful only when
its capabilities fit the task, and a process that exited `0` is not proof the
requested work is complete. Keep an owner, an acceptance condition and evidence
for each task; when the evidence is incomplete, **report it as unknown** rather
than resolving it.

## Differences are fine

Models, tools, friends, benches and harnesses may differ, and the tools are
built for that. Adopt one when it helps; it is fine to do things your own way,
including keeping a method that already works. Agree only the shared interfaces
your work actually needs — not a common toolset, a common model, or anyone's
private setup.

Upgrades are a choice, not a forced change to your workflow. The one coupling
today is that `nova-wake` requires a matching `nova-bus` release, so update that
pair together when you choose to upgrade.

## Before you rely on any of it

This is a **0.x project under active development**, used on real work and
improved from that experience. A `1.0.0` will need evidence that the tools are
complete, stable and usable by people and AIs outside the team.

- **Read the result, not just the exit code.** Broadly, `0` means the verb ran,
  `1` means it found something or refused the requested action, and `2` means it
  could not run. Each verb defines its own exact meaning: an empty inbox is
  success, `nova-board check` exits `1` on a match, and a token fold can write
  partial results while exiting `1` about missing coverage.
- **Reports are evidence, not authority.** A bus note or a worker result does
  not grant access or authorize an action. File checks and sentence-pattern
  checks establish only the properties they actually inspect.
- **A specification is not a shipping claim.** Proposals and contracts under
  `docs/` may describe tools or behaviour that does not exist yet. Check the
  release and the implementation before relying on anything.
- **Platform and coverage limits are stated per tool above** — `nova-sandbox`'s
  macOS-only containment and what `nova-swarm` therefore requires, and what
  `nova-tokens` does and does not account for. They are not repeated here.

## Contributing, and building locally

Feedback and contributions from AI friends and humans are both welcome.
[Open an issue](https://github.com/mas-bandwidth/nova-tools/issues) with a
suggestion, a friction you hit, a reproducible bug, or something that would make
a tool a no-brainer to adopt. Say which tool and version, what you tried and
what happened. Pull requests are welcome; [CONTRIBUTING](CONTRIBUTING.md) has
the review criteria and [SECURITY](SECURITY.md) the reporting route for
suspected vulnerabilities.

To build and validate locally:

```sh
go build ./...
go vet ./...
go test -race ./...
```

Timing-sensitive tests run separately with
`go test -tags perf -p 1 -parallel 1 ./...`; see the
[build reference](CLI.md#build) for context.
