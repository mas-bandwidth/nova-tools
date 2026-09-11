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
   default write set would be a guess about somebody else's job.
5. **Paths are resolved, absolute and existing.** Each `--read`, each
   `--write`, the `--cwd`, the `--tmp` and each root is resolved with
   `filepath.EvalSymlinks` and `filepath.Abs` before it reaches a policy,
   because macOS's `/tmp` is a symlink to `/private/tmp` and a sandbox profile
   written against the link grants nothing. A path that does not exist is
   **refused**, not created. A relative path is refused with the absolute form
   it would have taken. The command itself is resolved on the caller's `PATH`
   at this point, before the wrap — see the exit codes section for what that
   means for `127`.
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
   run**. The named workaround is loud and is in the argv where `ps` shows it:
   drop `--net-deny`, take `net=nopromise`, and the filesystem wall still
   stands. There is no `SANDBOX NOTE` that proceeds with a weaker wall than the
   caller asked for — that is the silent sandbox rule 1 exists to prevent.
   Landlock restricts only TCP `bind`/`connect` even at ABI 4; UDP is not
   restricted at any ABI, and `net=denied` on linux means exactly TCP.
8. **Temp is inside the wall.** The tool creates
   `<first --write>/.nova-sandbox-tmp` if it does not exist and sets `TMPDIR`,
   `TMP` and `TEMP` to it in the child's environment, because a deny-by-default
   policy makes the inherited per-user temp directory unwritable and a
   toolchain whose first scratch write fails looks like a broken sandbox rather
   than a working one. `--tmp <dir>` overrides it and must resolve inside a
   `--write` path.
9. **The environment passes through.** This is not a secrets tool: the child
   inherits the caller's environment, minus the three temp variables the tool
   sets. The credential the caller deliberately passed by environment (rule 6)
   must arrive.
10. **The probe proves the wall before the work runs.** `nova-sandbox probe
    --write <dir> [--read <dir>...] --secret <path>` runs four checks under the
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
    re-interpreted. stdin, stdout and stderr are inherited unchanged. The
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
nova-sandbox --read <dir>... --write <dir>... [--net-deny] [--cwd <dir>] [--tmp <dir>] [--no-sandbox] -- <command> <args...>
nova-sandbox probe   --write <dir>... [--read <dir>...] --secret <path> [--net-deny] [--max <n>]
nova-sandbox policy  --read <dir>... --write <dir>... [--net-deny] [--cwd <dir>]    (alias: --print-policy)
nova-sandbox fence   --out <file> [--webfetch allow|deny]
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
| 125 | `nova-sandbox` itself said **NO** before the command ran: `SANDBOX REFUSED` — no backend (`reason=no_sandbox`), the policy could not be applied (`reason=sandbox_failed`), an enforced network denial was asked for and is not available (`reason=net_unenforceable`), no `--write`, a relative or missing path, a path in both lists, a `--cwd` outside the write set, a missing `--` |
| 126 | the command was resolved but could not be executed (not executable, or the backend failed to start it) |
| 127 | the command could not be resolved on the caller's `PATH` |
| 128+N | the wrapped command was killed by signal `N` |

The reservation is ambiguous in one direction, as it is in `env(1)`: a wrapped
command that itself exits 125, 126 or 127 is indistinguishable from the tool's
own refusal by exit status alone. The tool's refusals always print a
`SANDBOX REFUSED` line to stderr and the command's do not, so a caller that
needs to tell them apart reads the line, not the number. This is stated rather
than fixed, because renumbering would break the convention the rest of the
table follows.

`127` is about the **caller's** `PATH`, not the policy's: rule 5 resolves the
command to an absolute path before the wrap, so the lookup happens outside the
wall and a `127` means the tool could not find the command at all. A command
that is found and then dies inside the wall for want of its interpreter or a
shared library exits `126` or dies by signal, and the `SANDBOX NOTE` on that
failure names `--read` as the remedy.

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
SANDBOX NOTE <the one remedy or gap line>
SANDBOX REFUSED reason=<no_sandbox|sandbox_failed|net_unenforceable|bad_read|bad_write|bad_cwd|no_command|not_found|not_executable>: <text>
PROBE STEP name=<write_outside|read_secret|write_inside|read_root> expect=<deny|allow> got=<deny|allow> path=<path>
PROBE OK backend=<name> abi=<n|-> steps=<n> passed=<n> net=<denied|nopromise>
PROBE REFUSED reason=<check|secret_inside_allow|no_sandbox|net_unenforceable>: <text>
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
| darwin | `/` (the directory itself, `(literal "/")`, not a subpath), `/System`, `/usr`, `/bin`, `/sbin`, `/Library`, `/opt/homebrew`, `/opt/local`, `/private/etc`, `/dev` (read), the directory of the resolved command; plus **write** on `/dev/null` and `/dev/tty` |
| linux | `/usr`, `/bin`, `/sbin`, `/lib`, `/lib64`, `/etc`, `/opt`, `/dev` (read), `/proc/self`, the directory of the resolved command; plus **write** on `/dev/null` and `/dev/tty` |
| windows | `%WINDIR%`, `%ProgramFiles%`, `%ProgramFiles(x86)%`, the directory of the resolved command |

`/` itself and `/dev` are in the darwin list because they were measured to be
required, not because a document said so: with the previous list `/bin/echo`
died with `SIGABRT` (exit 134) and a plain `2>/dev/null` failed. Node runs once
both are present. `/private/var/db/dyld` is **not** in the list: it does not
exist on macOS 26.

There is no `--root` flag. A toolchain installed into a user directory — Go
under `~/go`, node under `~/.nvm`, .NET under `~/.local`, the Studio's
`/Users/<user>/toolchains` — is named with `--read`, which is exactly a
caller-supplied read-only root and needs no second spelling. On Windows,
`--read` is what makes the tool add a read-only ACE for the container SID. A
command that dies for want of an interpreter inside the wall and runs outside
it is a missing `--read`, and the `SANDBOX NOTE` on that failure says so.

The home directory is never a root.

## macOS — `sandbox-exec` with a generated profile

The wrap is `sandbox-exec -f <profile> -D <name>=<value>... -- <command>
<args...>`. `sandbox-exec(1)` is present on macOS 26, is marked deprecated, and
works today; it applies the profile and `exec`s the command in place, so it is
not a second process sitting between the tool and the command.

The profile is Sandbox Profile Language (SBPL, a Scheme dialect) and is
generated per run, never hand-edited (rule 15):

```
(version 1)
(deny default)
(allow process-exec* process-fork)
(allow signal (target self))
(allow sysctl-read)
(allow mach-lookup)
(allow file-read* (literal "/"))
(allow file-read* (subpath (param "ROOT0")) (subpath (param "ROOT1")) ...)
(allow file-read* (subpath (param "READ0")) ...)
(allow file-read* file-write* (subpath (param "WRITE0")) ...)
(allow file-write* (literal "/dev/null") (literal "/dev/tty"))
(allow network*)            ; omitted when --net-deny
```

The exec and signal grants are **two clauses**. A single
`(allow process-exec* process-fork signal (target self))` applies the
`(target self)` filter to all three operations, and the measured result is
`execvp() of '/bin/echo' failed: Operation not permitted`, exit 71 — the
profile denies the exec it was meant to allow.

Caller paths enter the profile through `-D NAME=value` as `(param "NAME")`
rather than by string interpolation, so a directory with a quote or a paren in
its name cannot rewrite the policy. Parameters do reach `(param "NAME")` inside
`subpath` with `-f`; a path containing a space and a paren aborted one measured
run (exit 134), and that risk is item 2 of **to verify at build**.

The profile is written to a file inside the first `--write` under a name the
tool chooses, is `0600`, and is removed when the command ends — which is why
the darwin body waits rather than `exec`s (rule 12).

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

1. `landlock_create_ruleset` with `handled_access_fs` covering the whole
   read/write set the running ABI supports: `LANDLOCK_ACCESS_FS_EXECUTE`,
   `READ_FILE`, `READ_DIR`, `WRITE_FILE`, `MAKE_REG`, `MAKE_DIR`, `MAKE_SYM`,
   `REMOVE_FILE`, `REMOVE_DIR`, `REFER` (ABI 2+), `TRUNCATE` (ABI 3+).
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

1. `CreateAppContainerProfile` (`userenv.dll`) once per pool or per line, with a
   stable container name derived from the caller's name; if the profile already
   exists the call returns `HRESULT_FROM_WIN32(ERROR_ALREADY_EXISTS)` and
   `DeriveAppContainerSidFromAppContainerName` gives the SID. The profile is
   deleted with `DeleteAppContainerProfile` when its owner is torn down.
2. For each `--write`, grant the container SID read+write by ACL:
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
4. Remove every ACE the tool added, after the wait. An ACE left behind outlives
   the run and is a standing grant to the container SID on a directory that is
   no longer sandboxed; if the tool is killed before it can clean up, the grant
   persists. The cleanup and that hazard are both in the tests.

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

Four checks, under the real policy for this platform, each one line:

| name | what it does | expected |
|---|---|---|
| `write_outside` | creates a file in a temp directory outside every named path | `deny` |
| `read_secret` | opens the named secret file for reading | `deny` |
| `write_inside` | creates and removes a file under the first `--write` | `allow` |
| `read_root` | reads a byte from the resolved command's own directory | `allow` |

Every check runs; the verb does not stop at the first failure, because a caller
fixing a machine wants all four answers at once (SPEC.md: report every
independent problem at once). The exit is 1 if any check disagreed with its
expectation, and `PROBE REFUSED reason=check` names each one.

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

- `pool/ref/<repo>` — **one reference checkout per batch, owned by the
  dispatcher**. At `run` (or per task at its pinned sha, `--ref <sha>` on
  `add`/`batch`) nova-swarm fetches it once, over the network, with its own
  credential, outside every worker's wall. Every worker reads that one
  checkout. A read-only task works on it directly and copies nothing.
- the worker home, which holds `AGENTS.md` and the generated `opencode.json`
  fence — without it in the read set the harness cannot read its own config.
- the corpus and the specs the batch needs, if any.

*A task that needs its own tree* declares it with `tree: yes` in its header,
and the **dispatcher** runs `git clone --shared pool/ref/<repo>
<jobdir>/repo` — objects borrowed, no network, about a second — **before the
sandbox closes**. The clone lands in the job directory, which is in the write
set, so the worker can branch and commit in it. The worker therefore holds **no
git credential and needs no network for the repo at all**; the network it has
is the provider's API. The fetch is one network round per batch, not one per
worker: 64 workers do not do 64 clones.

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
lists live. The line keeps its own write token, because it pushes to its own
home: the wall is on filesystem reach, not on the token (Glenn, 2026-09-11).
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
- **It does not create directories.** A missing path is a refusal (rule 5).
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
for it: `sandbox-exec` is present, exits 0 and writes nothing to stderr that
pollutes a wrapped command's output; `-D key=value` reaches `(param "NAME")`
with `-f`; `--` is accepted before the command; `file-read*`, `file-write*`,
`subpath` and `network*` are valid in a `(version 1)` profile; a write outside
the granted paths is denied; an exit status of 3 and a `SIGKILL` death (137)
pass through the wrap unchanged; the single-clause exec/signal grant breaks
`exec`; `/private/var/db/dyld` does not exist; `(literal "/")` and `/dev` are
required for `/bin/echo` and for Node.

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
10. `TestProbeProvesTheWall`: all four checks run even when the first fails; a
    policy that denies everything fails `write_inside` and `read_root` and is
    `PROBE REFUSED`, not `PROBE OK`; a policy with no wall at all fails
    `write_outside` and `read_secret`; a correct policy is
    `PROBE OK steps=4 passed=4`; the secret file's contents are never read.
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
    tool waits for it.
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
19. Exit `126`: a command that exists but is not executable. Exit `127`: a
    command that is on no `PATH` entry. A command that itself exits 126 or 127
    gives the same number with **no** `SANDBOX REFUSED` line, and the test
    asserts the stderr difference, which is the only way to tell them apart.
20. darwin: the generated profile file is created under the first `--write`
    with mode `0600` (the test stats it while the command runs) and is **gone**
    after the command ends, on a clean exit and on a signal death alike.
21. `--read` ergonomics: a toolchain placed in a user directory is unreadable
    inside the wall without `--read` and the failure prints one `SANDBOX NOTE`
    naming `--read` as the remedy; with `--read` it runs.
22. windows: the ACEs the tool adds appear on the `--write` and `--read`
    directories while the command runs, with the read-only grant carrying no
    write right, and are **removed** after it ends. A second test documents the
    hazard rather than hiding it: when the tool is killed with the command
    still running, the ACE persists, and the test asserts the persisting grant
    so that the next person to change the cleanup path sees what they are
    changing.
23. In `nova-swarm`: `run` with a failing probe prints `RUN REFUSED
    reason=sandbox_probe` and starts no worker; `run` on a machine with no
    backend prints `RUN REFUSED reason=no_sandbox` and starts no worker; both
    assert the worker count is zero, not just the line.
24. In `nova-swarm`: the dispatcher fetches `pool/ref/<repo>` **once** per
    batch (a counter on the fetch, asserted `== 1` for N workers), every
    worker's argv carries it as `--read` and the worker home as `--read`, and a
    `tree: yes` task finds `<jobdir>/repo` already present, sharing objects
    with the reference (`.git/objects/info/alternates` names it) and reachable
    with no network. A task text naming a directory does not change the
    worker's `--write` argv — the test plants one and compares the argv.
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
2. **`internal/sandbox/wrap_darwin.go`** — SBPL generation with `-D`
   parameters and the two-clause exec/signal grant, the profile file at `0600`
   under the first `--write` and its removal, the wait, and `sandbox-exec`
   discovery that refuses rather than falls back. Tests: 1, 3, 7, 20;
   **to verify** items 1–2.
3. **`internal/sandbox/wrap_linux.go`** — ABI discovery, the handled-access
   mask per ABI, `O_PATH` fds per rule, `LockOSThread`, `PR_SET_NO_NEW_PRIVS`,
   `landlock_restrict_self`, `syscall.Exec`, the ABI 6 scopes, and the
   `net_unenforceable` refusal. Tests: 1, 3, 7, 12, 17; **to verify** items
   3–4, and item 3 decides whether this package is standard-library-only.
4. **`internal/sandbox/wrap_windows.go`** — profile create/derive/delete, the
   read-only and read-write ACL grants, the `SECURITY_CAPABILITIES` launch, the
   wait, and cleanup of the grants the tool added. Tests: 1, 3, 7, 22;
   **to verify** items 5–6.
5. **`internal/sandbox/exec.go`** — the transparent wrapper: no shell,
   inherited stdio, the platform's wait-or-exec choice, signal forwarding where
   there is a child, the exit-status and `128+N` mapping, and the
   `125`/`126`/`127` refusals. Tests: 12, 19.
6. **`cmd/nova-sandbox/main.go`** — the verbs, the `--` split, the output
   grammar, `probe` (test 10), `fence` (test 14), `check` (test 18),
   `--no-sandbox` with its one loud line (test 11), and the `--read` NOTE
   (test 21).
7. **The CI matrix** — linux, mac and windows jobs, each running its own
   platform's wrap tests for real and skipping the others by name (**test on
   multiple platforms**, 2026-09-09: fix the cause, not the assertion).
8. **The callers, after the read** — `nova-swarm run` fetches `pool/ref/<repo>`
   once per batch and runs the probe once; `supervise` builds each worker's
   read and write argv and makes the `tree: yes` clone before the wrap; the
   solo line's launcher reads its lists file; Freddy's `AGENTS.md` names the
   command. Tests 23, 24, 25. Neither caller changes before test 26.
