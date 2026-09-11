# nova-sandbox — specification

`nova-sandbox` is one binary at the **launch layer**. It runs one command with
its filesystem reach cut down by the operating system: the command may read the
OS and toolchain roots, it may read and write the directories the caller named,
and everything else on disk is denied to it by the kernel rather than by the
program's good intentions.

```
nova-sandbox --allow <dir> [--allow <dir>]... [--net] -- <command> <args...>
```

This spec is normative. If the code and this document disagree, one of them has
a bug, and the tests decide which. It stands beside [SPEC.md](../SPEC.md), whose
**Conventions** section — no guessed paths, the one-line output grammar, the
cap-and-count rule, `internal/oneline` and `internal/bounded` — applies here
unchanged and is not restated. The one deliberate departure from it is the exit
grammar of the exec verb, and that departure has its own section and its reason.

It exists because of [#69](https://github.com/mas-bandwidth/nova-tools/issues/69):
a swarm worker runs a cheaper model than the seat, a cheaper model follows a
planted instruction more readily, and so containment has to come from **below
the model**. A worker today holds the bench's SSH key and the bench's `gh`
token and reads untrusted input all day. The harness's own `permission` block
is a fence an honest program respects. This tool is the wall.

**Glenn's rulings, 2026-09-11, verbatim, and they are the shape of the tool:**

> "I don't want to force people to do user dirs, so if we can use macOS
> features (mac only) to make it safer, linux too that's best."

> "Is there a way to protect on windows too?"

> "I like nova-sandbox"

So: **OS-enforced containment, no dedicated users, on macOS, Linux and Windows.**

| the failure it closes | the rule that closes it |
|---|---|
| a worker with the bench's credentials can read `~/.ssh`, the `gh` config and the keychain, and nothing but the model's judgement stops it | deny by default; read only the OS and toolchain roots; read **and write** only the allow-list (rules 3, 4) |
| forcing a dedicated OS user per line is an administrative burden nobody will carry, so the containment would never ship | the three backends need no user, no root, no admin, no VM (rule 2) |
| a sandbox that silently does nothing on a platform it does not support is worse than no sandbox, because the caller believes it | `SANDBOX REFUSED reason=no_sandbox`, the command does not run, and `--no-sandbox` is the one loud named workaround (rules 1, 11) |
| a credential file readable inside the wall makes the wall decorative | the secret is **never inside the allow-list**; the caller reads it and passes the value by environment; the probe proves the file is unreadable (rules 6, 10) |
| `/tmp` on macOS is a symlink to `/private/tmp`, and a policy written against the unresolved path allows nothing | every path is resolved to its real absolute form before it enters a policy (rule 5) |
| a deny-by-default policy makes the inherited temp directory unwritable, and half a toolchain dies on its first scratch file | the tool creates a temp directory inside the allow-list and sets `TMPDIR`, `TMP` and `TEMP` to it (rule 8) |
| OpenCode's `external_directory` permission is relative to the harness cwd, so a job directory that is not the cwd is "external" to itself | the working directory is inside the allow-list and defaults to the first `--allow`; the swarm caller passes the job directory as both (rule 13) |
| a harness `permission` block set to `ask` hangs a headless job forever on a prompt nobody sees | the fence uses `allow` and `deny` only; `ask` is never written by this tool (rule 14) |

**The wall and the fence are different things and the difference is load-bearing.**
The fence is the `opencode.json` `permission` block: it catches an honest
mistake by a program that reads it. The wall is the kernel: it catches a program
that has decided not to. Shipping the fence and calling it containment is the
failure this tool exists to end. Everything below is about the wall, except one
section which is explicitly about the fence.

## The rules, numbered

Every rule here is normative. Each has one line in **tests this spec demands**
near the end.

1. **OS-enforced or refused.** There is one Go function,
   `sandbox.Wrap(cmd *exec.Cmd, p Policy) error`, with three bodies behind build
   tags: `sandbox-exec` on `darwin`, Landlock on `linux`, AppContainer on
   `windows`. If the platform's backend is not available at run time — no
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
3. **Deny by default, read the roots, write only the allow-list.** The policy
   denies filesystem access, then grants: **read** on the OS and toolchain roots
   (the platform lists are below, and they are data, not code), **read and
   write** on each `--allow` directory and everything beneath it. Nothing else
   is reachable. In particular `~/.ssh`, the `gh` configuration directory, the
   login keychain and the shell history are outside every default root list, and
   a caller that adds one back has done so on the record, in its own argv.
4. **The allow-list is explicit and is never guessed.** `--allow <dir>` is
   repeatable and has **no default**; zero of them is exit 125 and
   `refusing to guess`. A tool that supplied a default allow-list would be
   guessing about somebody else's job, and the guess would be wrong in the
   direction that costs the most.
5. **Paths are resolved, absolute and existing.** Each `--allow`, the `--cwd`
   and each root is resolved with `filepath.EvalSymlinks` and
   `filepath.Abs` before it reaches a policy, because macOS's `/tmp` is a
   symlink to `/private/tmp` and a sandbox profile written against the link
   grants nothing. A path that does not exist is **refused**, not created: a
   sandbox that creates the directory it was told to confine a program to has
   guessed the mode, the owner and the intent. A relative `--allow` is refused
   with the absolute form it would have taken.
6. **The secret is never inside the allow-list.** The caller reads its
   credential file **before** the wrap and passes the value by environment
   (`nova-swarm`'s rule 6: the key is read as data, never sourced, never an
   argument). `nova-sandbox` never reads a credential, never names one in a
   printed line, and never writes one to a file. The file itself stays outside
   every allowed path, so the wrapped command cannot read it even if it is told
   to. `probe --secret <path>` names the file by path — **a path is not a
   secret** — and a `--secret` that resolves inside any `--allow` is exit 2 and
   `reason=secret_inside_allow`, because that is a misconfiguration, not a
   failed probe.
7. **`--net` is explicit, off by default, and honest about enforcement.**
   Without `--net` the policy asks the backend to deny network access; with it,
   network is allowed for the provider's API. The backends differ in what they
   can actually enforce, and the tool says so rather than implying a guarantee:
   the `sandbox-exec` profile and the AppContainer capability set are believed to
   enforce it; Landlock enforces only TCP `bind`/`connect`, and only at ABI 4 or
   above. Where the running backend cannot enforce the network decision the tool
   prints one `SANDBOX NOTE` line naming the gap and **proceeds**, because the
   wall this tool sells is filesystem reach (Glenn, on the solo line: "the line
   keeps its write token, the wall is on filesystem reach, not on the token").
   A caller that needs network denial enforced must check the note.
8. **Temp is inside the wall.** The tool creates `<first --allow>/.nova-sandbox-tmp`
   if it does not exist and sets `TMPDIR`, `TMP` and `TEMP` to it in the child's
   environment, because a deny-by-default policy makes the inherited per-user
   temp directory unwritable and a toolchain whose first scratch write fails
   looks like a broken sandbox rather than a working one. `--tmp <dir>`
   overrides it and must resolve inside an allowed path.
9. **The environment passes through.** This is not a secrets tool: the child
   inherits the caller's environment, minus the three temp variables the tool
   sets. The credential the caller deliberately passed by environment (rule 6)
   must arrive, and a tool that scrubbed the environment would break the very
   pattern that keeps the key file out of the wall.
10. **The probe proves the wall before the work runs.** `nova-sandbox probe
    --allow <dir> --secret <path>` runs four checks under the real policy for
    this platform: a write **outside** every allowed path must fail; a read of
    the named secret file must fail; a write **inside** the allow-list must
    succeed; a read of a toolchain root must succeed. Any check that comes back
    the wrong way is `PROBE REFUSED` at exit 1 naming the check. The last two
    are not decoration: a wall that denies everything, including the work, is a
    broken sandbox that a two-check probe would call a pass.
11. **`--no-sandbox` is the one loud workaround.** It runs the command with no
    policy at all. It prints exactly one line to **stderr**,
    `SANDBOX UNSANDBOXED cmd=<name> allow=<n>: no OS containment; every read and
    write this command makes is yours`, before the command starts. It is never a
    default, never implied by a missing backend, never read from a config file
    or an environment variable, and never silent. A caller that passes it has
    said so in its own argv, where a person reading `ps` can see it.
12. **The exec verb is transparent.** Everything after `--` is executed
    verbatim through `exec.Command` — **never** through a shell, so no argument
    is re-parsed and no quote is re-interpreted. stdin, stdout and stderr are
    inherited unchanged. The child's exit status is the tool's exit status; a
    child killed by signal `N` gives exit `128+N`; `SIGINT` and `SIGTERM` are
    forwarded to the child's process group and the tool waits for it. The tool's
    own status lines go to **stderr**, so a wrapped command's stdout is its own.
13. **The working directory is inside the wall.** `--cwd <dir>` must resolve
    inside an allowed path; its default is the first `--allow`. This is not
    cosmetic: OpenCode's `external_directory` permission is evaluated **relative
    to the harness's working directory**, so a job directory that is not the cwd
    is "external" to the harness that is supposed to be working in it, and the
    fence denies the job its own files. The swarm caller therefore passes the
    job directory as the first `--allow` and as the cwd.
14. **The fence is `allow` or `deny`, never `ask`.** The `opencode.json`
    `permission` block this tool ships beside a wrapped harness uses only
    `allow` and `deny`. `ask` is never written, because a headless job with no
    person at the terminal hangs on the prompt until its deadline reaps it, and
    a hang is a worse outcome than either answer.
15. **The policy is generated and printable, never hand-edited.** The caller
    passes an allow-list; the tool generates the profile, the ruleset or the
    ACL grants. `--print-policy` writes the generated policy to stdout and exits
    0 without running anything, so a person can read the exact text that will be
    enforced. No profile is stored in the repository for editing, and the tool
    never accepts a caller-supplied profile file: a hand-edited wall is a wall
    with an undocumented door.
16. **Bounded output, and a refusal says what the input wants.** Every listing
    is a cap and a count per SPEC.md, `--max <n>` default 20, `0` for all, one
    MORE line naming the remedy. A refusal names the flag and the form it wants,
    reports every independent problem at once, and never prints the contents of
    a file it was handed.

## The verbs

```
nova-sandbox --allow <dir> [--allow <dir>]... [--net] [--cwd <dir>] [--tmp <dir>] [--no-sandbox] -- <command> <args...>
nova-sandbox probe   --allow <dir> [--allow <dir>]... --secret <path> [--net] [--max <n>]
nova-sandbox policy  --allow <dir> [--allow <dir>]... [--net] [--cwd <dir>]    (alias: --print-policy)
nova-sandbox fence   --out <file> [--webfetch allow|deny]
nova-sandbox check   [--max <n>]
```

`probe` is rule 10 and is the verb a caller runs **once before the first task**,
not per task: it costs a process and it answers a question about the machine,
not about the job. `nova-swarm run` runs it before it starts the first worker
and refuses the pass on a failure with `RUN REFUSED reason=sandbox_probe`.

`policy` prints the generated policy for an allow-list and runs nothing. It is
how a reader checks the wall without trusting this document.

`fence` writes the `opencode.json` `permission` block of rule 14 to a file. It
is the fence, it is shipped **beside** the wall and never instead of it, and the
verb exists so that the block is generated from one place rather than copied by
hand into every line's home.

`check` reports what this machine can enforce — the backend, its version or ABI,
and whether the network decision is enforceable — and exits 0 whether or not a
sandbox is available, because it is a question, not an attempt.

The binary is `nova-sandbox`, and that is its only name (Glenn: "I like
nova-sandbox").

## Exit codes

The exec verb cannot use SPEC.md's 0/1/2 grammar, because its exit status
belongs to the wrapped command: a tool that returned 2 for a bad flag would be
indistinguishable from a command that exited 2 on its own. It uses the
`env(1)` / `timeout(1)` convention instead, which reserves the top of the
range, and this is a deliberate, recorded departure from the conventions
(Glenn, 2026-09-11: **flexibility, not rigidity** — and a workaround is named on
the record).

| code | meaning |
|------|---------|
| 0–124 | the wrapped command's own exit status, passed through unchanged |
| 125 | `nova-sandbox` itself said **NO** before the command ran: `SANDBOX REFUSED` — no backend (`reason=no_sandbox`), the policy could not be applied (`reason=sandbox_failed`), no `--allow`, a relative or missing allow path, a `--cwd` outside the allow-list, a missing `--` |
| 126 | the command was found but could not be executed (not executable, or the wrapper itself failed to start) |
| 127 | the command was not found on `PATH` inside the policy |
| 128+N | the wrapped command was killed by signal `N` |

The `probe`, `policy`, `fence` and `check` verbs are not wrappers and use
SPEC.md's grammar unchanged: **0** the verb ran and passed, **1** the verb ran
and said NO (a probe check that came back the wrong way, a policy for a backend
this machine does not have), **2** could not run (a missing flag, an unreadable
path, `--secret` inside the allow-list, bad invocation).

## Output grammar

Every line below goes to **stderr** except the body of `policy` and `fence`,
which is the thing asked for and goes to stdout.

```
SANDBOX OK backend=<sandbox-exec|landlock|appcontainer> abi=<n|-> allow=<n> net=<allowed|denied|unenforced> cwd=<dir> cmd=<name>
SANDBOX UNSANDBOXED cmd=<name> allow=<n>: no OS containment; every read and write this command makes is yours
SANDBOX NOTE <the one remedy or gap line>
SANDBOX REFUSED reason=<no_sandbox|sandbox_failed|bad_allow|bad_cwd|no_command|not_found|not_executable>: <text>
PROBE STEP name=<write_outside|read_secret|write_inside|read_root> expect=<deny|allow> got=<deny|allow> path=<path>
PROBE OK backend=<name> abi=<n|-> steps=<n> passed=<n> net=<allowed|denied|unenforced>
PROBE REFUSED reason=<check|secret_inside_allow|no_sandbox>: <text>
POLICY OK backend=<name> allow=<n> bytes=<n>
POLICY REFUSED reason=<no_sandbox|bad_allow>: <text>
FENCE OK out=<path> keys=<n>
FENCE REFUSED: <reason>
CHECK OK backend=<name|none> abi=<n|-> net=<enforceable|unenforceable> note=<one clause|->
```

`SANDBOX OK` is printed **before** the command starts, so a log that ends in a
crash still says what the wall was. It names `cmd=<name>` — the base name of the
executable — and never the arguments, because arguments carry task text and task
text carries quoted rules.

`net=unenforced` is rule 7's honesty: the caller asked for network denial, the
backend cannot deliver it, the filesystem wall stands, and the word is in the
line rather than in a footnote.

**The tool never prints a credential, a file's contents, or an argument vector.**
A refusal about a path prints the path, which the caller supplied and already
knows.

## The allow-list, and the roots

**The allow-list** is what the caller names: read **and** write, recursively,
for each `--allow` and everything under it.

**The roots** are what any command needs to run at all: read **only**,
recursively. They are a per-platform list in one data file in the source, not a
string built in three places, and `policy` prints them:

| platform | read-only roots |
|---|---|
| darwin | `/System`, `/usr`, `/bin`, `/sbin`, `/Library`, `/opt/homebrew`, `/opt/local`, `/private/etc`, `/private/var/db/dyld`, the directory of the resolved command, and the user's toolchain roots named by `--root` |
| linux | `/usr`, `/bin`, `/sbin`, `/lib`, `/lib64`, `/etc`, `/opt`, `/proc/self`, the directory of the resolved command, and the user's toolchain roots named by `--root` |
| windows | `%WINDIR%`, `%ProgramFiles%`, `%ProgramFiles(x86)%`, the directory of the resolved command, and the user's toolchain roots named by `--root` |

`--root <dir>` is repeatable, adds one read-only root, and exists because a
toolchain installed into a user directory — Go under `~/go`, node under
`~/.nvm`, .NET under `~/.local`, anything under `%LOCALAPPDATA%` — is **not**
covered by the system roots and will not be readable inside the wall. The
Studio's own schema toolchains live under `/Users/<user>/toolchains`, which is
exactly this case. A command that dies with a missing interpreter inside the
wall and runs outside it is a missing `--root`, and the `SANDBOX NOTE` line on a
`127` says so.

The home directory is **never** a root. That is the whole point.

## macOS — `sandbox-exec` with a generated profile

The wrap is `sandbox-exec -f <profile> -D <name>=<value>... -- <command>
<args...>`. `sandbox-exec(1)` is present on macOS 26 and is marked deprecated;
it is the mechanism Apple's own tooling still uses, and it works today.

The profile is Sandbox Profile Language (SBPL, a Scheme dialect) and is
generated per run, never hand-edited (rule 15):

```
(version 1)
(deny default)
(allow process-exec* process-fork signal (target self))
(allow sysctl-read)
(allow mach-lookup)
(allow file-read* (subpath (param "ROOT0")) (subpath (param "ROOT1")) ...)
(allow file-read* file-write* (subpath (param "ALLOW0")) ...)
(allow network*)            ; only when --net
```

Caller paths enter the profile through `-D NAME=value` as `(param "NAME")`
rather than by string interpolation, so a directory with a quote or a paren in
its name cannot rewrite the policy. This is the same reason `nova-swarm` refuses
to take a task text as an argument.

The profile is written to a file inside the first `--allow` under a name the
tool chooses, is `0600`, and is removed when the command ends.

## Linux — Landlock, no root

Landlock is an LSM available from kernel **5.13**, usable by an unprivileged
process, and inherited across `execve(2)` so that the child cannot lift it. The
three syscalls are `landlock_create_ruleset(2)`, `landlock_add_rule(2)` and
`landlock_restrict_self(2)`; `prctl(PR_SET_NO_NEW_PRIVS, 1)` must succeed first,
or `landlock_restrict_self` fails with `EPERM`.

The sequence, in the child between `fork` and `exec` (Go: in the
`exec.Cmd`'s pre-exec path, which is why this backend needs its own small
`syscall`-level helper rather than `os/exec` alone):

1. `landlock_create_ruleset` with `handled_access_fs` covering the whole
   read/write set the running ABI supports: `LANDLOCK_ACCESS_FS_EXECUTE`,
   `READ_FILE`, `READ_DIR`, `WRITE_FILE`, `MAKE_REG`, `MAKE_DIR`, `MAKE_SYM`,
   `REMOVE_FILE`, `REMOVE_DIR`, `REFER` (ABI 2+), `TRUNCATE` (ABI 3+).
2. For each read-only root: `open(2)` it `O_PATH|O_CLOEXEC` and
   `landlock_add_rule` with `LANDLOCK_RULE_PATH_BENEATH` and the read subset.
3. For each `--allow`: the same with the full read+write subset.
4. `prctl(PR_SET_NO_NEW_PRIVS, 1)`, then `landlock_restrict_self`, then `exec`.

The ABI is discovered with `landlock_create_ruleset(NULL, 0,
LANDLOCK_CREATE_RULESET_VERSION)`, and the handled set is masked down to what
that ABI knows: a ruleset that handles an access the kernel does not understand
is rejected, so a tool that asked for ABI 4 bits on a 5.13 kernel would refuse
on every old machine. The discovered number is printed as `abi=<n>` on
`SANDBOX OK`.

**Network:** Landlock gained TCP `bind`/`connect` restriction at **ABI 4**
(kernel 6.7). Below that, and for UDP and unix sockets at any ABI, Landlock does
not restrict the network, so `--net`'s absence prints `net=unenforced` and one
`SANDBOX NOTE` (rule 7). Landlock also does not restrict `ptrace(2)` **out of**
the sandbox in the direction that matters here: a Landlock domain cannot ptrace
a process outside it, which is the protection wanted, and this is one of the
items in **to verify at build**.

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
2. For each `--allow`, grant the container SID read+write by ACL:
   `GetNamedSecurityInfoW`, `SetEntriesInAclW` with an `EXPLICIT_ACCESS`
   carrying `GENERIC_READ|GENERIC_WRITE|GENERIC_EXECUTE` and
   `CONTAINER_INHERIT_ACE|OBJECT_INHERIT_ACE`, then `SetNamedSecurityInfoW`.
   This needs ownership of the directory, not administrator rights.
3. Launch with `InitializeProcThreadAttributeList` +
   `UpdateProcThreadAttribute(PROC_THREAD_ATTRIBUTE_SECURITY_CAPABILITIES)`
   carrying a `SECURITY_CAPABILITIES` whose `AppContainerSid` is the SID and
   whose capability array holds `WinCapabilityInternetClientSid`
   (`CreateWellKnownSid`) **only when `--net` is passed**, then `CreateProcessW`
   with `EXTENDED_STARTUPINFO_PRESENT`.

Everything else on disk is denied to the container SID by default. `%WINDIR%`
and `%ProgramFiles%` are readable through the built-in *ALL APPLICATION
PACKAGES* (`S-1-15-2-1`) grants, so the system toolchain runs; a toolchain
installed under `%LOCALAPPDATA%` or a user profile typically carries **no** such
grant and must be named with `--root`, which makes the tool add a read-only ACE
for the container SID on it. The credential file is never granted, so it is
unreadable, and `~/.ssh` and the `gh` configuration directory are unreachable
for the same reason.

**Not chosen, and why:** Windows Sandbox is a VM per run and Pro/Enterprise
only; a restricted token at low integrity blocks writes but not reads; a job
object has no filesystem scope; WSL2 Landlock forces WSL on everyone.

## The probe

```
nova-sandbox probe --allow <jobdir> --secret ~/.config/<provider>/env
```

Four checks, under the real policy for this platform, each one line:

| name | what it does | expected |
|---|---|---|
| `write_outside` | creates a file in a temp directory outside every allowed path | `deny` |
| `read_secret` | opens the named secret file for reading | `deny` |
| `write_inside` | creates and removes a file under the first `--allow` | `allow` |
| `read_root` | reads a byte from the resolved command's own directory | `allow` |

Every check runs; the verb does not stop at the first failure, because a caller
fixing a machine wants all four answers at once (SPEC.md: report every
independent problem at once). The exit is 1 if any check disagreed with its
expectation, and `PROBE REFUSED reason=check` names each one.

The secret file's **contents are never read into memory**: the check is that
`open(2)` (or `CreateFileW`) fails, and a probe that succeeded in opening it
closes it without reading and reports `got=allow`.

## The two callers

**nova-swarm, at its launch seam.** `supervise` wraps the harness it spawns:
one job directory per worker, passed as the first `--allow` **and** as the
`--cwd` (rule 13), the per-job data home as the second `--allow`, `--net`
because the provider's API is the work, and the provider key read from its file
before the wrap and passed by environment (`nova-swarm` rule 6). `run` runs
`nova-sandbox probe` once before the first worker and refuses the pass with
`RUN REFUSED reason=sandbox_probe` on a failure; a machine with no backend is
`RUN REFUSED reason=no_sandbox`. The consequences follow from the wall: no SSH
agent socket is reachable, no key is readable, the clone is over HTTPS with the
read-scoped token, `git push` from inside the job fails, and the report copy
under `pool/reports/` remains the only publication path.

**A solo line's launcher.** A line started by hand gets no swarm and today gets
no wall. Its launcher calls `nova-sandbox` with an allow-list **per line** —
its home, its lane clones, its scratch — read from **one file the line's person
keeps**, one absolute directory per line, `#` comments ignored, and that file is
the only place the list lives. The line keeps its own write token, because it
pushes to its own home: **the wall is on filesystem reach, not on the token**
(Glenn, 2026-09-11). Freddy is the first user; his launcher and his `AGENTS.md`
name the command, so that the one page that is his self on a small harness says
what the wall is (**one file is the self on a small harness**, 2026-09-10).

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

`ask` is never written (rule 14): a headless job hangs on a prompt until its
deadline reaps it. `webfetch` is `deny` for a swarm worker and is the line's own
choice for a solo line, which is what `--webfetch allow|deny` selects.
`external_directory` is evaluated relative to the harness's working directory,
which is why rule 13 exists and why the job directory must be the cwd.

**This is the fence, not the wall.** It is worth shipping: it turns an honest
program's mistake into a clean refusal with a readable message instead of a
kernel denial in the middle of a write. It stops nothing that has decided to
proceed. A release that shipped the fence and not the wall would have shipped
the appearance of #69's answer and none of it.

## What it deliberately does not do

- **It does not manage credentials.** The environment passes through (rule 9).
  Reading a key file, scoping a token and refusing a writable one belong to the
  caller — `nova-swarm`'s rules 6 and its read-scoped-credential item.
- **It does not restrict syscalls.** No seccomp filter, no entitlement list.
  The question this tool answers is *what can this command reach on disk*, and
  a second, larger question would have delayed the answer to the first.
- **It does not restrict CPU, memory or process count.** A runaway worker is
  the deadline's problem, and the deadline is held by the machinery that
  launched it.
- **It does not create directories.** A missing `--allow` is a refusal (rule 5).
- **It does not run a shell.** Everything after `--` is `exec`'d (rule 12).
- **It does not take a caller-supplied profile.** The policy is generated
  (rule 15).
- **It does not have a config file.** There is no file from which `--no-sandbox`
  or an allow-list can arrive; both are argv, where `ps` shows them.

## To verify at build

Each item is a claim in this document that was written from documentation and
must be **executed on the machine** before the spec's word is trusted. A build
that cannot confirm one changes this document rather than asserting it.

1. `sandbox-exec` on macOS 26: that the deprecation is a warning and not a
   refusal, whether it writes anything to stderr that pollutes a wrapped
   command's output, and whether SIP or a signed harness binary changes the
   result.
2. That `(allow mach-lookup)` unqualified is enough for a Node-based harness
   and a Go toolchain under the profile, and if not, the exact service list —
   a deny-default SBPL profile that blocks `mach-lookup` breaks `dyld` and
   process spawn in ways that look like unrelated crashes.
3. That `-D NAME=value` parameters reach `(param "NAME")` inside `subpath`
   correctly for paths containing spaces, and that a path with a quote or a
   paren cannot escape the parameter.
4. Landlock syscall numbers as used from Go's `syscall` package on both
   `amd64` and `arm64`, and whether a `syscall`-only implementation is
   achievable under the repository's standard-library-only rule or needs
   `golang.org/x/sys/unix` — this is a dependency decision, not a detail.
5. That the Landlock restriction applied before `exec` survives `exec` for a
   Node harness that re-execs itself, and that a child process it spawns is
   equally restricted.
6. The ABI numbers stated here (2 = `REFER`, 3 = `TRUNCATE`, 4 = TCP network,
   kernel 5.13 / 5.19 / 6.2 / 6.7) against the running kernel's own version
   query, not against this table.
7. Whether a Landlock domain can `ptrace(2)` a process outside the domain, in
   both directions, on the kernels the fleet runs.
8. That `CreateAppContainerProfile` succeeds for a standard (non-admin) user,
   and the exact behaviour on a second call with the same name.
9. That *ALL APPLICATION PACKAGES* actually carries read+execute on `%WINDIR%`
   and `%ProgramFiles%` on the fleet's Windows images, and which of the
   toolchains the CI matrix uses are installed somewhere it does not cover.
10. That an AppContainer process can create and write files under a directory
    granted by an inherited ACE, including creating subdirectories, and that
    `git` and the harness work with `TMP`/`TEMP` redirected into it.
11. Whether an AppContainer without `internetClient` blocks loopback as well as
    outbound — loopback is separately gated by `CheckNetIsolation`, and a
    harness that talks to its own localhost helper would break.
12. That the child's exit status, and a signal death, survive each of the three
    wrappers unchanged — `sandbox-exec` in particular is another process
    between the tool and the command.

## Tests this spec demands

One line per rule. Each runs inside `t.TempDir()` with no network, against a
fake command that reports what it could read and write, and each must be seen
red before it is trusted. Every test names the platform it runs on and is
skipped with a reason, never silently, on the others.

1. On a machine with the backend, a wrapped `true` exits 0 and prints one
   `SANDBOX OK backend=<name>`; with the backend forced unavailable by the
   injected probe, nothing is executed — a tripwire on the child's exec path
   sees no call — and the output is `SANDBOX REFUSED reason=no_sandbox` at
   exit 125.
2. The whole suite runs as an unprivileged user, and a test asserts the process
   is not root (`os.Geteuid() != 0` on unix) before it trusts a pass.
3. A wrapped command that reads a file under a root succeeds; the same command
   reading a file in the user's home outside the allow-list fails; a file placed
   at `<home>/.ssh/id_test` is unreadable inside the wall and readable outside
   it in the same test.
4. No `--allow` is exit 125 with the sentence naming the flag; `--allow` given
   three times puts three paths in the generated policy and `allow=3` on the
   line.
5. An `--allow` that is a symlink to another directory produces the **resolved**
   path in the policy and the wrapped command can write through both names; a
   relative `--allow` is refused and the refusal prints the absolute form; a
   `--allow` that does not exist is refused and **is not created** — the test
   asserts the path is still absent afterwards.
6. A secret file outside the allow-list is unreadable by the wrapped command;
   `probe --secret` inside an `--allow` is exit 2 `reason=secret_inside_allow`;
   the secret's contents appear in no printed line, no generated policy file and
   no file under the allow-list — a scan of every byte the tool wrote.
7. Without `--net` on a backend that enforces it, a wrapped dialer fails and the
   line says `net=denied`; on a backend that cannot, the dialer succeeds, the
   line says `net=unenforced`, and exactly one `SANDBOX NOTE` is printed — a
   mutation that prints `net=denied` in the unenforceable case turns the test
   red.
8. A wrapped command that writes to `$TMPDIR` succeeds and the file lands under
   the first `--allow`; `TMPDIR`, `TMP` and `TEMP` all name it; `--tmp` outside
   the allow-list is refused.
9. An environment variable set by the caller arrives in the child unchanged,
   including one whose value is a credential-shaped string, and that value
   appears in no printed line.
10. `TestProbeProvesTheWall`: all four checks run even when the first fails;
    a policy that denies everything fails `write_inside` and `read_root` and is
    `PROBE REFUSED`, not `PROBE OK`; a policy with no wall at all fails
    `write_outside` and `read_secret`; a correct policy is
    `PROBE OK steps=4 passed=4`; the secret file's contents are never read.
11. `--no-sandbox` runs the command with no policy, prints exactly one
    `SANDBOX UNSANDBOXED` line to stderr, and passes the exit status through;
    no environment variable and no file can switch it on — the test sets every
    plausible name and the tool still sandboxes.
12. A wrapped command exiting 3 gives exit 3; one killed by `SIGKILL` gives 137;
    an argument containing a space, a quote, a `$` and a `;` arrives in the
    child's argv byte-for-byte; stdout and stderr are not interleaved by the
    tool; `SIGTERM` to the tool reaches the child and the tool waits.
13. The default `--cwd` is the first `--allow`; a `--cwd` outside the allow-list
    is exit 125 `reason=bad_cwd`; a wrapped command reports its own cwd as the
    job directory.
14. `fence --out` writes a file whose `permission` block contains
    `external_directory: deny` and the chosen `webfetch`, and **no value
    anywhere in the file is `ask`** — the test parses the JSON and walks it.
15. `policy` prints a policy and executes nothing; the same allow-list produces
    byte-identical output twice; there is no flag by which a caller-supplied
    profile file can be passed, asserted by the flag set itself.
16. A listing over 40 entries prints 20 and one MORE line naming the remedy;
    `--max 0` prints all; every refusal names the flag and its wanted form;
    two independent problems are both reported in one refusal; no test reaches
    outside `t.TempDir()` or touches the network (CONTRIBUTING.md, **test code
    is code**).
17. **The read and the adoption**, before this wraps a working loop: one
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
   `Policy` type (allow-list, roots, cwd, tmp, net), path resolution and
   refusal (rule 5), the per-platform root tables as data, the temp directory
   (rule 8), and `--print-policy`. Tests: demanded tests 4, 5, 8, 15.
2. **`internal/sandbox/wrap_darwin.go`** — SBPL generation with `-D`
   parameters, the profile file at `0600` under the first `--allow`, removal on
   exit, and `sandbox-exec` discovery that refuses rather than falls back.
   Tests: demanded tests 1, 3, 7 on darwin; **to verify at build** items 1–3.
3. **`internal/sandbox/wrap_linux.go`** — ABI discovery, the handled-access
   mask per ABI, `O_PATH` fds per rule, `PR_SET_NO_NEW_PRIVS`,
   `landlock_restrict_self` in the pre-exec path, and the `net=unenforced`
   determination. Tests: demanded tests 1, 3, 7 on linux; **to verify at
   build** items 4–7, and item 4 decides whether this package is
   standard-library-only.
4. **`internal/sandbox/wrap_windows.go`** — profile create/derive/delete, the
   ACL grants on the allow-list and on each `--root`, the
   `SECURITY_CAPABILITIES` launch, and cleanup of the grants the tool added.
   Tests: demanded tests 1, 3, 7 on windows; **to verify at build** items 8–11.
5. **`internal/sandbox/exec.go`** — the transparent wrapper: `exec.Command`
   with no shell, inherited stdio, signal forwarding, the exit-status and
   `128+N` mapping, and the `125`/`126`/`127` refusals. Tests: demanded test 12;
   **to verify at build** item 12.
6. **`cmd/nova-sandbox/main.go`** — the verbs, the `--` split, the output
   grammar, `probe` (demanded test 10), `fence` (demanded test 14), `check`,
   and `--no-sandbox` with its one loud line (demanded test 11).
7. **The CI matrix** — linux, mac and windows jobs, each running its own
   platform's wrap tests for real and skipping the others by name (**test on
   multiple platforms**, 2026-09-09: platforms surface bugs; fix the cause, not
   the assertion).
8. **The callers, after the read** — `nova-swarm supervise` wraps its harness
   and `run` runs the probe once; the solo line's launcher reads its allow-list
   file; Freddy's `AGENTS.md` names the command. Neither caller changes before
   demanded test 17.
9. **The `--root` ergonomics** — a `127` or a missing-interpreter failure
   inside the wall prints the `SANDBOX NOTE` naming `--root` as the remedy,
   because the first person to hit it will otherwise conclude the sandbox is
   broken (**dogfooding is a gift; fix the tool**).
