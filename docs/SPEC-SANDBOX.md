# nova-sandbox — specification

`nova-sandbox` is one binary at the **launch layer**. It runs one command with
its filesystem reach cut down by the operating system: the command may read the
OS and toolchain roots, it may read the directories the caller named with
`--read`, it may read and write the directories the caller named with
`--write`, and everything else on disk is denied to it by the kernel.

```
nova-sandbox --read <dir>... --write <dir>... [--net-deny] -- <command> <args...>
```

This spec is normative. If the code and this document disagree, one of them has
a bug, and the tests decide which. It stands beside [SPEC.md](../SPEC.md), whose
**Conventions** section — no guessed paths, the one-line output grammar, the
cap-and-count rule, `internal/oneline` and `internal/bounded` — applies here
unchanged and is not restated. The one deliberate departure from it is the exit
grammar of the exec verb, and that departure has its own section and its reason.

It exists because of [#69](https://github.com/mas-bandwidth/nova-tools/issues/69):
a swarm worker runs a cheaper model than the seat, reads untrusted input all
day, and today holds the bench's SSH key and the bench's `gh` token. The
harness's own `permission` block is a fence an honest program respects; this
tool is the wall the kernel enforces.

**Glenn's rulings, 2026-09-11, verbatim, and they are the shape of the tool:**

> "I don't want to force people to do user dirs, so if we can use macOS
> features (mac only) to make it safer, linux too that's best."

> "Is there a way to protect on windows too?"

> "I think we need to consider what the nova swarm needs to be able to read, vs.
> write. Maybe it is separate."

> "if 64 children in a swarm all do the same git clone redundantly, that will
> suck."

> "I like nova-sandbox"

So: **OS-enforced containment, no dedicated users, on macOS, Linux and Windows,
with the read set and the write set separate.**

| the failure it closes | the rule that closes it |
|---|---|
| a worker with the bench's credentials can read `~/.ssh`, the `gh` config, the keychain and the shell history | rules 3, 4 |
| shared inputs get read **and write** reach because there is only one list | rules 3, 4 |
| forcing a dedicated OS user per line is an administrative burden nobody will carry | rule 2 |
| a sandbox that silently does nothing on a platform it does not support | rules 1, 11 |
| a credential file readable inside the wall | rules 6, 10 |
| `/tmp` on macOS is a symlink to `/private/tmp`, and a policy written against the unresolved path grants nothing | rule 5 |
| a deny-by-default policy makes the inherited temp directory unwritable and half a toolchain dies on its first scratch file | rule 8 |
| OpenCode's `external_directory` is relative to the harness cwd, so a job directory that is not the cwd is "external" to itself | rule 13 |
| a harness `permission` block set to `ask` hangs a headless job on a prompt nobody sees | rule 14 |
| 64 workers each clone the repo over the network, each needing a credential | the swarm caller section: one dispatcher-owned reference checkout per batch |
| the wall stands and the job's first `git status` dies on `~/.gitconfig`, which reads as a broken sandbox | rule 9: the caller sets `HOME` to the per-job data home, and a `HOME` outside both lists is a refusal |

The fence is the `opencode.json` `permission` block; the wall is the kernel.
Everything below is about the wall, except one section which is about the fence.

## The rules, numbered

Every rule here is normative. Each has one line in **tests this spec demands**
near the end.

1. **OS-enforced or refused.** There is one Go function,
   `sandbox.Run(p Policy, argv []string) (code int, err error)`, with three
   bodies behind build tags: `sandbox-exec` on `darwin`, Landlock on `linux`,
   AppContainer on `windows`. The bodies differ in whether the tool survives
   the command: on `darwin` and `windows` the tool waits and returns the
   command's status; on `linux` the function does not return on success,
   because the tool restricts itself and then `exec`s the command in place
   (rule 12). If the platform's backend is not available at run time — no
   Landlock in the running kernel, no `sandbox-exec` on `PATH`, an AppContainer
   profile that cannot be created — the tool prints `SANDBOX REFUSED
   reason=no_sandbox` and **the command does not run**. A backend that is
   present but cannot apply the policy is `reason=sandbox_failed` and is equally
   fatal. There is no fallback, no degraded mode, and no partial wall.
2. **No dedicated users, no root, no admin, no VM.** Every backend chosen here
   is usable by an ordinary unprivileged user in their own session (Glenn's
   ruling). A design that needs `sudo`, a second login account, a container
   runtime or a Hyper-V feature is out of scope for this tool, and the reasons
   the obvious ones were not chosen are in **what it deliberately does not do**.
3. **Deny by default; read the roots and the read set; write only the write
   set.** The policy denies filesystem access, then grants: **read** on the OS
   and toolchain roots (the platform lists are below, and they are data, not
   code); **read** on each `--read` directory and everything beneath it;
   **read and write** on each `--write` directory and everything beneath it.
   Nothing else is reachable. In particular `~/.ssh`, the `gh` configuration
   directory, the login keychain (`~/Library/Keychains`) and the shell history
   (`~/.zsh_history`, `~/.bash_history`) are outside every root list, and a
   caller that adds one back has done so in its own argv.
4. **Both lists are explicit and are never guessed.** `--read <dir>` and
   `--write <dir>` are each repeatable and have **no default**. Zero `--write`
   is exit 125 and `refusing to guess`: a command with no writable directory is
   a misconfiguration, not a tighter sandbox. Zero `--read` is legal — the
   roots are the floor. A `--write` path is readable as well as writable; a
   path given to both is a refusal naming both flags, not a silent merge. A
   default write set would be a guess about somebody else's job. **The two
   named exceptions**, and there are no others: rule 8 puts the temp directory
   under the **first** `--write` and rule 13 defaults the `--cwd` to the
   **first** `--write`. Neither is a guess about the *lists* — the caller gave
   both paths — and both are stated here so that "never guessed" and "the first
   `--write`" stop contradicting each other. The order of `--write` flags is
   therefore meaningful and the callers below pass the job directory first.
5. **Paths are resolved, absolute and existing.** Each `--read`, each
   `--write`, the `--cwd`, the `--tmp` and each root is resolved with
   `filepath.EvalSymlinks` and `filepath.Abs` before it reaches a policy,
   because macOS's `/tmp` is a symlink to `/private/tmp` and a sandbox profile
   written against the link grants nothing. A path that does not exist is
   **refused**, not created. A relative path is refused with the absolute form
   it would have taken. The command itself is resolved on the caller's `PATH`
   at this point, before the wrap — see the exit codes section for what that
   means for `127`. **A root is skipped if it is absent; only a caller's path
   is refused for absence.** `/opt/local` exists on a Mac with MacPorts and on
   no other, `/lib64` on some linuxes and not others: a tool that refused an
   absent root would refuse on the majority of machines. A `--read` or
   `--write` the caller typed is a different thing — it is a claim about this
   job — and an absent one is still a refusal.
6. **The secret is never inside either list.** The caller reads its credential
   file **before** the wrap and passes the value by environment (`nova-swarm`'s
   rule 6: the key is read as data, never sourced, never an argument).
   `nova-sandbox` never reads a credential, never names one in a printed line,
   and never writes one to a file. The file itself stays outside every named
   path, so the wrapped command cannot read it even if it is told to.
   `probe --secret <path>` names the file by path — a path is not a secret —
   and a `--secret` that resolves inside any `--read` or `--write` is exit 2
   and `reason=secret_inside_allow`, because that is a misconfiguration, not a
   failed probe.
7. **Network: no promise by default, and `--net-deny` refuses where it cannot
   be enforced.** Without `--net-deny` the tool makes **no promise** about the
   network: the policy allows it where the backend needs an explicit grant
   (`(allow network*)` on darwin, the `internetClient` capability on windows),
   and the line says `net=nopromise`. With `--net-deny` the caller is asking
   for an enforced denial, and the tool either delivers it or refuses to run:
   on `darwin` and `windows` the grant is withheld and the line says
   `net=denied`; on `linux` below Landlock **ABI 4** (kernel 6.7) the tool
   prints `SANDBOX REFUSED reason=net_unenforceable` and **the command does not
   run**. The named workaround is to drop `--net-deny` and take
   `net=nopromise`, with the filesystem wall still standing — and the
   loudness of it is **`net=nopromise` on the `SANDBOX OK` line**, printed
   before the command starts and in every log that holds the run. An absent
   flag is not loud in the argv; the word on the line is what a reader sees,
   and it is the same word whether the caller never wanted a denial or gave
   one up. There is no `SANDBOX NOTE` that proceeds with a weaker wall than the
   caller asked for — that is the silent sandbox rule 1 exists to prevent.
   Landlock restricts only TCP `bind`/`connect` even at ABI 4; UDP is not
   restricted at any ABI, and `net=denied` on linux means exactly TCP.
8. **Temp is inside the wall.** The tool creates
   `<first --write>/.nova-sandbox-tmp` if it does not exist — the one
   directory the tool creates, inside the write set, its name chosen by the
   tool and never by a caller; **what it deliberately does not do** is about
   the paths it is *handed* — and sets `TMPDIR`,
   `TMP` and `TEMP` to it in the child's environment, because a deny-by-default
   policy makes the inherited per-user temp directory unwritable and a
   toolchain whose first scratch write fails looks like a broken sandbox rather
   than a working one. `--tmp <dir>` overrides it and must resolve inside a
   `--write` path.
9. **The environment passes through, and the caller points the child's home
   into the write set.** This is not a secrets tool: the child inherits the
   caller's environment, minus the three temp variables the tool sets. The
   credential the caller deliberately passed by environment (rule 6) must
   arrive. But an inherited `HOME` names a directory that is in no list and
   is therefore denied, and almost every tool a worker runs derives a path
   from it. Measured on this Mac under the profile below: with the caller's
   `HOME` inherited, `git -C <jobdir>/repo status` is `fatal: unable to
   access '/Users/<user>/.gitconfig': Operation not permitted`, so a
   `tree: yes` job cannot run its first git command; a harness that writes
   `~/.config/opencode` and `~/.local/share/opencode` dies the same way.
   **The caller therefore sets `HOME` to the per-job data home, and that
   directory must be inside a `--write`** — a `--read` is not enough and the
   previous revision was wrong to allow it: measured under the profile with
   `HOME` inside a `--read` path, `git status` exits 0 and the first config
   write is `Operation not permitted`, which is exactly the harness death two
   paragraphs up, moved later in the run and made harder to read. With `HOME`
   inside a `--write` the same `git status` exits 0 and the config write lands
   (measured, `profiles/darwin-check.sh`, check `home_config_write`).
   `HOME` rather than the XDG quartet (`XDG_CONFIG_HOME`, `XDG_DATA_HOME`,
   `XDG_CACHE_HOME` plus `GIT_CONFIG_GLOBAL`) because one variable covers
   every home-derived path a tool invents — `~/.gitconfig`, `~/.ssh`,
   `~/.npm`, `~/.cache`, and macOS's `~/Library/Application Support`, which
   no XDG variable reaches — while the quartet covers only the tools that
   honour it and must grow a name every time a toolchain invents one. (The
   quartet was measured too: `GIT_CONFIG_GLOBAL` + `XDG_CONFIG_HOME` fixes
   git. It fixes git.) The tool does not set `HOME` itself: rule 4 forbids it
   guessing which write path is a data home (rule 4 names its only two
   exceptions, and this is not one of them). It does **check**: a run whose
   `HOME` resolves outside every `--write` path is
   `SANDBOX REFUSED reason=home_outside` at exit 125 and the command does not
   run, because a wall that lets the job start and kills its first git
   command is the silent sandbox rule 1 exists to prevent.
10. **The probe proves the wall before the work runs.** `nova-sandbox probe
    --write <dir> [--read <dir>...] --secret <path>` runs five checks under the
    real policy for this platform: a write **outside** every named path must
    fail; a read of the named secret file must fail; a write **inside** the
    write set must succeed; a read of a toolchain root must succeed. Any check
    that comes back the wrong way is `PROBE REFUSED` at exit 1 naming the
    check. The last two are not decoration: a wall that denies the work too is
    broken, and a two-check probe would call it a pass.
11. **`--no-sandbox` is the one loud workaround.** It runs the command with no
    policy at all. It prints exactly one line to **stderr**,
    `SANDBOX UNSANDBOXED cmd=<name> read=<n> write=<n>: no OS containment; every
    read and write this command makes is yours`, before the command starts. It
    is never a default, never implied by a missing backend, never read from a
    config file or an environment variable, and never silent.
12. **The exec verb is transparent, and what happens to the tool's own process
    is stated per platform.** Everything after `--` is executed verbatim —
    **never** through a shell, so no argument is re-parsed and no quote is
    re-interpreted. stdin, stdout and stderr are inherited unchanged — and the caller owns what
    they point at. A descriptor the caller opened **before** the wrap is not
    made reachable by having been opened: measured, a wrapped `/bin/cat` whose
    stdout is a file outside every named path is denied, while `/bin/echo`
    writing the same descriptor succeeds, because the two reach the file
    differently and only one of them is checked by the policy. **The rule: the
    caller's stdout and stderr for a wrapped command are either a pipe the
    caller drains, or a path inside the write set.** `nova-swarm`'s
    `supervise` takes the pipe: it already reads the harness's output line by
    line, and its per-job log file is written by the supervisor outside the
    wall, never handed to the child as a descriptor onto an unnamed path. The
    child's exit status is the tool's exit status, and a death by signal `N`
    gives exit `128+N`. Per platform:
    - **linux:** the tool restricts *itself* (`runtime.LockOSThread`,
      `landlock_restrict_self`) and then `syscall.Exec`s the command, so the
      tool **becomes** the command: same pid, same process group, no wait, no
      signal forwarding, and the exit status is the command's by identity.
    - **darwin:** the tool spawns `sandbox-exec`, which applies the profile and
      `exec`s the command in place, and **waits**; `SIGINT` and `SIGTERM` are
      forwarded to the child's process group. The tool waits so that it can
      remove the generated profile file when the command ends.
    - **windows:** the tool `CreateProcessW`es the command into the container
      and **waits**, forwarding console control events, so that it can remove
      the ACEs it added when the command ends.

    The tool's own status lines go to **stderr**, so a wrapped command's stdout
    is its own.
13. **The working directory is inside the wall.** `--cwd <dir>` must resolve
    inside a `--write` path; its default is the first `--write`. OpenCode's
    `external_directory` permission is evaluated **relative to the harness's
    working directory**, so a job directory that is not the cwd is "external"
    to the harness that is supposed to be working in it, and the fence denies
    the job its own files. The swarm caller therefore passes the job directory
    as the first `--write` and as the cwd.
14. **The fence is `allow` or `deny`, never `ask`.** The `opencode.json`
    `permission` block this tool ships beside a wrapped harness uses only
    `allow` and `deny`. `ask` is never written, because a headless job with no
    person at the terminal hangs on the prompt until its deadline reaps it.
15. **The policy is generated and printable, never hand-edited.** The caller
    passes the two lists; the tool generates the profile, the ruleset or the
    ACL grants. `--print-policy` writes the generated policy to stdout and
    exits 0 without running anything. No profile is stored in the repository
    for editing, and the tool never accepts a caller-supplied profile file.
16. **Bounded output, and a refusal says what the input wants.** Every listing
    is a cap and a count per SPEC.md, `--max <n>` default 20, `0` for all, one
    MORE line naming the remedy. A refusal names the flag and the form it
    wants, reports every independent problem at once, and never prints the
    contents of a file it was handed.

## The verbs

```
nova-sandbox --read <dir>... --write <dir>... [--net-deny] [--cwd <dir>] [--tmp <dir>] [--name <container>] [--acl tool|caller] [--no-sandbox] -- <command> <args...>
nova-sandbox probe   --write <dir>... [--read <dir>...] --secret <path> [--net-deny] [--max <n>]
nova-sandbox policy  --read <dir>... --write <dir>... [--net-deny] [--cwd <dir>]    (alias: --print-policy)
nova-sandbox fence   --out <file> [--webfetch allow|deny]
nova-sandbox grant   --name <container> [--read <dir>]... [--write <dir>]...
nova-sandbox release --name <container> [--read <dir>]... [--write <dir>]...
nova-sandbox check   [--max <n>]
```

`probe` is rule 10 and is the verb a caller runs **once before the first task**,
not per task: it costs a process and it answers a question about the machine,
not about the job. `nova-swarm run` runs it before it starts the first worker
and refuses the pass on a failure with `RUN REFUSED reason=sandbox_probe`.

`policy` prints the generated policy for a read/write pair and runs nothing. It
is how a reader checks the wall without trusting this document.

`fence` writes the `opencode.json` `permission` block of rule 14 to a file, so
that the block is generated from one place rather than copied by hand.

`grant` and `release` are the verb pair `--acl caller` is owed, and they are
the **only** way the caller-owned grants of the windows section are made and
unmade. `grant` derives the container SID from `--name` (creating the profile
if it does not exist) and adds the read-only or read+write ACE on each named
directory; `release` removes exactly the ACEs `grant` added and deletes the
profile when it made it. Both are idempotent, both name every directory they
touched, and neither runs a command. **Who calls them, and when:** the swarm
dispatcher calls `grant` once at `run`, after it has created `pool/ref/<repo>@<sha>`,
the worker homes and the job-directory parent and before the first worker
starts, naming **those directories explicitly** — never the pool root, because
an inherited grant on the pool root would hand the container SID write over
`pool/ref` and `pool/reports` as well; and it calls `release` once at pool
teardown, after the last worker has exited, which is the moment only the
dispatcher knows. A solo line's launcher calls `grant` when it starts the line
and `release` when it stops it. A **per-job** directory created after `grant`
needs no second call: the ACE on the job-directory parent carries
`CONTAINER_INHERIT_ACE`, so each job directory is born with it — that is why
the parent, and not each job, is what is granted. On `darwin` and `linux` both
verbs print one line and do nothing, as `--name` and `--acl` are accepted and
ignored there, so one caller has one script for three platforms.

`check` reports what this machine can enforce — the backend, its version or
ABI, and whether an enforced network denial is available — and exits 0 whether
or not a sandbox is available, because it is a question, not an attempt.

The binary is `nova-sandbox`, and that is its only name (Glenn: "I like
nova-sandbox").

## Exit codes

The exec verb cannot use SPEC.md's 0/1/2 grammar, because its exit status
belongs to the wrapped command: a tool that returned 2 for a bad flag would be
indistinguishable from a command that exited 2 on its own. It uses the
`env(1)` / `timeout(1)` convention instead, which reserves the top of the
range, and this is a deliberate, recorded departure from the conventions
(Glenn, 2026-09-11: **flexibility, not rigidity**).

| code | meaning |
|------|---------|
| 0–124 | the wrapped command's own exit status, passed through unchanged |
| 125 | `nova-sandbox` itself said **NO** before the command ran: `SANDBOX REFUSED` — no backend (`reason=no_sandbox`), the policy could not be applied (`reason=sandbox_failed`), an enforced network denial that is not available (`reason=net_unenforceable`), no `--write`, a relative or missing path, a path in both lists, a `--cwd` outside the write set, a `HOME` outside every `--write` (`reason=home_outside`), a command that is not executable (`reason=not_executable`), on windows a missing `--name` (`reason=no_name`) or an absent caller-owned grant (`reason=acl_missing`), a missing `--` or nothing after it (`reason=no_command`) |
| 126 | the command could not be executed **and the tool was still there to say so**: on `linux` `syscall.Exec` returned an error, on `windows` `CreateProcessW` failed. On `darwin` the backend's own exec failure is 71 and the tool cannot see it — below |
| 127 | the command could not be resolved on the caller's `PATH`: `SANDBOX REFUSED reason=not_found`, printed like every other refusal of the tool's own |
| 128+N | the wrapped command was killed by signal `N` |

The reservation is ambiguous, as it is in `env(1)`: a wrapped command that
itself exits 125, 126 or 127 — **and on darwin 71** — is indistinguishable from
the tool's own refusal by exit status alone. The tool's refusals always print a
`SANDBOX REFUSED` line to stderr and the command's do not, so a caller that
needs to tell them apart reads the line, not the number. This is stated rather
than fixed, because renumbering would break the convention the rest of the
table follows.

**Executability is checked before the wrap, not mapped after it.** Measured:
when `sandbox-exec` cannot exec the command under the profile it prints
`execvp() of '<cmd>' failed: Operation not permitted` and exits **71**. The
previous revision promised to turn that 71 into a 126, and **the promise cannot
be kept**: `sandbox-exec` applies the profile and `exec`s in place, its stderr
is the caller's, and the tool sees the number 71 and nothing else —
`sandbox-exec -f p.sb -- /no/such` and `sh -c 'exit 71'` both exit 71
(measured), and no inspection of the status distinguishes them. The promise is
withdrawn. What the tool does instead is **pre-flight, outside the wall**: rule
5 has already resolved the command to an absolute path on the caller's `PATH`,
so the tool stats it there and refuses `SANDBOX REFUSED reason=not_executable`
at **125** — before any profile exists — when the path is absent, is a
directory, or carries no executable bit for this user. That catches the case
the 126 row was written for. A failure to exec that survives the pre-flight (a
bad interpreter line, a missing shared library, a profile that denies the exec
itself) is on darwin an exit **71 carrying `sandbox-exec`'s own message on
stderr**, which is why 71 is listed above with 125–127 as a number the tool
cannot claim.

`127` is about the **caller's** `PATH`, not the policy's: rule 5 resolves the
command to an absolute path before the wrap, so the lookup happens outside the
wall and a `127` means the tool could not find the command at all. A command
that is found and then dies inside the wall for want of its interpreter or a
shared library exits `126` or dies by signal. There is **no** `SANDBOX NOTE`
on that failure, and the previous revision was wrong to promise one: on linux
the tool has `syscall.Exec`'d itself away before the command runs, so nothing
of the tool is left to print anything (rule 12), and a promise the tool can
keep on one platform and not the other two is worse than no promise. The
remedy is printed where it can be printed on all three — the usage banner and
the `--read` paragraph of the roots section — and a reader diagnosing a `126`
compares it with the same command run without the wrap.

The `probe`, `policy`, `fence` and `check` verbs are not wrappers and use
SPEC.md's grammar unchanged: **0** the verb ran and passed, **1** the verb ran
and said NO, **2** could not run (a missing flag, an unreadable path,
`--secret` inside a named path, bad invocation).

## Output grammar

Every line below goes to **stderr** except the body of `policy` and `fence`,
which is the thing asked for and goes to stdout.

```
SANDBOX OK backend=<sandbox-exec|landlock|appcontainer> abi=<n|-> read=<n> write=<n> net=<denied|nopromise> cwd=<dir> cmd=<name>
SANDBOX UNSANDBOXED cmd=<name> read=<n> write=<n>: no OS containment; every read and write this command makes is yours
SANDBOX NOTE <the one remedy or gap line>   (always before the command starts)
SANDBOX REFUSED reason=<no_sandbox|sandbox_failed|net_unenforceable|bad_read|bad_write|bad_cwd|home_outside|acl_missing|no_name|no_command|not_found|not_executable>: <text>
PROBE STEP name=<write_outside|read_secret|write_inside|read_root> expect=<deny|allow> got=<deny|allow> path=<path>
PROBE OK backend=<name> abi=<n|-> steps=<n> passed=<n> net=<denied|nopromise>
PROBE REFUSED reason=<check|secret_inside_allow|probe_outside_inside|no_sandbox|net_unenforceable>: <text>
POLICY OK backend=<name> read=<n> write=<n> bytes=<n>
POLICY REFUSED reason=<no_sandbox|bad_read|bad_write>: <text>
FENCE OK out=<path> keys=<n>
FENCE REFUSED: <reason>
CHECK OK backend=<name|none> abi=<n|-> net=<enforceable|unenforceable> note=<one clause|->
```

`SANDBOX OK` is printed **before** the command starts, so a log that ends in a
crash still says what the wall was. It names `cmd=<name>` — the base name of
the executable — and never the arguments, because arguments carry task text and
task text carries quoted rules.

Every `SANDBOX NOTE` is printed **before** the command starts, for the reason
`SANDBOX OK` is: on linux the tool becomes the command and can print nothing
afterwards. There is no note about a failure the command suffered inside the
wall, on any platform.

`net=nopromise` is rule 7: the caller did not ask for network denial and the
tool is not implying one. There is no `net=unenforced`; a denial that cannot be
enforced is a refusal, not a word in a line.

**The tool never prints a credential, a file's contents, or an argument
vector.** A refusal about a path prints the path, which the caller supplied.

## The two lists, and the roots

**The write set** is each `--write` and everything under it: read and write,
recursively. **The read set** is each `--read` and everything under it: read
only, recursively. Shared inputs — one reference checkout, a corpus, the specs,
the worker home with its `AGENTS.md` — belong in the read set, named once, so
that N workers read one copy.

**The roots** are what any command needs to run at all: read only, recursively
unless marked otherwise. They are a per-platform list in one data file in the
source, not a string built in three places, and `policy` prints them:

| platform | roots |
|---|---|
| darwin | `/`, `/etc`, `/tmp`, `/var` (each the directory or link itself, `(literal ...)`, not a subpath), `/System`, `/usr`, `/bin`, `/sbin`, `/Library`, `/opt/homebrew`, `/opt/local`, `/private/etc`, `/private/var/select`, `/dev` (read), the directory of the resolved command; plus **write** on `/dev/null` and `/dev/tty` |
| linux | `/usr`, `/bin`, `/sbin`, `/lib`, `/lib64`, `/etc`, `/opt`, `/dev` (read), `/proc`, the directory of the resolved command; plus **write** on `/dev/null` and `/dev/tty` |
| windows | `%WINDIR%`, `%ProgramFiles%`, `%ProgramFiles(x86)%`, the directory of the resolved command |

`/` itself and `/dev` are in the darwin list because they were measured to be
required, not because a document said so: with the previous list `/bin/echo`
died with `SIGABRT` (exit 134), and `/dev` was measured to be required for a
plain `2>/dev/null` — the shell opens `/dev/null` before the command runs.
(`/dev` was not shown to be required by `/bin/echo` itself, and "Node runs
once both are added" is the measurement that Node runs with the list complete,
not a per-item necessity for either.) `/private/var/db/dyld` is **not** in the
list: it does not exist on macOS 26.

`/etc`, `/tmp` and `/var` are the second measured correction, and they are
`literal` grants on the **symlinks**, not subpaths. Granting `(literal "/")`
grants the root directory; it does not grant the top-level symlinks that macOS
puts in it. Measured with the previous list: `cat /etc/hosts` is denied while
`cat /private/etc/hosts` succeeds, `ls /tmp` is denied, and **every**
`/bin/sh -c ...` dies with `Error opening /private/var/select/sh` — which made
the wall unusable for any wrapped shell command. With the three literals and
`/private/var/select` added, `cat /etc/hosts` and `/bin/sh -c true` both work
(measured). A subpath grant on `/var` or `/tmp` is **not** wanted and is not
what is written: the literal grants the link, and what the link points at is
granted, or not, by the other roots.

One measured consequence of the same shape, named here so a build does not
rediscover it: `/usr/bin/git` on a Mac is an Xcode shim that reads
`/var/db/xcode_select_link`, which no root grants, so the shim fails inside the
wall. Rule 5 resolves the command on the caller's `PATH` before the wrap, so a
caller whose `git` is the real binary (`/opt/homebrew/bin/git`, measured
working) is unaffected; a caller stuck with the shim names `/private/var/db`
with `--read`.

There is no `--root` flag. A toolchain installed into a user directory — Go
under `~/go`, node under `~/.nvm`, .NET under `~/.local`, the Studio's
`/Users/<user>/toolchains` — is named with `--read`, which is exactly a
caller-supplied read-only root and needs no second spelling. On Windows,
`--read` is what makes the tool add a read-only ACE for the container SID. A
command that dies for want of an interpreter inside the wall and runs outside
it is a missing `--read`. The tool cannot say so after the fact — on linux it
is gone by then (rule 12) — so the sentence lives in the usage banner instead:
*a command that runs outside the wall and dies inside it is missing a
`--read`.*

The home directory is never a root.

## macOS — `sandbox-exec` with a generated profile

The wrap is `sandbox-exec -f <profile> -D <name>=<value>... -- <command>
<args...>`. `sandbox-exec(1)` is present on macOS 26, is marked deprecated, and
works today; it applies the profile and `exec`s the command in place, so it is
not a second process sitting between the tool and the command.

The profile text is **not in this document**. It ships in the repository as
`profiles/darwin.sb.tmpl`, and this section describes that file and the script
that checks it. Two copies of a profile is one copy too many, and the copy that
lived in this document was wrong for three revisions running.

**`profiles/darwin.sb.tmpl`** is Sandbox Profile Language (SBPL, a Scheme
dialect) and is the generator's only input. It carries the fixed clauses
verbatim and five markers, each documented in the file's own header, that the
generator replaces for one run: `@@OPTROOTS@@` (the optional roots that exist
on this machine), `@@ANCESTORS@@`, `@@READS@@`, `@@WRITES@@` and `@@NET@@`
(empty under `--net-deny`). Caller paths never enter the text: they arrive as
`-D NAME=<resolved path>` and are read back as `(param "READn")`,
`(param "WRITEn")` and `(param "HOME")`, so a directory with a quote or a paren
in its name cannot rewrite the policy. Rule 15 still holds: the file is a
template, never a policy, and nothing runs under it until the generator has
filled it for one run's two lists. `HOME` gets no grant of its own beyond the
`WRITEn` it must resolve inside (rule 9); it is passed so that the profile
states the requirement.

Four things in that file are load-bearing, and each was measured rather than
read:

- The exec and signal grants are **two clauses**. A single
  `(allow process-exec* process-fork signal (target self))` applies the
  `(target self)` filter to all three operations, and the measured result is
  `execvp() of '/bin/echo' failed: Operation not permitted`, exit 71 — the
  profile denies the exec it was meant to allow.
- The signal grant carries **both** `(target self)` and `(target children)`.
  With `(target self)` alone, `sh -c 'sleep 5 & kill $!'` prints
  `kill: Operation not permitted`: the wrapped harness cannot kill its own
  timed-out child, the one thing a supervisor must always be able to do.
  `(target children)` grants nothing outside the wrapped process tree.
- The `/`, `/etc`, `/tmp` and `/var` grants are `literal` grants on the
  **symlinks**, with `/private/etc` and `/private/var/select` as subpaths;
  without them `cat /etc/hosts` is denied and every `/bin/sh -c ...` dies with
  `Error opening /private/var/select/sh`.
- The **ancestor `file-read-metadata` literals**, which are new in this
  revision and are the reason the section was re-derived. Without them every
  absolute path into the write set fails at its leading components: measured,
  `git init $W/x` is `cannot mkdir: Operation not permitted`, `mkdir -p $W/a/b`
  is `mkdir: /private: Operation not permitted`, and `/bin/sh -c "cd $W"` is
  `Not a directory`, while the relative forms and `git status` succeed — a wall
  that passes a shallow test and kills the first second of a real job. The
  generator emits one literal per proper ancestor of every `--read`, `--write`,
  `--cwd` and `--tmp` path (`/` excluded, it is granted above).
  `file-read-metadata` is `stat(2)` only: **listing** an ancestor stays denied,
  and so does writing anywhere outside the write set.

**`profiles/darwin-check.sh`** is how that file is known to be right. It fills
the template for a scratch write set beside itself (bash, `set -euo pipefail`,
no `/tmp`, any cwd), then runs inside the wall, by absolute path, the first
second of a real job: `cd`, `mkdir -p`, `git init`, `git clone --shared` of a
local repository, a config write under `HOME`, `cat /etc/hosts`,
`/bin/sh -c true`, `sleep 5 & kill $!`, stdout to a pipe the caller drains and
stdout to a file inside the write set. It then asserts the three denials — a
write outside every named path, a read of the named secret file, a listing of
an ancestor — **each with a control run outside the wall**, so that no denial
can pass by being impossible. One line per check,
`CHECK OK name=...` / `CHECK FAIL name=...`, exit 1 on any FAIL. Sixteen
checks, all `OK` on this Mac (macOS 26.6.2, arm64), 2026-09-11. No revision of
this section is trusted until the script has been run on a Mac and its output
pasted into the commit.

Two things the script measured that the rules above now carry. The **cwd** is
load-bearing beyond rule 13's fence argument: with a cwd outside every named
path, `getcwd(3)` is denied and every `git` command dies with
`shell-init: error retrieving current directory ... Operation not permitted`
before it looks at anything else. And git's upward repository discovery reaches
**above** the write set: a job directory nested inside somebody else's checkout
finds that checkout's `.git`, cannot read it, and fails — which is why a job
directory is its own tree and not a subdirectory of one.

`-D` parameter escaping is still a live risk and not a formality: one measured
run of a multi-line profile with seven parameters printed `invalid data type of
path filter; expected pattern, got boolean` (exit 65) because a referenced
`(param ...)` had no `-D`, and a path containing a space and a paren aborted a
run (exit 134). Every param the filled profile names must be passed; that, and
what the tool does with metacharacters in a path, is item 2 of **to verify at
build**.

The filled profile is written to a file inside the first `--write` under a name
the tool chooses, is `0600`, and is removed when the command ends — which is
why the darwin body waits rather than `exec`s (rule 12).

## Linux — Landlock, no root

Landlock is an LSM available from kernel **5.13**, usable by an unprivileged
process, and inherited across `execve(2)` so that the child cannot lift it. The
three syscalls are `landlock_create_ruleset(2)`, `landlock_add_rule(2)` and
`landlock_restrict_self(2)`; `prctl(PR_SET_NO_NEW_PRIVS, 1)` must succeed
first, or `landlock_restrict_self` fails with `EPERM`.

**There is no pre-exec hook in Go.** `os/exec` has no `PreExec` callback and
`SysProcAttr` carries no user code, so the restriction cannot be applied
"in the child between fork and exec" from Go. The body is therefore
**restrict-then-exec in place**, in the tool's own process:

1. `landlock_create_ruleset` with `handled_access_fs` covering **every
   filesystem access the running ABI defines** — stated per ABI, because an
   access left unhandled is an access the kernel does not check, and that is a
   hole with no line in any log:

   | ABI (kernel) | `handled_access_fs` |
   |---|---|
   | 1 (5.13) | `EXECUTE`, `WRITE_FILE`, `READ_FILE`, `READ_DIR`, `REMOVE_DIR`, `REMOVE_FILE`, `MAKE_CHAR`, `MAKE_DIR`, `MAKE_REG`, `MAKE_SOCK`, `MAKE_FIFO`, `MAKE_BLOCK`, `MAKE_SYM` |
   | 2 (5.19) | + `REFER` |
   | 3 (6.2) | + `TRUNCATE` |
   | 5 (6.10) | + `IOCTL_DEV` |

   ABI 4 (6.7) and ABI 6 (6.12) add no filesystem access — 4 adds the network
   rule type, 6 the two scopes — so the filesystem set at 4 equals 3's and at
   6 equals 5's. `IOCTL_DEV` at ABI 5 is what the previous revision omitted
   while claiming "the whole set": without it a sandboxed process can `ioctl`
   any device file it can open. The set is masked down to the discovered ABI
   (a ruleset handling an access the kernel does not know is rejected), and
   the `MAKE_*`, `REMOVE_*`, `WRITE_FILE`, `TRUNCATE`, `REFER` and `IOCTL_DEV`
   bits are *granted* to the write set only; the read set and the roots get
   `EXECUTE|READ_FILE|READ_DIR`.
   The linux root list names `/proc`, not `/proc/self`. `/proc/self` opened
   `O_PATH` resolves at open time to the pid that opened it — the tool's,
   which after `syscall.Exec` is the command's — so a rule built on it grants
   the wrapped process its own `/proc` entry and grants **every child it
   spawns nothing**: a harness that runs a subprocess which reads
   `/proc/self/status` would fail for no legible reason. `/proc` read-only is
   the grant.

2. For each root and each `--read`: `open(2)` it `O_PATH|O_CLOEXEC` and
   `landlock_add_rule` with `LANDLOCK_RULE_PATH_BENEATH` and the read subset.
3. For each `--write`: the same with the full read+write subset.
4. `runtime.LockOSThread` (the restriction is per-thread until it is applied,
   and Go may otherwise move the goroutine), `prctl(PR_SET_NO_NEW_PRIVS, 1)`,
   `landlock_restrict_self`.
5. `syscall.Exec(path, argv, env)` — the tool **becomes** the command. Nothing
   after this line runs, so every status line, including `SANDBOX OK`, is
   printed and flushed before step 4.

The alternative is a re-exec helper (the tool re-executes itself with a hidden
flag, restricts, then execs), which buys a waiting parent at the cost of a
second process and a hidden flag; it is not chosen, because nothing on linux
needs cleanup after the command ends.

The ABI is discovered with `landlock_create_ruleset(NULL, 0,
LANDLOCK_CREATE_RULESET_VERSION)`, and the handled set is masked down to what
that ABI knows: a ruleset that handles an access the kernel does not understand
is rejected. The discovered number is printed as `abi=<n>` on `SANDBOX OK`.

**Network:** Landlock gained TCP `bind`/`connect` restriction at **ABI 4**
(kernel 6.7). UDP is not restricted at any ABI. Below ABI 4, `--net-deny` is
`SANDBOX REFUSED reason=net_unenforceable` (rule 7).

**Abstract unix sockets and signals** are unrestricted below **ABI 6** (kernel
6.12), which added `LANDLOCK_SCOPE_ABSTRACT_UNIX_SOCKET` and
`LANDLOCK_SCOPE_SIGNAL`; a sandboxed process on an older kernel can connect to
an abstract socket outside its domain and signal a process outside it. Where
ABI 6 is available the tool sets both scopes. This is a real gap on the fleet's
older kernels and is named here rather than in a footnote.

**ptrace** is restricted by Landlock without a scope flag: a process in a
Landlock domain may only `ptrace(2)` processes in the same domain or in a
domain nested inside it, so a sandboxed command cannot attach to a process
outside its wall. This is the protection wanted and it needs no verification
item.

Landlock is unavailable when the kernel predates 5.13, when it is not compiled
in, or when it is not in the boot-time `lsm=` list. All three come back as a
failed version query, and all three are `SANDBOX REFUSED reason=no_sandbox`
(rule 1).

## Windows — AppContainer, no admin

AppContainer is the isolation Edge and Store applications run under, and it is
creatable by an unprivileged user.

0. The container name is **`--name <container>`**, a flag, and on `windows` it
   is required: `nova-sandbox --name <container> --read ... --write ... -- cmd`.
   The previous revision said the name was "derived from the caller's name",
   and there is no caller name in the argv and no config file to hold one
   (**what it deliberately does not do**). It is argv, where `ps` shows it,
   like everything else this tool decides by. The swarm passes its pool id;
   a solo line passes the line's name. On `darwin` and `linux` `--name` is
   accepted and ignored, so one caller builds one argv for three platforms.
1. `CreateAppContainerProfile` (`userenv.dll`) once per pool or per line, with
   the name from `--name`; if the profile already
   exists the call returns `HRESULT_FROM_WIN32(ERROR_ALREADY_EXISTS)` and
   `DeriveAppContainerSidFromAppContainerName` gives the SID. The profile is
   deleted with `DeleteAppContainerProfile` when its owner is torn down.
2. **Who adds the ACEs: `--acl <tool|caller>`, default `tool`.** N workers of
   one pool name the same `--read` directories, and with each tool adding and
   removing its own ACEs the first worker to exit removes a grant the other
   workers are still reading through — the previous revision's step 4 had
   exactly that bug. The fix chosen is **ownership, not a reference count**:
   with `--acl caller` the tool adds nothing and removes nothing; it checks
   that the container SID already holds the right grant on every `--read` and
   `--write` and refuses `SANDBOX REFUSED reason=acl_missing` naming the
   directory if it does not. The **dispatcher** (or the solo line's launcher)
   adds the grants once when it creates the pool and removes them once at pool
   end, and it is the process that knows when the last worker is gone. A
   reference count in a file was rejected: it needs shared state with its own
   crash story, and a tool killed mid-run leaves a stale count exactly where
   it would have left a stale ACE — two hazards for the price of one. With
   `--acl tool` (the default, for a single run with no pool) the tool adds and
   removes its own grants as below, and concurrent runs on one directory are
   the caller's problem to avoid.

   For each `--write`, grant the container SID read+write by ACL:
   `GetNamedSecurityInfoW`, `SetEntriesInAclW` with an `EXPLICIT_ACCESS`
   carrying `GENERIC_READ|GENERIC_WRITE|GENERIC_EXECUTE` and
   `CONTAINER_INHERIT_ACE|OBJECT_INHERIT_ACE`, then `SetNamedSecurityInfoW`.
   For each `--read`, the same with `GENERIC_READ|GENERIC_EXECUTE` only. This
   needs ownership of the directory, not administrator rights.
3. Launch with `InitializeProcThreadAttributeList` +
   `UpdateProcThreadAttribute(PROC_THREAD_ATTRIBUTE_SECURITY_CAPABILITIES)`
   carrying a `SECURITY_CAPABILITIES` whose `AppContainerSid` is the SID and
   whose capability array holds `WinCapabilityInternetClientSid`
   (`CreateWellKnownSid`) **unless `--net-deny` is passed**, then
   `CreateProcessW` with `EXTENDED_STARTUPINFO_PRESENT`, and wait.
4. Under `--acl tool`, remove every ACE the tool added, after the wait. An ACE
   left behind outlives the run and is a standing grant to the container SID
   on a directory that is no longer sandboxed; if the tool is killed before it
   can clean up, the grant persists. Under `--acl caller` the tool removes
   nothing and the pool owner's teardown does it. The cleanup, the hazard and
   the concurrent case are all in the tests.

Everything else on disk is denied to the container SID by default. `%WINDIR%`
and `%ProgramFiles%` are readable through the built-in *ALL APPLICATION
PACKAGES* (`S-1-15-2-1`) grants, so the system toolchain runs; a toolchain
installed under `%LOCALAPPDATA%` or a user profile typically carries no such
grant and must be named with `--read`. The credential file is never granted,
and `~/.ssh` and the `gh` configuration directory are unreachable for the same
reason.

**Not chosen, and why:** Windows Sandbox is a VM per run and Pro/Enterprise
only; a restricted token at low integrity blocks writes but not reads; a job
object has no filesystem scope; WSL2 Landlock forces WSL on everyone.

## The probe

```
nova-sandbox probe --write <jobdir> --secret ~/.config/<provider>/env
```

Five checks, under the real policy for this platform, each one line:

| name | what it does | expected |
|---|---|---|
| `write_outside` | creates a file at one **explicitly named** path outside every named path — `<parent of the first --write>/.nova-sandbox-probe-<pid>` — printed on the step line | `deny` |
| `read_secret` | opens the named secret file for reading | `deny` |
| `write_inside` | creates and removes a file under the first `--write` | `allow` |
| `read_root` | reads a byte from the resolved command's own directory — the resolved **command file itself**, not a directory with no named file in it | `allow` |
| `write_outside_control` | the same write as `write_outside`, run **outside** the wall, before the wrapped one | `allow` |

`write_outside_control` is why the deny checks can be believed. A denial that
was never possible is not a wall: if the named outside path is unwritable to
this user anyway (`/opt` is `EACCES` on a Mac), `write_outside` comes back
`deny` on a **broken** wall and the probe passes. The control runs the same
write outside the wall first; if the control says `deny`, the probe is exit 2
`reason=probe_outside_unwritable` naming the path, because the machine cannot
answer the question — not a failed check, a misconfigured one. `read_secret`
needs no control (rule 6 refuses a `--secret` inside a list, and the caller
just read the file to pass its value by environment), and the two `allow`
checks are their own controls.

Every check runs; the verb does not stop at the first failure, because a caller
fixing a machine wants all five answers at once (SPEC.md: report every
independent problem at once). The exit is 1 if any check disagreed with its
expectation, and `PROBE REFUSED reason=check` names each one.

`write_outside` names its path instead of calling `os.TempDir()`, because
rule 8 points `TMPDIR` **inside** the wall: a probe that wrote to
`os.TempDir()` would write inside the write set, watch it succeed, and report
a false refusal — it would fail on a working wall. If the named path resolves
inside any `--read` or `--write` (a first `--write` whose parent is itself in
a list), the verb is exit 2 `reason=probe_outside_inside` and names the path,
because a probe that cannot find an outside is a misconfiguration, not a
failed check.

The secret file's **contents are never read into memory**: the check is that
`open(2)` (or `CreateFileW`) fails, and a probe that succeeded in opening it
closes it without reading and reports `got=allow`.

## The two callers

**nova-swarm, at its launch seam.** `supervise` wraps the harness it spawns.

*The write set*, derived per job by the dispatcher and **never configurable by
the task text**: the worker's job directory (also the `--cwd`, rule 13) and its
per-job data home. Nothing else. A task file that names a directory buys
nothing: the argv is built by the dispatcher from the job it created.

*The read set*, shared and named once per batch:

- `pool/ref/<repo>@<sha>` — **one reference checkout per distinct ref in the
  batch, owned by the dispatcher**. The previous revision said both "once per
  batch" and "per task at its pinned sha", and the two cannot both hold: two
  tasks in one batch with different `--ref` cannot share one directory while
  readers are running in it. The path therefore carries the sha. The
  dispatcher collects the distinct refs of the batch (the default ref counts
  as one), resolves each to a sha, and fetches **each one once**, over the
  network, with its own credential, outside every worker's wall, before the
  first worker starts; a sha already materialised by an earlier batch is not
  refetched. Each worker's read set names the one checkout for **its** task's
  sha. Two tasks at the same sha share one checkout, and N workers at one sha
  still cost one fetch, which is the number #69 cares about. A read-only task
  works on its checkout directly and copies nothing.
- the worker home, which holds `AGENTS.md` and the generated `opencode.json`
  fence — without it in the read set the harness cannot read its own config.
- the corpus and the specs the batch needs, if any.

*A task that needs its own tree* declares it with `tree: yes` in its header,
and the **dispatcher** runs `git clone --shared pool/ref/<repo>@<sha>
<jobdir>/repo` — that task's checkout, objects borrowed, no network, about a
second — **before the sandbox closes**. The clone lands in the job directory, which is in the write
set, so the worker can branch and commit in it. The worker therefore holds **no
git credential and needs no network for the repo at all**; the network it has
is the provider's API. The fetch is one network round per distinct sha in the
batch, not one per worker: 64 workers at one sha do not do 64 clones.

*The rest of the seam:* `--net-deny` is not passed, because the provider's API
is the work (`net=nopromise`); the provider key is read from its file before
the wrap and passed by environment (`nova-swarm` rule 6); `run` runs
`nova-sandbox probe` once before the first worker and refuses the pass with
`RUN REFUSED reason=sandbox_probe` on a failure, and a machine with no backend
is `RUN REFUSED reason=no_sandbox`. The consequences follow from the wall: no
SSH agent socket is reachable, no key is readable, `git push` from inside the
job fails, and the report copy under `pool/reports/` remains the only
publication path. A line's own self is in no task's write set, so a task's
shell cannot delete it (#69's worked specimen).

**A solo line's launcher.** A line started by hand gets no swarm and today gets
no wall. Its launcher calls `nova-sandbox` with lists **per line** — its home,
its lane clones and its scratch as `--write`, shared references as `--read` —
read from **one file the line's person keeps**, one absolute directory per line
prefixed by its flag, `#` comments ignored, and that file is the only place the
lists live.

The launcher does two more things, and without them a solo line is refused at
every start. It **sets `HOME` to a per-line data directory inside its write
set** — `<line home>/.data`, created by the launcher, named in the lists file
as a `--write` like any other — because the inherited `/Users/<user>` is in
neither list and rule 9 is `SANDBOX REFUSED reason=home_outside` for every run
that keeps it. And it **passes the line's write token by environment**
(`GH_TOKEN`, read by the launcher from wherever its person keeps it, outside
every list, as data and never sourced), because with `HOME` moved the token
cannot arrive the way it used to: the `gh` configuration directory and
`~/.ssh` are unreachable by rule 3, and they stay unreachable. The line still
pushes to its own home — the wall is on filesystem reach, not on the token
(Glenn, 2026-09-11) — and the remote is HTTPS, because no SSH key is readable
inside the wall.
Freddy is the first user; his launcher and his `AGENTS.md` name the command
(**one file is the self on a small harness**, 2026-09-10).

## The harness fence that ships beside it

`nova-sandbox fence --out <dir>/opencode.json` writes the block below into each
wrapped line's home or each worker home. OpenCode 1.18.20's `permission` block
takes `allow`, `ask` or `deny`, and pattern maps for some keys:

```json
{
  "permission": {
    "external_directory": "deny",
    "webfetch": "deny"
  }
}
```

`ask` is never written (rule 14). `webfetch` is `deny` for a swarm worker and
is the line's own choice for a solo line, which is what `--webfetch
allow|deny` selects. `external_directory` is evaluated relative to the
harness's working directory, which is why rule 13 exists.

The fence turns an honest program's mistake into a readable refusal; it stops
nothing that has decided to proceed. It is shipped beside the wall, never
instead of it.

## What it deliberately does not do

- **It does not manage credentials.** The environment passes through (rule 9).
- **It does not restrict syscalls.** No seccomp filter, no entitlement list;
  the question this tool answers is what a command can reach on disk.
- **It does not restrict CPU, memory or process count.** A runaway worker is
  the deadline's problem.
- **It does not create the directories it is handed.** Every `--read`,
  `--write`, `--cwd` and `--tmp` path must already exist; a missing one is a
  refusal and is not created (rule 5). The one directory it creates is its
  own: `<first --write>/.nova-sandbox-tmp` under rule 8 — inside the write
  set, named by the tool, never by the caller. An explicit `--tmp` is a caller
  path and follows rule 5: it must exist.
- **It does not run a shell.** Everything after `--` is `exec`'d (rule 12).
- **It does not take a caller-supplied profile.** The policy is generated
  (rule 15).
- **It does not clone anything.** The dispatcher owns the checkout; the tool
  only names directories.
- **It does not have a config file.** There is no file from which
  `--no-sandbox` or either list can arrive; all three are argv, where `ps`
  shows them.

## Measured on the machine, 2026-09-11 (macOS 26.6.2, arm64)

These were claims and are now facts, and the verification list below is shorter
for it.

From the first round: `sandbox-exec` is present, exits 0 and writes nothing to
stderr that pollutes a wrapped command's output; `-D key=value` reaches
`(param "NAME")` with `-f`; `--` is accepted before the command; `file-read*`,
`file-write*`, `subpath` and `network*` are valid in a `(version 1)` profile; a
write outside the granted paths is denied; an exit status of 3 and a `SIGKILL`
death (137) pass through the wrap unchanged; the single-clause exec/signal
grant breaks `exec` and `sandbox-exec` reports that failure as exit **71**;
`/private/var/db/dyld` does not exist; `(literal "/")` is required — without it
`/bin/echo` dies with `SIGABRT` (134) — and `/dev` is required for a plain
`2>/dev/null`, which is the redirection the shell performs before the command
runs. (The earlier sentence "`(literal \"/\")` and `/dev` are required for
`/bin/echo` and for Node" claimed a per-item necessity neither run showed:
Node was measured to run with the list complete, not to need each entry.)

From the second round, under the same profile: `sandbox-exec`'s wrap is
**exec in place** — the wrapped `sh` reports a `PPID` equal to the caller's own
pid, so no extra process sits between the tool and the command; with `(allow
signal (target self))` alone, `sh -c 'sleep 30 & kill $!'` prints `kill:
Operation not permitted`, and with `(target self) (target children)` the same
line exits 0; `cat /etc/hosts` and `ls /tmp` are denied by `(literal "/")`
alone while `/private/etc/hosts` is readable, and every `/bin/sh -c ...` dies
with `Error opening /private/var/select/sh`, all four fixed by the `/etc`,
`/tmp`, `/var` literals and `/private/var/select`; with the caller's `HOME`
inherited, `git -C <dir> status` is `fatal: unable to access
'/Users/<user>/.gitconfig': Operation not permitted`, and with `HOME` set to a
directory inside the write set it exits 0 (as does `GIT_CONFIG_GLOBAL` +
`XDG_CONFIG_HOME` pointed inside, for git alone); `/usr/bin/git` — the Xcode
shim — fails inside the wall on `/var/db/xcode_select_link` while
`/opt/homebrew/bin/git` works; a wrapped `/bin/cat` whose stdout is a file
outside every named path is denied while `/bin/echo` writing the same
descriptor succeeds.


Added in this revision, all from `profiles/darwin-check.sh` on this Mac
(sixteen checks, every one `OK`, exit 0): with the ancestor
`file-read-metadata` literals in the profile, `mkdir -p`, `git init` and
`git clone --shared` by **absolute** path into the write set succeed, where
without them they died at `/private`; a listing of an ancestor is still denied,
and so is a write outside and a read of the named secret, each with a control
run outside the wall that succeeds; a config write through a `HOME` inside the
write set lands; `sleep 5 & kill $!` exits 0; stdout to a pipe and stdout to a
file inside the write set both carry. Two failures found while getting there
and now in the rules: a cwd outside every named path denies `getcwd(3)` and
kills every `git` command with `shell-init: error retrieving current
directory`, and git's upward repository discovery reaches above the write set
into a `.git` the wall denies. And: `sandbox-exec -f p.sb -- /no/such` exits
**71**, exactly as `sh -c 'exit 71'` does, which is why the 71→126 mapping is
withdrawn.

## Commands for a reader

A reader on another machine, or on another model, checks this document by
running it rather than by trusting it. The darwin check is not four lines of
shell in a document any more — it is `profiles/darwin-check.sh`, which ships in
this repository, fills `profiles/darwin.sb.tmpl` itself and prints sixteen
`CHECK` lines:

```
# 1. darwin, the whole wall, from any cwd, writing only beside itself:
bash profiles/darwin-check.sh ; echo "exit=$?"
```

A reader who wants the one-liner instead needs a filled `p.sb`, and the script
is where one comes from: it fills the template into its scratch directory, so
`bash -x profiles/darwin-check.sh` shows the exact `sandbox-exec` argv and the
filled profile for every check, and the template's header says what each marker
is replaced by. Then:

```
# 2. darwin: cwd and stdout are part of the wall, not decoration.
#    Run this from INSIDE w (a cwd outside every named path denies getcwd(3),
#    and every git command dies there before it reads anything), with stdout a
#    PIPE the caller drains or a file inside w — a wrapped /bin/cat whose
#    stdout is a file outside every named path is denied (rule 12). Expect:
#    PPID == this shell's pid, the first line of /etc/hosts, and no
#    "Operation not permitted".
cd w && sandbox-exec -f ../p.sb -D READ0=<ref> -D WRITE0="$PWD" -D HOME="$PWD/home" \
  -- /bin/sh -c 'echo $PPID; cat /etc/hosts; sleep 9 & kill $!'

# 3. linux: the Landlock ABI, without Go. syscall 444 is
#    landlock_create_ruleset; flag 1 is LANDLOCK_CREATE_RULESET_VERSION.
python3 -c 'import ctypes;l=ctypes.CDLL(None,use_errno=True);print("landlock abi",l.syscall(444,0,0,1))'
cat /sys/kernel/security/lsm        # landlock must appear in the list

# 4. windows: who holds what on a directory, before, during and after a run —
#    the ACE lifetime of the windows section, of `grant`/`release`, and of
#    test 22.
icacls <dir>

# 5. darwin: the dyld cache path this spec says is absent on macOS 26.
ls /private/var/db/dyld
```

Commands 3–5 are not runnable on a Mac and commands 1–2 are not runnable
anywhere else; a reader runs the ones their machine can answer. A reader who
gets a different answer to any of them has found a defect in this document, and
the document changes.

## To verify at build

Each item is a claim in this document that was written from documentation and
must be **executed on the machine** before the spec's word is trusted. A build
that cannot confirm one changes this document rather than asserting it.

1. That `(allow mach-lookup)` unqualified, with `/` and `/dev` in the roots, is
   enough for a Node-based harness and a Go toolchain under the profile, and if
   not, the exact service list — a deny-default profile that blocks
   `mach-lookup` breaks `dyld` and process spawn in ways that look like
   unrelated crashes.
2. `-D` parameter escaping, which is a live risk and not a formality: one
   measured run of a multi-line profile with seven parameters printed
   `invalid data type of path filter; expected pattern, got boolean`, and a
   path containing a space and a paren aborted the run (134). Decide from the
   measurement whether the tool refuses paths carrying SBPL metacharacters,
   changes how parameters are grouped, or both.
3. Landlock syscall numbers as used from Go's `syscall` package on both
   `amd64` and `arm64`, and whether the restrict-then-exec body (which needs
   `runtime.LockOSThread`, `prctl`, three raw syscalls and `syscall.Exec`) is
   achievable under the repository's standard-library-only rule or needs
   `golang.org/x/sys/unix` — a dependency decision, not a detail.
4. That the restriction applied before `syscall.Exec` survives it for a Node
   harness that re-execs itself, and that a child process it spawns is equally
   restricted.
5. That *ALL APPLICATION PACKAGES* actually carries read+execute on `%WINDIR%`
   and `%ProgramFiles%` on the fleet's Windows images, and which of the
   toolchains the CI matrix uses are installed somewhere it does not cover.
6. That an AppContainer process can create and write files under a directory
   granted by an inherited ACE, including creating subdirectories, and that
   `git` and the harness work with `TMP`/`TEMP` redirected into it.

## Tests this spec demands

Each runs inside `t.TempDir()` with no network, against a fake command that
reports what it could read and write, and each must be seen red before it is
trusted. Every test names the platform it runs on and is skipped with a reason,
never silently, on the others.

One per rule:

1. On a machine with the backend, a wrapped `true` exits 0 and prints one
   `SANDBOX OK backend=<name>`; with the backend forced unavailable by the
   injected probe, nothing is executed — a tripwire on the exec path sees no
   call — and the output is `SANDBOX REFUSED reason=no_sandbox` at exit 125.
2. The whole suite runs as an unprivileged user, and a test asserts the process
   is not root (`os.Geteuid() != 0` on unix) before it trusts a pass.
3. A wrapped command reads a file under a root: succeeds. The same command
   reads a file under a `--read` path: succeeds. It **writes** under that
   `--read` path: fails. It reads and writes under a `--write` path: succeeds.
   Files planted at `<home>/.ssh/id_test`, `<home>/.config/gh/hosts.yml`,
   `<home>/Library/Keychains/probe.db` (darwin) and `<home>/.zsh_history` are
   each unreadable inside the wall and readable outside it in the same test.
   On darwin the roots themselves are asserted through their symlinks:
   `cat /etc/hosts` succeeds and `/bin/sh -c true` exits 0 inside the wall
   (both fail without the `/etc`, `/tmp`, `/var` literals and
   `/private/var/select`). On linux a wrapped command's **child** reads
   `/proc/self/status` successfully, which `/proc/self` as a root would
   deny.
4. No `--write` is exit 125 with the sentence naming the flag; three `--read`
   and two `--write` put five paths in the generated policy, read-only and
   read-write respectively, and print `read=3 write=2`; the same path in both
   lists is a refusal naming both flags.
5. A path that is a symlink to another directory produces the **resolved** path
   in the policy and the wrapped command can write through both names; a
   relative path is refused and the refusal prints the absolute form; a path
   that does not exist is refused and **is not created** — the test asserts the
   path is still absent afterwards.
6. A secret file outside both lists is unreadable by the wrapped command;
   `probe --secret` inside a `--read` or a `--write` is exit 2
   `reason=secret_inside_allow`; the secret's contents appear in no printed
   line, no generated policy file and no file under the write set — a scan of
   every byte the tool wrote.
7. With `--net-deny` on a backend that enforces it, a wrapped TCP dialer fails
   and the line says `net=denied`; on linux below ABI 4 (ABI forced down in the
   test) `--net-deny` is `SANDBOX REFUSED reason=net_unenforceable` at exit 125
   and the command does not run — a tripwire on the exec path sees no call;
   without `--net-deny` the line says `net=nopromise` and no `SANDBOX NOTE`
   claims a denial. A mutation that proceeds with a note instead of refusing
   turns the test red.
8. A wrapped command that writes to `$TMPDIR` succeeds and the file lands under
   the first `--write`; `TMPDIR`, `TMP` and `TEMP` all name it; `--tmp` outside
   the write set is refused.
9. An environment variable set by the caller arrives in the child unchanged,
   including one whose value is a credential-shaped string, and that value
   appears in no printed line.
10. `TestProbeProvesTheWall`: the `write_outside` path is the named one and is
    asserted to be outside every list **with `TMPDIR` pointed inside the wall
    by rule 8** — a probe built on `os.TempDir()` turns this red; a first
    `--write` whose parent is inside a list is exit 2
    `reason=probe_outside_inside`; all five checks run even when the first
    fails; a named outside path that this user cannot write to anyway is exit 2
    `reason=probe_outside_unwritable`, asserted with a directory the test makes
    read-only — the check that would otherwise pass on a broken wall; a
    policy that denies everything fails `write_inside` and `read_root` and is
    `PROBE REFUSED`, not `PROBE OK`; a policy with no wall at all fails
    `write_outside` and `read_secret`; a correct policy is
    `PROBE OK steps=5 passed=5`; the secret file's contents are never read.
11. `--no-sandbox` runs the command with no policy, prints exactly one
    `SANDBOX UNSANDBOXED` line to stderr, and passes the exit status through;
    no environment variable and no file can switch it on — the test sets every
    plausible name and the tool still sandboxes.
12. A wrapped command exiting 3 gives exit 3; one killed by `SIGKILL` gives
    137; an argument containing a space, a quote, a `$` and a `;` arrives in
    the child's argv byte-for-byte; stdout and stderr are not interleaved by
    the tool. Per platform: on linux the tool's pid **is** the command's pid
    after the wrap (the test reads `/proc/self/stat` from the wrapped command
    and compares it with the pid it spawned) and no wait happens; on darwin and
    windows `SIGTERM` (or the console control event) reaches the child and the
    tool waits for it. stdio: a wrapped command whose stdout is a **pipe**
    writes through it, and one whose stdout is a **file inside the write set**
    writes through it; one whose stdout is a file **outside** every named path
    fails for `/bin/cat` — the test asserts that failure rather than hiding
    it, because it is the reason the rule names only two legal shapes. On
    every platform the wrapped command can signal its
    **own** child: `sh -c 'sleep 30 & kill $!'` exits 0 and the sleep is gone,
    which is red on darwin without `(target children)` in the signal clause.
13. The default `--cwd` is the first `--write`; a `--cwd` outside the write set
    is exit 125 `reason=bad_cwd`; a wrapped command reports its own cwd as the
    job directory.
14. `fence --out` writes a file whose `permission` block contains
    `external_directory: deny` and the chosen `webfetch`, and **no value
    anywhere in the file is `ask`** — the test parses the JSON and walks it.
15. `policy` prints a policy and executes nothing; the same lists produce
    byte-identical output twice; there is no flag by which a caller-supplied
    profile file can be passed, asserted by the flag set itself.
16. A listing over 40 entries prints 20 and one MORE line naming the remedy;
    `--max 0` prints all; every refusal names the flag and its wanted form; two
    independent problems are both reported in one refusal; no test reaches
    outside `t.TempDir()` or touches the network (CONTRIBUTING.md, **test code
    is code**).

And one for each thing the rules above assert but no test yet reached:

17. `SANDBOX OK` is written and flushed **before** the command starts: the
    wrapped command writes a marker to a file, and the test asserts the stderr
    line is complete before the marker exists — on linux this is load-bearing,
    because after `syscall.Exec` the tool cannot print anything.
18. `check` on this machine prints one `CHECK OK` naming the backend, the ABI
    or `-`, and `net=enforceable|unenforceable`; with the backend forced
    unavailable it prints `backend=none` and still **exits 0**, because it is a
    question, not an attempt.
19. Exit `125`: a command that exists but is not executable — the pre-flight
    stats the path rule 5 resolved, **outside the wall and before any profile
    exists**, and refuses `SANDBOX REFUSED reason=not_executable`. Exit `127`:
    a command on no `PATH` entry, with one `SANDBOX REFUSED reason=not_found`.
    A command that itself exits 125, 126 or 127 gives the same number with
    **no** `SANDBOX REFUSED` line, and the test asserts the stderr difference,
    which is the only way to tell them apart. darwin, the measured case: the
    tool does **not** map the backend's exec failure, because it `exec`s in
    place and cannot see it. A profile under which `sandbox-exec` cannot exec
    the command (the single-clause exec/signal grant is one such) exits **71**
    raw, carrying `sandbox-exec`'s own `execvp() of '<cmd>' failed: Operation
    not permitted` on the inherited stderr and **no** `SANDBOX REFUSED` line;
    a wrapped command that genuinely exits 71 exits 71 the same way, and the
    test asserts that the two are indistinguishable — a mutation that turns
    either 71 into a 126, or prints a refusal line beside it, turns the test
    red. The refusal that does fire on darwin is the pre-flight's 125
    `reason=not_executable`, asserted in the same test, before the wrap.
20. darwin: the generated profile file is created under the first `--write`
    with mode `0600` (the test stats it while the command runs) and is **gone**
    after the command ends, on a clean exit and on a signal death alike.
21. `--read` ergonomics: a toolchain placed in a user directory is unreadable
    inside the wall without `--read` — the wrapped command fails (126 or a
    signal death) — and with `--read` it runs. The test asserts that **no**
    `SANDBOX NOTE` is printed after the command has started, on every platform
    (the linux body cannot, and the others must not diverge), and that the
    usage banner carries the `--read` remedy sentence.
22. windows, and it is **multi-worker**: under `--acl tool` the ACEs the tool
    adds appear on the `--write` and `--read` directories while the command
    runs, with the read-only grant carrying no write right, and are
    **removed** after it ends. Then three concurrent runs share one `--read`
    directory and exit at different times: under `--acl tool` the first exit
    removes the grant the other two are still reading through, and the test
    asserts that failure, because it is why the swarm does not use the
    default; under `--acl caller`, with the grant added by `nova-sandbox grant --name`
    run by the test standing in for the dispatcher, the ACE is present for all three from first start
    to last exit and is still present afterwards, and a run whose grant is
    missing is `SANDBOX REFUSED reason=acl_missing`. A `--name` is required on
    windows and a missing one is a refusal naming the flag. `grant` twice in a
    row adds no second ACE and `release` twice is not an error (both
    idempotent); `release` removes exactly what `grant` added and leaves a
    pre-existing ACE on the same directory standing; `grant` on the three
    named directories leaves a fourth sibling ungranted — the
    inherited-grant-on-the-pool-root bug, asserted as absent. A last test
    documents the hazard rather than hiding it: when the tool is killed under
    `--acl tool` with the command still running, the ACE persists, and the
    test asserts the persisting grant so that the next person to change the
    cleanup path sees what they are changing.
23. In `nova-swarm`: `run` with a failing probe prints `RUN REFUSED
    reason=sandbox_probe` and starts no worker; `run` on a machine with no
    backend prints `RUN REFUSED reason=no_sandbox` and starts no worker; both
    assert the worker count is zero, not just the line.
24. In `nova-swarm`: the dispatcher fetches `pool/ref/<repo>@<sha>` **once per
    distinct sha** — a counter on the fetch, asserted `== 1` for N workers at
    one sha and `== 2` for a batch holding two distinct `--ref` values, each
    worker's argv naming the checkout for its own sha — every worker's argv
    carries that checkout as `--read` and the worker home as `--read`, and a
    `tree: yes` task finds `<jobdir>/repo` already present, sharing objects
    with the reference (`.git/objects/info/alternates` names it) and reachable
    with no network. A task text naming a directory does not change the
    worker's `--write` argv — the test plants one and compares the argv.
    The same test proves rule 9 end to end **inside** the wall: with the argv
    the dispatcher built, `git -C <jobdir>/repo status` exits 0 and a harness
    config write (`$HOME/.config/opencode/opencode.json`, through the `HOME`
    the caller set) lands under the write set; with `HOME` left at the
    caller's, the run is `SANDBOX REFUSED reason=home_outside` and no worker
    starts.
25. From inside a sandboxed task, `rm -rf` of a line's self path fails with
    `EPERM` and the self is byte-identical afterwards (#69's worked specimen).
26. **The read and the adoption**, before this wraps a working loop: one
    recorded read of `nova-sandbox` against this spec by a line that is not its
    author, then one swarm batch run with the wrap and one without on the same
    task list, with the two compared. `freddy-swarm.sh` and `run-freddy.sh` are
    not touched by any step above (**production tool: do not change it**).

## The work list

To build it in Go under `cmd/nova-sandbox`, the way `cmd/nova-bus` is built: no
hardcoded paths, no default paths, the exit grammar above, `internal/oneline`
for every printed value, `internal/bounded` for every listing, and
`ONBOARDING.md`'s first-day standard — a usage banner ending in a runnable
`example:` block, refusals that say what the flag wants, a `### First run` in
`README.md`, a `quickstart` verb, and tests that pin all three by executing
them.

1. **`internal/sandbox/policy.go`** — the platform-independent half: the
   `Policy` type (read set, write set, roots, cwd, tmp, net), path resolution
   and refusal (rule 5), the per-platform root tables as data, the temp
   directory (rule 8), and `--print-policy`. Tests: 4, 5, 8, 15.
2. **`internal/sandbox/wrap_darwin.go`** — the generator for
   `profiles/darwin.sb.tmpl` (the file is embedded with `go:embed`, so the tool
   and the check script fill one text, not two): the five markers, the ancestor
   `file-read-metadata` literals, `-D` parameters, the two-clause exec/signal
   grant, the profile file at `0600` under the first `--write` and its removal,
   the wait, and `sandbox-exec` discovery that refuses rather than falls back.
   Tests: 1, 3, 7, 20; **to verify** items 1–2. `profiles/darwin-check.sh` is
   run by the mac CI job and its exit status is the job's.
3. **`internal/sandbox/wrap_linux.go`** — ABI discovery, the handled-access
   mask per ABI, `O_PATH` fds per rule, `LockOSThread`, `PR_SET_NO_NEW_PRIVS`,
   `landlock_restrict_self`, `syscall.Exec`, the ABI 6 scopes, and the
   `net_unenforceable` refusal. Tests: 1, 3, 7, 12, 17; **to verify** items
   3–4, and item 3 decides whether this package is standard-library-only.
4. **`internal/sandbox/wrap_windows.go`** — profile create/derive/delete from
   `--name`, the read-only and read-write ACL grants under `--acl tool`, the
   `grant` and `release` verbs and the grant **check** under `--acl caller`, the `SECURITY_CAPABILITIES` launch,
   the wait, and cleanup of the grants the tool added. Tests: 1, 3, 7, 22;
   **to verify** items 5–6.
5. **`internal/sandbox/exec.go`** — the transparent wrapper: no shell,
   inherited stdio, the platform's wait-or-exec choice, signal forwarding where
   there is a child, the exit-status and `128+N` mapping, and the
   `125`/`126`/`127` refusals. Tests: 12, 19.
6. **`cmd/nova-sandbox/main.go`** — the verbs, the `--` split, the output
   grammar, `probe` (test 10), `fence` (test 14), `check` (test 18),
   `grant`/`release` (test 22), the executability pre-flight (test 19),
   `--no-sandbox` with its one loud line (test 11), and the `--read` remedy
   sentence in the usage banner (test 21).
7. **The CI matrix** — linux, mac and windows jobs, each running its own
   platform's wrap tests for real and skipping the others by name (**test on
   multiple platforms**, 2026-09-09: fix the cause, not the assertion).
8. **The callers, after the read** — `nova-swarm run` fetches
   `pool/ref/<repo>@<sha>` once per distinct sha in the batch, sets `HOME` to
   each job's data home (inside that job's `--write`), calls `grant` on windows
   and `release` at teardown, runs the probe once, and a solo launcher sets
   `HOME` to the line's `.data` and passes the line's token by environment; `supervise` builds each worker's
   read and write argv and makes the `tree: yes` clone before the wrap; the
   solo line's launcher reads its lists file; Freddy's `AGENTS.md` names the
   command. Tests 23, 24, 25. Neither caller changes before test 26.
