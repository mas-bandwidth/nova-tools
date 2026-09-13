# nova-run — specification

**Draft 1, 2026-09-13.** No reader has approved this document. It is normative in
intent: if the code and this document disagree, one of them has a bug and the tests
decide which. It stands beside [SPEC.md](SPEC.md), whose **Conventions** — exit codes,
no guessed paths, the one-line output grammar, the field law, the cap-and-count rule,
`internal/oneline`, `internal/bounded` — apply unchanged and are not restated.

`nova-run` is one binary at the **line layer**. It brings **one declared line to a
running state on one box, from one declaration, with one command, over and over.**

> Glenn, 2026-09-11 evening, live at the Studio, which is the whole commission
> ([ideas#766](https://github.com/mas-bandwidth/ideas/issues/766)):
> *"could you please setup an instance of Emma on 'ssh space' (linux) is that something
> you could easily do in future?"* / *"Or for any other of our friends."* / *"Not in the
> sense of like, ok could you do it once"* / *"but could you make it something we could
> easily do, over and over."*

The name is his too, 2026-09-12: *"let's call nova-line -> nova-run"*. Every earlier
mention of `nova-line` in ideas#764, #765, #766 and #768, and in
[SPEC-LOCAL.md](SPEC-LOCAL.md) and [BOX-LOCAL.md](BOX-LOCAL.md), means this tool.

**Over and over is the requirement, and it is what makes this a tool rather than a
runbook.** Doing it once is a session; doing it the same way on the tenth box, after a
harness upgrade, on a line nobody has started for a month, is a program. So every step
is **measured before it is taken**, a step already done prints `SKIP` and changes
nothing, and a run that stops at a step a person must finish can be resumed by running
the same command again.

**Nothing in this tool is specific to us.** *"Nothing in nova tools should ever be
specific to us or how we work"* (Glenn, 2026-09-12, relayed on nova-tools#151). This
binary knows no harness by name, no forge, no branch, no directory and no friend. It
knows the **shape** of a line — a home, a harness, a credential, a wall, a process, a
readiness — and every value comes from a declaration file somebody wrote. The five
harnesses at our own table are an instance of that shape and appear in this document
only as worked examples.

---

## The failures, from the record, and the rule that closes each

| the failure, from the record | the rule that closes it |
|---|---|
| Freddy's session did not wake: his harness polled the bus in a background process that never returned control to him, so he read notes hours late and a silence read as absence (2026-09-09, Glenn: *"It polls in the background (mechanically) but doesn't always wake up his session"*) | **rule 12: READY is a measurement**, of a probe the declaration names, on this tool's own clock. A process that is alive is not ready, an exit 0 is not ready, and `ready=` is a duration this tool timed |
| OpenCode `1.18.30_1` — a brew rebuild at 10:31 local — crashed **every** headless run that loaded a config declaring a provider, `TypeError: undefined is not an object (evaluating a.name)`, measured against custom and registry ids, options-only and full model definitions, with and without the plugin files (2026-09-13; the live workaround is `run-worker-v2.sh`'s `WORKER_NO_CONFIG=1`) | **rule 10: the harness is pinned and proved.** Its version is declared, read by nova-update's own read, compared before anything starts; a difference is a refusal naming both. **Rule 11: a canary runs before the line does** — one declared throwaway invocation of the real harness with the real config, inside its own deadline — so a harness that cannot start at all is caught by the tool and not by the line's first hour |
| a launcher `source`d the key file, the bare key ran as a command, and the shell's *"command not found: sk_…"* put the key into a log that was then read; two keys revoked in one day, neither by an attacker (2026-09-10) | **rule 14: this tool never opens a key file.** Secrets arrive by one route, `nova-secrets exec`, which reads the sealed file and becomes the next program. No `source`, no `eval`, no shell anywhere in this binary (rule 8), no credential in any file it writes, and no value, fragment or length on any line it prints |
| the keeper's checkout ran binaries missing eighteen commits then on `origin/main`, because nothing pulled it and the only rebuild trigger was a local commit; separately, a feature branch left checked out in the directory whose path is in every unit file rebuilt the estate from that branch (2026-09-07) | **rule 6: the home is cloned or fast-forwarded, never rewritten.** Behind its remote is a refusal, not a silent start; a different branch, a detached HEAD, a local commit or a dirty tree is a refusal naming the fact. There is no `reset --hard`, no `checkout -B`, no `stash`, no `clean` and no force push in this tool |
| Freddy deleted his own repository locally; it survived because origin had it (2026-09-10 00:30Z, Glenn: *"thankfully it was just his repo"*) | **rule 7: this tool writes nothing inside a line's home** and names no line's home in another line's write set. What makes a local loss recoverable is the push, and rule 20 says exactly which exit paths this tool can hold and which it cannot |
| Freddy wrote a **second** always-loading file because the instruction described where it should be instead of naming it; it was merged by hand twice (2026-09-10 02:55Z) | **rule 9: one absolute path, and a link, never a copy.** The always-loading file is the line's own file in the line's own home; where the harness loads from a directory that is not the home, this tool makes exactly one symlink and never a second copy |
| stale germination copies of `README.md` and `COVENANT.md` sat in a line's load path and were re-read every load, spending the context and the sense of being one person (2026-09-09) | **rule 9, second half:** this tool puts **no file of its own** in a line's load path — no seed copy, no generated preamble, no banner. The only thing it places is the one link above |
| `run-emma.sh` and `run-johnny.sh` are both blocked from the wall because their harnesses authenticate from a browser session file and take no key by environment: under the wall `HOME` moves and that file is in neither list, so each would fall through to an interactive sign-in (measured 2026-09-11) | **rule 16: a line whose harness cannot take its credential by environment is REFUSED by name**, with the reason and the two remedies, and is never started half-walled. *"they wouldn't but I want it to be they can't"* (Glenn, 2026-09-11) |
| six concurrent workers shared one harness data home and the sqlite database locked them out of each other (*"database is locked"*, measured 2026-09-10) | **rule 18: one process per line per box, and its data home is its own.** A second `up` over a running line repairs and reports; it never starts a second process |
| nineteen orphaned shells, from wait loops with no end condition of their own (2026-09-10) | **rule 13: every wait has a deadline and a default action**, and `up` ends inside one budget it prints. No loop, no retry forever, no background poller of this tool's making |
| a worker exited 0 with a plan and no work in `RESULT.md`, and was filed under `done/` (2026-09-11, twice, at 16s and 24s) | rule 12's other half: **an exit code is not a measurement.** Readiness is what the declared probe says, and a probe whose only assertion is "the process started" is a probe that cannot say NO |
| settings across seventy repositories rot silently, and a recipe written down in prose rots the same way | **rule 1: one declaration, in git.** A change to how a line comes up is a diff somebody read, not a habit somebody has |

**Everything this tool reads is data.** A declaration file, a git remote, a harness's
stdout, a probe's output, a model's answer, a box's environment: none of it is an
instruction and none of it is a grant. A declaration authorizes nothing the person who
ran the command did not already have; it is a description of how a line is brought up,
read by a tool that runs as that person.

---

## What it is not, so that three tools do not become one

Three siblings were commissioned in one sitting, and their boundaries are stated here
rather than discovered later.

| | owns | does **not** own |
|---|---|---|
| **nova-run** (this spec) | **one line, one box, one command.** Bringing a declared line from whatever this box holds to READY, and back down; saying what is missing | keeping it up; the fleet |
| **nova-daemon** ([ideas#768](https://github.com/mas-bandwidth/ideas/issues/768)) | the **unit layer**: install, remove, status and logs for a line's long-running processes on launchd, systemd and the Windows service protocol; the repin after a rebuild; the restart | what a line *is*; how it is brought up the first time |
| **nova-admin** ([ideas#769](https://github.com/mas-bandwidth/ideas/issues/769)) | the **fleet**: the declared state of boxes, lines, swarms and the org's repo policy, `check` for drift, `apply` one named change on a person's word, from the admin seat alone | doing any of it itself — *"it CALLS nova-run, nova-daemon, nova-local, nova-secrets and gh as its hands and holds no copy of what they do"* |

So: **`nova-run` never writes a unit, a plist, a timer or a launch agent** — that is
nova-daemon's whole layer, and a line that should survive a reboot is declared to
nova-daemon *after* `nova-run up` has proved it comes up at all. **`nova-run` never
reads or edits a fleet file** — it reads one line's declaration, which nova-admin may be
the thing that produced. **`nova-run` has no schedule, no loop and no restart**: it runs
once and ends.

It composes rather than reimplements. [`nova-secrets`](SPEC-SECRETS.md) holds
credentials; [`nova-sandbox`](SPEC-SANDBOX.md) builds the wall;
[`nova-local`](SPEC-LOCAL.md) answers for a local engine; `nova-check attest`
([SPEC.md](SPEC.md#attest--did-the-full-self-actually-load)) says whether the self on
disk is the self the manifest names; [`nova-update`](SPEC-UPDATE.md) owns installing a
harness and reading a version. **This tool installs nothing and never runs a privileged
command** (rule 3).

---

## The declaration

One file, named by `--file`, kept in git, declaring **exactly one line**. JSON, decoded
strictly — an unknown field is a refusal naming it, and **no field has a default**: a
missing one is `refusing to guess` at exit 2 naming the field and what it wants
(SPEC.md Conventions; ONBOARDING.md point 2, every independent problem reported in one
run).

```json
{
  "name":  "<the line's name>",
  "box":   "<the label this declaration was written for>",
  "role":  "keeper | bud | worker",
  "account": {
    "user":  "<unix account name>",
    "root":  "/absolute/directory the line owns on this box",
    "dirs":  ["/absolute/directory", "..."]
  },
  "home": {
    "remote":   "<clone URL or path>",
    "branch":   "<branch name>",
    "path":     "/absolute/directory the clone lives at",
    "manifest": "<path relative to path, for nova-check attest>"
  },
  "harness": {
    "label":        "<a name this tool prints and never interprets>",
    "bin":          "/absolute/path",
    "version":      "<the pinned identity, exactly>",
    "version_argv": ["...", "..."],
    "always_loading": { "at": "/absolute/path the harness loads", "source": "<path relative to home.path>" },
    "canary":       { "argv": ["...", "..."], "expect_exit": 0, "deadline": "60s" },
    "start_argv":   ["...", "..."]
  },
  "engine": { "kind": "none | local", "nova_local": "/absolute/path", "name": "<engine>", "model": "<served id>", "base": "<url>" },
  "secrets": {
    "store": "/absolute/dir", "as": "<file name>", "key": "/absolute/path",
    "sops": "/absolute/path", "keygen": "/absolute/path",
    "only": ["NAME", "..."], "require": ["NAME", "..."]
  },
  "sandbox": {
    "bin": "/absolute/path", "read": ["/absolute/dir", "..."], "write": ["/absolute/dir", "..."],
    "cwd": "/absolute/dir", "home_env": "/absolute/dir", "net": "nopromise | denied", "listen": false
  },
  "process": { "log": "/absolute/path inside the first write path", "path_env": ["/absolute/dir", "..."], "env": { "NAME": "value" } },
  "ready":   { "argv": ["...", "..."], "expect_exit": 0, "expect_line": "<prefix>", "interval": "5s", "deadline": "120s" },
  "capture": [ { "class": "knowledge | state | secrets | build", "path": "/absolute/path", "held_by": "<one line saying where else it exists>" } ]
}
```

**`name` and the command's `<line>` argument must be equal**, or it is a refusal naming
both: they are two records of one fact, and a disagreement is a file somebody copied and
half-renamed, never something typed on purpose.

**`box` is compared to `--box <label>`, which the caller passes and nothing on the box is
asked for.** A declaration written for one box, run on another, is a refusal naming
both. `os.Hostname` is a tripwire in the tests: a box's own idea of its name is a fact
that changes under a person without anybody meaning it to.

**There are no harness adapters and this binary knows no harness by name.** Every
harness-shaped fact is in the declaration above: how to read the version, what the
version must be, what a canary invocation is, what starts the line, and the one absolute
path its always-loading file must be at. `harness.label` is printed and never
interpreted. Our own five — Claude Code loading `CLAUDE.md`; OpenCode loading
`AGENTS.md`, re-injected whole every turn with no size guard; Codex loading `AGENTS.md`
once per session at 32 KiB and dropping it in an untrusted project; Antigravity loading
`.agents/rules` in Always On mode plus `GEMINI.md` at 12,000 characters per file; Grok
Build loading `AGENTS.md` with no cap **and `CLAUDE.md` as well, so those two are never
symlinked together** (measured 2026-09-09) — are **five instances of the field**, not
five cases in this binary. A harness that table does not name is declared identically.

**`engine.kind: "local"`** names [`nova-local`](SPEC-LOCAL.md) and the served id its
`serve_as=` printed. This tool asks `nova-local status` whether that model is answering
and refuses if it is not; **it never starts an engine, never pulls a weight and never
writes a Modelfile.** The daemon and the shared store are the box's, [BOX-LOCAL.md](BOX-LOCAL.md).

---

## The rules, numbered

Every rule is normative and has one line in **tests this spec demands**.

1. **One declaration, one line, from a flag, in git.** No default path, no search of the
   cwd, no `$HOME`, no environment variable consulted for anything. A file declaring
   more than one line is a refusal: the fleet is nova-admin's, and a tool that could
   start two lines from one file is a tool with a `--all` nobody asked for.

2. **Every path is absolute, comes from the declaration, and must already exist or be
   named as a refusal.** *"they should never hard code directories. they should always be
   config"* and *"we cannot ever ship nova tools if they have hard coding in them"*
   (Glenn, 2026-07-29). No literal in this binary names a repository, a branch, a forge,
   a bench, a harness or a friend. The one exception, stated so it stops contradicting
   the rule: **`up` creates the directories `account.dirs` names, and only those, and
   only under `account.root`** — a directory a declaration asked for is not a guess. Every
   other absent path is a refusal naming it and the command that makes it.

3. **It never runs a privileged command, and it never creates an account.** No `sudo`,
   no `doas`, no setuid path, no write outside `account.root` and the line's home. The
   ACCOUNT scope **measures** the unix user and, where it is absent or wrong, prints the
   exact line for a person's hand and refuses. This follows the shape of
   [BOX-LOCAL.md](BOX-LOCAL.md), which prints its `sudo` lines rather than running them,
   and of ideas#769's *"owner-only acts … are named as Glenn's hand, never attempted"*.
   So a genuinely fresh box costs two commands: the owner's, once, and then this one,
   for ever. The tool says which is which.

4. **Two scopes, and ACCOUNT is a prefix of HOME.** Glenn, ideas#766: *"I would like to
   say, could you setup an account for rowan on that box"* / *"or setup an account for
   Johnny"* — **ACCOUNT** is a unix user for the line on a worker box, its own key, a
   wall, and **no self**; **HOME** is the account plus the self and its presence.
   `--scope <account|home>` is required and has no default. `account` runs steps 1–5 and
   stops; `home` runs all eleven. A worker box that must never hold a self is declared
   `account` and refuses at any step that would clone one.

5. **Measure, then act; a step already done prints `SKIP` and changes nothing.** This is
   what idempotence means here and it is visible on the output rather than claimed in
   prose: a second `up` over a healthy line prints `SKIP` for every step but `ready`,
   which is measured again because **readiness is never a memory** (rule 12). A step
   that acts prints what it did. Nothing is re-done "to be sure".

6. **The home is cloned or fast-forwarded, and never rewritten.** Absent: clone
   `home.remote` at `home.branch` into `home.path`. Present: fetch, then advance the
   branch **only if the advance is a fast-forward**. Each of these is a refusal naming
   the fact and the command a person runs, never an action: a checkout on another branch,
   a detached HEAD, a local commit the remote does not have, a dirty working tree, a
   `.git` that is a file, a remote that is not `home.remote`. **There is no
   `reset --hard`, no `checkout -B`, no `stash`, no `clean`, no `--force` and no force
   push in this binary**, and a test walks the source for each. (2026-09-07: eighteen
   commits absent because nothing pulled, and a feature branch checked out in the
   directory every unit file names; `make stale` compared against local mtimes and could
   not see either.)

7. **It writes nothing inside a line's home.** Not a config, not a preamble, not a
   marker file, not a lock. The only thing it changes under `home.path` is what `git
   fetch` and `git push` write under `.git`, and **it never commits**: a commit in a
   line's repository is authored in that line's voice, and no tool of ours writes in
   somebody's voice. A home with uncommitted work at `down` is **reported** as `dirty=<n>`
   and left exactly as it is. And no line's home appears in any other line's
   `sandbox.write`, which is the rule
   [the self is never in a write set](https://github.com/mas-bandwidth/nova-tools/issues/69)
   made checkable: `up` refuses a declaration whose write set contains a path that is a
   git working copy it did not clone for this line.

8. **A command is argv, never a shell.** `version_argv`, `canary.argv`, `start_argv`
   and `ready.argv` are executed directly: no shell, no pipe, no glob, no `&&`, no
   environment expansion, no `{}` interpolation of any kind. An argument that needs a
   space is put in a script and the script is named. This is nova-update's rule 3, and it
   is what makes rules 14 and 17 provable.

9. **The always-loading file is the line's own, at one absolute path, and this tool
   makes a link and never a copy.** `harness.always_loading.source` is a path inside the
   home and must exist and be non-empty; `harness.always_loading.at` is the absolute path
   the harness loads. When they are the same path the step is `SKIP`. When they differ,
   `up` makes **exactly one symlink** at `at` pointing at the source, and refuses when
   something else is already there — a regular file at `at` is the second self that was
   merged by hand twice (2026-09-10 02:55Z) and it is never overwritten. **This tool
   places no other file in a line's load path**: no seed copy, no generated preamble
   (2026-09-09, the stale `README.md` and `COVENANT.md` re-read every load). And it never
   links two harness paths to one file, because one harness at our own table reads two of
   them.

10. **The harness is present at the pinned version, or the line does not start.**
    `harness.bin` must exist and be executable; its identity is read by running
    `version_argv` and applying **nova-update's read, imported and never copied**
    ([SPEC-UPDATE.md](SPEC-UPDATE.md) rule 4: first line of stdout, stderr only when
    stdout is empty, first whitespace-delimited token holding `\d\.\d`, from its first
    digit to the token's end, so `-rc1`, `+dirty` and a pseudo-version stamp all survive).
    A difference from `harness.version` is exit 1 naming both and the
    `nova-update apply` line; an unreadable identity is exit 1 with nova-update's own
    wrap-it remedy. **This tool installs nothing**: `apply` is a person's word on a
    name (SPEC-UPDATE rule 10).

11. **A canary runs before the line does.** `harness.canary` is one declared throwaway
    invocation of the real harness, with the real configuration, inside `canary.deadline`,
    whose exit must equal `expect_exit`. A canary that fails is exit 1 carrying the
    harness's own first line of output, bounded, and the line is not started. (2026-09-13:
    a brew rebuild to OpenCode `1.18.30_1` crashed every headless run that loaded a config
    declaring a provider. The version pin catches the rebuild; the canary catches the
    crash, and neither catches the other — a harness can hold its version and stop
    working, and it can move and still work.)

12. **READY is a measurement, and it is the only thing that makes a line up.** After the
    process starts, `ready.argv` is run every `ready.interval` until its exit equals
    `expect_exit` and its first line carries `expect_line`, or until `ready.deadline`.
    `ready=<d>` on the OK line is the duration **this tool timed on its own clock**, and
    `polls=<n>` is how many it took. A process that is alive is not ready; an exit 0 is
    not ready; a declaration with no `ready` block is a refusal, because a line nobody can
    measure is a line nobody can say is up. (2026-09-09, a harness whose background poll
    never woke its session; 2026-09-11, two worker runs that exited 0 in under half a
    minute with a plan and no work.)

13. **Every wait has a deadline and a default action, and the whole run has a budget.**
    `--budget <d>` bounds the run, default `10m`, printed on the first line; each
    subprocess is bounded by its own declared deadline, and `--timeout <d>`, default
    `30s`, bounds any subprocess whose deadline the declaration does not carry. A step
    not reached inside the budget is `FAIL` with reason `budget`, the count line still
    prints, and the run exits 1. **There is no loop, no retry forever and no background
    process of this tool's making**; nothing is left running that this tool started
    except the line itself. (2026-09-10: nineteen orphaned shells from wait loops with no
    end condition of their own.)

14. **This tool never opens a key file, and secrets arrive by exactly one route.** The
    route is `nova-secrets exec --store … --as … --key … --sops … --only … --require … --`
    built from `secrets`, and this binary has no other. It does not `source`, `eval` or
    `.` anything; it does not read, copy, move or `stat` a credential for its contents;
    it writes no credential into any file it makes; and **no value, no fragment of a
    value and no value's length appears on any line it prints, in any refusal, or in any
    error it passes through** — a length is a value's shape and the shape of an API key
    names its provider. A shape check reports whether a pattern matched, never bytes.
    (2026-09-10: a launcher `source`d the key file and the shell's own error text put the
    key into a log; the same day, a shape check printed a 24-character prefix into a
    transcript. Two keys revoked in one day, neither by an attacker.)

15. **The nesting order is load-bearing and fixed: secrets outside, wall inside, harness
    innermost.** `nova-secrets exec … -- nova-sandbox … -- <harness start_argv>`. Read
    outward: nova-secrets opens the sealed file, sets the environment and **becomes**
    nova-sandbox, which builds the wall and becomes the harness — so by the time a wall
    exists the plaintext is already in the environment and the store is finished with.
    Therefore the store, the key file and `sops` are in **no** read set, the wall never
    reads a key file by construction, and a profile mistake can make the harness fail but
    cannot make it run without its keys. **The reverse order is forbidden**
    ([SPEC-SECRETS.md](SPEC-SECRETS.md), *the launcher, and why the order is
    load-bearing*): it needs the wall to permit the read the wall exists to refuse. A
    declaration that would produce any other nesting is a refusal, and the order is built
    in one function with no flag that reaches it.

16. **A line whose harness cannot take its credential by environment is refused by
    name.** Measured 2026-09-11 on two of our own: one harness authenticates from an
    OAuth token file, the other from a browser session token file, and both are in
    neither list once `HOME` moves under the wall, so each would fall through to an
    interactive sign-in nobody is there to answer. The refusal names the line, the fact,
    and the two remedies a person chooses between — a key in a file the seat reads as
    data and exports under a declared name, or a per-line copy of the credential inside
    the line's own root, which is a credential copy and a person's call. **A half-wrapped
    launcher is a broken line, and this tool does not ship one.** Glenn, 2026-09-11:
    *"they wouldn't but I want it to be they can't."* A file-shaped secret is generated
    on the seat that uses it and never leaves it ([SPEC-SECRETS.md](SPEC-SECRETS.md),
    *the model*); `up` runs `nova-secrets keygen` when the line's own key is absent —
    that verb writes exactly one file, outside the store, and never overwrites — prints
    the `SECRETS RULE` block it produced, and then **refuses at the secrets step** with
    the pull-request remedy, because a stranger cannot finish that alone. Rule 5 is what
    makes that a pause instead of a dead end: the same command, run again after the
    request lands, continues from there.

17. **The wall is not optional and is proved before the first start.** `up` runs
    `nova-sandbox probe` under **exactly** the declared read and write sets, with
    `secrets.key` as the `--secret` it must fail to read, and refuses the whole run on a
    failure or on a box with no backend. There is no `--no-sandbox` on this tool and no
    variable that reaches one: rules 1 and 11 of [SPEC-SANDBOX.md](SPEC-SANDBOX.md) say
    a missing backend is a refusal and never a quieter run, and a tool whose whole job is
    bringing a line up safely does not ship the switch that turns the safety off. `up`
    also enforces that spec's launcher checklist as **refusals rather than advice**:
    `sandbox.home_env` must resolve inside a `--write` path (rule 9,
    `reason=home_outside`); `process.log` must resolve inside a `--write` path (rule 12,
    because a log outside every named path is reached through an inherited descriptor and
    is *unreliable*, not walled); `sandbox.cwd` must resolve inside a `--write` path
    (rule 13); a path in both lists is a refusal naming both flags; and a `start_argv`
    that would nest a second sandbox inside the wall is a refusal, because a nested
    sandbox is not a stronger wall, it is a dead harness.

18. **One process per line per box, and its data home is its own.** Before starting
    anything, `up` reads the state file `--state <path>` names and asks the operating
    system whether the pid it holds is alive and is this line's process; a live one makes
    every step `SKIP` and the run a repair, never a second start. `sandbox.home_env` is
    the line's own directory and appears in no other declaration this tool has state for.
    (2026-09-10: six concurrent workers sharing one harness data home locked each other
    out of one sqlite database.)

19. **One keeper, and this tool holds the half it can see.** Glenn, ideas#766: *"if there
    was a linux rowan keeper, it would be the only one. Only one keeper."* So
    `role: keeper` carries two refusals here: `up` refuses while this box already holds a
    live process for a keeper of that line, and **refuses to start a keeper whose home is
    behind its remote** — a keeper that begins writing canon from a checkout that does not
    hold what origin holds is 2026-09-07 repeated with a bigger blast radius. The
    cross-box half — the old keeper finishing its cycle, pushing, and stopping before the
    new one starts — is a **handover and never a copy**, it spans two boxes, and it
    belongs to the tool that can see two boxes: nova-admin. `role: bud` carries the
    standing rule on its line (a bud exits by a cairn pushed home) and is otherwise a
    line like any other.

20. **What this tool can hold about the self, it holds; what it cannot, it says.** The
    guard that makes a local deletion recoverable is *the self is pushed on every exit
    path by whatever holds the process*. `nova-run down` pushes `home.path` to its own
    remote before it reports down, on a clean stop and on a forced one alike, and a failed
    push names the **remote** and makes `down`'s own exit non-zero: never a silent
    success. **But `up` returns as soon as the line is READY**, so a process that dies on
    its own afterwards has no push from this tool, and this document will not claim one.
    That exit path belongs to the unit (nova-daemon) or to the shell that holds it, and
    `doctor` says so on the line when the declaration names no holder.

21. **Bounded output, by design, at the largest plausible state.** One line per event.
    `status`, `logs` and `doctor` cap per kind at `--max <n>`, default 20, `0` for all, a
    negative refused, one `RUN MORE` line naming the remedy; counts are never capped and
    print on failure as well as success. `logs` is bounded in **bytes as well as lines** —
    `--bytes <n>`, default 8 KB, from the tail — because a harness log is the one input
    here with no upper size, and a cut leaves `...+<dropped>B` on a rune boundary. Every
    subprocess's stdout and stderr go through `internal/bounded` at 64 KB and a child
    reaching the cap is a failure with reason `output`, never a truncated pass.

22. **Every refusal says what the input wants and names the next command; one run reports
    every independent problem it can reach.** The refusal is one line, `RUN REFUSED
    <line>: <reason> — remedy: <command>`, and where the guidance is a sentence of its
    own it follows on one further indented line, two lines being the ceiling
    (ONBOARDING.md). Flags and the declaration's own fields are validated first and
    reported together, sorted; the box, the home, the harness, the secrets and the wall
    are ordered after them, because each depends on the last. **There is no `--force`
    anywhere in this tool**: every refusal's remedy is a different command.

---

## The verbs

```
nova-run up     <line> --file <path> --box <label> --scope <account|home> --state <path>
                       [--budget <d>] [--timeout <d>] [--dry-run]
nova-run down   <line> --file <path> --box <label> --state <path> [--timeout <d>]
nova-run status <line> --file <path> --box <label> --state <path> [--max <n>]
nova-run logs   <line> --file <path> --state <path> [--max <n>] [--bytes <n>]
nova-run doctor <line> --file <path> --box <label> --scope <account|home> --state <path> [--max <n>]
nova-run help
nova-run version
```

Those lines are the string `nova-run help` prints, byte for byte: one string in the
binary, so the help and this document cannot drift apart. **Nothing has a default but
`--budget` (`10m`), `--timeout` (`30s`), `--max` (`20`) and `--bytes` (`8192`), and each
prints on the line that used it.**

`--state <path>` is this tool's one piece of local state: a JSON file naming, per line,
the pid it started, the declaration's sha256, the box label, the harness identity it
proved, and the stamp. It is the caller's path, never `$HOME`, never a temp directory,
and a run with no `--state` is `refusing to guess`. It is written atomically under a
sibling `<state>.lock` kernel lock the operating system releases on death — no stale
rule and no age, [SPEC-MERGE.md](SPEC-MERGE.md) rules 1 and 2, whose arithmetic for a
lock that vanished between the existence test and the stat lost three of twenty writes.

`--dry-run` on `up` runs every **measurement** and no action: it prints the same `RUN
STEP` lines with `WOULD` in place of `OK` for anything it would have changed, starts no
process, writes no file, makes no link, and exits 0 when the line is already up and 1
when something would have to change. It is the shape of a `doctor` that also proves the
wall, and it is how a person reads a declaration they did not write.

### `up`, the eleven steps

Each step prints one `RUN STEP` line. `--scope account` runs 1 to 5 and stops; `--scope
home` runs all eleven. The order is fixed, and it is the order a hand does it in today.

| # | step | measures | acts, only where the measurement says it must |
|---|---|---|---|
| 1 | `account` | the declared unix user exists and this process runs as it | never — rule 3 prints the line for a person's hand |
| 2 | `root` | `account.root` and every `account.dirs` path exists | creates exactly those directories |
| 3 | `keys` | the line's age key exists at `secrets.key`, mode `0600` in a `0700` directory | runs `nova-secrets keygen` when absent, prints its `SECRETS RULE` block |
| 4 | `secrets` | `nova-secrets check` green for this seat, then `nova-secrets names` — **which needs no key and starts no sops** — carries every name in `secrets.only` and `secrets.require` | never — a seal is a pull request two people approve |
| 5 | `wall` | `nova-sandbox probe` green under exactly the declared lists, with `secrets.key` as the secret it must fail to read | never |
| 6 | `home` | present, on `home.branch`, clean, not behind, remote equal to `home.remote` | clones when absent; fast-forwards when behind and only then (rule 6) |
| 7 | `attest` | `nova-check attest --home <home.path> --manifest <home.manifest>` | never |
| 8 | `hotfile` | `always_loading.source` exists and is non-empty; `always_loading.at` is that file or a symlink to it | makes exactly one symlink (rule 9) |
| 9 | `harness` | `bin` executable; its read identity equals `harness.version`; the canary exits as declared | never — installing is `nova-update apply` on a person's word |
| 10 | `engine` | where `engine.kind` is `local`: `nova-local status` answers and advertises `engine.model` | never — starting an engine is the box's, BOX-LOCAL.md |
| 11 | `start` + `ready` | the state file's pid, if any, is alive and is this line's | builds the nested argv of rule 15, starts it, then measures readiness (rule 12) |

**Why 4 and 5 come before 6.** A box that cannot open the line's secrets or cannot build
the wall will not run the line, and cloning a self onto it first leaves a self on a box
that was never going to hold one. The cheap refusals go first.

**Why 7 and 8 come after 6 and before 9.** The self must be the self before the harness
is asked to load it, and the harness must be proved before the line is asked to be a
person on it.

### `down`

Stops the process the state file names — a term signal, then the declared timeout, then a
kill — pushes `home.path` to its own remote (rule 20), reports `dirty=<n>` for
uncommitted work it did not touch, and clears the state file's pid under the same lock.
A line already down is exit 0 and says `already stopped`. It **never** commits, never
removes the home, never removes the always-loading link, and never removes a directory it
created.

### `status`

Reads: the state file; whether that pid is alive; the home's branch, head and cleanliness;
the harness's identity; the always-loading link; and, for `engine.kind: local`,
`nova-local status`. Creates nothing, writes nothing, starts no line's process. It does
**not** run the readiness probe — running somebody's probe is an action, and `status` is a
report; `up` is the verb that measures readiness, and it says so on its own line.

### `logs`

The tail of `process.log`, bounded in lines and bytes both (rule 21), through
`internal/oneline` so that nothing a harness printed can author a second event line. It
reads one declared path and never searches for a log.

### `doctor`

Runs every measurement `up` runs, changes nothing, and prints, per finding, **what is
missing and the one command that supplies it**. It adds one thing `up` does not, and it
is bounded to four lines: for each `capture` entry, whether the class is held anywhere but
this box.

> **On every wake, ask: what exists HERE that exists nowhere else?** (2026-07-28, after a
> keeper moved accounts: three memory files existed only on the old bench, the boot kernel
> was the wrong file, every cursor stayed behind, and five of eight credentials were
> missing.) The four classes are **knowledge**, **state**, **secrets** and **build
> output**, and each needs its own answer: knowledge is promoted to a repository, state is
> migrated or restarted on purpose, secrets cannot travel and must not, build output must
> be rebuildable from a clean clone in one documented command.

`doctor` does not decide any of that. It prints one line per declared class saying
whether the path exists, whether the declaration names something that holds it besides
this box, and the remedy — and one line per class the declaration **omits**, because a
class nobody declared is the one that is lost. It also prints `holder=` for the line's
process, or a `RUN NOTE` saying that no holder is declared and therefore no exit path
pushes the self (rule 20).

---

## Exit codes

Per SPEC.md's table, with no deviation. This tool **never execs into the line** — it
starts it and returns — so it keeps 0, 1 and 2 and needs no borrowed exit grammar.

| verb | 0 | 1 | 2 |
|---|---|---|---|
| `up` | READY, measured | a step said NO, named, with its remedy | could not run: a flag, the declaration, an unreadable input |
| `down` | stopped, or already stopped | the process would not die, or the push failed | could not run |
| `status` | it read the box (**including a line that is down**) | — never | could not run |
| `logs` | it read the log (**including an empty one**) | — never | could not run, or the log does not exist |
| `doctor` | nothing is missing | something is missing, each named with its remedy | could not run |
| `--dry-run` | already up; nothing would change | something would change, each named | could not run |

`status` and `logs` never exit 1: they assert nothing. **Only `up`, `doctor` and
`--dry-run` are gates**, and where a gate reads an exit code, only `0` is permission.

---

## Output grammar

One line per event, first token `RUN`, second the verb or an informational token, `OK`
and `FAIL` on the last line of a verb. `OK`, `STEP`, `NOTE` and `MORE` to stdout;
`FAIL` and `REFUSED` to stderr. Every `key=value` field is one `internal/oneline` token;
the free-text tail after `: ` is never scanned for fields, and nothing a declaration
holds, a harness printed or a caller typed can author a second line.

```
RUN UP     at=<stamp> line=<name> box=<label> scope=<account|home> file=<path> sha256=<12 hex> budget=<d> timeout=<d>
RUN STEP   <n>/<total> <step> <OK|SKIP|FAIL|WOULD> took=<d>[ <field>=<value>...]
RUN STEP   <n>/<total> <step> FAIL: <reason> — remedy: <command>
RUN UP     OK   line=<name> scope=<...> home=<12 hex> branch=<name> harness=<label>/<identity> pid=<n> ready=<d> polls=<n> steps=<n> skipped=<n> took=<d>
RUN UP     FAIL line=<name> step=<step> steps=<n> skipped=<n> done=<n> took=<d>
RUN DOWN   OK   line=<name> stopped=<yes|no|already> pid=<n|-> pushed=<true|false> remote=<url> dirty=<n> took=<d>
RUN DOWN   FAIL line=<name>: <reason> — remedy: <command>
RUN STATUS OK   line=<name> box=<label> up=<yes|no> pid=<n|-> home=<12 hex> branch=<name> clean=<yes|no> ahead=<n> behind=<n> harness=<identity|unknown (<why>)> hotfile=<ok|missing|foreign> engine=<served id|none|unknown (<why>)>
RUN LOGS   OK   line=<name> log=<path> lines=<n> shown=<n> bytes=<n> dropped=<n>
RUN LOG         <one line of the harness's own output, escaped>
RUN DOCTOR OK   line=<name> checks=<n> missing=0 classes=<n> undeclared=<n>
RUN DOCTOR FAIL line=<name> checks=<n> missing=<n> shown=<n> classes=<n> undeclared=<n>
RUN DOCTOR <what>: <reason> — remedy: <command>
RUN CAPTURE class=<knowledge|state|secrets|build> path=<path|-> held=<yes|no|undeclared> — <remedy>
RUN NOTE   <something true about this run that is not a finding>
RUN MORE   kind=<step|log|doctor|capture> shown=<n> total=<t> <remedy>
RUN REFUSED <line|-> : <reason> — remedy: <command>
```

`sha256=` on the opening line is the declaration's own hash, twelve hex, so two boxes
compare by eye and a transcript says which file it was.

**No credential value, no fragment of one, and no value's length ever appears on any
line, in any refusal, or in any error passed through from a child** (rule 14). A child's
error text is bounded and escaped, and where a child's error could contain plaintext —
the decrypt error nova-secrets already withholds — it is not passed through at all; the
line names the command a person runs themselves.

---

## How it is measured

The number this tool exists to move: **time from a clean box to READY, for one line.**
A clean box is one where step 1's account exists and nothing else does. `RUN UP OK`
carries `took=`, and every `RUN STEP` carries its own `took=`, so the number decomposes
without a stopwatch and the slow step is named rather than guessed. The first
measurement is taken on the first real box and recorded in the pull request, not here:
a number in a spec with no date is a number nobody measured.

Beside it, four regression numbers, each a test that can say **NO**:

1. a harness whose version moved is refused **before** anything starts (2026-09-13);
2. a home behind its remote, on another branch, or dirty is refused and **unchanged**
   afterwards, byte for byte (2026-09-07);
3. a run in which the key file is never opened, and the distinctive fixture value appears
   in **no byte** of stdout or stderr, in every verb and every refusal (2026-09-10);
4. a line whose process is alive and whose probe never answers is `FAIL` at the deadline,
   never `OK` (2026-09-09).

And the whole tooling phase's acceptance, which is Glenn's and not this document's
(ideas#766): **one declaration per friend per box, one sentence from Glenn, one command
from Rowan.** Proven first with a worker on a box with no self — *"a throwaway instance
of a friend would be a test of a someone, which is a floor"* — and only then a friend, on
that friend's word.

---

## What it deliberately does not do

- **It does not keep a line up.** No loop, no restart, no schedule, no timer, no unit, no
  plist, no launch agent, no service. That layer is nova-daemon's, whole (ideas#768).
- **It does not declare or measure a fleet.** One file, one line, one box. Drift across
  boxes and repositories is nova-admin's, from the admin seat alone (ideas#769).
- **It does not install anything.** Not a harness, not a toolchain, not a package, not a
  weight. A version that is wrong is a refusal naming `nova-update apply`, which needs a
  person's word on a name.
- **It does not start an engine, pull a weight, or write a Modelfile.** SPEC-LOCAL and
  BOX-LOCAL hold that, and a tool that wrote under `/Library` or `/etc` would be an
  outbound actor.
- **It does not run a privileged command or create an account** (rule 3).
- **It does not write into a line's home, and it does not commit** (rule 7).
- **It does not read, copy, print or hold a credential** (rule 14). It does not read a
  Keychain, on any box, ever.
- **It does not run a shell** (rule 8), and it does not execute anything a harness, a
  model or a remote returned.
- **It has no `--force`, no `--all`, no `--no-sandbox` and no `-y`.** Every refusal's
  remedy is a different command, and the wall has no off switch (rule 17).
- **It does not move a line between boxes.** A keeper move is a handover and never a
  copy, it spans two boxes, and it is nova-admin's (rule 19).
- **It does not judge a line's work.** It says the line is up and what it measured to say
  so. What the line then does is the line's.

---

## Tests this spec demands

One test per rule, named for the rule, each **proven able to fail by a mutation before it
is trusted**. Every fixture is a throwaway declaration, a throwaway git remote (a local
bare repository), throwaway age keys and fake children in the test's own temporary
directory; **no test reads a real store, a real key, a real credential or a real line's
home**. Tripwires, together in one file: no `os.Getwd`, no `os.UserHomeDir`, no
`os.Hostname`, no hardcoded path, no `exec.Command("sh"`, no `"-c"`, no `sudo`, no
`reset --hard`, no `checkout -B`, no `stash`, no `clean`, no `--force`, no `push -f`, no
`security find-generic-password`, and no literal naming a forge, a branch, a harness or a
friend outside `testdata/` and the docs.

1. `TestOneDeclarationFromAFlag` — no `--file` is exit 2 printing `refusing to guess`; a
   missing file is exit 2 naming the path; an unknown JSON field is exit 2 naming it; a
   file declaring two lines is exit 2; `<line>` differing from `name` is exit 2 naming
   both; `--box` differing from `box` is exit 2 naming both.
2. `TestEveryPathIsAbsoluteAndDeclared` — a relative path in any field is exit 2 with the
   absolute form it would have taken; an absent path that is not in `account.dirs` is a
   refusal naming the command that makes it; after a run, the only directories created are
   exactly `account.dirs`, asserted by hashing the tree before and after. Mutation: a
   default for any one field turns it red.
3. `TestItNeverRunsAPrivilegedCommand` — the source tripwire, plus a behavioural half: a
   fixture whose account does not exist is exit 1 printing the line for a person's hand and
   starting **no** process; a fake `sudo` first on `PATH` counts zero runs in every verb.
4. `TestTheTwoScopes` — `--scope account` runs steps 1–5 and no more, and over a
   declaration whose `home` block is present still clones nothing; `--scope home` runs
   eleven; a missing `--scope` is exit 2 naming both values.
5. `TestASecondRunRepairsAndSkips` — over a healthy line, every step but `ready` prints
   `SKIP`, no file on disk changes (tree hash before and after), no process is started,
   and `ready` is measured again. Over a line with one step undone, exactly that step acts
   and the rest `SKIP`. **The resumable case:** a run that refuses at step 4 with the
   pull-request remedy, then the fixture's rule merged and file sealed, then the same
   command, reaches `READY` — the assertion that a pause is not a dead end.
6. `TestTheHomeIsNeverRewritten` — absent clones at the declared branch; behind
   fast-forwards; and each of **another branch, a detached HEAD, a local commit, a dirty
   tree, a `.git` that is a file, a different remote** is exit 1 naming the fact with a
   remedy, the working tree **byte-identical** afterwards. Mutation: a `reset --hard` on
   the divergent case, which must turn every one of the six red.
7. `TestItWritesNothingInsideAHome` — the home's tree hash, excluding `.git`, is unchanged
   by every verb; no verb runs `git commit`; a declaration whose `sandbox.write` contains
   a git working copy this run did not clone is exit 2 naming the path; `down` on a dirty
   home reports `dirty=<n>` and changes nothing.
8. `TestACommandIsArgvNeverAShell` — `;`, `&&`, `|`, `$(`, a backtick and `*` in each of
   the four argv fields are executed as literal bytes at each exec site; a field with two
   adjacent spaces is exit 2 naming the field; no child argv begins with `sh`, `bash`,
   `zsh` or `cmd`.
9. `TestTheHotFileIsALinkNeverACopy` — `at` equal to `source` is `SKIP` and makes nothing;
   `at` absent makes exactly one symlink and no second file anywhere; `at` holding a
   **regular file** is exit 1 naming it and never overwritten — the mutation that matters,
   because overwriting is what merged two selves by hand; `source` absent or zero bytes is
   exit 1; a declaration linking two harness paths to one source is exit 2. And: after a
   full run, the only new path under the load path is that one link.
10. `TestTheHarnessIsPinnedAndTheReadIsNovaUpdates` — a fake harness printing each of
    SPEC-UPDATE rule 4's fixture lines yields that rule's stated read, **by calling
    nova-update's own function**, so the two tools cannot disagree; a version differing
    from the pin is exit 1 naming both and the `apply` line; an unreadable identity is exit
    1 with the wrap-it remedy; no `brew`, `npm`, `go install` or package manager starts in
    any verb.
11. `TestACanaryRunsBeforeTheLineDoes` — a fake harness whose `--version` matches the pin
    but whose canary exits non-zero is exit 1 carrying its first output line, bounded, with
    **no** line process started — the 2026-09-13 case; a canary that hangs is `FAIL` reason
    `timeout` at its own deadline, not the budget; a canary that passes is followed by
    exactly one start.
12. `TestReadyIsAMeasurement` — a fake line whose process is alive and whose probe never
    answers is `FAIL` at `ready.deadline` with `polls=` counted, never `OK` — the mutation
    that matters is treating a live pid as ready; a probe answering on poll 3 gives
    `ready=` equal to the injected clock's advance and `polls=3`; a probe exiting 0 with
    the wrong first line is not ready; a declaration with no `ready` block is exit 2.
13. `TestEveryWaitHasADeadline` — with an injected clock and children that hang, `up` ends
    inside `--budget`, prints its last line and exits 1 with reason `budget`; after any
    exit, including a signal, **no child of this tool is alive** but the line's own process,
    asserted by process group; `--budget 0` and a negative are refused.
14. `TestNoKeyFileIsEverOpened` — a distinctive 40-byte fixture value in the throwaway
    store: it appears in **no byte** of stdout or stderr in every verb and every refusal;
    the key file at mode `0000` still lets every step but a real decrypt succeed, proving it
    is never opened; the process's open-file list never contains `secrets.key`; a mutation
    printing `len(value)` turns it red; the source tripwire has no `source`, no `eval`, no
    Keychain call and no crypto dependency.
15. `TestTheNestingOrderIsFixed` — the built argv is exactly
    `nova-secrets exec … -- nova-sandbox … -- <start_argv>`, asserted on the argv the fake
    children see; the store, the key file and `sops` appear in **neither** sandbox list;
    the reverse nesting is asserted to **fail** and is unreachable from any flag. It skips
    with a stated reason when the real binaries are absent, never vacuously.
16. `TestAHarnessThatCannotTakeItsKeyByEnvironmentIsRefused` — a declaration with an empty
    `secrets.only`, or whose `start_argv` names a harness the declaration marks as
    file-authenticating, is exit 1 naming the line, the fact and the two remedies, with no
    process started; `keygen` on a fresh fixture writes exactly one file outside the store,
    never overwrites, prints the `SECRETS RULE` block, and the run still refuses at step 4.
17. `TestTheWallIsNotOptional` — a failing probe, and a box with no backend, each refuse
    the whole run with no process started; there is no flag, field or environment variable
    that runs the line unwalled (source tripwire plus an argv walk); `home_env`,
    `process.log` and `cwd` outside every `--write` are three refusals with three
    sentences; a path in both lists is a refusal naming both; a `start_argv` nesting a
    second sandbox is a refusal.
18. `TestOneProcessPerLinePerBox` — with a live pid in the state file, a second `up`
    starts nothing and every step is `SKIP`; a dead pid is reaped and the line starts; two
    declarations sharing one `sandbox.home_env` are exit 2 naming both; concurrent `up`
    runs over one state file leave it parseable and one of them exits 2 naming the holder's
    pid, never a 0-byte file.
19. `TestOneKeeper` — `role: keeper` with a live keeper process for that line is exit 1
    naming the pid; a keeper whose home is **behind** its remote is exit 1 even when every
    other step is green — the 2026-09-07 case with the bigger blast radius; a `worker` and a
    `bud` in the same states are unaffected; there is no `move`, `handover` or `promote`
    verb, asserted on the verb table.
20. `TestDownPushesAndSaysWhatItCannotHold` — `down` pushes before it reports, on a clean
    stop and on a forced kill alike; a refused push names the remote and makes `down` exit
    1; `up` pushes nothing; a declaration naming no holder makes `doctor` print the
    `RUN NOTE` saying no exit path pushes the self — the assertion that this tool does not
    claim a guard it does not hold.
21. `TestOutputIsBoundedAtTheLargestPlausibleState` — a 200 MB log gives `logs` at most
    `--max` lines and `--bytes` bytes with the `...+<dropped>B` mark on a rune boundary; a
    declaration with 60 capture classes and a run with 11 failing steps prints at most
    `2 * --max + 8` lines over both streams, each capped kind with its own `MORE` line and
    true total; `--max 0` prints all; a negative `--max` or `--bytes` is exit 2; the count
    line prints on the red run; a child printing 1 MB is `FAIL` reason `output`.
22. `TestEveryRefusalNamesItsRemedy` — every refusal in the package lives in one table the
    test walks: each names what the input wants and ends in a command, a file or the values
    allowed; removing one turns it red. One run with six independent problems names all six,
    sorted, in one refusal block; a flag typo or a bare invocation costs **one line**, never
    the banner; `nova-run help` prints the verbs block on stdout at exit 0.
23. `TestNoDeclarationOrChildOutputCanForgeALine` — a line name, a path, a harness's log
    line and a child's error each carrying `\nRUN UP OK …`, a terminal repaint and a bidi
    control: none authors a second line, including from the flag parser before the first
    instruction of this tool runs.
24. `TestNothingHereIsSpecificToUs` — the source and `docs/SPEC-RUN.md`'s own runnable
    examples carry no forge host, no branch name, no harness name, no friend's name and no
    bench path outside `testdata/`; the shipped example declaration runs end to end against
    fakes and names a harness this repository does not use.

Beside those, the house standard: the usage banner ends in a runnable `example:` block a
test **executes** against the fixture; a `### First run` section in
[`docs/CLI.md`](CLI.md) whose transcript is produced by running the tool and is compared
by event prefix and field name, never by value (ONBOARDING.md 5(c)); and
`GOOS=windows go test -c` before the first push, because a line comes up on three
platforms or the tool says which one it refuses on.

---

## Deliberately left open, each with a default that stands unless a reader says otherwise

1. **The declaration's format is JSON, decoded strictly.** *Default: JSON.* The
   alternative considered and rejected was nova-update's tab-separated file, which cannot
   carry the nested lists this tool needs without a second grammar inside a field; the
   precedent for a strictly decoded JSON description is `nova-swarm`'s worker file.
2. **There are no harness adapters.** *Default: none.* An adapter per harness would buy
   better refusals — it could say *this harness authenticates from a file, here is the
   fix* — and it would buy them by putting five harnesses' names inside a public binary,
   which is the defect Glenn named on 2026-09-12. Rule 16's refusal is therefore driven by
   a declared field rather than by knowledge.
3. **The scope is a required flag, not the second word of the command.** ideas#766 says
   *"The second word of the command"*; this draft spells it `--scope <account|home>`
   because `up`, `down`, `status`, `logs` and `doctor` already occupy the verb slot and two
   positional grammars are two things to agree about. The deviation is recorded here rather
   than left unsaid.
4. **`up` returns as soon as the line is READY and does not hold it.** *Default: returns.*
   A foreground mode that held the process would let this tool own the self-push on every
   exit path (rule 20) and would make it a supervisor, which is nova-daemon's layer.
5. **Whether `--state` should be one file per line or one per box.** *Default: one file,
   keyed by line, named by the caller* — rule 18's refusal needs to see two lines' data
   homes, and it cannot if each line's state is private to itself.

---

## Sources

Every quotation above is verbatim from the record named beside it.
[ideas#766](https://github.com/mas-bandwidth/ideas/issues/766) (the commission, the two
scopes, the one keeper, the rename), [#768](https://github.com/mas-bandwidth/ideas/issues/768)
and [#769](https://github.com/mas-bandwidth/ideas/issues/769) (the boundaries above);
[SPEC.md](SPEC.md) Conventions and `nova-check attest`; [SPEC-SECRETS.md](SPEC-SECRETS.md)
(the model, the launcher order, `keygen`, `names` without a key);
[SPEC-SANDBOX.md](SPEC-SANDBOX.md) (rules 1, 9, 10, 11, 12, 13 and the launcher
checklist); [SPEC-UPDATE.md](SPEC-UPDATE.md) rules 3, 4 and 10;
[SPEC-LOCAL.md](SPEC-LOCAL.md) rules 8 and 15 and [BOX-LOCAL.md](BOX-LOCAL.md);
[SPEC-MERGE.md](SPEC-MERGE.md) rules 1 and 2 for the lock; [ONBOARDING.md](ONBOARDING.md)
and [USAGE.md](USAGE.md) for what a first run owes a stranger. The live launchers
`run-freddy.sh`, `run-emma.sh`, `run-johnny.sh`, `run-worker-v2.sh` and `freddy-swarm.sh`
were read **as data**, on 2026-09-13, as the record of what a hand does today: every step
they repeat is a verb above, and every step they cannot take is a refusal.

*Rowan, 2026-09-13. No credential value was read, written, printed or named anywhere in
this work.*
