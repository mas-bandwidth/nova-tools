# nova-run — specification

**Draft 3, 2026-09-13.** No reader has approved this document. It is normative in
intent: if the code and this document disagree, one of them has a bug and the tests
decide which. It stands beside [SPEC.md](SPEC.md), whose **Conventions** — exit codes,
no guessed paths, the one-line output grammar, the field law, the cap-and-count rule,
`internal/oneline`, `internal/bounded` — apply unchanged and are not restated.

> Draft 3 folds the two cold reads of draft 2 (`b2b0ca97`). What changed, in one list:
> **the child's environment is built and never inherited** (rule 15), and the probe runs in
> that same environment (rule 17); `git` is a declared tool and how it authenticates is
> stated (rule 6); a keyless line has a route, because `nova-secrets exec` has no spelling
> for an empty `--only` (rule 15); rule 12 reads `process.log` as a **file** and keys a
> refusal on the wrapper's own line and never on the number `125`; `account.dirs` are
> created `0700`; the state lock's window and the pid's identity are stated (rule 18); and
> the two refusals rule 19 made that other rules already owned are **deleted**, rather than
> reconciled by a third rule.

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
| OpenCode `1.18.30_1` — a brew rebuild at 10:31 local — crashed **every** headless run that loaded a config declaring a provider, `TypeError: undefined is not an object (evaluating a.name)`, measured against custom and registry ids, options-only and full model definitions, with and without the plugin files (2026-09-13; the live workaround is `run-worker-v2.sh`'s `WORKER_NO_CONFIG=1`) | **rule 11: a canary runs before the line does** — one declared throwaway invocation of the real harness, **through the real nesting**, inside its own deadline — so a harness that cannot start at all is caught by the tool and not by the line's first hour. **Rule 10's version pin does not close this row and rule 11 says so**: a rebuild that keeps its version string is invisible to a pin, and a version that moved while the harness still works is invisible to a canary |
| a launcher `source`d the key file, the bare key ran as a command, and the shell's *"command not found: sk_…"* put the key into a log that was then read; two keys revoked in one day, neither by an attacker (2026-09-10) | **rule 14: this tool never opens a key file.** Secrets arrive by one route, `nova-secrets exec`, which reads the sealed file and becomes the next program. No `source`, no `eval`, no shell anywhere in this binary (rule 8), no credential in any file it writes, and no value, fragment or length on any line it prints |
| the keeper's checkout ran binaries missing eighteen commits then on `origin/main`, because nothing pulled it and the only rebuild trigger was a local commit; separately, a feature branch left checked out in the directory whose path is in every unit file rebuilt the estate from that branch (2026-09-07) | **rule 6: the home is cloned or fast-forwarded, never rewritten.** Behind its remote is a refusal, not a silent start; a different branch, a detached HEAD, a local commit or a dirty tree is a refusal naming the fact. There is no `reset --hard`, no `checkout -B`, no `stash`, no `clean` and no force push in this tool. **Rule 6 closes the checkout half of that day and not the build half**: nothing here compiles anything, and the rebuild-and-repin after a fetch is the unit layer's (nova-daemon, ideas#768). Rule 6 says so rather than implying a guard this tool does not hold |
| Freddy deleted his own repository locally; it survived because origin had it (2026-09-10 00:30Z, Glenn: *"thankfully it was just his repo"*) | **rule 7: this tool writes nothing inside a line's home** and names no line's home in another line's write set. What makes a local loss recoverable is the push, and rule 20 says exactly which exit paths this tool can hold and which it cannot |
| Freddy wrote a **second** always-loading file because the instruction described where it should be instead of naming it; it was merged by hand twice (2026-09-10 02:55Z) | **rule 9: one absolute path, and a link, never a copy.** The always-loading file is the line's own file in the line's own home; where the harness loads from a directory that is not the home, this tool makes exactly one symlink and never a second copy |
| stale germination copies of `README.md` and `COVENANT.md` sat in a line's load path and were re-read every load, spending the context and the sense of being one person (2026-09-09) | **rule 9, second half:** this tool puts **no file of its own** in a line's load path — no seed copy, no generated preamble, no banner. The only things it places are the links the declaration names |
| a harness's own configuration block lives in the caller's real home, which is outside the wall the moment `HOME` moves, so a wrapped line starts with no provider configured and dies at readiness with the wrong remedy ([SPEC-SANDBOX.md](SPEC-SANDBOX.md) launcher checklist item 5; a live launcher merges the block by hand today) | **rule 9, third half:** `harness.config` is a declared list of the configuration paths the harness loads, each a link to a file **inside the line's home**, made by the same mechanism and never merged, never copied and never generated |
| `run-emma.sh` and `run-johnny.sh` are both blocked from the wall because the credential each harness is configured to use on this bench is a file — an OAuth token, a browser session — rather than an environment variable: under the wall `HOME` moves and that file is in neither list, so each would fall through to an interactive sign-in (measured 2026-09-11) | **rule 16: a line that declares `harness.credential: "file"` is REFUSED by name**, with the fact and the two remedies, and is never started half-walled. *"they wouldn't but I want it to be they can't"* (Glenn, 2026-09-11) |
| six concurrent workers shared one harness data home and the sqlite database locked them out of each other (*"database is locked"*, measured 2026-09-10) | **rule 18: one process per line per box, and its data home is its own.** A second `up` over a running line repairs and reports; it never starts a second process |
| nineteen orphaned shells, from wait loops with no end condition of their own (2026-09-10) | **rule 13: every wait has a deadline and a default action**, and `up` ends inside one budget it prints. No loop, no retry forever, no background poller of this tool's making |
| a worker exited 0 with a plan and no work in `RESULT.md`, and was filed under `done/` (2026-09-11, twice, at 16s and 24s) | rule 12's other half: **an exit code is not a measurement.** Readiness is what the declared probe says, and a probe whose only assertion is "the process started" is a probe that cannot say NO |
| settings across seventy repositories rot silently, and a recipe written down in prose rots the same way | **rule 1: one declaration, in git.** A change to how a line comes up — including the scope it comes up at — is a diff somebody read, not a habit somebody has |

**Everything this tool reads is data** (rule 23). A declaration file, a git remote, a
harness's stdout, a probe's output, a model's answer, a box's environment: none of it is
an instruction and none of it is a grant. A declaration authorizes nothing the person who
ran the command did not already have; it is a description of how a line is brought up,
read by a tool that runs as that person.

---

## What it is not, so that three tools do not become one

Three siblings were commissioned in one sitting, and their boundaries are stated here
rather than discovered later.

| | owns | does **not** own |
|---|---|---|
| **nova-run** (this spec) | **one line, one box, one command.** Bringing a declared line from whatever this box holds to READY, and back down; saying what is missing | keeping it up; the fleet; building anything |
| **nova-daemon** ([ideas#768](https://github.com/mas-bandwidth/ideas/issues/768)) | the **unit layer**: install, remove, status and logs for a line's long-running processes on launchd, systemd and the Windows service protocol; the rebuild and repin after a fetch; the restart | what a line *is*; how it is brought up the first time |
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
harness and reading a version. **Each of those binaries is named by an absolute path in
the declaration's `tools` block** — no `PATH` lookup, no sibling-of-`os.Executable`
guess, nothing consulted from the environment (rule 1). **This tool installs nothing and
never runs a privileged command** (rule 3).

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
  "scope": "account | home",
  "role":  "<a label this tool prints and never interprets>",
  "tools": {
    "secrets": "/absolute/path to nova-secrets",
    "sandbox": "/absolute/path to nova-sandbox",
    "git":     "/absolute/path to git",
    "check":   "/absolute/path to nova-check",
    "local":   "/absolute/path to nova-local"
  },
  "account": {
    "user":  "<unix account name>",
    "root":  "/absolute/directory the line owns on this box",
    "dirs":  ["/absolute/directory", "..."]
  },
  "secrets": {
    "store": "/absolute/dir", "as": "<file name>", "key": "/absolute/path",
    "sops": "/absolute/path", "age_keygen": "/absolute/path",
    "only": ["NAME", "..."], "require": ["NAME", "..."]
  },
  "sandbox": {
    "read": ["/absolute/dir", "..."], "write": ["/absolute/dir (the first is under account.root)", "..."],
    "cwd": "/absolute/dir", "home_env": "/absolute/dir", "net": "nopromise | denied", "listen": false
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
    "credential":   "env | file | none",
    "always_loading": { "at": "/absolute/path the harness loads", "source": "<path relative to home.path>" },
    "config":       [ { "at": "/absolute/path the harness loads", "source": "<path relative to home.path>" } ],
    "canary":       { "argv": ["...", "..."], "expect_exit": 0, "deadline": "60s" },
    "start_argv":   ["...", "..."]
  },
  "engine": { "kind": "none | local", "name": "<engine>", "model": "<served id>", "base": "<url>" },
  "process": { "log": "/absolute/path inside the first write path", "path_env": ["/absolute/dir", "..."], "env": { "NAME": "value" }, "holder": "<label|none>" },
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

**`scope` is the same kind of fact and is compared to `--scope` the same way.** The scope
is what decides whether a self is cloned onto this box, and a fact that decides that lives
in a file somebody reviewed, not in a word somebody typed: `--scope home` against a
declaration that says `account` is a refusal naming both, and an `account` declaration
has no `home` block at all to clone from (below). A line grows from `account` to `home`
by a commit that adds the blocks and flips the word — a diff somebody read (rule 1).

### Which blocks are present, and when

Strict decode with no defaults needs its conditionals stated, or the decoder cannot be
written. **A block whose condition is false is absent, and its absence is not a default**:
the condition is itself a declared field, so nothing is guessed. Inside a block that is
present, every field is required.

| block or field | present exactly when |
|---|---|
| `name`, `box`, `scope`, `role`, `account`, `secrets`, `sandbox`, `tools.secrets`, `tools.sandbox`, `tools.git` | always |
| `home`, `harness`, `engine`, `process`, `ready`, `capture`, `tools.check` | `scope` is `home` |
| `engine.name`, `engine.model`, `engine.base`, `tools.local` | `engine.kind` is `local` |
| `harness.config` | with `harness`; the empty list `[]` declares a harness that loads no configuration outside its home |
| `secrets.only`, `secrets.require` | with `secrets`; **both** empty exactly when `harness.credential` is `none`; and every `require` name is also an `only` name, because a `--require` that `--only` excludes is `exec`'s own 125 at step 11 and a contradiction inside one declaration is exit 2 before step 1 |

A field present when its condition is false is a refusal naming the field and the
condition — `home` in an `account` declaration is the typo that would put a self on a
worker box, and it is exit 2 before any step runs.

**`harness.credential` is the field rule 16 refuses on.** `env`: the harness takes its
credential from the environment, which is what `nova-secrets exec` sets, and `secrets.only`
names it. `file`: the harness authenticates from a file in the caller's home — an OAuth
token, a browser session — which is outside the wall the moment `HOME` moves, and the line
is refused (rule 16). `none`: the harness needs no credential at all, its engine is local
or its work needs no provider, and `secrets.only` and `secrets.require` are both empty —
**which is legal**, and is the first case ideas#766 asks to prove. **A keyless line is not
wrapped in `nova-secrets exec` at all**, because that verb cannot be asked for nothing:
`--only <NAME,...|all>` is required, *"a missing `--only` is a refusal"* at 125, and
`--only all` would hand the line every value its sealed file holds, which is the opposite of
what an empty `only` declares — *"Wide must be typed"*
([SPEC-SECRETS.md](SPEC-SECRETS.md)). So `harness.credential` chooses between exactly two
nestings and there is no third (rule 15), and the line that needs no key starts without
touching the store's plaintext path at all. **The `secrets` block stays present for a
keyless line**, because the line's age key is its identity in the store and is also the
probe's `--secret` (rules 4 and 17), and steps 3 and 4 measure it as they do for any other
line: what `none` removes is the wrapper around the line's own process, and nothing else.

**There are no harness adapters and this binary knows no harness by name.** Every
harness-shaped fact is in the declaration above: how to read the version, what the
version must be, what a canary invocation is, what starts the line, what shape its
credential is, and the absolute paths of the files it loads. `harness.label` is printed
and never interpreted. Our own five — Claude Code loading `CLAUDE.md`; OpenCode loading
`AGENTS.md`, re-injected whole every turn with no size guard; Codex loading `AGENTS.md`
once per session at 32 KiB and dropping it in an untrusted project; Antigravity loading
`.agents/rules` in Always On mode plus `GEMINI.md` at 12,000 characters per file; Grok
Build loading `AGENTS.md` with no cap **and `CLAUDE.md` as well, so those two are never
symlinked together** (measured 2026-09-09) — are **five instances of the field**, not
five cases in this binary. A harness that table does not name is declared identically.

**`engine.kind: "local"`** names [`nova-local`](SPEC-LOCAL.md) at `tools.local` and the
served id its `serve_as=` printed. This tool asks `nova-local status` whether that model
is answering and refuses if it is not; **it never starts an engine, never pulls a weight
and never writes a Modelfile.** The daemon and the shared store are the box's,
[BOX-LOCAL.md](BOX-LOCAL.md).

**`sandbox.listen` is true only with `net: "nopromise"`.** `--net-deny` and `--net-listen`
together are `SANDBOX REFUSED reason=bad_net` at 125 ([SPEC-SANDBOX.md](SPEC-SANDBOX.md),
exit table), so a declaration that would build both flags is a refusal here, naming both
fields, before anything starts.

---

## The rules, numbered

Every rule is normative and has one line in **tests this spec demands**.

1. **One declaration, one line, from a flag, in git.** No default path, no search of the
   cwd, no `$HOME`, no environment variable consulted for anything, and **no binary found
   by `PATH`**: every program this tool executes is an absolute path from `tools` or from
   `harness.bin`. **`git` is one of them.** `tools.git` is always present, because step 6
   clones and fast-forwards with it and `down` pushes with it, and a `git` resolved from
   `PATH` is exactly the guess this rule exists to refuse — a declaration with no
   `tools.git` is exit 2 naming it. A file declaring more than one line is a refusal: the fleet is
   nova-admin's, and a tool that could start two lines from one file is a tool with a
   `--all` nobody asked for.

2. **Every path is absolute, comes from the declaration, and must already exist or be
   named as a refusal.** *"they should never hard code directories. they should always be
   config"* and *"we cannot ever ship nova tools if they have hard coding in them"*
   (Glenn, 2026-07-29). No literal in this binary names a repository, a branch, a forge,
   a bench, a harness or a friend. The one exception, stated so it stops contradicting
   the rule: **`up` creates the directories `account.dirs` names, and only those, and
   only under `account.root`, each at mode `0700`** — a directory a declaration asked for is
   not a guess, and `0700` is not a taste: step 3 measures the key at *"mode `0600` in a
   `0700` directory"*, and `nova-secrets keygen` refuses a directory *"absent or not
   `0700`"* naming `mkdir -m 700 -p <dir>`
   ([SPEC-SECRETS.md](SPEC-SECRETS.md)), so a tool that made them `0755` would refuse at
   step 3 with a remedy for a directory it had itself just made, on the first run of every
   fresh account. Every
   other absent path is a refusal naming it and the command that makes it. **So
   `sandbox.home_env` must itself be one of `account.dirs`, or under one**: it is a
   directory the line needs on the first run of a fresh box, and a `home_env` nothing
   creates is a wall refusal (`reason=home_outside`) wearing a missing directory's clothes.
   **Two paths this tool does not make are made underneath it by the tools it composes, and
   they are named here rather than found by a test at 3 a.m.**: every wrapped run creates
   `<first sandbox.write>/.nova-sandbox-tmp` ([SPEC-SANDBOX.md](SPEC-SANDBOX.md) rule 8,
   *"the one directory the tool creates"*), and `nova-sandbox probe` writes and removes
   `<parent of the first sandbox.write>/.nova-sandbox-probe-<pid>` **outside** the wall,
   twice, as its control and its check. Both are exempt from this rule by name. And because
   the second of them must be writable by this account for the probe to answer at all —
   an unwritable parent is `PROBE REFUSED reason=probe_outside_unwritable`
   ([SPEC-SANDBOX.md](SPEC-SANDBOX.md), *the probe*) — **`sandbox.write`'s first entry is a
   directory under `account.root` and is never `account.root` itself**, so its parent is a
   directory this line owns. A first write path that is not is exit 2 naming both fields.

3. **It never runs a privileged command, it never creates an account, and it never
   changes user.** No `sudo`, no `doas`, no setuid path, no `su`, no write outside
   `account.root` and the line's home. **This process must already BE `account.user`** —
   step 1 measures that and refuses otherwise, naming the account; reaching the account is
   the caller's, one `ssh <user>@<box>` or one session, and the tool says so rather than
   doing it. **The printed line makes the account and not the door**: this binary has no
   field for anybody's public key, prints none, and writes no `authorized_keys` — how the
   owner then reaches that account is the owner's, and a tool that offered to place a key
   would be handing out an entry nobody could take back by editing a declaration. The ACCOUNT scope likewise **measures** the unix user and, where it is absent,
   prints the exact line for a person's hand and refuses. This follows the shape of
   [BOX-LOCAL.md](BOX-LOCAL.md), which prints its `sudo` lines rather than running them,
   and of ideas#769's *"owner-only acts … are named as Glenn's hand, never attempted"*.
   So a genuinely fresh box costs two commands: the owner's, once, and then this one,
   for ever. The tool says which is which.

4. **Two scopes, and ACCOUNT is a prefix of HOME.** Glenn, ideas#766: *"I would like to
   say, could you setup an account for rowan on that box"* / *"or setup an account for
   Johnny"* — **ACCOUNT** is a unix user for the line on a worker box, its own age key, a
   wall, and **no self**; **HOME** is the account plus the self and its presence. The scope
   is declared and `--scope <account|home>` must equal it (above); `account` runs steps
   1–5 and stops, `home` runs all eleven. **An `account` box holds no forge credential and
   therefore cannot push.** ideas#766 asks for *"its ssh key handed by nova-secrets"*;
   what step 3 makes is the line's **age** key, which opens the store and pushes nothing.
   So an account-scope line's first run ends at the secrets step with the pull-request
   remedy, always, and that is a **pause and not a failure** (rule 5): the *one command
   from Rowan* is one command that a person's approval unblocks, not one command that
   finishes alone. Handing a line a forge credential is a seal in the store, which is two
   people's hands, and this tool will not pretend otherwise.

5. **Measure, then act; a step already done prints `SKIP` and changes nothing.** This is
   what idempotence means here and it is visible on the output rather than claimed in
   prose: a second `up` over a healthy line prints `SKIP` for every step that measures
   green, and `ready` is measured again because **readiness is never a memory** (rule 12).
   A step that acts prints what it did. **A step whose measurement says NO is a refusal
   even over a line that is already running** (rule 18): `SKIP` is what a green measurement
   prints, never what a live pid excuses. Nothing is re-done "to be sure".

6. **The home is cloned or fast-forwarded, and never rewritten.** The program that does it
   is `tools.git`, by absolute path (rule 1). Absent: clone
   `home.remote` at `home.branch` into `home.path`. Present: fetch, then advance the
   branch **only if the advance is a fast-forward**. Each of these is a refusal naming
   the fact and the command a person runs, never an action: a checkout on another branch,
   a detached HEAD, a local commit the remote does not have, a dirty working tree, a
   `.git` that is a file, a remote that is not `home.remote`. **There is no
   `reset --hard`, no `checkout -B`, no `stash`, no `clean`, no `--force` and no force
   push in this binary**, and a test walks the source for each. (2026-09-07: eighteen
   commits absent because nothing pulled, and a feature branch checked out in the
   directory every unit file names; `make stale` compared against local mtimes and could
   not see either.) **What this rule does not close:** the binaries on that box were also
   never rebuilt, and **this tool compiles nothing**. The rebuild-and-repin after a fetch
   is nova-daemon's; a `capture` entry of class `build` is where a declaration names the
   one command that rebuilds it (`doctor`), and that is the whole of what this tool holds
   about it. **`secrets.store` is a second working copy, and this tool does not advance it
   either**: `nova-secrets check` invariant 8 refuses a store behind its remote, and step
   4 passes that refusal through with `git -C <store> pull --ff-only` as the remedy — *"the
   pull is the launcher's, never the tool's"* ([SPEC-SECRETS.md](SPEC-SECRETS.md)), and a
   launcher is a person's line, not this one.
   **`git` authenticates as the account and never from the store.** This is the one program
   this tool runs **outside** rule 15's nesting — no wall around it and no
   `nova-secrets exec` above it — so `home.remote` is reached with exactly what
   `account.user`'s own `git` finds for itself on this box: that account's git
   configuration and that account's keys in its real home, which is the file-shaped
   credential a seat generates and never moves ([SPEC-SECRETS.md](SPEC-SECRETS.md), *the
   model*). **No value from the store ever reaches `git`**, because rule 14 says this binary
   has no route to a value but the wrapper and the wrapper is not in this path. So **a fetch
   or a push that fails to authenticate is exit 1 naming the remote and this fact** — never a
   retry, never a prompt, never a credential read — and the remedy is a `home.remote` that
   authenticates as the account, which is a diff in the declaration or a key in that
   account's own home, and either way a person's hand.

7. **It writes nothing inside a line's home.** Not a config, not a preamble, not a
   marker file, not a lock. The only thing it changes under `home.path` is what `git
   fetch` and `git push` write under `.git`, and **it never commits**: a commit in a
   line's repository is authored in that line's voice, and no tool of ours writes in
   somebody's voice. A home with uncommitted work at `down` is **reported** as `dirty=<n>`
   and left exactly as it is. And no line's home appears in **another** line's
   `sandbox.write`: `up` refuses a declaration whose write set contains a git working copy
   that is not `home.path` itself. **The deviation from
   [the self is never in a write set](https://github.com/mas-bandwidth/nova-tools/issues/69)
   is deliberate and is stated rather than left to be noticed**: that rule was written for
   a *task*, whose write set must not contain the self it might edit; a *line* is a person
   whose home is the thing it writes in all day, and a line that cannot write its own home
   cannot keep a memory. So this line's own home is permitted in this line's own write set,
   and every other working copy is refused — including on the second run, when nothing was
   cloned by anybody.

8. **A command is argv, never a shell.** `version_argv`, `canary.argv`, `start_argv`
   and `ready.argv` are executed directly: no shell, no pipe, no glob, no `&&`, no
   environment expansion, no `{}` interpolation of any kind. An argument that needs a
   space is put in a script and the script is named. This is nova-update's rule 3, and it
   is what makes rules 14 and 17 provable.

9. **The files a harness loads are the line's own, at declared absolute paths, and this
   tool makes links and never copies.** Two kinds, one mechanism:
   **the always-loading file** — `harness.always_loading.source` is a path inside the home
   and must exist and be non-empty; `harness.always_loading.at` is the absolute path the
   harness loads. **The harness's configuration** — every entry of `harness.config` names
   the same pair, because a harness's own config block otherwise lives in the caller's real
   home and is outside the wall the moment `HOME` moves (SPEC-SANDBOX launcher checklist,
   item 5), and a line that starts with no provider configured dies at readiness with the
   wrong remedy. For each pair: when `at` and the resolved `source` are the same path the
   step is `SKIP`; when they differ, `up` makes **exactly one symlink** at `at` pointing at
   the source, and refuses when something else is already there — a regular file at `at` is
   the second self that was merged by hand twice (2026-09-10 02:55Z) and it is never
   overwritten. **This tool merges nothing, generates nothing and copies nothing**: a
   configuration this tool would have to compose is a file a person commits to the line's
   home, and the declaration names it. **It places no other file in a line's load path**:
   no seed copy, no generated preamble (2026-09-09, the stale `README.md` and
   `COVENANT.md` re-read every load). And it never links two `at` paths to one source,
   because one harness at our own table reads two of them.

10. **The harness is present at the pinned version, or the line does not start.**
    `harness.bin` must exist and be executable; its identity is read by running
    `version_argv` and applying **nova-update's read, imported and never copied**
    ([SPEC-UPDATE.md](SPEC-UPDATE.md) rule 4: first line of stdout, stderr only when
    stdout is empty, first whitespace-delimited token holding `\d\.\d`, from its first
    digit to the token's end, so `-rc1`, `+dirty` and a pseudo-version stamp all survive).
    A difference from `harness.version` is exit 1 naming both and the
    `nova-update apply` line; an unreadable identity is exit 1 with nova-update's own
    wrap-it remedy. **This tool installs nothing**: `apply` is a person's word on a
    name (SPEC-UPDATE rule 10). **A pin sees a version, not a build**: a rebuild that keeps
    its version string — a package manager's revision bump — passes this rule untouched,
    which is why rule 11 exists and why this rule does not claim the 2026-09-13 row.

11. **A canary runs before the line does, through the same nesting the line will use.**
    `harness.canary` is one declared throwaway invocation of the real harness, built as
    rule 15's nesting with `canary.argv` in the innermost slot in place of `start_argv` —
    the same `nova-secrets exec`, the same `nova-sandbox` lists, the same
    `sandbox.home_env`, the same `sandbox.cwd`, the same `process.env` — inside
    `canary.deadline`, and its exit must equal `expect_exit`. **Outside that nesting the
    canary is a different program**: it would run with this tool's own `HOME` and read the
    caller's global configuration rather than the line's, so a harness that fails only when
    its real config loads would pass, which is exactly the 2026-09-13 failure. A canary
    that fails is exit 1 carrying the harness's own first line of output, bounded, and the
    line is not started. (2026-09-13: a brew rebuild to OpenCode `1.18.30_1` crashed every
    headless run that loaded a config declaring a provider. The canary catches a harness
    that cannot start; the pin catches a harness that moved; **neither catches the other**.)
    It is also what catches a harness that nests a second sandbox of its own: inside the
    wall that is `sandbox_apply: Operation not permitted`, measured
    ([SPEC-SANDBOX.md](SPEC-SANDBOX.md) checklist item 3), so the canary dies and the line
    is never started — a measurement, where a list of harness flags in this binary would be
    harness knowledge in disguise (rule 17).

12. **READY is a measurement, and it is the only thing that makes a line up.** After the
    process starts, `ready.argv` is run every `ready.interval` until its exit equals
    `expect_exit` and its first line carries `expect_line`, or until `ready.deadline`.
    **Each poll is itself bounded by `ready.interval`**: a probe still running when the
    next poll is due is killed and counts as a NO, because a probe that hangs must not
    quietly become `--timeout`'s problem. `ready=<d>` on the OK line is the duration
    **this tool timed on its own clock**, and `polls=<n>` is how many it took. A process
    that is alive is not ready; an exit 0 is not ready; a declaration with no `ready` block
    is a refusal, because a line nobody can measure is a line nobody can say is up.
    **And a process that exits before READY ends the wait at once**, rather than waiting
    out a deadline for a probe that will never answer. **What it reports is read from
    `process.log` as a file, and it is keyed on the wrapper's line and never on a number.**
    The line's stdout and stderr are descriptors onto `process.log` and this tool never
    reads that **stream** (rule 21); so on an early exit, after the process is gone, it
    opens that **file** once, reads it bounded, and reports `FAIL` reason `refused`
    carrying the wrapper refusal line it finds there — `SANDBOX REFUSED
    reason=home_outside` is the truth, and a readiness timeout would have been a lie — or
    `FAIL` reason `died` carrying the child's exit status when there is no such line.
    **The status `125` is not the key and must not be**: *"a caller must never read its exit
    status as a check result … read the line, not the number"*
    ([SPEC-SECRETS.md](SPEC-SECRETS.md)), and a command that itself exits 125 is
    indistinguishable from a wrapper that refused
    ([SPEC-SANDBOX.md](SPEC-SANDBOX.md)). The line it looks for is the fixed refusal
    grammar of the two binaries it composes and nothing else — no harness's name and no
    harness's error format enters this test (rule 24). (2026-09-09, a harness whose
    background poll never woke its session; 2026-09-11, two worker runs that exited 0 in
    under half a minute with a plan and no work.)

13. **Every wait has a deadline and a default action, and the whole run has a budget.**
    `--budget <d>` bounds the run, default `10m`, printed on the first line; each
    subprocess is bounded by its own declared deadline, and `--timeout <d>`, default
    `30s`, bounds any subprocess whose deadline the declaration does not carry. A step
    not reached inside the budget is `FAIL` with reason `budget`, the count line still
    prints, and the run exits 1. **There is no loop, no retry forever and no background
    process of this tool's making**; nothing is left running that this tool started
    except the line itself. **And the line itself is started in a process group of its
    own** — `setpgid` on the outermost child, recorded as `pgid`, signalled as a group by
    `down` — because nova-sandbox deliberately makes none (*"the wrapped tree stays in the
    caller's group, and the caller owns pgid and reaping"*,
    [SPEC-SANDBOX.md](SPEC-SANDBOX.md) rule 12) and this tool is the caller: a signal to the
    pid alone kills the waiting wrapper and orphans the harness, which is the same day's
    failure with a better name. (2026-09-10: nineteen orphaned shells from wait loops with no
    end condition of their own.)

14. **This tool never opens a key file, and secrets arrive by exactly one route.** The
    route is `<tools.secrets> exec --store … --as … --key … --sops … --only … --require … --`
    built from `secrets`, and this binary has no other — and a line declaring
    `harness.credential: "none"` takes that route **zero** times, because it receives
    nothing and that verb cannot be asked for nothing (rule 15). It does not `source`, `eval` or
    `.` anything; it does not read, copy, move or `stat` a credential for its contents;
    it writes no credential into any file it makes; and **no value, no fragment of a
    value and no value's length appears on any line it prints, in any refusal, or in any
    error it passes through** — a length is a value's shape and the shape of an API key
    names its provider. A shape check reports whether a pattern matched, never bytes.
    **`process.env` is a git-tracked map and is therefore not a place for a credential**:
    a name that appears both in `process.env` and in `secrets.only` or `secrets.require`
    is a refusal naming it, because the two would fight over one variable and the loser
    would be silent, and a declaration that carries a secret's *value* in that map has put
    a secret in git, which is the thing the store exists to prevent. **And every
    `secrets.require` name is a `secrets.only` name**: *"a `--require` naming a key `--only`
    excludes is a refusal"* at 125 ([SPEC-SECRETS.md](SPEC-SECRETS.md)), and a refusal that
    would otherwise arrive at step 11 — after the wall is built and the harness is proved —
    is a contradiction inside one file, which is exit 2 before step 1 (rule 22).
    (2026-09-10: a launcher `source`d the key file and the shell's own error text put the
    key into a log; the same day, a shape check printed a 24-character prefix into a
    transcript. Two keys revoked in one day, neither by an attacker.)

15. **The nesting order is load-bearing and fixed: secrets outside, wall inside, harness
    innermost.** `nova-secrets exec … -- nova-sandbox … -- <harness argv>`. Read
    outward: nova-secrets opens the sealed file, sets the environment and **becomes**
    nova-sandbox, which builds the wall and becomes the harness — so by the time a wall
    exists the plaintext is already in the environment and the store is finished with.
    Therefore the store, the key file and `sops` are in **no** read set, the wall never
    reads a key file by construction, and a profile mistake can make the harness fail but
    cannot make it run without its keys. **The reverse order is forbidden**
    ([SPEC-SECRETS.md](SPEC-SECRETS.md), *the launcher, and why the order is
    load-bearing*): it needs the wall to permit the read the wall exists to refuse.
    **There are exactly two nestings, and `harness.credential` chooses between them.**
    `env` is the three-deep one above. `none` is
    `nova-sandbox … -- <harness argv>`, two deep, because a line that receives no key has
    nothing for `nova-secrets exec` to carry and that verb cannot be asked for nothing —
    `--only` is required, a missing one is 125, and `--only all` means every value in the
    file (the declaration section above). `file` is refused before either is built
    (rule 16). A declaration that would produce any other shape is a refusal, and **both
    shapes are built in one function with no flag that reaches it** — the same function
    builds the canary (rule 11) and the start (step 11), which is why the canary cannot
    drift from the thing it is a rehearsal for.

    **The child's environment is built, not inherited, and this tool is the one that builds
    it.** The outermost child of that nesting is started with exactly
    `HOME=<sandbox.home_env>`, `PATH=<process.path_env, joined with the platform's list
    separator>`, and every pair of `process.env` — **and nothing at all from the environment
    this tool's own process was started with.** Not a variable from the caller's shell, not
    a token exported at a seat, not a harness's variable in somebody's profile. Both tools
    it composes **add to what they are handed**: `nova-secrets exec` sets the keys `--only`
    names on top of the environment it is given, and nova-sandbox's rule 9 is *"the
    environment passes through minus the agent"* ([SPEC-SANDBOX.md](SPEC-SANDBOX.md)). So an inherited
    environment here is a **second route for a credential**, which rule 14 says does not
    exist, and it is the route a Go program takes by accident — `os.Environ()` plus
    additions — unless a specification says otherwise, which is why this one does. `HOME` is
    this tool's to set for the same reason: nova-sandbox *"does not set `HOME` itself"* and
    its launcher checklist makes the data home the launcher's duty, and **this launcher is
    the launcher**.

16. **A line whose harness takes its credential from a file is refused by name.** The
    declaration says which: `harness.credential: "file"` is exit 2 naming the line, the
    fact and the two remedies a person chooses between — a key in a file the seat reads as
    data and exports under a declared name, or a per-line copy of the credential inside the
    line's own root, which is a credential copy and a person's call. Measured 2026-09-11 on
    two of our own: one harness authenticates from an OAuth token file, the other from a
    browser session token file, and both are in neither list once `HOME` moves under the
    wall, so each would fall through to an interactive sign-in nobody is there to answer.
    **The refusal is on the declared shape of the credential and never on the harness's
    name**: both of those harnesses *can* take a key by environment, and what was measured
    is this bench's current auth choice for them, not a property of the program — so the
    field is what a person changes when the choice changes. **A half-wrapped launcher is a
    broken line, and this tool does not ship one.** Glenn, 2026-09-11: *"they wouldn't but
    I want it to be they can't."* A file-shaped secret is generated on the seat that uses it
    and never leaves it ([SPEC-SECRETS.md](SPEC-SECRETS.md), *the model*); `up` runs
    `nova-secrets keygen` when the line's own key is absent — that verb writes exactly one
    file, outside the store, and never overwrites — prints the `SECRETS RULE` block it
    produced, and then **refuses at the secrets step** with the pull-request remedy, because
    a stranger cannot finish that alone. Rule 5 is what makes that a pause instead of a dead
    end: the same command, run again after the request lands, continues from there.

17. **The wall is not optional and is proved before the first start.** `up` runs
    `nova-sandbox probe` under **exactly** the declared read and write sets, with
    `secrets.key` as the `--secret` it must fail to read, and refuses the whole run on a
    failure or on a box with no backend. **It runs it in the environment rule 15 builds** —
    `HOME=<sandbox.home_env>` — because the probe's own rule 9 check runs before the policy
    is built, so a probe run with this tool's inherited `HOME` is `PROBE REFUSED
    reason=check … home_outside` on every real box: *"A caller that runs the probe runs it
    with the job's data home"* ([SPEC-SANDBOX.md](SPEC-SANDBOX.md), *the probe*). **And it
    passes `--net-deny` exactly when `sandbox.net` is `denied`**, because that flag is what
    a backend refuses at 125 `reason=net_unenforceable`, and a denial this box cannot
    enforce must be the step-5 refusal it exists to be rather than a death at step 9 or 11. There is no `--no-sandbox` on this tool and no
    variable that reaches one: rules 1 and 11 of [SPEC-SANDBOX.md](SPEC-SANDBOX.md) say
    a missing backend is a refusal and never a quieter run, and a tool whose whole job is
    bringing a line up safely does not ship the switch that turns the safety off. `up`
    also enforces that spec's launcher checklist as **refusals rather than advice**:
    `sandbox.home_env` must resolve inside a `--write` path (rule 9,
    `reason=home_outside`); `process.log` must resolve inside a `--write` path (rule 12,
    because a log outside every named path is reached through an inherited descriptor and
    is *unreliable*, not walled); `sandbox.cwd` must resolve inside a `--write` path
    (rule 13); a path in both lists is a refusal naming both flags; **`secrets.store`,
    `secrets.key` or `secrets.sops` resolving inside any `sandbox.read` or `sandbox.write`
    is exit 2 naming both fields**, because a `--secret` inside a list is that spec's own
    exit 2 `reason=secret_inside_allow`, *"a misconfiguration, not a failed probe"*
    (rule 6 there), and rule 15's whole argument is that those three paths are in neither
    list; and `sandbox.listen`
    with `net: "denied"` is a refusal, because that pair is `reason=bad_net` at 125. **The
    one refusal this rule does not try to make is a semantic one**: a `start_argv` whose
    element is `tools.sandbox` itself, or whose basename is the platform wrapper the wall
    is built on, is refused here because it is *detectable*; a harness's own private
    sandbox flag is not detectable without knowing that harness, and knowing a harness by
    name is the defect Glenn named on 2026-09-12. Rule 11's canary is what catches that
    one, as a dead harness before the line starts, which is a measurement rather than a
    list.

18. **One process per line per box, and its data home is its own.** Before starting
    anything, `up` takes the state lock, reads the state file `--state <path>` names, and
    asks the operating system whether the pid it holds is alive **and is the process this
    tool started**: the state file records that process's **start time** beside its pid,
    and the test is the pair, because a pid on its own is reused. A reused pid answering
    "alive" would skip every step, leave the probe saying NO for the whole of
    `ready.deadline`, and report a readiness failure on a line that is simply down and that
    nothing would ever restart — the dead-pid branch that reaps and starts would never be
    taken.
    A live one makes **every step that measures green print `SKIP`** and the run a repair,
    never a second start. **A step that measures NO over a live line is still a refusal**,
    named, with its remedy: a harness upgraded underneath a running line is rule 10's exit 1
    at step 9 whether the line is up or not, and a `harness.config` path replaced by a
    regular file is rule 9's exit 1 at step 8 — a repair that printed `SKIP` over a failed
    measurement would be the memory rule 12 forbids, wearing an idempotence costume.
    Readiness is measured again on a repair as on any other run (rules 5 and 12, which this
    rule obeys rather than re-decides).
    **The lock's window is stated, because the failure it prevents happens in seconds**: the
    lock is taken before the state file is read and released after `pid`, `pgid`, the start
    time, `home_env` and `process.log` are written — which is **before the first readiness
    poll**, never across the ready wait, so a Ctrl-C during that wait cannot leave a running
    line the next `up` has no record of, and two `up` runs five seconds apart cannot both
    read no pid, both pass every step and both start. A lock another run holds is a fact of
    the box: exit 1 naming the holder's pid (rule 22). `status` and `logs` read without the
    lock and take none.
    `sandbox.home_env` is the line's own directory and appears in no other **live** entry
    this tool has state for — which is why the state file records each line's `home_env`
    (below) and not only its pid; a line that is down claims no data home, and `down` leaves
    its `home_env` recorded as a report rather than as a claim. (2026-09-10: six concurrent
    workers sharing one harness data home locked each other out of one sqlite database.)

19. **One keeper — and this rule's job is to name which tool holds each half, because this
    one holds no half alone.** Glenn, ideas#766: *"if there was a linux rowan keeper, it
    would be the only one. Only one keeper."* Draft 2 made two refusals here and **both are
    deleted**, each because a rule that already existed owned it, and two rules giving one
    state two exit codes is a specification nobody can implement:
    a live process for this line is **rule 18's repair at exit 0**, and a keeper's exit 1
    over the same state file was the second answer;
    a home behind its remote is **rule 6's exit 1 at step 6**, for every line and not only
    a keeper, taken before any role is looked at — and rule 19's version refused *after*
    the step that fixes it, so a keeper could never come up on any day origin had moved,
    which is every day. 2026-09-07 is rule 6's row in the table above and it stays there.
    The cross-box half — the old keeper finishing its cycle, pushing, and stopping before
    the new one starts — is a **handover and never a copy**, it spans two boxes, and it
    belongs to the tool that can see two boxes: nova-admin.
    **So `role` is a label this tool prints and never interprets, `"keeper"` included.**
    A single-box tool that advertised "only one keeper" would be claiming a guard it cannot
    hold, which is the defect rule 20 exists to refuse — applied here to this document
    itself.

20. **What this tool can hold about the self, it holds; what it cannot, it says.** The
    guard that makes a local deletion recoverable is *the self is pushed on every exit
    path by whatever holds the process*. `nova-run down` pushes `home.path` to its own
    remote before it reports down, on a clean stop and on a forced one alike, and a failed
    push names the **remote** and makes `down`'s own exit non-zero: never a silent
    success. **But `up` returns as soon as the line is READY**, so a process that dies on
    its own afterwards has no push from this tool, and this document will not claim one.
    That exit path belongs to the unit (nova-daemon) or to the shell that holds it, which
    the declaration names in `process.holder`: a label this tool prints and never
    interprets, or the word `none`. **`holder=` is on `RUN UP OK` and not only in
    `doctor`**, so the person who started the line sees the gap at the moment they created
    it, and `holder=none` is the honest answer that nothing will push this self when it
    dies.

21. **Bounded output, by design, at the largest plausible state.** One line per event.
    `status`, `logs` and `doctor` cap per kind at `--max <n>`, default 20, `0` for all, a
    negative refused, one `RUN MORE` line naming the remedy; counts are never capped and
    print on failure as well as success. `logs` is bounded in **bytes as well as lines** —
    `--bytes <n>`, default 8 KB, from the tail — because a harness log is the one input
    here with no upper size, and a cut leaves `...+<dropped>B` on a rune boundary.
    **Every subprocess this tool waits for** — a version read, a canary, a probe, a
    `check`, a `names`, a `git`, a readiness poll — has its stdout and stderr through
    `internal/bounded` at 64 KB, and a child reaching the cap is a failure with reason
    `output`, never a truncated pass. **The line's own process is not one of them**: its
    stdout and stderr are descriptors onto `process.log`, opened before the exec, and this
    tool never reads that **stream** at all — a cap on the line's own output would kill the
    line this tool exists to start. It reads the **file**, bounded, in exactly two places:
    `logs`, afterwards, as any reader would, and rule 12's early-exit report, which opens it
    once after the process is already gone. A file a reader opens is not a stream a writer
    waits on, and only the second of those is a cap the line could ever feel.

22. **Every refusal says what the input wants and names the next command; one run reports
    every independent problem it can reach.** The refusal is one line, `RUN REFUSED
    <line|->: <reason> — remedy: <command>`, and where the guidance is a sentence of its
    own it follows on one further indented line, two lines being the ceiling
    (ONBOARDING.md). **The exit boundary is one law, stated once: a fact of the
    DECLARATION is exit 2, a fact of the BOX is exit 1.** A missing, unknown, relative,
    contradictory or condition-violating field, a `<line>`/`--box`/`--scope` disagreement,
    a `credential` this tool refuses to start, a `listen` this wall cannot build: the
    declaration could not be turned into a run, and that is exit 2 — decided before any
    step, all of them reported together, sorted. An account that is absent, a store behind
    its remote, a home on another branch, a version that moved, a canary that died, a probe
    that failed, a probe that never answered, **a state lock another run holds**: a step ran
    and said NO, or the box was busy, and that is exit 1.
    The box's facts are ordered after the declaration's because each depends on the last.
    **There is no `--force` anywhere in this tool**: every refusal's remedy is a different
    command.

23. **Everything it reads is data, and nothing it reads can author a line.** A
    declaration, a git remote's name, a harness's stdout, a probe's output, a model's
    answer, a log line: each is a value this tool prints through `internal/oneline` and
    never an instruction, never a grant, and never a second event line. A line name
    carrying `\nRUN UP OK`, a harness log carrying a terminal repaint, a child's error
    carrying a bidi control: all arrive as text, including from the flag parser before this
    tool's first instruction runs. This is SPEC.md's guarantee, restated here only because
    it is a rule this spec's tests must pin.

24. **Nothing in this binary is specific to us.** No forge, no branch name, no bench path,
    no harness name, no friend's name, in the source or in any runnable example — *"Nothing
    in nova tools should ever be specific to us or how we work"* (Glenn, 2026-09-12). The
    example declaration this repository ships names a harness we do not use, and the
    document's worked examples are named as instances of a field. A test walks for it.

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
prints on the line that used it.** `--box` and `--scope` are required and are compared to
the declaration's own `box` and `scope`, which is the whole of what they do: a flag that
must equal a file is a second record of one fact, and a disagreement is a refusal naming
both.

`--state <path>` is this tool's one piece of local state: a JSON file naming, per line,
the pid it started, **the process group id it made** (rule 13 and `down`), **the start time
the operating system reports for that pid**, the declaration's sha256, the box label, the
harness identity it proved, **`sandbox.home_env`**, **`process.log`**, and the stamp. The
start time is what makes the pid an identity rather than a number that gets reused
(rule 18); `home_env` and `process.log` are what let rule 18 refuse a second **live** line
onto one data home and let `logs` answer for a line whose declaration moved. It is the
caller's path, never `$HOME`, never a temp directory, and a
run with no `--state` is `refusing to guess`. It is written atomically under a sibling
`<state>.lock` kernel lock the operating system releases on death — no stale rule and no
age, [SPEC-MERGE.md](SPEC-MERGE.md) rules 1 and 2, whose arithmetic for a lock that
vanished between the existence test and the stat lost three of twenty writes. **`up` holds
that lock from before it reads the file until after it has written the pid, and never
across the readiness wait** (rule 18); a lock another run holds is exit 1 naming its pid.

`--dry-run` on `up` runs every **measurement** and no action: it prints the same `RUN
STEP` lines with `WOULD` in place of `OK` for anything it would have changed, starts no
line process, makes no link, creates no directory, and exits 0 when the line is already up
and 1 when something would have to change. **"Already up" is a readiness claim, so
`--dry-run` measures readiness exactly as `up` does** — rule 12 forbids that word being a
memory — but only where the state file holds a live process for this line; with nothing
running there is nothing to poll and step 11 prints `WOULD`. (`status` still never runs the
probe: it reports `up=` from the pid and claims readiness nowhere. Only a gate measures
readiness, and `status` is not a gate.) **Three exemptions, named rather than discovered**:
step 5 runs `nova-sandbox probe`, which is a process, which writes inside the write set and
— twice, as its control and its check — at
`<parent of the first sandbox.write>/.nova-sandbox-probe-<pid>` **outside** it, removing
both ([SPEC-SANDBOX.md](SPEC-SANDBOX.md), *the probe*), proving the wall being the one
measurement that cannot be taken by looking; the wrapped run underneath it creates
`<first sandbox.write>/.nova-sandbox-tmp`, as every wrapped run does (that spec's rule 8);
and step 3 **never runs `keygen`** under `--dry-run` or under `doctor`, reporting the
absent key as a finding instead, because generating a key is an act with a person's name on
it. It is the shape of a `doctor` that also proves the wall, and it is how a person reads
a declaration they did not write.

### `up`, the eleven steps

Each step prints one `RUN STEP` line. `--scope account` runs 1 to 5 and stops; `--scope
home` runs all eleven. The order is fixed, and it is the order a hand does it in today.

| # | step | measures | acts, only where the measurement says it must |
|---|---|---|---|
| 1 | `account` | the declared unix user exists and **this process already runs as it** | never — rule 3 prints the line for a person's hand |
| 2 | `root` | `account.root` and every `account.dirs` path exists; `sandbox.write`'s first entry is under `account.root` | creates exactly those directories, each at mode `0700` (rule 2) |
| 3 | `keys` | the line's age key exists at `secrets.key`, mode `0600` in a `0700` directory | runs `nova-secrets keygen` when absent, prints its `SECRETS RULE` block — never under `--dry-run` or `doctor` |
| 4 | `secrets` | `nova-secrets check` green for this seat — invariant 8 included, so a store behind its remote is a refusal naming `git -C <store> pull --ff-only` — then `nova-secrets names --max 0`, **which needs no key and starts no sops**, carries every name in `secrets.only` and `secrets.require` | never — a seal is a pull request two people approve, and the store's pull is a person's line |
| 5 | `wall` | `nova-sandbox probe` green under exactly the declared lists, in the built environment (`HOME=<sandbox.home_env>`), with `--net-deny` exactly when `net` is `denied`, and `secrets.key` as the secret it must fail to read | never — the probe is the named exemption to `--dry-run`'s "no process" |
| 6 | `home` | present, on `home.branch`, clean, not behind, remote equal to `home.remote` | clones with `tools.git` when absent; fast-forwards when behind and only then (rule 6) |
| 7 | `attest` | `<tools.check> attest --home <home.path> --manifest <home.manifest>` | never |
| 8 | `files` | `always_loading.source` and every `config[].source` exist and are non-empty; each `at` is that file or a symlink to it | makes exactly one symlink per pair that differs (rule 9) |
| 9 | `harness` | `bin` executable; its read identity equals `harness.version`; the canary, **built through rule 15's nesting**, exits as declared | never — installing is `nova-update apply` on a person's word |
| 10 | `engine` | where `engine.kind` is `local`: `<tools.local> status` answers and advertises `engine.model` | never — starting an engine is the box's, BOX-LOCAL.md |
| 11 | `start` + `ready` | the state file's pid, if any, is alive **and its start time is the one recorded** (rule 18) | builds the same nesting with `start_argv` innermost, in the built environment (rule 15), starts it **in a process group of its own**, writes the state under the lock, then measures readiness (rule 12) |

**Why 4 and 5 come before 6.** A box that cannot open the line's secrets or cannot build
the wall will not run the line, and cloning a self onto it first leaves a self on a box
that was never going to hold one. The cheap refusals go first.

**Why 7 and 8 come after 6 and before 9.** The self must be the self before the harness
is asked to load it, and the harness must be proved before the line is asked to be a
person on it.

**Why the canary at 9 needs 4 and 5 to have passed.** It runs through the real nesting
(rule 11), so it needs the secrets route and the wall the line will use; that it can only
run this late is the reason the wall and the secrets are cheap refusals ahead of it.

### `down`

Stops the process the state file names — a term signal to **the process group** this tool
made at `up` (rule 13), then the declared timeout, then a kill to the same group — pushes
`home.path` to its own remote with `tools.git` (rules 6 and 20), reports `dirty=<n>` for
uncommitted work it did not touch, and clears the state file's pid, pgid and start time
under the same lock, leaving that line's `home_env` and `process.log` recorded as a report
and not as a claim (rule 18). A line already down is exit 0 and says
`already stopped`. It **never** commits, never removes the home, never removes a link,
and never removes a directory it created.

### `status`

Reads: the state file; whether that pid is alive; the home's branch, head and cleanliness;
the harness's identity; the always-loading link and every `config` link; and, for
`engine.kind: local`, `nova-local status`. Creates nothing, writes nothing, starts no
line's process. It does **not** run the readiness probe — running somebody's probe is an
action, and `status` is a report; `up` is the verb that measures readiness, and it says so
on its own line.

### `logs`

The tail of `process.log`, bounded in lines and bytes both (rule 21), through
`internal/oneline` so that nothing a harness printed can author a second event line. It
reads one declared path and never searches for a log.

### `doctor`

Runs every measurement `up` runs, changes nothing, generates no key, and prints, per
finding, **what is missing and the one command that supplies it**. It adds one thing `up`
does not: for each `capture` entry, whether the class is held anywhere but this box.

> **On every wake, ask: what exists HERE that exists nowhere else?** (2026-07-28, after a
> keeper moved accounts: three memory files existed only on the old bench, the boot kernel
> was the wrong file, every cursor stayed behind, and five of eight credentials were
> missing.) The four classes are **knowledge**, **state**, **secrets** and **build
> output**, and each needs its own answer: knowledge is promoted to a repository, state is
> migrated or restarted on purpose, secrets cannot travel and must not, build output must
> be rebuildable from a clean clone in one documented command — **and for a `capture` entry
> of class `build`, `held_by` is that command**, which is the only place this tool holds
> anything about a rebuild (rule 6).

`doctor` does not decide any of that. It prints **one line per declared `capture` entry**,
capped at `--max` with its own `RUN MORE` line, saying whether the path exists, whether the
declaration names something that holds it besides this box, and the remedy — plus **at most
four further lines, one per class the declaration omits entirely**, because a class nobody
declared is the one that is lost and there are exactly four classes. (Draft 1 said the
whole section was "bounded to four lines", which a declaration with sixty entries makes
false; the cap is the house cap and the four is the number of classes.) It also prints
`holder=` for the line's process, or a `RUN NOTE` saying that `process.holder` is `none`
and therefore no exit path pushes the self (rule 20).

---

## Exit codes

Per SPEC.md's table, with no deviation. This tool **never execs into the line** — it
starts it and returns — so it keeps 0, 1 and 2 and needs no borrowed exit grammar. Rule
22 states the boundary between 1 and 2 once: **a fact of the declaration is 2, a fact of
the box is 1.**

| verb | 0 | 1 | 2 |
|---|---|---|---|
| `up` | READY, measured | a step said NO, named, with its remedy | could not run: a flag, or the declaration |
| `down` | stopped, or already stopped | the process would not die, or the push failed | could not run |
| `status` | it read the box (**including a line that is down**) | — never | could not run |
| `logs` | it read the log (**including an empty one**) | — never | could not run, or the log does not exist |
| `doctor` | nothing is missing | something is missing, each named with its remedy | could not run |
| `--dry-run` | already up **and measured ready**; nothing would change | something would change, each named | could not run |

`status` and `logs` never exit 1: they assert nothing. **Only `up`, `doctor` and
`--dry-run` are gates**, and where a gate reads an exit code, only `0` is permission.
A child's status is never this tool's exit code, and a child's `125` is not by itself a
refusal: what this tool reports is the wrapper's own line from `process.log`, and it exits
1 (rule 12).

---

## Output grammar

One line per event, first token `RUN`, second the verb or an informational token, `OK`
and `FAIL` on the last line of a verb. `OK`, `STEP`, `NOTE` and `MORE` to stdout;
`FAIL` and `REFUSED` to stderr. Every `key=value` field is one `internal/oneline` token;
the free-text tail after `: ` is never scanned for fields, and nothing a declaration
holds, a harness printed or a caller typed can author a second line (rule 23).

```
RUN UP     at=<stamp> line=<name> box=<label> scope=<account|home> file=<path> sha256=<12 hex> budget=<d> timeout=<d>
RUN STEP   <n>/<total> <step> <OK|SKIP|FAIL|WOULD> took=<d>[ <field>=<value>...]
RUN STEP   <n>/<total> <step> FAIL: <reason> — remedy: <command>
RUN UP     OK   line=<name> scope=<...> home=<12 hex> branch=<name> harness=<label>/<identity> pid=<n> pgid=<n> holder=<label|none> ready=<d> polls=<n> steps=<n> skipped=<n> took=<d>
RUN UP     FAIL line=<name> step=<step> reason=<budget|refused|died|output|...> steps=<n> skipped=<n> done=<n> took=<d>
RUN DOWN   OK   line=<name> stopped=<yes|no|already> pid=<n|-> pgid=<n|-> pushed=<true|false> remote=<url> dirty=<n> took=<d>
RUN DOWN   FAIL line=<name>: <reason> — remedy: <command>
RUN STATUS OK   line=<name> box=<label> up=<yes|no> pid=<n|-> home=<12 hex> branch=<name> clean=<yes|no> ahead=<n> behind=<n> harness=<identity|unknown (<why>)> hotfile=<ok|missing|foreign> configs=<ok>/<n> engine=<served id|none|unknown (<why>)>
RUN LOGS   OK   line=<name> log=<path> lines=<n> shown=<n> bytes=<n> dropped=<n>
RUN LOG         <one line of the harness's own output, escaped>
RUN DOCTOR OK   line=<name> checks=<n> missing=0 classes=<n> undeclared=<n>
RUN DOCTOR FAIL line=<name> checks=<n> missing=<n> shown=<n> classes=<n> undeclared=<n>
RUN DOCTOR <what>: <reason> — remedy: <command>
RUN CAPTURE class=<knowledge|state|secrets|build> path=<path|-> held=<yes|no|undeclared> — <remedy>
RUN NOTE   <something true about this run that is not a finding>
RUN MORE   kind=<step|log|doctor|capture> shown=<n> total=<t> <remedy>
RUN REFUSED <line|->: <reason> — remedy: <command>
```

`sha256=` on the opening line is the declaration's own hash, twelve hex, so two boxes
compare by eye and a transcript says which file it was. `<line|->` is the line's name, or
`-` where the refusal happened before a name could be read at all.

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

1. a harness whose version moved is refused **before** anything starts, and a harness whose
   version held but whose canary dies is refused the same way (2026-09-13, both halves);
2. a home behind its remote, on another branch, or dirty is refused and **unchanged**
   afterwards, byte for byte (2026-09-07);
3. a run in which **this tool never opens the key file** — proven by its own open-file
   list, not by a mode — and in which the distinctive fixture value appears in **no byte**
   of stdout or stderr, in every verb and every refusal (2026-09-10);
4. a line whose process is alive and whose probe never answers is `FAIL` at the deadline,
   never `OK`; a line whose process exits before READY is `FAIL` at once with the child's
   own reason, never a readiness timeout (2026-09-09).

And the whole tooling phase's acceptance, which is Glenn's and not this document's
(ideas#766): **one declaration per friend per box, one sentence from Glenn, one command
from Rowan.** Proven first with a worker on a box with no self — *"a throwaway instance
of a friend would be a test of a someone, which is a floor"* — and only then a friend, on
that friend's word. Rule 4 records where that acceptance meets a person's hand: the first
run of a new account ends at a pull request, every time.

---

## What it deliberately does not do

- **It does not keep a line up.** No loop, no restart, no schedule, no timer, no unit, no
  plist, no launch agent, no service. That layer is nova-daemon's, whole (ideas#768).
- **It does not declare or measure a fleet.** One file, one line, one box. Drift across
  boxes and repositories is nova-admin's, from the admin seat alone (ideas#769).
- **It does not install anything and it does not build anything.** Not a harness, not a
  toolchain, not a package, not a weight, and not the line's own binaries. A version that
  is wrong is a refusal naming `nova-update apply`, which needs a person's word on a name;
  a rebuild after a fetch is nova-daemon's (rule 6).
- **It does not start an engine, pull a weight, or write a Modelfile.** SPEC-LOCAL and
  BOX-LOCAL hold that, and a tool that wrote under `/Library` or `/etc` would be an
  outbound actor.
- **It does not run a privileged command, create an account, or change user** (rule 3).
- **It does not pull the secrets store**, which is a person's launcher line and a
  refusal here (rule 6, SPEC-SECRETS invariant 8).
- **It does not write into a line's home, and it does not commit** (rule 7).
- **It does not merge, generate or template a configuration file** (rule 9). It links what
  a person committed.
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

One test per rule, named for the rule, **twenty-four rules and twenty-four tests**, each
**proven able to fail by a mutation before it is trusted**. Every fixture is a throwaway
declaration, a throwaway git remote (a local bare repository), throwaway age keys and fake
children in the test's own temporary directory; **no test reads a real store, a real key, a
real credential or a real line's home**. Tripwires, together in one file: no `os.Getwd`, no
`os.UserHomeDir`, no `os.Hostname`, no `exec.LookPath`, no hardcoded path, no
`exec.Command("sh"`, no `"-c"`, no `sudo`, no `su`, no `reset --hard`, no `checkout -B`, no
`stash`, no `clean`, no `--force`, no `push -f`, no `security find-generic-password`, no
`os.Environ`, no `authorized_keys`, and no
literal naming a forge, a branch, a harness or a friend outside `testdata/` and the docs.

1. `TestOneDeclarationFromAFlag` — no `--file` is exit 2 printing `refusing to guess`; a
   missing file is exit 2 naming the path; an unknown JSON field is exit 2 naming it; a
   file declaring two lines is exit 2; `<line>` differing from `name`, `--box` differing
   from `box`, and `--scope` differing from `scope` are each exit 2 naming both; a
   declaration with no `tools.git` is exit 2 naming it; no child
   is ever launched by a bare name (every exec site, `git` included, is asserted to receive
   an absolute path from `tools` or `harness.bin`).
2. `TestEveryPathIsAbsoluteAndDeclared` — a relative path in any field is exit 2 with the
   absolute form it would have taken; an absent path that is not in `account.dirs` is a
   refusal naming the command that makes it; a `sandbox.home_env` under no `account.dirs`
   path is exit 2 naming both fields; a first `sandbox.write` entry equal to `account.root`
   or outside it is exit 2 naming both fields; **every created directory's mode is `0700`**,
   asserted by `stat` and not by whatever umask happened to be set; after a run, the only
   directories created are exactly `account.dirs` plus the two the composed tools make by
   name (`.nova-sandbox-tmp` inside the first write path, and the probe's
   `.nova-sandbox-probe-<pid>` outside it, which must be gone again), asserted by hashing
   the tree before and after with those two exempted. Mutations: a
   default for any one field turns it red, and creating the directories at `0755` turns the
   mode half red at step 3's own measurement.
3. `TestItNeverRunsAPrivilegedCommand` — the source tripwire, plus a behavioral half: a
   fixture whose account does not exist is exit 1 printing the line for a person's hand and
   starting **no** process; a fixture whose account exists but is not the running uid is
   exit 1 naming both and starting no process; a fake `sudo` and a fake `su` first on
   `PATH` count zero runs in every verb; no verb writes an `authorized_keys` and no field
   names a public key, so the printed line makes the account and never the door.
4. `TestTheTwoScopes` — an `account` declaration carrying a `home`, `harness`, `engine`,
   `process`, `ready` or `capture` block is exit 2 naming the field and the condition; a
   `home` declaration missing any of them is exit 2; `--scope account` runs steps 1–5 and
   no more and clones nothing; `--scope home` runs eleven; a missing `--scope` is exit 2
   naming both values; an `account` run's last line names the pull-request remedy (rule 4).
5. `TestASecondRunRepairsAndSkips` — over a healthy line, every step that measures green
   prints `SKIP`, no file on disk changes (tree hash before and after), no process is
   started, and `ready` is measured again. Over a line with one step undone, exactly that
   step acts and the rest `SKIP`. **Over a LIVE line whose harness version has moved under
   it, step 9 is exit 1 and not a `SKIP`** — the assertion that a repair measures rather
   than remembers (rule 18). **The resumable case:** a run that refuses at step 4 with the
   pull-request remedy, then the fixture's rule merged, the file sealed and the fixture
   store fast-forwarded by the test's own `git` (never by the tool), then the same
   command, reaches `READY` — the assertion that a pause is not a dead end.
6. `TestTheHomeIsNeverRewritten` — absent clones at the declared branch; behind
   fast-forwards; and each of **another branch, a detached HEAD, a local commit, a dirty
   tree, a `.git` that is a file, a different remote** is exit 1 naming the fact with a
   remedy, the working tree **byte-identical** afterwards. A store behind its remote is
   exit 1 at step 4 naming `git -C <store> pull --ff-only`, and the store is unchanged.
   **Every `git` this tool runs, in every verb, is `tools.git` by absolute path**, asserted
   at the exec site; a fetch or a push that fails to authenticate is exit 1 naming the
   remote, with no credential read, no prompt and no retry, and the fixture's store value
   appears in no byte of that child's environment. Mutation: a `reset --hard` on the
   divergent case, which must turn every one of the six
   red; and a `git pull` of the store, which must turn the store case red.
7. `TestItWritesNothingInsideAHome` — the home's tree hash, excluding `.git`, is unchanged
   by every verb; no verb runs `git commit`; a declaration whose `sandbox.write` contains a
   git working copy **that is not `home.path`** is exit 2 naming the path, on the first run
   and on the second alike; `home.path` inside its own `sandbox.write` is accepted, which is
   the stated deviation; `down` on a dirty home reports `dirty=<n>` and changes nothing.
8. `TestACommandIsArgvNeverAShell` — `;`, `&&`, `|`, `$(`, a backtick and `*` in each of
   the four argv fields are executed as literal bytes at each exec site; a field with two
   adjacent spaces is exit 2 naming the field; no child argv begins with `sh`, `bash`,
   `zsh` or `cmd`.
9. `TestTheLoadedFilesAreLinksNeverCopies` — for `always_loading` and for each `config`
   entry: `at` equal to `source` is `SKIP` and makes nothing; `at` absent makes exactly one
   symlink and no second file anywhere; `at` holding a **regular file** is exit 1 naming it
   and never overwritten — the mutation that matters, because overwriting is what merged two
   selves by hand; `source` absent or zero bytes is exit 1; two `at` paths pointing at one
   source is exit 2; `config: []` makes nothing and is not a refusal. And: after a full run,
   the only new paths under the load paths are exactly those links.
10. `TestTheHarnessIsPinnedAndTheReadIsNovaUpdates` — a fake harness printing each of
    SPEC-UPDATE rule 4's fixture lines yields that rule's stated read, **by calling
    nova-update's own function**, so the two tools cannot disagree; a version differing
    from the pin is exit 1 naming both and the `apply` line; an unreadable identity is exit
    1 with the wrap-it remedy; **a fake harness whose version string is unchanged but whose
    behavior is broken passes this test and fails test 11**, which is the claim rule 10
    makes about its own reach; no `brew`, `npm`, `go install` or package manager starts in
    any verb.
11. `TestACanaryRunsBeforeTheLineDoes` — the canary's argv, read from the fake children, is
    exactly `<tools.secrets> exec … -- <tools.sandbox> … -- <canary.argv>`, with the same
    lists, `home_env` and `cwd` the start would use — the assertion that it is a rehearsal
    and not a different program; a fake harness whose `--version` matches the pin but whose
    canary exits non-zero is exit 1 carrying its first output line, bounded, with **no**
    line process started — the 2026-09-13 case; a canary whose argv element is
    `tools.sandbox` is refused by rule 17 before it runs; a canary that hangs is `FAIL`
    reason `timeout` at its own deadline, not the budget; a canary that passes is followed
    by exactly one start; and a `credential: "none"` fixture's canary argv is the **two-deep**
    nesting, the same shape its start takes (rule 15).
12. `TestReadyIsAMeasurement` — a fake line whose process is alive and whose probe never
    answers is `FAIL` at `ready.deadline` with `polls=` counted, never `OK` — the mutation
    that matters is treating a live pid as ready; a probe answering on poll 3 gives
    `ready=` equal to the injected clock's advance and `polls=3`; a probe exiting 0 with
    the wrong first line is not ready; a probe that outlives `ready.interval` is killed and
    counts as a NO; a child that exits before READY carrying a wrapper refusal line is
    `FAIL` reason `refused` within one interval, and any other early exit is `FAIL`
    reason `died` — both asserted to be faster than `ready.deadline`; the refusal line is
    read from `process.log` **as a file**, with the fake child writing `SECRETS EXEC OK …`
    first and its refusal after, so an implementation that took the log's first line is red;
    a child that exits `125` carrying **no** wrapper line is `died` and not `refused` — the
    mutation that matters is keying on the number instead of the line; a declaration with no
    `ready` block is exit 2.
13. `TestEveryWaitHasADeadline` — with an injected clock and children that hang, `up` ends
    inside `--budget`, prints its last line and exits 1 with reason `budget`; after any
    exit, including a signal, **no child of this tool is alive** but the line's own process,
    asserted by the process group `up` itself created and recorded; `--budget 0` and a
    negative are refused.
14. `TestNoKeyFileIsEverOpened` — a distinctive 40-byte fixture value in the throwaway
    store: it appears in **no byte** of stdout or stderr in every verb and every refusal;
    **the proof of non-opening is the process's own open-file list, which never contains
    `secrets.key`** (draft 1 asked for a `0000` fixture, which step 3's `0600` measurement
    and `nova-secrets check` invariant 6 both refuse, so the fixture stays `0600` and the
    assertion moves to where the fact is); a name in both `process.env` and `secrets.only`
    is exit 2 naming it; a `secrets.require` name that is not in `secrets.only` is exit 2
    before step 1, never a 125 at step 11; a mutation printing `len(value)` turns it red;
    the source tripwire has no `source`, no `eval`, no Keychain call and no crypto
    dependency.
15. `TestTheNestingOrderIsFixed` — with `credential: "env"` the built argv is exactly
    `<tools.secrets> exec … -- <tools.sandbox> … -- <innermost>`, and with
    `credential: "none"` it is exactly `<tools.sandbox> … -- <innermost>` with
    `nova-secrets exec` never executed at all — both asserted on the argv the
    **fake** children see, for the canary and for the start, from the one builder, with no
    flag reaching the choice; the
    store, the key file and `sops` appear in **neither** sandbox list; the reverse nesting
    is asserted to **fail** and is unreachable from any flag. **The outermost child's
    environment is exactly `HOME=<sandbox.home_env>`, `PATH` from `process.path_env` and
    `process.env`, and nothing else**: the test plants a distinctive variable in its own
    process's environment and asserts it reaches no child, which is the mutation that
    matters, because `os.Environ()` plus additions is what a Go program writes by accident. The assertions run against
    fakes on every platform; the **additional** pass against the real `nova-secrets` and
    `nova-sandbox` binaries skips with a stated reason when they are absent, naming which,
    never vacuously.
16. `TestAHarnessThatCannotTakeItsKeyByEnvironmentIsRefused` — `harness.credential: "file"`
    is exit 2 naming the line, the fact and the two remedies, with no process started;
    `credential: "none"` with an empty `secrets.only` and an empty `secrets.require` runs to
    `READY` **through the two-deep nesting, with `nova-secrets exec` never executed** — the
    keyless line, which draft 1 refused and draft 2 could not start, asserted on the argv
    and not only on the exit code; `credential: "env"` with an empty `only`, and
    `credential: "none"` with a non-empty `only` or a non-empty `require`, are each exit 2
    naming the condition;
    `keygen` on a fresh fixture writes exactly one file outside the store, never overwrites,
    prints the `SECRETS RULE` block, and the run still refuses at step 4 at exit 1, which is
    the box's fact and not the declaration's (rule 22).
17. `TestTheWallIsNotOptional` — a failing probe, and a box with no backend, each refuse
    the whole run with no process started; **the probe's own argv and environment are
    asserted: the declared lists exactly, `secrets.key` as `--secret`,
    `HOME=<sandbox.home_env>` and nothing inherited, and `--net-deny` present exactly when
    `net` is `denied`** — the mutation that matters is running the probe with this tool's
    own `HOME`, which is `PROBE REFUSED reason=check … home_outside` on every real box;
    there is no flag, field or environment variable
    that runs the line unwalled (source tripwire plus an argv walk); `home_env`,
    `process.log` and `cwd` outside every `--write` are three refusals with three
    sentences; a path in both lists is a refusal naming both; `secrets.store`, `secrets.key`
    or `secrets.sops` inside any `sandbox.read` or `sandbox.write` is exit 2 naming both
    fields, one case each; `listen: true` with
    `net: "denied"` is exit 2 naming both fields; a `start_argv` element equal to
    `tools.sandbox`, or whose basename is the platform wrapper, is a refusal — and a
    harness's own private sandbox flag is asserted **not** to be refused here, with test
    11 carrying that case instead, so no harness name ever enters this package.
18. `TestOneProcessPerLinePerBox` — with a live pid in the state file, a second `up`
    starts nothing and every step that measures green prints `SKIP`, `ready` being measured
    again (the assertion that rules 5, 12 and 18 agree); a dead pid is reaped and the line
    starts; **a live pid whose start time differs from the recorded one is treated as dead
    and reaped**, the pid-reuse case, whose mutation is testing liveness alone; two
    declarations sharing one `sandbox.home_env` are exit 2 naming both, read
    from the state file's recorded **live** `home_env` and not from a second declaration,
    while the same pair with the first line **down** is not a refusal; an interrupt during
    the readiness wait still leaves the pid, pgid and start time recorded, because the lock
    is released before the first poll; concurrent
    `up` runs over one state file leave it parseable and one of them **exits 1** naming the
    holder's pid — a lock another process holds is a fact of the box, and test 22 walks the
    same table — never a 0-byte file.
19. `TestOneKeeper` — `role: "keeper"` changes **nothing** this tool does, which is the
    assertion draft 3 replaced two contradicting refusals with: a live process for a keeper
    is rule 18's repair at exit 0, byte-identical in its output to the same fixture at any
    other role but the printed `role=`; a keeper whose home is behind its remote takes
    rule 6's step-6 fast-forward and comes up, and one whose advance is not a fast-forward
    is rule 6's exit 1, the same line every other role gets; three role labels, including
    one this repository has never used, are printed verbatim and change no branch; there is
    no `move`, `handover` or `promote` verb, asserted on the verb table.
20. `TestDownPushesAndSaysWhatItCannotHold` — `down` pushes before it reports, on a clean
    stop and on a forced kill alike, signalling **the group** and leaving no survivor; a
    refused push names the remote and makes `down` exit 1; `up` pushes nothing;
    `process.holder: "none"` prints `holder=none` on `RUN UP OK` **and** the `doctor`
    `RUN NOTE`, and a declared label prints on both — the assertion that this tool does not
    claim a guard it does not hold, made where the person who started the line will see it.
21. `TestOutputIsBoundedAtTheLargestPlausibleState` — a 200 MB log gives `logs` at most
    `--max` lines and `--bytes` bytes with the `...+<dropped>B` mark on a rune boundary; a
    declaration with 60 capture classes and a run with 11 failing steps prints at most
    `2 * --max + 8` lines over both streams, each capped kind with its own `MORE` line and
    true total; `--max 0` prints all; a negative `--max` or `--bytes` is exit 2; the count
    line prints on the red run; a **waited-on child** printing 1 MB is `FAIL` reason
    `output`, while **the line's own process** printing 1 GB to `process.log` reaches
    `READY` normally and this tool never reads that stream — the mutation that matters,
    because draft 1's cap would have killed the line it started; and the same 1 GB log read
    **as a file** by `logs` and by rule 12's early-exit report is bounded in both, so a
    read-after-death is not a second way to blow the budget.
22. `TestEveryRefusalNamesItsRemedyAndItsExit` — every refusal in the package lives in one
    table the test walks: each names what the input wants, ends in a command, a file or the
    values allowed, **and carries its exit code, which the table asserts is 2 for every
    declaration fact and 1 for every box fact** — the held state lock among the box facts,
    at 1; removing one, or flipping one's code, turns it red. One run with six independent problems names all six, sorted, in one
    refusal block; a flag typo or a bare invocation costs **one line**, never the banner;
    `nova-run help` prints the verbs block on stdout at exit 0.
23. `TestNoDeclarationOrChildOutputCanForgeALine` — a line name, a path, a harness's log
    line and a child's error each carrying `\nRUN UP OK …`, a terminal repaint and a bidi
    control: none authors a second line, including from the flag parser before the first
    instruction of this tool runs.
24. `TestNothingHereIsSpecificToUs` — the source and `docs/SPEC-RUN.md`'s **runnable
    blocks** — the usage banner's `example:`, the shipped `testdata/` declaration and the
    `### First run` transcript in `docs/CLI.md` — carry no forge host, no branch name, no
    harness name, no friend's name and no bench path; the prose's worked examples are
    exempt by being named as instances of a field, and the test asserts on the runnable
    blocks alone, never on this document's sentences, so it is an assertion rather than a
    wish; the shipped example declaration runs end to end against fakes and names a harness
    this repository does not use.

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
   a declared field (`harness.credential`), and rule 17's undetectable half is driven by a
   measurement (rule 11's canary) rather than by a list.
3. **The scope is a declared fact, and the flag is its second record.** *Default: both.*
   ideas#766 says *"The second word of the command"*; the verb slot is already spent on
   `up`, `down`, `status`, `logs` and `doctor`, so the word is spelled `--scope
   <account|home>` — and, after draft 1's read, the fact itself moved into the declaration,
   where `name` and `box` already live, because a typed word that decides whether a self is
   cloned onto a worker box is one keystroke from a self on the wrong box. The flag now
   agrees with the file or the run refuses.
4. **`up` returns as soon as the line is READY and does not hold it.** *Default: returns.*
   A foreground mode that held the process would let this tool own the self-push on every
   exit path (rule 20) and would make it a supervisor, which is nova-daemon's layer. What
   the tool owes instead is honesty about the gap: `holder=` on the OK line.
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
(the model, the launcher order, `keygen`, `names` without a key, invariant 8's pull, the
125 refusal, and *"read the line, not the number"*);
[SPEC-SANDBOX.md](SPEC-SANDBOX.md) (rules 1, 6, 7, 8, 9, 10, 11, 12, 13, the
launcher checklist items 1, 3 and 5, the probe section whole — its `HOME`, its
`--net-deny`, `probe_outside_unwritable` and the two paths it writes outside the wall —
the exit table's `bad_net`, `home_outside` and `secret_inside_allow`, and
rule 12's *"the caller owns pgid and reaping"*); [SPEC-UPDATE.md](SPEC-UPDATE.md) rules 3,
4 and 10; [SPEC-LOCAL.md](SPEC-LOCAL.md) rules 8 and 15 and [BOX-LOCAL.md](BOX-LOCAL.md);
[SPEC-MERGE.md](SPEC-MERGE.md) rules 1 and 2 for the lock; [ONBOARDING.md](ONBOARDING.md)
and [USAGE.md](USAGE.md) for what a first run owes a stranger. The live launchers
`run-freddy.sh`, `run-emma.sh`, `run-johnny.sh`, `run-worker-v2.sh` and `freddy-swarm.sh`
were read **as data**, on 2026-09-13, as the record of what a hand does today: every step
they repeat is a verb above, and every step they cannot take is a refusal.

*Rowan, 2026-09-13. No credential value was read, written, printed or named anywhere in
this work.*
