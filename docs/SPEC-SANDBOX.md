# nova-sandbox — specification

`nova-sandbox` is one binary at the **launch layer**. It runs one command with
its filesystem reach cut down by the operating system: the command may read the
OS and toolchain roots, it may read the directories the caller named with
`--read`, it may read and write the directories the caller named with
`--write`, and everything else on disk is denied to it by the kernel.

```
nova-sandbox --read <dir>... --write <dir>... [--net-deny] [--net-listen] -- <command> <args...>
```

This spec is normative. If the code and this document disagree, one of them has
a bug, and the tests decide which. It stands beside [SPEC.md](SPEC.md), whose
**Conventions** section — no guessed paths, the one-line output grammar, the
cap-and-count rule, `internal/oneline` and `internal/bounded` — applies here
unchanged and is not restated. The one deliberate departure from it is the exit
grammar of the exec verb, and that departure has its own section and its reason.

The tool contains commands that read untrusted input. It separates the read
and write sets and enforces them through the operating system, without a
dedicated user account. The bare wrapper uses `sandbox-exec` on macOS and
Landlock on Linux; other platforms refuse to run the command.

| the failure it closes | the rule that closes it |
|---|---|
| a worker with the bench's credentials can read `~/.ssh`, the `gh` config, the keychain and the shell history | rules 3, 4 |
| shared inputs get read **and write** reach because there is only one list | rules 3, 4 |
| forcing a dedicated OS user per line is an administrative burden nobody will carry | rule 2 |
| a sandbox that silently does nothing on a platform it does not support | rules 1, 11 |
| a credential file readable inside the wall | rules 6, 10 |
| `/tmp` on macOS is a symlink to `/private/tmp`, and a policy written against the unresolved path grants nothing | rule 5 |
| a deny-by-default policy makes the inherited temp directory unwritable and half a toolchain dies on its first scratch file | the tool creates its own temp directory under the **first** `--write` |
| The harness's `external_directory` is relative to the harness cwd, so a job directory that is not the cwd is "external" to itself | rule 13 |
| 120 native cards each download the Go toolchain and every module into their own data home, up to 5 GB per slot, and the runners fill their disk | an explicitly named shared cache directory in the write set |
| the wall stands and the job's first `git status` dies on `~/.gitconfig`, which reads as a broken sandbox | the caller sets `HOME` to the per-job data home, and a `HOME` outside both lists is a refusal |

## The rules, numbered

Every rule here is normative. Rule and test numbers are stable identifiers.
The test requirements are listed under **Tests this spec demands**.

1. **OS-enforced or refused.** There is one Go function,
   `sandbox.Run`, with two implemented backends behind build tags:
   `sandbox-exec` on `darwin` and Landlock on `linux`. On both, the tool
   waits and returns the command's status. On `linux` it restricts **itself**
   first and starts the command afterwards, so the tool is inside the wall it
   applied while it waits, as the Linux section describes. If the platform's backend is not available at run time — no
   Landlock in the running kernel, no `sandbox-exec` on `PATH` and none at
   `/usr/bin/sandbox-exec`, or a platform without an implemented backend — the tool prints `SANDBOX REFUSED
   reason=no_sandbox` and **the command does not run**. A backend that is
   present but cannot apply the policy is `reason=sandbox_failed` and is equally
   fatal. There is no fallback, no degraded mode, and no partial wall.
2. **No dedicated users, no root, no admin, no VM.** Every backend chosen here
   is usable by an ordinary unprivileged user in their own session. A design
   that needs `sudo`, a second login account, a container runtime or a Hyper-V
   feature is out of scope for this tool, and the reasons the obvious ones are
   not chosen are in **what it deliberately does not do**.
3. **Deny by default; read the roots and the read set; write only the write
   set.** The policy denies filesystem access, then grants: **read** on the OS
   and toolchain roots (the platform lists are below, and they are data, not
   code); **read** on each `--read` directory and everything beneath it;
   **read and write** on each `--write` directory and everything beneath it.
   Nothing else is reachable. In particular `~/.ssh`, the `gh` configuration
   directory, the login keychain (`~/Library/Keychains`) and the shell history
   (`~/.zsh_history`, `~/.bash_history`) are outside every root list, and a
   caller that adds one back has done so in its own argv.
4. **Every list is explicit and is never guessed.** `--read <dir>`,
   `--read-noexec <dir>` and `--write <dir>` are each repeatable and have **no
   default**. Zero `--write`
   is exit 125 and `refusing to guess`: a command with no writable directory is
   a misconfiguration, not a tighter sandbox. Zero `--read` is legal — the
   roots are the floor. A `--write` path is readable as well as writable; a
   path given to both is a refusal naming both flags, not a silent merge.
   **`--read` CARRIES EXECUTE and `--read-noexec` does not**: landlock's read
   subset is `EXECUTE|READ_FILE|READ_DIR` and the darwin profile grants
   `process-exec*` globally, so under `--read` a program anywhere under the
   root runs. `--read-noexec` is the same read grant with the execute taken
   back — on darwin a last-wins `deny process-exec*` emitted after the global
   grant, on linux the read subset minus `fsExecute` — and it is what a cache
   or a data tree this user can write to is named with: a module cache, a
   `node_modules/.bin`, a `pip --user` tree. A path in both read lists, or in
   `--read-noexec` and `--write`, is a refusal naming both flags: one asks for
   execute and the other takes it away, and `--write` carries both. A
   default write set would be a guess about somebody else's job. **The two
   named exceptions**, and there are no others: the tool puts the temp directory
   under the **first** `--write` and defaults the `--cwd` to the
   **first** `--write`. Neither is a guess about the *lists* — the caller gave
   both paths — and both are stated here so that "never guessed" and "the first
   `--write`" stop contradicting each other. The order of `--write` flags is
   therefore meaningful and the callers below pass the job directory first.
5. **Paths are resolved, absolute and existing.** Each `--read`, each
   `--read-noexec`, each `--write`, the `--cwd`, the `--tmp` and each root is resolved with
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
   file **before** the wrap and passes the value by environment: the key is
   read as data, never sourced, never an argument.
   `nova-sandbox` never reads a credential, never names one in a printed line,
   and never writes one to a file. The file itself stays outside every named
   path, so the wrapped command cannot read it even if it is told to.
   `probe --secret <path>` names the file by path — a path is not a secret —
   and a `--secret` that resolves inside any `--read` or `--write` is exit 2
   and `reason=secret_inside_allow`, because that is a misconfiguration, not a
   failed probe.
7. **Network: no promise by default, and `--net-deny` refuses where it cannot
   be enforced.** Without `--net-deny` the tool makes **no promise** about the
   network: the policy allows **IP** where the backend needs an explicit grant
   (`(allow network-outbound (remote ip))` on darwin), and the line says
   `net=nopromise`. Darwin's broader `network*` grant would also allow
   unix-domain sockets outside the named paths, including agent sockets.
   Measured on macOS: under `(allow network*)` a connect to a
   socket one directory outside the write set succeeds `rc=0`; under
   `(allow network-outbound (remote ip))` the same connect is `rc=1` inside
   the wall and `rc=0` outside it. **Unix-domain sockets are reachable only
   under the write set**: the generator emits one
   `(allow network-outbound (subpath (param "WRITEn")))` per `--write`, so the
   job's own socket (a language server, a test harness) connects and nothing
   else does. **Inbound is not granted at all** unless the caller asks with
   `--net-listen`, which emits `(allow network-inbound (local ip))` and
   nothing wider; a job that does not listen cannot be listened to.
   (`tools/sandboxcheck`, checks `unix_socket_outside`,
   `unix_socket_outside_control` and `unix_socket_inside`.)
   **The loopback is opened by name with `--net-allow <host:port>`.** The
   no-promise grant `(allow network-outbound (remote ip))` reaches remote IP
   only, not `127.0.0.1`, so a job that must reach a keyless local provider
   (ollama on `localhost`) dies silently without its host:port named.
   `--net-allow` emits one `(allow network-outbound (remote ip "localhost:<p>"))`
   per entry — the one port, nothing wider — and Build refuses an entry whose
   host is not the machine's own loopback (`reason=bad_net` at exit 125) before
   a profile is ever generated. THE FORM MATTERS: the nested
   `(local ip (host "<h>") (port "<p>"))` this section named before is not SBPL
   sandbox-exec accepts at all — measured on darwin 27.2, it aborts the whole
   profile with `unbound variable: host` at exit 65, before the child ever
   runs. `remote ip`'s own host slot additionally accepts only the literal
   `localhost` or `*`, never a numeric address (`host must be * or localhost
   in network address`), which is why the host is confirmed loopback in Build
   and always written as the literal `localhost` here — measured,
   `"localhost:<p>"` admits both a `127.0.0.1` and a `::1` listener on that
   port.
   **One unix socket is granted by literal, and it is DNS.** macOS does not
   resolve names over IP from the process: it asks `mDNSResponder` over the
   unix socket `/private/var/run/mDNSResponder`, so IP-only outbound is a wall
   with a working network and no name resolution — measured on macOS, `curl https://example.com` inside the wall is `rc=6`, `http_code=000`,
   and `nslookup` is `bind: Operation not permitted`; with
   `(allow network-outbound (remote ip) (literal "/private/var/run/mDNSResponder"))`
   the same curl is `200`. Every wrapped worker would otherwise fail its first
   request while the `SANDBOX OK` line said `net=nopromise`, which is the
   silent sandbox this spec forbids. The literal is emitted **inside** the network
   marker, so `--net-deny` takes the resolver away with the network.
   (`tools/sandboxcheck`, checks `dns_resolves` and
   `dns_resolves_control`: the same profile with the literal removed does not
   resolve, so the check cannot pass by the socket being irrelevant.)
   **`mach-lookup` is narrowed to three services, measured.** An unqualified
   `(allow mach-lookup)` allows `pbpaste` to read the clipboard inside the
   wall. The bounded set supports
   `/bin/sh -c true`, `git status`, `curl https://example.com`,
   `opencode --version` and `node -e 1`:

   ```
   (allow mach-lookup
     (global-name "com.apple.system.opendirectoryd.libinfo")   ; getpwuid, getaddrinfo
     (global-name "com.apple.SecurityServer")                  ; TLS trust evaluation
     (global-name "com.apple.system.logger"))                  ; os_log
   ```

   All five pass under it and `pbpaste` is `rc=1`
   (`tools/sandboxcheck`, check `clipboard_denied`). **Accepted width,
   named rather than removed:** under this narrowed set `launchctl print
   system` still answers and `security list-keychains` still lists the keychain
   **file names** — neither reads a secret, and both were measured to still
   answer. `osascript` evaluates a local expression; Apple Events are denied at
   every width. A service a future harness needs is added to this list by
   measurement, never by widening to the unqualified form.
   With `--net-deny` the caller is asking
   for an enforced denial, and the tool either delivers it or refuses to run:
   on `darwin` the grant is withheld and the line says
   `net=denied`; on `linux` below Landlock **ABI 4** (kernel 6.7) the tool
   prints `SANDBOX REFUSED reason=net_unenforceable` and **the command does not
   run**. The named workaround is to drop `--net-deny` and take
   `net=nopromise`, with the filesystem wall still standing — and the
   loudness of it is **`net=nopromise` on the `SANDBOX OK` line**, printed
   before the command starts and in every log that holds the run. An absent
   flag is not loud in the argv; the word on the line is what a reader sees,
   and it is the same word whether the caller never wanted a denial or gave
   one up. There is no `SANDBOX NOTE` that proceeds with a weaker wall than the
   caller asked for — that is the silent sandbox this spec exists to prevent.
   Landlock restricts only TCP `bind`/`connect` even at ABI 4; UDP is not
   restricted at any ABI, and `net=denied` on linux means exactly TCP.
8. **Temp is inside the wall.** The tool creates
   `<first --write>/.nova-sandbox-tmp` if it does not exist — the one
   directory the tool creates, inside the write set, its name chosen by the
   tool and never by a caller; **what it deliberately does not do** is about
   the paths it is *handed* — and sets `TMPDIR`, `TMP` and `TEMP` to that
   directory. It also sets zsh's `TMPPREFIX` to `<temp>/zsh`: this is a
   **prefix** for zsh-created temporary files, not another directory. A
   deny-by-default policy makes the inherited per-user temp directory
   unwritable, and a toolchain whose first scratch write fails looks like a
   broken sandbox rather than a working one. `--tmp <dir>` overrides the temp
   directory and must resolve inside a `--write` path.
9. **The environment passes through minus the agent, and the caller points the
   child's home into the write set.** This is not a secrets tool: the child
   inherits the caller's environment, minus the three temp-directory variables
   and zsh's temp-file prefix that the tool sets, and minus **this exact set,
   by name**:

   ```
   SSH_AUTH_SOCK
   SSH_AGENT_*                      (SSH_AGENT_PID, SSH_AGENT_LAUNCHER, ...)
   GPG_AGENT_INFO
   *_AGENT_PID   *_AGENT_INFO   *_AGENT_SOCK
   ```

   Each of those names an **address of, or a handle on, a running agent**.
   `AI_AGENT` and `CLAUDE_AGENT_SDK_VERSION` name what is running the job and
   pass through. When the scrub removes anything, it prints before the command
   starts: `SANDBOX NOTE dropped from the child's environment: <names>; an agent
   socket speaks for a key the wall denies`. `<names>` lists exactly the
   variables removed by the set above.
   The scrub is the second half of the network policy: the wall denies
   the agent's *socket* and the scrub removes the *address* of it, so a
   command that would otherwise sign a push with a key it cannot read has
   neither half. It is by **exclusion**, never an allow-list, because the caller's
   credential must still arrive: the tool drops the names it knows are agents
   and passes everything else through untouched. The evidence is a **Go test**, not
   the check: `internal/sandbox/policy_test.go`'s `TestChildEnv` and
   `TestScrubSetIsExactlyTheSpecs` (test 27(c)) plant `SSH_AUTH_SOCK`,
   `SSH_AGENT_PID`, `GPG_AGENT_INFO`, `PODMAN_AGENT_SOCK`, `AI_AGENT`,
   `CLAUDE_AGENT_SDK_VERSION` and `FOO_TOKEN` and assert the exact set, and
   `cmd/nova-sandbox`'s `TestTheNoteNamesExactlyWhatWasDropped` asserts the same
   thing end to end through the binary. `tools/sandboxcheck`'s
   `env_no_ssh_auth_sock` builds the child environment by its **own** filter
   before `sandbox-exec` runs, so it can only agree with itself. The credential
   the caller deliberately passed by environment must arrive.

   **A credential passed by environment reaches the child.** The caller owns
   any further filtering before that child starts a tool subprocess; the
   sandbox does not infer which environment variables are credentials.

   **And the wall cannot finish the job, for a reason this rule's own
   `/proc` note already measured.** On linux the child can read its PARENT's
   environment through `/proc/<pid>/environ` — same uid, and Yama's
   `ptrace_scope` does not apply to `PTRACE_MODE_READ` — so the harness's key
   is reachable whatever the child's own environment holds (measured inside
   the wall on Linux: the read succeeds and carries one
   secret-named entry; the probe reported a yes/no and a count, never a value).
   The read roots below name `/proc` and not `/proc/self` **because a
   `/proc/self` opened `O_PATH` resolves to the pid that opened it**, which is
   the same fact from the other side: Landlock's rules are inode-based and
   resolved when the ruleset is built, before the descendants' pids exist, and
   it has no "the directory whose name is my own pid". The wall may therefore
   allow all of `/proc` or none of it, and none of it kills every toolchain a
   card runs. **Landlock cannot path-restrict procfs by pid**, so this is not
   a wall defect and no wall change closes it; the closures are `hidepid=2` on
   the bench or a harness that takes its credential by something other than
   the environment. But an inherited `HOME` names a directory that is in no list and
   is therefore denied, and almost every tool a worker runs derives a path
   from it. Measured on this Mac under the profile below: with the caller's
   `HOME` inherited, `git -C <jobdir>/repo status` is `fatal: unable to
   access '/Users/<user>/.gitconfig': Operation not permitted`, so a
   `tree: yes` job cannot run its first git command; a harness that writes
   `~/.config/opencode` and `~/.local/share/opencode` dies the same way.
   **The caller therefore sets `HOME` to the per-job data home, and that
   directory must be inside a `--write`** — a `--read` is not enough: under the profile with
   `HOME` inside a `--read` path, `git status` exits 0 and the first config
   write is `Operation not permitted`, which is exactly the harness death two
   paragraphs up, moved later in the run and made harder to read. With `HOME`
   inside a `--write` the same `git status` exits 0 and the config write lands
   (measured, `tools/sandboxcheck`, check `home_config_write`).
   `HOME` rather than the XDG quartet (`XDG_CONFIG_HOME`, `XDG_DATA_HOME`,
   `XDG_CACHE_HOME` plus `GIT_CONFIG_GLOBAL`) because one variable covers
   every home-derived path a tool invents — `~/.gitconfig`, `~/.ssh`,
   `~/.npm`, `~/.cache`, and macOS's `~/Library/Application Support`, which
   no XDG variable reaches — while the quartet covers only the tools that
   honour it and must grow a name every time a toolchain invents one. (The
   quartet was measured too: `GIT_CONFIG_GLOBAL` + `XDG_CONFIG_HOME` fixes
   git. It fixes git.) The tool does not set `HOME` itself: guessing
   which write path is a data home is forbidden (the spec names its only two
   exceptions, and this is not one of them). It does **check**: a run whose
   `HOME` resolves outside every `--write` path is
   `SANDBOX REFUSED reason=home_outside` at exit 125 and the command does not
   run, because a wall that lets the job start and kills its first git
   command is the silent sandbox this spec exists to prevent. The refusal (and
   `probe`'s and `policy`'s) ends with the command that answers it, the same
   invocation with a data home made inside the first `--write`:
   `run: mkdir -p <first --write>/home && HOME=<first --write>/home nova-sandbox <the same arguments>`.
   That is a line for the caller to paste, not a default: nothing is set.
10. **The probe proves the wall before the work runs.** `nova-sandbox probe
    --write <dir> [--read <dir>...] [--secret <path>]` runs five checks under the
    real policy for this platform: the control write outside the wall must
    succeed; a write **outside** every named path must fail; a read of the
    named secret file must fail; a write **inside** the write set must
    succeed; and a **read of the probe's own executable** must succeed. Any
    check that comes back the wrong way is `PROBE REFUSED` at exit 1 naming
    the check. The last two are not decoration: a wall that denies the work
    too is broken, and a two-check probe would call it a pass. **`--secret` is
    optional.** A caller whose key is delivered by `nova-secrets
    exec` into the environment has **no key file** for the wall to protect —
    the key is never a file on disk — so the probe runs its other four checks
    and no `read_secret` step is invented; the `secret_inside_allow` check of
    rule 6 is skipped for the same reason. **The probe
    takes no command**, and that is why the last check reads the probe's own
    binary: `probe` re-executes `os.Executable()` under the policy it just
    generated, so the resolved command of that wrapped run is `nova-sandbox`
    itself, its directory is the root "the directory of the resolved command"
    by construction, and the file is certain to exist and be readable on all
    three platforms with no `PATH` lookup, no caller command and no guessed
    path. The re-exec is the probe's alone — the exec verb never re-execs
    (rule 12) — and the child is the same binary with an internal verb, never
    a shell.
11. **This tool has no way to run a command unwalled.** There is no
    `--no-sandbox`, no environment variable and no config file: a
    `nova-sandbox --no-sandbox` is `SANDBOX REFUSED reason=bad_flag:
    unknown flag --no-sandbox; the flags are --read, ...; run: nova-sandbox help` at
    exit 125, like any other flag the tool does not have. A wall this tool cannot build is a refusal (rule 1),
    and it stays a refusal — a tool whose whole reason is containment does not
    ship the switch that turns containment off.
12. **The exec verb is transparent, and what happens to the tool's own process
    is stated per platform.** Everything after `--` is executed verbatim —
    **never** through a shell, so no argument is re-parsed and no quote is
    re-interpreted. stdin, stdout and stderr are inherited unchanged — and the caller owns what
    they point at. **The rule is about stdout and stderr onto an outside
    path, and only that:** the caller's stdout and stderr for a wrapped
    command are either a pipe the caller drains, or a path inside the write
    set. Measured, a wrapped `/bin/cat` whose stdout is a file outside every
    named path is denied, while `/bin/echo` writing the same descriptor
    succeeds, because the two reach the file differently and only one of them
    is checked by the policy — so a write through an inherited descriptor is
    *unreliable*, not walled. **An inherited descriptor is not walled at all
    for reads**: under the darwin profile, `cat /dev/fd/9 9<secret` inside
    the wall **printed the secret**, because `/dev` is `file-read*` and
    `/dev/fd/9` re-opens the descriptor the caller already had. **The caller
    rule that follows: no descriptor onto a secret is held open across the
    exec.** The tool itself leaks none — every file it opens is `CLOEXEC` and
    only 0, 1 and 2 are passed — so this is a rule for launchers, and a
    launcher that reads a key file must close it before it wraps. The
    child's exit status is the tool's exit status, and a death by signal `N`
    gives exit `128+N`. Per platform:
    - **linux:** the tool restricts *itself* (`runtime.LockOSThread`,
      `landlock_restrict_self`) and then starts the command as a **child** and
      **waits**; `SIGINT` and `SIGTERM` are forwarded **to the child**, not to
      a process group. **The tool creates no process group of its own**, for
      the reason the darwin bullet gives at length below, and the wall reaches
      the child by Landlock's inheritance across `fork(2)`, not by identity:
      the command has a pid of its own, and the tool is inside the same wall
      while it waits and stays there (the Linux section's "`Run` is one-way").
      The tool waits so that it can forward signals and return the command's
      status, not to clean anything up: there is nothing to remove.
    - **darwin:** the tool spawns `sandbox-exec`, which applies the profile and
      `exec`s the command in place, and **waits**; `SIGINT` and `SIGTERM` are
      forwarded **to the child**, not to a process group. **The tool creates no
      process group of its own**: the wrapped tree stays in the caller's group,
      and the caller owns pgid and reaping. Keeping the process tree in that
      group lets the caller reap descendants at its deadline. On a tty the
      group-wide signal reaches the whole tree already, because the tree is in
      the caller's group. The tool waits so that it can forward signals and
      return the command's status, not to clean anything up: the profile is
      inline (`-p`).
    - **other platforms:** the bare wrapper refuses with `reason=no_sandbox`
      and does not start the command.

    The tool's own status lines go to **stderr**, so a wrapped command's stdout
    is its own.
13. **The working directory is inside the wall.** `--cwd <dir>` must resolve
    inside a `--write` path; its default is the first `--write`. OpenCode's
    `external_directory` permission is evaluated **relative to the harness's
    working directory**, so a job directory that is not the cwd is "external"
    to the harness that is supposed to be working in it, and the fence denies
    the job its own files. The caller passes the job directory as the first
    `--write` and as the cwd.
15. **The policy is generated and printable, never hand-edited.** The caller
    passes the two lists; the tool generates the policy text and, on a
    supported backend, the profile or ruleset that enforces it. The `policy` verb writes the generated policy to stdout and
    exits 0 without running anything. There is one spelling and no alias: a
    keyword before the flags and a flag among them would be two names for one
    thing. No profile is stored in the repository
    for editing, and the tool never accepts a caller-supplied profile file.
16. **Bounded output, and a refusal says what the input wants.** Every listing
    is a cap and a count per SPEC.md, `--max <n>` default 20, `0` for all, one
    MORE line naming the remedy. A refusal names the flag and the form it
    wants, reports every independent problem at once, and never prints the
    contents of a file it was handed.
17. **A shared cache is an explicit write path.** A caller may name a shared
    cache directory with `--write` beside the job directory and data home.
    Other jobs using that directory can see its writes. The caller sets any
    cache environment variables; `nova-sandbox` derives no cache path from task
    text, creates no default cache, and supplies no cache environment variable.

## The verbs

```
nova-sandbox --read <dir>... [--read-noexec <dir>...] --write <dir>... [--net-deny] [--net-listen] [--net-allow <host:port>]... [--cwd <dir>] [--tmp <dir>] [--name <container>] [--acl tool|caller] -- <command> <args...>
nova-sandbox probe   --write <dir>... [--read <dir>...] [--secret <path>] [--net-deny] [--max <n>] [--json]
nova-sandbox policy  --read <dir>... --write <dir>... [--net-deny] [--net-allow <host:port>]... [--cwd <dir>] [--json] [-- <command> <args...>]
nova-sandbox check   [--json]
nova-sandbox run     --name <n> --size <8g> [--timeout <30m>] [--go] [--read <dir>]... [--container <disk>] -- <command> <args...>
nova-sandbox reap    [--dry-run]
nova-sandbox worktree --repo <dir> --scratch <dir> --pr <id> [--base <branch>]
nova-sandbox worktree --repo <dir> --scratch <dir> --prune
nova-sandbox egress plan  --run <id> --policy <file> --model-host <host> --resolver <ip> [--bench-cidr <cidr>]... [--uid <n>] [--veth <if>] --out <file>
nova-sandbox egress apply --plan <file> --run <id>
nova-sandbox egress check --plan <file>
nova-sandbox egress drop  --run <id>
nova-sandbox version
nova-sandbox help
```

`probe` is rule 10 and is the verb a caller runs **once before the first task**,
not per task: it costs a process and it answers a question about the machine,
not about the job. A caller checks the probe result before starting work.

`policy` prints the generated policy for a read/write pair and runs nothing. It
is how a reader checks the wall without trusting this document. The command
after `--` is optional and is **not run**: it is there because one root is
computed from the command ("the directory of the resolved command"), so a
`policy` that always stood on `/bin/sh` could not print the one root a reader
most needs to see. With no `--`, `sh` is the floor every wrapped shell command
already stands on.

`check` reports what this machine can enforce — the backend, its version or
ABI, and whether an enforced network denial is available — and exits 0 whether
or not a sandbox is available, because it is a question, not an attempt. The
`hosts=none` field is a fixed fixture: this tool has no per-host wall rule, so
the token is always `none` and is printed only to keep the check line's shape
across platforms.

`check`, `policy` and `probe` take `--json`: the same result as one JSON object
on stdout, a refusal included (`{"result":{"verb","status","exit","remedy","why"},
"facts":{...},"items":[...],"notes":[...],"payload":"..."}`, the shape of
`internal/tool`'s output value). `policy`'s profile is the `payload`, `probe`'s
steps are `items` of kind `step`, and every typed line's fields are `facts`. The
bare form and `run` wrap a command whose output is its own, and refuse `--json`.

A flag that belongs to another verb is never silently dropped: the bare form
refuses `--secret`, `--max` and `--json`; `probe` refuses `--cwd`, `--tmp`,
`--name`, `--acl` and `--net-allow`, because a probe of a wall built without them
answers a different question; `policy` accepts `--secret`, `--max` and `--acl` and
prints one `POLICY NOTE` per flag saying it changed nothing. `<verb> -h` lists
each verb's flags with what each wants, and that verb's own exit codes.

The binary is `nova-sandbox`.

## The run verb — a disposable place, on darwin

```
nova-sandbox run --name <n> --size <8g> [--timeout <30m>] [--go] [--read <dir>]... [--container <disk>]
                 [--out <dir> [--artifact <relpath>]... [--out-max-bytes <64m>]] -- <command> <args...>
nova-sandbox run --help
```

The bare form gives a command a **wall**; `run` gives it a **place**, and
then takes the place away. On darwin the place is an APFS volume of its own in
the boot container: `diskutil apfs addVolume <container> APFS nova-<n> -quota
<size>`, mounted at `/Volumes/nova-<n>`.

What the verb does, in order, and there is no other order:

1. **Look.** The boot volume's APFS container is read from `diskutil info /`
   (or named by `--container`). A volume already called `nova-<n>` is a
   refusal — `reason=volume_exists` — because a run never joins a place it did
   not make, and it deletes the place on the way out.
2. **Create.** One volume, with the quota `--size` names. `--size` is
   **required**: a disposable place with no ceiling can fill the boot disk,
   which is the failure a disposable place exists to prevent. A volume that comes
   up with **no mount point** is a different failure from one that could not be
   made, and the refusal says which of the two it is: the volume was created, the
   mount was denied or never happened, and the volume has been deleted again. The
   usual cause is the **caller**, because a process that is itself inside an OS
   sandbox may not mount a volume, so the line names both remedies — a shell that
   is not sandboxed, or the bare wall form, which needs no volume at all. Both
   failures are `reason=volume_failed`.

   That cause has a **second face one step earlier**: under a seatbelt wall
   `diskutil` cannot reach DiskArbitration at all and fails every call with
   *"framework being unavailable due to being booted in single-user mode"*, which
   is neither what happened nor anywhere to look. Wherever that phrase is in a
   disk failure, the refusal carries the same cause-and-remedy sentence as the
   unmounted volume — one sentence, written once, so the faces cannot drift — and
   the container refusal (`reason=no_container`) drops its `--container` advice,
   because a container named by hand fails the same way one call later.
3. **Run.** The volume is the run's **only `--write`**, so the seatbelt profile
   of the darwin section allows writes there and nowhere else; rule 8's temp
   directory defaults inside it, which puts `TMPDIR` on the volume too. The
   working directory is `<volume>/work` and rule 9's `HOME` is `<volume>/home`,
   both made by the tool, both thrown away with the volume. `--read` passes
   through unchanged, so a shared toolchain or reference checkout is still read
   in place and never copied.
4. **Kill.** The command runs in a **process group of its own** — the one place
   this tool makes a group, and the reason is the volume: a forked child that
   outlives its parent holds the volume open, an open volume cannot be
   unmounted, and a survivor would turn a clean exit into a leak. The group is
   killed on every path out, including a clean one.
5. **Delete.** `diskutil apfs deleteVolume <disk>`, on a normal exit, an error,
   a signal or a `--timeout` alike. **Nothing of the run survives on the boot
   volume**, so there is no cleanup step to forget and no half-cleaned job
   directory for the next card to inherit.

One receipt per run, on stderr:

```
SANDBOX DONE name=<n> exit=<code> wall=<s> freed=<bytes>
```

`wall=` is the whole verb — look, create, run, delete — because that is what the
caller waited for. `freed=` is what the volume held when it was deleted, read
with one `statfs` before the delete.

**A delete that fails is never silent.** It prints

```
SANDBOX LEAK name=<n> volume=<disk> remedy="diskutil apfs deleteVolume <disk>"
```

and exits **3**, whatever the command's own status was: a caller that read `0`
would believe the machine was clean. A volume reported busy is unmounted with
force and the delete is tried once more before the leak is declared; the leak
line names the disk and the one command that removes it, so the remedy is a
line to run and not an investigation.

**`--timeout`** is a Go duration. When it passes, the group gets `SIGTERM`, then
`SIGKILL` if it is still there, the volume goes anyway, and the verb exits
**124** — `timeout(1)`'s status — because what the caller needs to know is that
the deadline ended the run, not which signal did it.

**No sudo.** `diskutil apfs addVolume` and `diskutil apfs deleteVolume` on the
boot container are the ordinary user's to run, measured on the Studio (macOS 26,
arm64): rule 2 holds here as it does everywhere else, and a verb
that needed root would be a different thing than the one measured.

**Every other platform REFUSES**, with `reason=no_sandbox` and one remedy line
naming the container path to use instead. On linux a card is already disposable
— it runs *inside its image*, and the image is the container — so the remedy is
`nova-sandbox --write <dir> -- <command>` with the card's image root as `<dir>`.
A `run` that quietly worked in an ordinary directory would leave exactly the
cleanup debt this verb abolishes, on the platform nobody was watching.

**What is tested, and how.** The disk is reached through one small interface
with a fake behind it, so the contract — create → run → **always** delete, a
leak reported, a duplicate name refused before anything is made, the process
group killed on a timeout — is unit-tested without touching a disk. Exactly one
real end-to-end test creates a real 64m volume, runs a command that writes a
file and sleeps, and asserts the volume is gone from `/Volumes` and from
`diskutil apfs list` afterwards; it is behind the `novadisk` build tag, because
eight CI runners share the Mac this repository is built on.

### `--go`, and every card that builds Go

```
nova-sandbox run --name card1 --size 8g --go -- /bin/sh -c 'cd repo && go build ./...'
```

`--go` adds the Go toolchain's own two roots to the read set, as `go env` reports
them: **`GOROOT`** and **`GOMODCACHE`**. Neither is a path the caller typed and
neither is guessed — both are asked of the toolchain that is actually on the
`PATH`, with `internal/goenv`'s cleaned environment, because a `GOFLAGS` inherited
from a Makefile can reshape a `go` command's output under the reader's feet. A
path `go env` names that is **not there** — an empty module cache on a machine
that has never downloaded a module — is skipped with a note, not refused: rule 5's
refusal-for-absence is about the paths the *caller* named.

Without it, a card names both by hand in every argv, which is a step that will be
forgotten. `--read $(go env GOROOT)` remains the manual equivalent.

### `SANDBOX DENIED` — the wall says what it refused

When a contained command exits **non-zero**, the tool asks the operating system
what it refused during the run and prints one line per path:

```
SANDBOX DENIED path=<p> op=<read|write> remedy="--read <dir>"
```

The remedy names a **directory**, because that is what the flags take: the path
itself when it is one, its parent when it is a file. Denials on paths **inside**
the allowed set are dropped — those are some other operation on a path the caller
already named, and a remedy naming a flag already in the argv sends a reader to
fix what is not broken. The list is capped at ten with one line standing for the
rest, and only `file-read*` and `file-write*` operations are reported: a
`mach-lookup` denial is real and no `--read` answers it. A run that exits **0**
asks nothing at all — the query costs a process, and a clean run has no question.

**What this can and cannot see, measured on macOS 26, arm64.** macOS *does* report seatbelt violations to the unified log, under
subsystem `com.apple.sandbox.reporting`, category `violation`, and the parser
reads that exact shape. It does **not** report them for a profile applied with
`sandbox-exec -p`: a denial produced by this tool is absent from `log show` at
every level, `--info` and `--debug` included, while other processes' violations
sit in the same window. The two ways to ask for them do not exist here either —
`(deny default (with report))` is refused by the compiler ("report modifier does
not apply to deny action") and `(trace "<file>")` aborts `sandbox-exec` with
SIGABRT, exit 134. So on this macOS these lines are usually silent, and a
`SANDBOX NOTE` naming the size of the allowed set is printed instead. The reader
is built and kept because it costs one bounded query on a run that already
failed, it is right wherever the OS does report, and the alternative is a tool
with no way at all to say what it denied.

The query is bounded at **two seconds** and its absence is silence, never an
error: measured, a `log show` for a three-second window took over ten seconds and
found nothing, and a card whose test suite fails would have paid that on every
run.

### Volume creation and cleanup

The disposable-volume implementation serializes creation and verifies cleanup.

1. **Two `diskutil apfs addVolume` may not run at once, so `Create` takes an
   inter-process lock.** Four concurrent runs: three of four, then four of four,
   died before their card ran with

   ```
   SANDBOX REFUSED reason=volume_failed: /Volumes/nova-conc-N/work could not be
   made on the disposable volume: mkdir ...: permission denied
   ```

   exit 125. The cause is **outside this tool**, and was isolated without it: an
   `addVolume` that runs while another one is running leaves the new volume's
   root `root:wheel drwxr-xr-x` instead of the caller's `glenn:staff
   drwxrwxr-x`, and it **does not settle** — still denied two seconds later.
   Uncontended, the root is the caller's and writable the instant `diskutil
   info` reports a mount point. The same four runs staggered twelve seconds
   apart all passed **with their execution overlapping**, so it is *creation*
   alone that cannot be shared, not the volumes and not the runs.

   The old `Create` returned as soon as `diskutil info` named a mount point,
   which assumed the answer to a question it never asked. It now takes an
   exclusive `flock` on one file under the **caller's own cache directory**
   (`os.UserCacheDir()/nova-sandbox/volume-create.lock` — never `/tmp` and never
   a path a contained command could write, because a lock anyone can write is a
   lock anyone can take), and after the mount it asks: is this root **mine**, and
   can I **write** it. There is no repair to apply — `chown` on another user's
   directory needs root, which rule 2 does not have — so a root that is not the
   caller's means the volume is **deleted and made again**, up to three times,
   and then the tool **refuses and names it** rather than letting the run die at
   `mkdir` with `permission denied` and no cause. The lock waits, with a
   deadline; a wait with no deadline is how a fleet ends up holding a file.

2. **A `SIGKILL`ed run leaks the volume *and* the process inside it, so there is
   a `reap` verb.** `run` deletes its volume on every path out it can take —
   clean, error, catchable signal, `--timeout` — and a delete that fails prints
   `SANDBOX LEAK`. `SIGKILL` is none of those: the tool is gone between one
   instruction and the next, so there is no path out and **no line is printed**.
   Both halves then leak. The volume stays mounted, and the contained command's
   own `sleep 60` is reparented to PID 1 **with its working directory on that
   volume**, which holds it open against every unmount — so it is not a leak a
   later `diskutil apfs deleteVolume` clears by itself. `check` says nothing
   about it: `check` asks what the backend can *enforce*, not what this machine
   is still *holding*.

   ```
   nova-sandbox reap [--dry-run]
   SANDBOX REAP volume=<n> procs=<n> deleted=<yes|no>
   SANDBOX REAP OK volumes=<n>
   ```

   `reap` lists every `nova-*` volume — the prefix is the whole of its authority,
   exactly as the run verb's delete is — finds the processes holding each one
   open, sends them `SIGTERM` and then `SIGKILL` after a short grace, and deletes
   the volume through the same delete path `run` uses. Exit **0** when the machine
   is clean and **3** when anything remained, which includes every `--dry-run`
   that found something: that is what makes `nova-sandbox reap --dry-run` a gate
   a card can end on. `--dry-run` prints and **touches nothing** — no signal, no
   delete.

   And the half without which the verb is unusable: `run` now writes
   `.nova-sandbox-owner` at its volume root, carrying the tool's **pid and the
   moment that process started**, and `reap` never takes a volume whose marker
   names a live run. Both fields, because a pid is a small number the operating
   system hands out again and a guard on the number alone would keep an orphan
   for as long as some unrelated process wore it. Every uncertainty resolves to
   *orphan* — no marker, an unreadable one, a pid that is gone — because the
   alternative is a volume kept forever, which is the leak the verb exists to
   end; the one exception is a pid that **is** alive whose start time cannot be
   read, where the process is real and only the evidence is missing. A reaper
   that cannot tell a working card from an orphan is a reaper nobody dares run,
   and a reaper nobody runs is the same as no reaper at all.

3. **A reaper is tested against the real listing, never an assumed one.**
   `diskutil apfs list` draws a tree. A trim set containing only `|`, `+`, `-`,
   `<` and a space misses the `>` that opens each volume record and can report
   `SANDBOX REAP OK volumes=0` while a volume remains. The record begins:

   ```
   |   +-> Volume disk3s7 6CD8025B-76B4-4336-918B-04FEE498F9BD
   ```

   Without trimming `>`, this becomes `> Volume disk3s7 …` and does not match
   a reader expecting `Volume`. **A
   reaper that reports a dirty machine clean is worse than no reaper**, so the
   listing is parsed against a fixture copied off the Studio verbatim — the tree
   characters are the whole point — and that fixture holds `Macintosh HD` one
   record above the leaked volume, so the test that proves the parser reads is the
   same test that proves it never returns a volume this tool did not make.

   Proved end to end afterwards, which is the only reason it was found at all: a
   run `SIGKILL`ed with `sleep 120` inside it left its volume mounted and three
   processes holding it open; `reap --dry-run` reported `procs=3 deleted=no` and
   exit 3 while touching nothing, and `reap` killed all three, deleted the volume
   and exited 0.

4. **A timeout is not a denial.** A run that hit its `--timeout` paid the bounded
   two-second seatbelt-denials query and was then told

   ```
   SANDBOX NOTE the command failed and this OS reported no seatbelt denials for
   it; ... add a --read, or --go
   ```

   Nothing had been refused. The command was still working when its deadline
   passed, and a hint pointing at the read set sends the reader to widen a wall
   that was never in the way — on this macOS, where the query finds nothing
   anyway (see above), it is two seconds spent to print a wrong remedy. The probe
   is skipped when the exit was the timeout kill, and the one true line is
   printed instead:

   ```
   SANDBOX TIMEOUT after=<d> name=<n>
   ```

### The handoff — what leaves the disposable place

A command must copy its intended results out before the disposable volume is
deleted. Scratch is removed with the volume.

**`--out <dir>`** is the door. After the command exits and **before** the volume
is deleted — there is exactly one place in the verb where both are true — the
named artifacts are copied to `<out>/<name>/`, and one line says what left:

```
SANDBOX OUT name=<n> files=<k> bytes=<b>
```

It is printed before `SANDBOX DONE`, because it happens before the delete.

**What leaves.** `RESULT.md`, `usage.tsv` and `repo.bundle`, each taken **if
present** — a read card writes no bundle and that is not a failure.
`--artifact <relpath>` replaces that set, repeatable, each path relative to the
card's working directory. An artifact the **caller named** and did not write is
a refusal, the same way rule 5 refuses a `--read` the caller named that is not
there; a default that is absent is skipped. A directory is taken whole, one row
per regular file, with its shape kept.

**Nothing escapes the volume.** A process the card started can outlive the
command: `supervise` kills the command's process group, and a `setsid()` child
is outside it. So the copy runs beside something that can rewrite any path on
the volume, and it reads the volume by descriptor, never by a path it checked
earlier. The volume's device is the mount point's own, read with `lstat`. The
mount is opened, then `work/` from it, then each component of each artifact
from its parent's descriptor with `openat` and `O_NOFOLLOW | O_NONBLOCK`, and
every descriptor is `fstat`'d before it is used: it is kept only when it is a
regular file or a directory **and** its device is the volume's. The bytes are
read from the descriptor that passed that check. So a file or a parent
directory swapped for a symlink fails its open, a FIFO swapped in is opened
without waiting and refused by its type, and a file on any other device is
refused however it was reached. An absolute path, a `..` element, a symlink
named as the artifact and anything that is not a file or a directory are all
refused, and the shape checks run as text before any filesystem call so the
refusal names the flag rather than an errno. Inside a directory artifact a
symlink, a device or a socket is skipped; an entry that changes type while the
handoff reads it is a refusal. The copy of each file writes at most what is left
of `--out-max-bytes`; a file that grew past that after it was measured is
refused, its partial copy is removed, and nothing past the cap is left in
`--out`. Files copied before the refusal stay in `<out>/<name>/`, and the
handoff prints `SANDBOX REFUSED reason=out_failed`.

**`--out-max-bytes`**, default `64m`. The whole set is **measured before a byte
is written** and refused over the cap. A handoff is a door, not a backup: a
truncated artifact is worse than none, and a card that wants to move gigabytes
wants a bundle or a different tool.

**A commit leaves as a bundle.** The documented way, and the card's own last
step:

```
git bundle create repo.bundle <branch>
```

One file, the complete history of that branch, and `git fetch ./repo.bundle
<branch>` on the other side.

**The status.** A handoff that fails after a command that **succeeded** turns the
run into `SANDBOX REFUSED reason=out_failed`, exit 125: a zero exit would tell
the caller the artifacts are in `--out` when they are not. A handoff that fails
after a command that already failed leaves that status alone — the command's own
failure is the more important truth, and it is almost always why there was
nothing to hand back. The volume is deleted either way.

**Checked before anything is made.** `--artifact` or `--out-max-bytes` without
`--out` is `reason=no_out`; a `--artifact` that is absolute, empty, `.` or
carries `..` is `reason=bad_artifact`; a `--out-max-bytes` that is not a positive
quantity is `reason=bad_out_max`. `--out` on **windows** is `reason=no_out`: the
windows half keeps its per-run scratch under `--scratch` and there is nothing to
copy off. A typo found after the card has run is worth nothing.

### `run --help`

`nova-sandbox run --help`, `-h` or `help` prints the verb's own usage on stdout
and exits **0**, before validating the flags needed to run a command.

## The egress verbs — the card's outbound wall, on linux

```
nova-sandbox egress plan  --run <id> --policy <file> --model-host <host> --resolver <ip> [--bench-cidr <cidr>]... [--uid <n>] [--veth <if>] --out <file>
nova-sandbox egress apply --plan <file> --run <id>
nova-sandbox egress check --plan <file>
nova-sandbox egress drop  --run <id>
```

Every other verb in this document says what a command may **read and write**.
These four say what it may **talk to**. The wall is
*nftables on the bench*, applied to the card's own traffic — **not an env list
the worker applies, because the worker is the adversary**, and **not
`--network=host`**, which would hand a card the bench's whole namespace.

**The allowlist is a file in git**: `infra/image/egress.txt`, one hostname per
line, `#` comments, **default deny**. A card reaches those names on **TCP 443**
and nothing else. Adding a name is **a PR to that file, reviewed by the security
lane — never a runtime flag**.

What a run allows, and it is the whole list:

| | |
|---|---|
| `github.com`, `api.github.com`, `objects.githubusercontent.com` | TCP 443, to the addresses they resolved to **at plan time**, pinned for the run |
| the **one** model host `--model-host` names | TCP 443, pinned the same way |
| the resolver `--resolver` names | **UDP 53 only** |
| everything else | dropped |

`plan` judges every flag and the policy file before it resolves a name, and a
plan refused there names every problem at once and resolves nothing, so a
mistyped invocation never waits on a resolver.

Denied outright, before any allow is considered: `169.254.169.254/32` (the
metadata address, **by name**, so a reader finds it without arithmetic), the rest
of `169.254.0.0/16`, `127.0.0.0/8` as a destination, `::1/128` and `fe80::/10`,
and every `--bench-cidr` — the other benches.

**Three silences in the page, read the safer way**, and said here because a
silence read the loose way is a hole:

1. **`--model-host` may only name a host the policy file already carries.** The
   page says an update is "a PR to `egress.txt` … Not a runtime flag", and a flag
   that could name *any* host would be exactly that flag. The file is the
   reviewed universe; the flag picks the one model host out of it for this run,
   so a file that grows a second model host does not widen any existing run.
2. **A pinned address inside a denied range refuses the whole plan**
   (`reason=bad_address`). The answer came from a resolver, the resolver is not
   ours, and a name that resolves to `127.0.0.1` or to a bench is a poisoned
   answer or a rebinding. Fail closed; the same holds for a `--resolver` that is
   itself inside a denied range (`reason=bad_resolver`), which would otherwise
   leave DNS silently dropped by a rule above it.
3. **Every rule is scoped to the card's own traffic** — `meta skuid <n>` for
   rootless podman's slirp/pasta, `iifname "<veth>"` for the forward path — and a
   plan with neither selector **refuses** (`reason=no_selector`). The chain's base
   policy stays `policy accept` and the **default deny is the bare selector
   `drop` at the bottom of the chain**: an unscoped `policy drop` in the output
   hook would firewall the bench itself, which is a worse failure than the one it
   prevents.

**The shape of a plan.** One table per run, `nova_egress_<run>`, denies first,
then the one DNS allow and the pinned TCP 443 allows, then the default deny:

```
table inet nova_egress_j1 {
	chain output {
		type filter hook output priority 0; policy accept;
		meta skuid 10001 ip daddr 169.254.169.254/32 drop
		meta skuid 10001 ip daddr 127.0.0.0/8 drop
		meta skuid 10001 ip daddr 10.1.0.0/24 drop
		meta skuid 10001 ip daddr 10.9.0.53 udp dport 53 accept
		meta skuid 10001 ip daddr 140.82.121.4 tcp dport 443 accept
		meta skuid 10001 drop
	}
}
```

**`check` is the test of the tests.** It parses a plan back — it does not trust
the renderer that wrote it — and asserts: exactly one `nova_egress_` table, every
chain based on `policy accept` in the output or forward hook, **every** rule
carrying that chain's selector, **every** `accept` naming ONE address (a prefix
is how a wall becomes a suggestion) and port 443/TCP or 53/UDP, the metadata
address denied by name, and the **last** rule of every chain the bare selector
`drop`. Anything the grammar does not cover is a refusal, not a shrug: a line
whose effect the audit cannot judge is a line nobody has checked. `plan` runs the
same audit over what it just rendered, and **`apply` runs it before nft ever sees
the file** — a plan that cannot pass it is never applied, whoever wrote it.

**A blocked destination.** The card is told in exactly one line on **its own
stdout**, and the run exits non-zero — fail closed, and **no retry to a different
host**:

```
EGRESS DENIED host=<name>
```

**Who calls what, and when.** The card runner, on the bench, around one
`podman run`: `egress plan` → `egress apply` → the run → `egress drop`, with the
drop on **every** path out, the way the `run` verb deletes its volume. `drop`
names one table — the one this tool made — and touches nothing else on the
bench's ruleset.

**`apply` and `drop` are linux's**, because nftables is: on darwin they refuse
with `reason=not_linux` and the refusal says where the outbound wall is there
instead — the seatbelt profile this binary already generates, with `--net-deny`
for a card that needs no network at all. **`plan` and `check` run everywhere**: a
plan is text and an audit is a read, so a reviewer on a Mac builds and checks the
ruleset a bench will apply. A bench with no `nft` refuses `reason=no_nft` with one
remedy line, and nothing is applied and nothing is dropped.

**No root, one binary.** The privileged step is `sudo -n nft …` — `-n` because a
card runner's shell has no tty and a password prompt there is a hang nobody sees.
It is the one command these verbs execute, behind one interface, which is why the
whole contract above is unit-tested with **no packet, no `nft` and no `sudo`**:
the resolver is a fake table and the privileged command is a recorder. Johnny's
page asks for exactly that ("unit test feeds a fake resolver + a fake connect"),
and the one real probe — `github.com:443` connects, `example.com:443` is denied —
is nightly, on a bench, never in this suite.

## The worktree verb

```
nova-sandbox worktree --repo <dir> --scratch <dir> --pr <id> [--base <branch>]
nova-sandbox worktree --repo <dir> --scratch <dir> --prune
```

`worktree` is not a wrapper and builds no wall: it uses SPEC.md's exit grammar
unchanged — **0** the verb ran, **1** `--prune` ran and removed nothing, **2**
could not run — so one review pass materialises a pull request's exact head in
a scratch tree of its own instead of every friend keeping a hand-rolled script.
`--repo <dir>` names the repository and `--scratch <dir>` the existing parent
under which the tool creates its own guid directory; both are required and
neither is guessed, because the tool names its own directory only inside a path
the caller gave. It reads the repository through `git` on `PATH` and the pull
request's head sha, merged-or-closed state and base branch through a **forge
client seam**; a token arrives by environment and is printed nowhere. It writes
`<scratch>/<guid>/`, the linked worktree inside it, and one record file
`<scratch>/<id>.pr` naming that guid so the next call finds the tree again.

`WORKTREE OK path=<dir> head=<sha>` is the one line an action prints. `path=`
is the worktree's absolute resolved directory and `head=` the full 40-hex sha
the pull request's head resolved to at this call. A second call for the same
`--pr` reads the record, fetches the head, and reuses the tree when it is
still there and clean — **same path, same line, no second `git worktree add`,
so no collision on the worktree lock**; only a tree that is gone, dirty, or at
a different head is rebuilt, and the rebuild is still one line. `--base
<branch>` is the branch compared against and written on the record; omitted,
the forge's own reported base for the pull request is used.

`--prune` removes each worktree this tool made whose pull request the forge
reports merged or closed, and each whose guid directory a fake-injectable clock
says is older than a day and a fake-injectable process probe says is in use by
no process, one line per removal, `WORKTREE REMOVED path=<dir>
reason=<pr_merged|pr_closed|stale>`, then one `WORKTREE OK removed=<n> kept=<n>`
and exit 0; it removes only trees named by its own record files, so a `git
worktree` the friends made by hand and a tree whose PR state is unknown are
kept, never deleted.

Every refusal is exit 2 with **one remedy line** and creates nothing, and one run
names every problem it finds, one `WORKTREE REFUSED` line each, before that remedy
line. An argument the verb has no flag for is `reason=bad_flag: unknown flag
--<x>; run: nova-sandbox help worktree`, never ignored. A missing,
non-numeric or zero `--pr`, or `--pr` together with `--prune`, is `WORKTREE
REFUSED reason=bad_pr: --pr wants one pull-request number and one mode`; a
`--repo` that is missing, relative, or not a git work tree is `reason=bad_repo:
--repo wants an existing repository named by an absolute path`; a `--scratch`
that is missing or not a directory is `reason=bad_scratch: --scratch wants an
existing directory and is not created`; a repository whose `origin` remote names
no owner and name is `reason=bad_origin: --repo wants an origin remote whose path
names <owner>/<name>`, and the line names the origin it read; each of the four
carries the remedy `run: nova-sandbox worktree --repo <dir> --scratch <dir> --pr
<id>`. The owner and name are read out of the remote's PATH and the host is read
by nobody, so an ssh `Host` alias standing where the forge's own name would is
one of the shapes that works; `gh` is what resolves the forge. A remote naming a
place on this machine — a `file://` URL, an absolute path, one beginning with `.`
or `..`, a windows drive letter — names no owner and name and is `bad_origin`,
because a bare push target's directories are not an owner and a repository. A pull request the
forge does not know is `reason=no_pr` and an unreachable forge is
`reason=no_forge`, each with the one remedy naming the flag and saying to retry
once the forge answers — `no_forge` is the forge's own silence and never an input
the forge was not asked about. The mistake it removes is abandoned scratch
worktrees and git lock collisions across review passes.

**Tests a card writes first.** Each runs in `t.TempDir()` with a fake in place
of every network, bench and clock, and no real forge or network is touched.

1. A fake forge answering head `<sha>` makes the verb print exactly `WORKTREE
   OK path=<tmp>/<guid> head=<sha>`, and the tree and its `.git` file exist.
2. A second call for the same `--pr`, with the fake forge and fake clock,
   reuses the first path, prints the same line, and adds no second entry to the
   fake `git worktree` call log.
3. A record whose tree the test deletes is rebuilt on the next call with
   exactly one `git worktree add`.
4. `--prune` with a fake forge reporting one PR merged and one closed prints
   one `WORKTREE REMOVED reason=pr_merged` and one `reason=pr_closed`, removes
   both trees, and leaves a third open PR's tree standing.
5. `--prune` with a fake clock one day and one minute past a tree's guid mtime
   removes it as `reason=stale` while one minute under a day is kept, and a
   stale tree the fake process probe reports in use is kept with no line.
6. `--prune` over a fake `git worktree list` holding a hand-made worktree no
   record names leaves it byte-identical and prints `removed=0 kept=<n>`.
7. No `--repo`, no `--scratch`, `--repo <tmp>/not-a-repo`, the relative `--repo
   .`, `--scratch <tmp>/absent`, `--pr 0`, `--pr abc` and `--pr --prune` are each
   exit 2 with one remedy line, the relative one in words that say an absolute
   path is what `--repo` wants, and the test asserts the absent scratch dir still
   does not exist.
8. A fake forge token in the environment appears on no line, scanned over every
   byte the verb wrote.
9. Owner and name are read out of `https://<host>/o/n.git`, the same without
   `.git`, the scp-like `git@<host>:o/n.git`, `ssh://git@<host>/o/n.git`, the same
   with a port, an ssh `Host` alias in place of the host, and `o/n` alone; a
   remote whose path names no owner and name yields nothing, and so does every
   local-path shape — `file:///tmp/x/o/n.git`, `../o/n.git`, `./o/n`,
   `/abs/path/o/n.git`, `C:/repos/o/n` and `C:\repos\o\n`.
10. A pathless `origin`, and a repository with no `origin` at all, are the bad
    origin failure and not the unreachable one, and the error names the origin
    read.
11. The forge seam's three failures refuse in their own words: `bad_origin`
    carries the plain remedy and says what it wants and what it read, while
    `no_pr` and `no_forge` carry the retry line.

## Exit codes

The exec verb cannot use SPEC.md's 0/1/2 grammar, because its exit status
belongs to the wrapped command: a tool that returned 2 for a bad flag would be
indistinguishable from a command that exited 2 on its own. It uses the
`env(1)` / `timeout(1)` convention instead, which reserves the top of the
range. This departure from the conventions preserves the child's exit status.

| code | meaning |
|------|---------|
| 0–124 | the wrapped command's own exit status, passed through unchanged |
| 3 | `run` only: the disposable volume could not be deleted — `SANDBOX LEAK`, naming the disk and the one command that removes it. It overrides the command's own status, because "nothing survives" is the whole contract and a caller that read `0` would believe the machine was clean |
| 124 | `run` only: `--timeout` passed, the whole process group was killed and the volume was deleted anyway — `timeout(1)`'s status |
| 125 | `nova-sandbox` itself said **NO** before the command ran: `SANDBOX REFUSED` — no backend (`reason=no_sandbox`), the policy could not be applied (`reason=sandbox_failed`), an enforced network denial that is not available (`reason=net_unenforceable`), a Landlock ABI below the first row of this tool's table (`reason=landlock_abi_unknown`; an ABI *above* the table is clamped, not refused), `--net-deny` and `--net-listen` together (`reason=bad_net`), no `--write` (`reason=bad_write`), a relative or missing path (`reason=bad_read` or `reason=bad_write`, whichever flag carried it), a path in both lists (`reason=bad_read`, naming both flags: the `--read` is the one that adds nothing, because a `--write` already carries read), a `--cwd` outside the write set, a `HOME` outside every `--write` (`reason=home_outside`), a command that is not executable (`reason=not_executable`), a missing `--` or nothing after it (`reason=no_command`), a flag the bare form does not have (`reason=bad_flag`, naming the flags it has); and on the `run` verb a `--name` that is not a volume name (`reason=no_name`), a `--size` that is not a quota (`reason=bad_size`), a `--timeout` that is not a positive duration (`reason=bad_timeout`), an APFS container that could not be read or named (`reason=no_container`), a volume of that name already on the machine (`reason=volume_exists`), a volume that could not be made, or that was made and not mounted (`reason=volume_failed`), and the handoff's own — `--artifact` or `--out-max-bytes` with no `--out`, or `--out` on windows (`reason=no_out`), an artifact path that is absolute, empty, `.` or carries `..` (`reason=bad_artifact`), a `--out-max-bytes` that is not a positive quantity (`reason=bad_out_max`), and a handoff that could not be completed after a command that exited 0 (`reason=out_failed`) |
| 126 | the command could not be executed **and the tool was still there to say so**: on `linux` the child could not be started inside the wall. On `darwin` the backend's own exec failure is 71 and the tool cannot see it — below |
| 127 | the command could not be resolved on the caller's `PATH`: `SANDBOX REFUSED reason=not_found`, printed like every other refusal of the tool's own |
| 128+N | the wrapped command was killed by signal `N` |

The reservation is ambiguous, as it is in `env(1)`: a wrapped command that
itself exits 125, 126 or 127 — **and on darwin 71** — is indistinguishable from
the tool's own refusal by exit status alone. The tool's refusals always print a
`SANDBOX REFUSED` line to stderr and the command's do not, and a status the
command returned is announced after it ends by `SANDBOX DONE exit=<n>`, the last
line the tool writes; so a caller that needs to tell them apart reads the line,
not the number. This is stated rather than fixed, because renumbering would
break the convention the rest of the table follows.

A bare `nova-sandbox`, with no arguments at all, wrapped nothing: it is
`SANDBOX REFUSED reason=no_command: no arguments; ...; run: nova-sandbox help`
at exit **2**, SPEC.md's "could not run", like the verbs below.

**Executability is checked before the wrap, not mapped after it.** Measured:
when `sandbox-exec` cannot exec the command under the profile it prints
`execvp() of '<cmd>' failed: Operation not permitted` and exits **71**. The
tool cannot distinguish that failure from a command that returns 71: `sandbox-exec` applies the profile and `exec`s in place, its stderr
is the caller's, and the tool sees the number 71 and nothing else —
`sandbox-exec -f p.sb -- /no/such` and `sh -c 'exit 71'` both exit 71
(measured), and no inspection of the status distinguishes them. The tool does
**pre-flight, outside the wall**: rule
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
on that failure: on linux
the tool is inside the wall it applied by the time the command runs, so it can
print no more than the command's own status (rule 12), which is what `SANDBOX
DONE exit=<n>` says and all it says, and a promise the tool
can keep on one platform and not the other two is worse than no promise. The
remedy is printed where it can be printed on all three — the usage banner and
the `--read` paragraph of the roots section — and a reader diagnosing a `126`
compares it with the same command run without the wrap.

The `probe`, `policy`, `check` and `egress` verbs are not wrappers and
use SPEC.md's grammar unchanged: **0** the verb ran and passed, **1** the verb ran
and said NO, **2** could not run (a missing flag, an unreadable path,
`--secret` inside a named path, bad invocation, unknown verb: `SANDBOX REFUSED reason=unknown_verb: unknown verb "<v>"; available: check, egress, policy, probe, reap, run, version, worktree; run: nova-sandbox help`). For the egress verbs the split
is: a plan whose invariants fail, and an `nft` that refused the ruleset, are
**1** — the verb ran and the answer is no; a flag that cannot be read, a policy
file that is not there, a plan that belongs to another run, a bench with no `nft`
and a platform with no nftables are **2**.

## Output grammar

Every line below goes to **stderr** except the body of `policy`, which is the
thing asked for, and the answers of the verbs that wrap nothing (`CHECK OK`,
`PROBE STEP`, `PROBE OK`, `WORKTREE OK`, `WORKTREE REMOVED`, the version line),
which go to stdout; under `--json` the one object is all a verb prints, on
stdout. A wrapped command's stdout is its own and the tool writes nothing there.

```
SANDBOX OK backend=<sandbox-exec|landlock> abi=<n|-> [used=<n>] read=<n> read-noexec=<n> write=<n> net=<denied|nopromise> cwd=<dir> cwdb64=<base64url> ancestors=<n> cmd=<name> gpu=<none|metal>
SANDBOX NOTE <the one remedy or gap line>   (always before the command starts)
SANDBOX REFUSED reason=<no_sandbox|sandbox_failed|net_unenforceable|landlock_abi_unknown|bad_read|bad_write|bad_cwd|bad_net|bad_gpu|bad_size|bad_timeout|home_outside|no_name|no_container|no_command|bad_flag|not_found|not_executable|volume_exists|volume_failed|unknown_verb>: <text>
SANDBOX STEP name=<container|look|create|delete|denials|list> state=<start|done> [ms=<n>]
SANDBOX DENIED path=<p> op=<read|write> remedy="--read <dir>"
SANDBOX TIMEOUT after=<d> name=<n>
SANDBOX DONE exit=<code> cmd=<name>   (the bare form: the last line, after the command ends)
SANDBOX DONE name=<n> exit=<code> wall=<s> freed=<bytes>
SANDBOX LEAK name=<n> volume=<disk> remedy="diskutil apfs deleteVolume <disk>"
SANDBOX REAP volume=<n> procs=<n> deleted=<yes|no>
SANDBOX REAP OK volumes=<n>
PROBE STEP name=<write_outside_control|write_outside|read_secret|write_inside|read_root> expect=<deny|allow> got=<deny|allow> path=<path>
PROBE OK backend=<name> abi=<n|-> steps=<n> passed=<n> net=<denied|nopromise> gpu=<none|metal>
PROBE REFUSED reason=<check|secret_inside_allow|probe_outside_inside|probe_outside_unwritable|no_sandbox|net_unenforceable>: <text>
POLICY OK backend=<name> read=<n> read-noexec=<n> write=<n> bytes=<n> gpu=<none|metal>
POLICY NOTE <flag> is <verb>'s flag and is ignored here: the policy printed is the same without it
POLICY REFUSED reason=<any reason of the SANDBOX REFUSED set above>: <text>
CHECK OK backend=<name|none> abi=<n|-> net=<enforceable|unenforceable> hosts=none note=<one clause|->
CHECK REFUSED reason=<bad_flag>: <text>
nova-sandbox <build identity> <goos>/<goarch> <go version> backend=<name> platform=<os>
EGRESS PLAN run=<id> allow=<n> deny=<n> names=<name,name,...>
EGRESS CHECK table=<nova_egress_<run>> chains=<n> rules=<n> allow=<n> deny=<n>
EGRESS OK verb=<apply|drop> run=<id> table=<nova_egress_<run>>
EGRESS STEP name=<resolve|apply|drop> state=<start|done> [ms=<n>]
EGRESS REFUSED reason=<bad_policy|bad_model_host|bad_resolver|bad_cidr|bad_address|bad_uid|bad_veth|bad_out|bad_plan|bad_table|bad_chain|bad_rule|no_name|no_selector|no_command|no_nft|not_linux|resolve_failed|plan_mismatch|allow_any|allow_port|unscoped_rule|no_default_deny|no_metadata_deny|nft_failed>: <text>
EGRESS DENIED host=<name>
```

`version` is SPEC.md's Conventions line, not a shape of its own: the four tokens
every binary in the set prints, and then this tool's two named extras. It used to
be `SANDBOX VERSION tool=… version=… backend=… platform=…`, which no reader of a
version line could take apart — `nova-version snapshot` could not inventory a bin
holding this binary at all (#1297). The backend and the platform a sandbox is
judged by are not lost; they are said in the grammar the whole set shares.

`SANDBOX OK` is printed **before** the command starts, so a log that ends in a
crash still says what the wall was; its `OK` says the wall is up and the command
is starting, never how the command ended. That is `SANDBOX DONE exit=<code>
cmd=<name>`, the bare form's last line, printed when the command has ended with
the status the tool then exits with: a reader that sees `SANDBOX OK` and then a
non-zero `SANDBOX DONE` knows the wall stood and the command failed, and a
refusal prints no `DONE` at all. It is printed after the command, which on
linux is from inside the wall: the tool writes to the stderr it already holds
and opens nothing. `SANDBOX OK` names `cmd=<name>` — the base name of
the executable — and never the arguments, because arguments carry task text and
task text carries quoted rules. The `cwd=<dir>` slot is a one-line field
rendered through `internal/oneline` like every other path, so a directory whose
path holds a space reaches a reader escaped; a consumer that compares it with a
path it holds decodes that field first.

**`SANDBOX OK` names the cwd twice.** `cwd=<dir>` is the readable rendering of
the working directory through `oneline.Field`, for the operator;
`cwdb64=<base64url>` is the machine-readable receipt, a strict base64url
encoding (RFC 4648 §5, no padding) of the raw path bytes the wall applied. A
reader that must match the cwd decodes `cwdb64=` and never the readable field:
oneline's escape is not injective (a literal backslash is not escaped), so the
readable spelling cannot be reversed to the bytes. A reader that finds `cwdb64=`
absent or not valid base64url treats the wall as one that cannot name its own
containment.

Every `SANDBOX NOTE` is printed **before** the command starts, for the reason
`SANDBOX OK` is: on linux the tool is inside the wall from the moment it is
applied, and the wall goes up before the command does. There is no note about a failure the command suffered inside the
wall, on any platform.

`net=nopromise` says the caller did not ask for network denial and the
tool is not implying one. There is no `net=unenforced`; a denial that cannot be
enforced is a refusal, not a word in a line.

**`EGRESS DENIED host=<name>` is the card's line, not the tool's**, and it is the
one place in this grammar where the line goes to **stdout** — the card's own,
where the worker's transcript is — because it is what the card is told when it
reaches for a destination the wall denies. Everything else the egress verbs print
is the bench's and goes to stderr like every other line here. A card that sees it
exits non-zero and does **not** try another host.

`EGRESS PLAN` is one plan's receipt: `allow=` and `deny=` are the accept and drop
rules the file actually holds (the default deny counted among the drops), and
`names=` is the allow set in order, so the receipt and the ruleset can be
compared without reading the ruleset. `EGRESS CHECK` is the same shape read back
out of a file by the audit.

`SANDBOX STEP`, `SANDBOX LEAK` and `SANDBOX DENIED` are the `run` verb's alone,
and `SANDBOX DONE` with `name=` is its form of the bare form's last line. `SANDBOX DENIED` is the one line this tool prints about a failure
the command suffered INSIDE the wall, and it is an exception to the sentence
above with a reason: on the `run` verb the tool is still there when the command
dies, because it owns the disposable volume and has to delete it. It is not a
`NOTE`, it names a path and an operation and a flag, and it is printed only on a
non-zero exit.
A `STEP` line is printed **before** the step it names and again when it is done,
for every step that takes longer than about a tenth of a second — making and
deleting an APFS volume each take seconds, and a caller staring at a silent
terminal cannot tell a slow `diskutil` from a hung one. There is exactly **one**
`SANDBOX DONE` per run that got as far as creating a volume, whatever happened
afterwards, and a run that leaked prints `freed=0` on it and the `SANDBOX LEAK`
line after it.

**The tool never prints a credential, a file's contents, or an argument
vector.** A refusal about a path prints the path, which the caller supplied.

## The two lists, and the roots

**The write set** is each `--write` and everything under it: read and write,
recursively. **The read set** is each `--read` and everything under it: read
only, recursively. Shared inputs — one reference checkout, a corpus, the specs,
the worker home with its `AGENTS.md` — belong in the read set, named once, so
that N workers read one copy.

**"Inside" is asked of the filesystem, not of a string prefix.** The one predicate
behind `--secret`, the outside path, `HOME`,
`--cwd`, `--tmp` and the command-directory home guard — and behind no
other question — asks `os.SameFile` of the path and its existing ancestors against
the directory, keeping the string prefix as the cheap first answer and, for a
directory that is not there to be asked, falling back to that prefix
case-insensitively only where a probe file written and removed in the nearest
directory that does exist measures a fold (a spelling that differs only in case is
one file on APFS and on NTFS, and the backend grants it: measured, a `--secret` at
`<base>/R/env` under a `--read` of `<base>/r` was READABLE inside the wall); that
names a mechanism and adds no rule.

**The roots** are what any command needs to run at all: read only, recursively
unless marked otherwise. They are a per-platform list in one data file in the
source, not a string built in three places, and `policy` prints them:

| platform | roots |
|---|---|
| darwin | `/`, `/etc`, `/tmp`, `/var` (each the directory or link itself, `(literal ...)`, not a subpath), `/var/db/xcode_select_link` and `/private/var/db/xcode_select_link` (literals on the Xcode-select link, not a subpath on `/private/var/db`), `/System`, `/usr`, `/bin`, `/sbin`, `/Library`, `/opt/homebrew`, `/opt/local`, `/private/etc`, `/private/var/select`, `/dev` (read), the directory of the resolved command, and the directory `/var/db/xcode_select_link` points at when it exists and is not already a root (`Xcode.app/Contents` when Xcode is selected, not `Contents/Developer`); plus **write** on `/dev/null` and `/dev/tty` |
| linux | `/usr`, `/bin`, `/sbin`, `/lib`, `/lib64`, `/etc`, `/run/systemd/resolve`, `/opt`, `/dev` (read), `/proc`, the directory of the resolved command, and the directory `/etc/resolv.conf` resolves to (its symlink target's parent: `/run/systemd/resolve` on a systemd machine, `/mnt/wsl` on WSL2); plus **write** on `/dev/null` and `/dev/tty` |
| windows | Policy data only: `%WINDIR%`, `%ProgramFiles%`, `%ProgramFiles(x86)%`, the directory of the resolved command. The bare wrapper refuses; no Windows containment is enforced. |

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

One measured consequence of the same shape: `/usr/bin/c++`,
`/usr/bin/cc` and `/usr/bin/git` on a Mac are Xcode shims that read
`/var/db/xcode_select_link`. `/var` is a literal on the symlink, not a
subpath, so without a literal on the link itself every C and C++ compile
inside the wall died with xcode-select's "unable to read data link" and a
worker read that as "no compiler installed". The profile grants the two
spellings of the link as literals — not a subpath on `/private/var/db`, which
holds host state the wall is not for — and OptionalRoots follows the link
outside the wall the way `--go` asks `go env`, adding the directory it points
at when that directory is not already a root (`CommandLineTools` sits under
`/Library`; `Xcode.app/Contents` does not). The grant is `Contents`, not
`Contents/Developer`: `xcode-select -p` prints Developer, and the shims also
stat `Info.plist` and load `SharedFrameworks` next to it — Developer alone is
"couldn't stat Xcode's Info.plist". Homebrew's `git` on `PATH` remains the
usual caller path; the shim no longer needs `--read /private/var/db`.

There is no `--root` flag. A toolchain installed into a user directory — Go
under `~/go`, node under `~/.nvm`, .NET under `~/.local`, a machine's
`/Users/<user>/toolchains` — is named with `--read`, which is exactly a
caller-supplied read-only root and needs no second spelling. On Windows,
`--read` is what makes the tool add a read-only ACE for the container SID. A
command that dies for want of an interpreter inside the wall and runs outside
it is a missing `--read`. The tool cannot say so after the fact — on linux it
is gone by then — so the sentence lives in the usage banner instead:
*a command that runs outside the wall and dies inside it is missing a
`--read`.*

On Linux the sandbox always reads the system roots the resolver needs;
a harness that cannot resolve a name inside the sandbox is a sandbox bug, not a
network one. Measured on Linux (Landlock ABI 4): inside nova-sandbox
the harness could not resolve DNS, because `/etc/resolv.conf` is a symlink into
`/run/systemd/resolve`, which the default read set did not include, so the
resolver runtime path was hidden and curl said `Could not resolve host`; adding
`/run/systemd/resolve` to `linuxReadRoots` fixed it, and curl got `http=200`.
The Linux backend therefore always reads the roots in `linuxReadRoots` in
`internal/sandbox/wrap_linux.go`,
applied by `addRules`, including the resolver runtime directory
`/run/systemd/resolve`, and it says so in the roots table above. They are part
of the one roots table, not a separate policy and not a caller switch: there is
no flag that turns them off. The `SANDBOX OK` line's `read=` count is the
caller's `--read` list and does not include these roots.

The same shape has a machine-chosen target, and a fixed row cannot name it:
measured on WSL2 (kernel 6.18.33.2), the distro's `/etc/resolv.conf`
is a symlink to `/mnt/wsl/resolv.conf`, `/mnt/wsl` is in no row above, and glibc
inside the wall had no nameserver — every lookup failed with `Could not resolve
host` while TCP by IP still worked. So `addRules` applies `linuxRoots`, not the
bare `linuxReadRoots` slice: it is the table above plus the directory
`/etc/resolv.conf` resolves to, read-only and skip-if-absent like every other
root. The containing directory is granted rather than the file, because WSL
rewrites the file and a rule on the prior inode would be left holding a path that
is no longer read.

The home directory is never a root — **including by way of the command**. One
root is computed rather than named, "the directory of the resolved command", and
a command placed in a home directory would hand the wall that whole home:
`~/x.sh` grants read on `~/.ssh`, the `gh` configuration and the login keychain,
while the `SANDBOX OK` line says `read=0` (measured, #73 at `1922f9d`). So the
tool **refuses** when the directory of the resolved command is the caller's home
directory — the passwd home, and `$HOME` as the tool inherited it — or an
ancestor of it, naming the directory and the home: "install the command in a
directory of its own". The refusal is `bad_read` and it happens before anything
runs. Two exemptions, both of them "a caller that adds one back has
done so in its own argv": a directory the caller named in its own `--read` or
`--write`, and a home that lies inside the caller's own lists, which is what the
job's data home always is.

## macOS — `sandbox-exec` with a generated profile

The wrap is `sandbox-exec -p <profile text> -D <name>=<value>... -- <command>
<args...>` — the profile is passed **inline**, never written to a file. A
profile file has to live somewhere the tool can write, which is inside the
write set, and a process already inside the write set can replace it between
the `WriteFile` and the `Start`; on a `SIGKILL` it is left behind. `-p` has no
file, no write-set entry, no race and no cleanup, and it is why the darwin body
has nothing to unlink when the command ends. `sandbox-exec(1)` is present on macOS 26, is marked deprecated, and
works today; it applies the profile and `exec`s the command in place, so it is
not a second process sitting between the tool and the command.

The profile text is **not in this document**. It ships in the repository as
`profiles/darwin.sb.tmpl`, and this section describes that file and the check
that measures it. A second copy of the profile can diverge from the generator.

**`profiles/darwin.sb.tmpl`** is Sandbox Profile Language (SBPL, a Scheme
dialect) and is the generator's only input. It carries the fixed clauses
verbatim and five markers, each documented in the file's own header, that the
generator replaces for one run: `@@OPTROOTS@@` (the optional roots that exist
on this machine), `@@ANCESTORS@@`, `@@READS@@`, `@@WRITES@@` and `@@NET@@`
(empty under `--net-deny`). Caller paths never enter the text: they arrive as
`-D NAME=<resolved path>` and are read back as `(param "READn")`,
`(param "WRITEn")` and `(param "HOME")`, so a directory with a quote or a paren
in its name cannot rewrite the policy: the file is a
template, never a policy, and nothing runs under it until the generator has
filled it for one run's two lists. `HOME` gets no grant of its own beyond the
`WRITEn` it must resolve inside; it is passed so that the profile
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
- The **ancestor `file-read-metadata` literals** make absolute paths
  traversable. Without them every
  absolute path into the write set fails at its leading components: measured,
  `git init $W/x` is `cannot mkdir: Operation not permitted`, `mkdir -p $W/a/b`
  is `mkdir: /private: Operation not permitted`, and `/bin/sh -c "cd $W"` is
  `Not a directory`, while the relative forms and `git status` succeed — a wall
  that passes a shallow test and kills the first second of a real job. The
  generator emits one literal per proper ancestor of every `--read`, `--write`,
  `--cwd`, `--tmp` path **and every OPTIONAL ROOT** (`/` excluded, it is granted
  above). `file-read-metadata` is `stat(2)` only: **listing** an ancestor stays
  denied, and so does writing anywhere outside the write set. The rule in one
  sentence: a generator grants **metadata on ancestors, never data** —
  `lstat`/`stat`/`access` resolve, and no read of an ancestor's contents is ever
  allowed.

  **Optional roots need ancestor metadata access too.** Measured on macOS:
  without that access, `go build` inside the wall fails with `go:
  cannot find GOROOT directory: 'go' binary is trimmed and GOROOT is not set`.
  The profile granted `(allow file-read* (subpath "/opt/homebrew"))`, so every
  *file* of the toolchain was readable; what was not readable was **`/opt`**.
  Homebrew builds `go` with `-trimpath`, so it finds `GOROOT` by resolving its own
  executable, `/opt/homebrew/bin/go` is a symlink into `../Cellar/...`, resolving
  it `lstat`s every leading component, and the `lstat` of `/opt` was denied. **A
  wall that grants a directory and denies the path TO it has granted nothing that
  a symlink must be followed to reach.** The card worked around it with `--read
  /opt/homebrew/Cellar/go/1.27.1`, which looks like a read grant and is really an
  ancestor grant — naming *any* path under `/opt` is what put `/opt` in the
  literals. That is a workaround every caller would have to carry, for a root the
  *tool* added and the caller never named, so it belongs in the generator. The
  grant stays `file-read-metadata`: `/opt` becomes traversable, never readable.

**What the wall costs, measured (2026-10-02).** A card is walled once:
`nova-swarm native` wraps the harness in one `nova-sandbox`, `sandbox-exec`
applies the profile and execs in place, and every process the harness starts
inherits the sandbox; nothing re-enters it. A card's filled profile is 50-60
rules (most of them the ancestor and PATH-directory metadata grants), not
hundreds. On two Intel Mac benches (8 cores at 3.2 GHz, 18 cores at 2.3 GHz) and
an Apple Silicon Mac, 500 execs of `/usr/bin/true`, a `git clone --shared` with
checkout, and a warm-cache `go build ./cmd/nova-sprint` cost the same, best of
five, inside this profile, inside a ten-rule profile of broad `subpath` grants,
inside `(allow default)` and outside any sandbox: the generated profile against
no sandbox was at most +7% (0.05 s on a clone), and the four walls showed no
consistent order between them. So the profile's shape is not the wall's cost,
and widening it to save time would trade a denial for nothing. A slow Intel
bench is slow in the kernel for every process, walled or not.

**`tools/sandboxcheck`** is how that file is known to be right. It is a Go
driver (`internal/sandbox/darwincheck`) around the real probes, and it fills the
embedded template for a scratch write set under the working directory (no
`/tmp`, any cwd), then runs inside the wall, by absolute path, the first
second of a real job: `cd`, `mkdir -p`, `git init`, `git clone --shared` of a
local repository, a config write under `HOME`, `cat /etc/hosts`,
`/bin/sh -c true`, `sleep 5 & kill $!`, stdout to a pipe the caller drains and
stdout to a file inside the write set. It then asserts the four denials — a
write outside every named path, a read of the named secret file, a listing of
an ancestor, and a connect to a unix-domain socket outside the write set —
**each with a control run outside the wall**, so that no denial can pass by
being impossible. It also asserts that a unix-domain socket **outside** the
write set cannot be connected to while the job's own socket **inside** it can,
and that the child environment holds none of the dropped set while
a caller variable beside them survives. One line per check,
`CHECK OK name=...` / `CHECK FAIL name=...`, exit 1 on any FAIL. **The count is
the check's own** and no number is stated here: a document that named one would
be wrong the first time a check was added. The recorded macOS 26.6.2 arm64
measurement has every check `OK`. A platform claim requires the check to run
on that platform, with its output included in the commit. It removes only what
it made: a scratch directory handed to it keeps whatever else it holds.

The check's child-environment filter is the **check's**, so it can only agree
with itself: what it measures is the profile, not the tool's scrub. The scrub is
asserted in Go, and the spec names those tests rather than this check.

The check takes four options for a caller that is a **test** rather than an
operator, each a flag and each read from an environment variable when the flag
is absent, because test 16 is absolute — no test reaches outside `t.TempDir()`
or touches the network. `--scratch` (`NOVA_CHECK_SCRATCH`) puts the scratch tree
where the caller says instead of under the working directory; `--no-network`
(`NOVA_CHECK_NO_NETWORK=1`) skips the two DNS checks, which are the only ones
that leave the machine, and prints `CHECK SKIP name=... reason=no_network` for
each; `--dump-profile` (`NOVA_CHECK_DUMP_PROFILE=1`) prints the filled profile
and exits 0 before any check runs, so a test can compare the hand filler with
the tool generator; `--fill BIN` (`NOVA_SANDBOX_FILL`) judges the profile a
`nova-sandbox` binary generates for the same write set instead of the hand-filled
one, so a drift between the two fillings is a named FAIL. The operator run and
the mac CI job set none of them: there the DNS checks are the rule-7
measurement and they run.

A third thing the check measured, small and load-bearing: `sun_path` is **104
bytes**, and a socket bound by absolute path under a deep scratch directory
silently fails to bind — which would make `unix_socket_outside` pass because
nothing was listening, the exact shape of failure the controls exist to catch.
The check binds and connects by **relative** path with the cwd set, and treats
a socket that did not appear within a bounded wait as a FAIL, not a pass.

Two things the check measured that the rules above now carry. The **cwd** is
load-bearing beyond the fence argument: with a cwd outside every named
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

The filled profile is **never written to a file**: it is handed to
`sandbox-exec` inline with `-p`, so no profile text lands in the
write set and there is nothing to remove when the command ends. The darwin body
waits rather than `exec`s in order to forward signals and return the command's
status, not to clean anything up.

## Linux — Landlock, no root

The Linux body is `internal/sandbox/wrap_linux.go` and
`internal/sandbox/landlock_linux.go`. It restricts the current process before
starting the child. Its limits are listed at the end of this section.

Landlock is an LSM available from kernel **5.13**, usable by an unprivileged
process, and inherited across `execve(2)` so that the child cannot lift it. The
three syscalls are `landlock_create_ruleset(2)`, `landlock_add_rule(2)` and
`landlock_restrict_self(2)`; `prctl(PR_SET_NO_NEW_PRIVS, 1)` must succeed
first, or `landlock_restrict_self` fails with `EPERM`.

**There is no pre-exec hook in Go.** `os/exec` has no `PreExec` callback and
`SysProcAttr` carries no user code, so the restriction cannot be applied
"in the child between fork and exec" from Go. The body is therefore
**restrict-then-fork**: the ruleset is applied to the tool's **own** process,
and `fork(2)` is what carries it to the command:

1. `landlock_create_ruleset` with `handled_access_fs` covering every
   filesystem access **the ABIs in the table below define** — stated per ABI,
   because an access left unhandled is an access the kernel does not check,
   and that is a hole with no line in any log:

   | ABI (kernel) | `handled_access_fs` |
   |---|---|
   | 1 (5.13) | `EXECUTE`, `WRITE_FILE`, `READ_FILE`, `READ_DIR`, `REMOVE_DIR`, `REMOVE_FILE`, `MAKE_CHAR`, `MAKE_DIR`, `MAKE_REG`, `MAKE_SOCK`, `MAKE_FIFO`, `MAKE_BLOCK`, `MAKE_SYM` |
   | 2 (5.19) | + `REFER` |
   | 3 (6.2) | + `TRUNCATE` |
   | 4 (6.7) | + nothing (adds the network rule type) |
   | 5 (6.10) | + `IOCTL_DEV` |
   | 6 (6.12) | + nothing (adds the two scopes) |

   **The table ends at ABI 6, and the sentence above is true only of the table.**
   A newer kernel can define access rights the tool does not yet handle.
   **A discovered ABI
   greater than the highest row is CLAMPED to that row — the ruleset is built
   at the table's maximum, the command runs, and the `SANDBOX OK` line carries
   the kernel's number on `abi=` and the wall's on `used=` — preceded by a
   `SANDBOX NOTE` naming both numbers and the word `clamped`.** A discovered
   ABI **below the first row** has no row to clamp to and is
   `SANDBOX REFUSED reason=landlock_abi_unknown` at exit 125, naming the
   discovered number and the lowest the tool knows, with the command not run;
   so is no Landlock at all (`reason=no_sandbox`). This tool has no
   workaround for those two. A backend must be available and its
   ABI supported before the command can run.

   **Why the clamp is valid.** A newer Landlock kernel accepts a ruleset for
   an older ABI. The tool uses the highest ABI it knows at or below the running
   kernel's, and reports `used=` and a clamp note so this limit is visible.
   Later rights remain unhandled until the table grows. Verification of new
   rows is required before the tool claims their coverage.

   `IOCTL_DEV` at ABI 5 controls ioctl access to opened devices. The set is
   masked down to the discovered ABI: a ruleset handling an access the kernel
   does not know is rejected. The `MAKE_*`, `REMOVE_*`, `WRITE_FILE`,
   `TRUNCATE`, `REFER` and `IOCTL_DEV`
   bits are *granted* to the write set only; the read set and the roots get
   `EXECUTE|READ_FILE|READ_DIR`.
   The linux root list names `/proc`, not `/proc/self`. `/proc/self` opened
   `O_PATH` resolves at open time to the pid that opened it — the **tool's**,
   and the tool forks rather than becomes the command, so that pid is never the
   command's — so a rule built on it grants the wrapped process **nothing, not
   even its own `/proc` entry**, and grants every child it spawns nothing: the
   wrapped command, and any harness subprocess that reads `/proc/self/status`,
   would fail for no legible reason. `/proc` read-only is the grant.

2. For each root and each `--read`: `open(2)` it `O_PATH|O_CLOEXEC` and
   `landlock_add_rule` with `LANDLOCK_RULE_PATH_BENEATH` and the read subset.
3. For each `--write`: the same with the full read+write subset.
4. `runtime.LockOSThread` (the restriction is per-thread until it is applied,
   and Go may otherwise move the goroutine), `prctl(PR_SET_NO_NEW_PRIVS, 1)`,
   `landlock_restrict_self`.
5. The command is started as a child and the tool **waits** for it, exactly as
   the darwin body waits on `sandbox-exec`'s child, and returns its status.
   Every status line, including `SANDBOX OK`, is printed and flushed before
   step 4, because past step 4 the tool is itself inside the wall.

**Restrict-then-fork lets the probe read each step's status.** `Run` returns
that status instead of replacing the tool's process with the command.

The bare wrapper uses no re-exec helper or hidden flag. The tool
applies the ruleset to itself and forks the command, so the process count is
the darwin body's — tool plus command — and Landlock's inheritance across
`fork(2)` is what carries the wall to the child. `runtime.LockOSThread` pins
the goroutine to the thread being restricted so that the fork happens on that
thread, and there is no matching `UnlockOSThread`: the thread is walled for
good and handing it back to the runtime's pool would hand an unrelated
goroutine a wall it never asked for.

The cost is stated rather than hidden: **`Run` is one-way.** Past
`landlock_restrict_self` the tool's own process is inside the wall and no call
takes it back out. A caller that runs `Run` twice in one process nests a second
domain inside the first — which is what `probe` does, and because its walled
steps all share one policy the nested domain is the same wall again. Anything a
caller must do unwalled it must do **before** the first `Run`, which is exactly
why the probe runs `write_outside_control` first.

The ABI is discovered with `landlock_create_ruleset(NULL, 0,
LANDLOCK_CREATE_RULESET_VERSION)`, and the handled set is masked down to what
that ABI knows: a ruleset that handles an access the kernel does not understand
is rejected. The discovered number is printed as `abi=<n>` on `SANDBOX OK`.

**Network:** Landlock gained TCP `bind`/`connect` restriction at **ABI 4**
(kernel 6.7). UDP is not restricted at any ABI. Below ABI 4, `--net-deny` is
`SANDBOX REFUSED reason=net_unenforceable`.

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
— the command does not run.

**What this backend cannot do that the darwin one can.** Four things, and they
are here rather than in a footnote because a wall's gaps are the part a reader
must be able to find:

1. **It denies; it does not hide.** Landlock has no mount namespace, so a path
   in neither `--read` nor `--write` is *unreadable*, not *absent*: its
   **contents** never come out, while its **name** can still appear in a
   listing of a readable parent directory. This matches the darwin backend,
   which also denies rather than hides, and it is the promise both make — the
   bytes, not the name.
2. **Network denial is TCP only.** `--net-deny` is TCP `bind`/`connect`, which
   is all Landlock restricts at any ABI; **UDP is not restricted**, so a walled
   process under `net=denied` can still send and receive UDP, DNS included. The
   darwin backend withholds the whole network grant and has no such hole.
3. **No `ioctl` restriction below ABI 5** (kernel 6.10), which the fleet's
   linux bench at kernel 6.8 is: `LANDLOCK_ACCESS_FS_IOCTL_DEV` does not exist
   there, so a walled process can `ioctl` any device file it can open. The bit
   is handled the moment the kernel defines it, and `abi=` on the `SANDBOX OK`
   line — with `used=` beside it when the kernel is newer than the table — is
   how a reader knows which machine they are on and which wall it got.
4. **No abstract-unix-socket or signal scope below ABI 6** (kernel 6.12), as
   the paragraph above says: a walled process on the 6.8 bench can connect to
   an abstract socket outside its domain and signal a process outside it.

A fifth is not this backend's but the roots table's, and it bites hardest on
linux: **"the directory of the resolved command" is a read root**, so a command
that lives in a directory holding secrets makes that directory readable. Keep
the tool and the commands it wraps in a `bin` directory, never in the job's
parent or in a shared `/tmp`.

## Unsupported platforms

The bare wrapper has no Windows containment backend. On Windows and other
platforms without an implementation, `check` reports `backend=none`, and an
attempt to wrap a command refuses with `reason=no_sandbox`. A parsed `--name`
or `--acl` does not supply a backend or an ACL-management command.

This is the bare wrapper's limit. The separate `run` verb has its own platform
handling; a disposable directory or process resource limit is not evidence of
filesystem containment.

## The probe

```
HOME=<jobdir>/home nova-sandbox probe --write <jobdir> --secret ~/.config/<provider>/env
```

`HOME` is set here for the same reason it is set on the reader commands: the home-outside
check runs before the policy is built, so a probe run with the dispatcher's own
`HOME` — which is outside every `--write` by construction — is
`PROBE REFUSED reason=check ... home_outside` and every swarm pass would refuse
with it. A caller that runs the probe runs it with the job's data home. A caller
whose key is delivered by `nova-secrets exec` names **no `--secret`**: the
key is never a file, so there is no secret file to prove unreadable,
and the probe runs the other four checks.

Five checks, under the real policy for this platform, each one line — the five
are the five rows below, `write_outside_control` included, and the names in the
`PROBE STEP` grammar are exactly these five; a probe **without `--secret`** runs
four (no `read_secret`). `probe` is the one verb that
**re-executes the tool itself** under the generated policy (an internal verb,
not a shell, not a caller command): it has no command to wrap, and running the
checks in-process would test nothing, because on linux the restriction is
applied to the tool's own process and on darwin the policy exists only around
`sandbox-exec`'s child. Everything a check touches is therefore named by the
probe, not by a caller:

| name | what it does | expected |
|---|---|---|
| `write_outside` | creates a file at one **explicitly named** path outside every named path — `<parent of the first --write>/.nova-sandbox-probe-<pid>` — printed on the step line | `deny` |
| `read_secret` | opens the named secret file for reading | `deny` |
| `write_inside` | creates and removes a file under the first `--write` | `allow` |
| `read_root` | reads the first byte of **the probe's own executable** (`os.Executable()`, which is the resolved command of the probe's re-executed self and therefore lies under the root "the directory of the resolved command") | `allow` |
| `write_outside_control` | the same write as `write_outside`, run **outside** the wall, before the wrapped one | `allow` |

`write_outside_control` is why the deny checks can be believed. A denial that
was never possible is not a wall: if the named outside path is unwritable to
this user anyway (`/opt` is `EACCES` on a Mac), `write_outside` comes back
`deny` on a **broken** wall and the probe passes. The control runs the same
write outside the wall first; if the control says `deny`, the probe is exit 2
`reason=probe_outside_unwritable` naming the path, because the machine cannot
answer the question — not a failed check, a misconfigured one. `read_secret`
needs no control (the tool refuses a `--secret` inside a list, and the caller
just read the file to pass its value by environment), and the two `allow`
checks are their own controls.

Every check runs; the verb does not stop at the first failure, because a caller
fixing a machine wants all five answers at once (SPEC.md: report every
independent problem at once). The exit is 1 if any check disagreed with its
expectation, and `PROBE REFUSED reason=check` names each one.

`write_outside` names its path instead of calling `os.TempDir()`, because
the tool points `TMPDIR` **inside** the wall: a probe that wrote to
`os.TempDir()` would write inside the write set, watch it succeed, and report
a false refusal — it would fail on a working wall. If the named path resolves
inside any `--read` or `--write` (a first `--write` whose parent is itself in
a list), the verb is exit 2 `reason=probe_outside_inside` and names the path,
because a probe that cannot find an outside is a misconfiguration, not a
failed check.

The secret file's **contents are never read into memory**: the check is that
`open(2)` (or `CreateFileW`) fails, and a probe that succeeded in opening it
closes it without reading and reports `got=allow`.

### Local GPU, and what the capability is and is not

A bounded local model trial on Apple Silicon runs MLX GPU arithmetic normally
outside the wall but fails at import inside it with `[metal::load_device] No
Metal device available`, under the narrowed mach-lookup profile with no IOKit
clauses. The only
opt-in is `--gpu none|metal` (default `none`, printed as `gpu=` on every OK
line); it records intent and never widens mach-lookup nor grants blanket
device access, whose minimum mechanisms are still unmeasured — the generated
profile stays closed. The compared option is a separately supervised inference
service: sandboxing its client does not sandbox the service, and that trust
and resource boundary stays visible. Dedicated child HOME/cache/output roots,
deadlines, process ownership, and measured receipts are unchanged.

### The internal verb, and what its guard is and is not

The re-executed self is `probe-step <nonce> <name> <path>`. It is not in the
usage banner, and it **opens, truncates and reads the path it is handed** — so
"nothing else runs it" has to be held by a mechanism, not by this sentence. It
is held by three things, and each one is something a caller typing a command
line cannot supply:

1. The parent mints one 128-bit value per probe, keeps it in its own memory, and
   writes the **16 raw bytes into a pipe whose read end is the child's fd 3**,
   closing its own end at once. The child insists that fd 3 is open, that it is
   a pipe, that it carries exactly those 16 bytes, and that it carries no more.
2. Those bytes, hex-encoded, equal the value in the child's **argv** and the
   value in its **environment** — three copies, compared in constant time, all
   three required to agree. The environment copy is the guard's second factor and
   nothing else reads it.
3. The child asks the OS for its **parent's executable** and refuses unless that
   is this same binary. `sandbox-exec` execs in place, so the probe's child has
   the tool for a parent and nothing else does. On darwin the question is
   `proc_pidpath(2)` and **not** `ps -o comm= -p`: the child asks it *inside*
   the wall, and `ps` is setuid root (`-rwsr-xr-x root wheel` for `/bin/ps` on
   macOS 26) while a set-id exec is denied inside the wall, so `ps` there is
   `/bin/ps: Operation not permitted`, exit 126.

**What a refused step prints.** The verb refuses in **three places**, and all
three print the same line: the **argument count** — the verb takes `<nonce>
<name> <path>` and nothing else, and it is the one a caller typing the verb by
hand meets first — then the **guard**, whose three mechanisms above answer with
one line between them, then the **absolute path** the verb insists on
afterwards. Each refusal is one line on standard error, exit 2, nothing opened:
`PROBE REFUSED reason=probe_step_not_a_child: <text>`. That token is the **one
`PROBE REFUSED` reason outside the fixed set above**, and it is outside it on
purpose: the set is the contract a caller's parser stands on, nothing but this
binary's own probe runs this verb, and so no parser ever sees this line. It is
written down here because the grammar above is fixed at six, and a refusal the
tool can print that the spec never names is a line whose reader has nowhere to
look it up.

**The honest bound.** The verb grants **no capability the caller lacks**, because
everything it does is bounded by the wall it runs in. A same-user caller can open
and truncate a file with `>` and needs no verb of ours to do it. What the guard
buys is that no invocation from **outside** that wall — by hand, or driven by
*content*: a script, a `Makefile`, a repository's own hook — reaches this verb's
`O_TRUNC` by guessing a word.

A caller already **inside** the wall gains nothing by it, and that is measured,
not assumed. On `676d432` a shell under this tool's own exec verb built a
16-byte pipe as its fd 3, minted one value for both the argv and the
environment, and `exec`'d `probe-step`: `sandbox-exec` and the shell both exec
in place, so the child's parent *is* the tool, and all three halves passed. The
`O_TRUNC` it reached was on a file **inside** the wall — one the wall already
allowed, which content there could empty with `: > file` anyway. The same run
against a file **outside** the wall was refused by the wall: exit 1, the file
intact. So "nothing else runs it" is not a property the guard makes true by
mechanism; what bounds anything else that reaches the verb is the wall.

It is stated because the first form was **not** true by mechanism. It compared
the argv copy to the environment copy, and both of those are the caller's own to
set, so it checked only that a caller had agreed with itself. Measured on
`29646c1` by hand, from an ordinary shell:
`NOVA_SANDBOX_PROBE_NONCE=<x> nova-sandbox probe-step <x> write_outside <file>`
ran, exited 0, and left the file at zero bytes.

## Caller responsibilities

The caller supplies the read and write paths, sets `HOME` inside the write
set, and uses a working directory inside that set. A shared reference checkout
can be read-only; a checkout that the command will modify belongs in the write
set. Preparing either checkout is the caller's responsibility.

Credentials passed by environment remain available to the command. A caller
that reads a credential file closes its descriptor before the wrap and keeps
the file outside both path lists. Filesystem containment does not revoke an
environment credential or prevent its use over an allowed network connection.

Standard output and error go to a pipe the caller drains or a file inside the
write set. The caller owns process-group cleanup, publication and
backup; none is performed by the bare wrapper. A missing backend refuses the
command rather than running it without containment.

## What it deliberately does not do

- **It does not manage credentials.** The environment passes through.
- **It does not restrict syscalls.** No seccomp filter, no entitlement list;
  the question this tool answers is what a command can reach on disk.
- **It does not restrict CPU, memory or process count.** A runaway worker is
  the deadline's problem.
- **It does not create the directories it is handed.** Every `--read`,
  `--write`, `--cwd` and `--tmp` path must already exist; a missing one is a
  refusal and is not created. The one directory it creates is its
  own: `<first --write>/.nova-sandbox-tmp` — inside the write
  set, named by the tool, never by the caller. An explicit `--tmp` is a caller
  path like any other: it must exist.
- **It does not run a shell.** Everything after `--` is `exec`'d.
- **It does not take a caller-supplied profile.** The policy is generated
  from the lists the caller passes.
- **It does not clone anything.** The dispatcher owns the checkout; the tool
  only names directories.
- **It does not have a config file.** There is no file from which either list
  can arrive; both are argv, where `ps` shows them. There is no switch that
  turns the wall off, in a file or anywhere else.

## Commands for a reader

A reader on another machine, or on another model, checks this document by
running it rather than by trusting it. The darwin check is not four lines of
shell in a document any more — it is `tools/sandboxcheck`, which ships in
this repository, fills `profiles/darwin.sb.tmpl` itself and prints one
`CHECK` line per check:

```
# 1. darwin, the whole wall, from the repository root, writing only beside itself:
go run ./tools/sandboxcheck ; echo "exit=$?"
```

A reader who wants to see the policy itself asks the tool for it: the `policy`
verb prints exactly what a wrapped run would apply, and runs nothing. It is
pasteable as written — no scratch directory to find, no `-f <file>` form (which
this tool never uses), no placeholder. `go run ./tools/sandboxcheck --dump-profile` shows
the same profile filled the check's own way for every check, and the template's
header says what each marker is replaced by.

```
# 2. darwin: the generated policy, and then the wall around a real command.
#    cwd and stdout are part of the wall, not decoration: run the second line
#    with the cwd inside the write set (a cwd outside every named path denies
#    getcwd(3), and every git command dies there before it reads anything) and
#    stdout a PIPE the caller drains or a file inside it — a wrapped /bin/cat
#    whose stdout is a file outside every named path is denied.
#    Expect: $PPID == the TOOL's pid — sandbox-exec execs the command in
#    place and the tool waits, so the shell's parent is nova-sandbox itself —
#    the first line of /etc/hosts, and no "Operation not permitted".
#    HOME is set on BOTH lines: the HOME check runs before the policy is built,
#    so `policy` refuses an outside HOME even though it runs nothing.
mkdir -p w/home && cd w
HOME="$PWD/home" nova-sandbox policy --read /opt/homebrew --write "$PWD"
HOME="$PWD/home" nova-sandbox --read /opt/homebrew --write "$PWD" \
  -- /bin/sh -c 'echo $PPID; cat /etc/hosts; sleep 9 & kill $!'

# 3. linux: the Landlock ABI, without Go. syscall 444 is
#    landlock_create_ruleset; flag 1 is LANDLOCK_CREATE_RULESET_VERSION.
#    Expect: the same number the tool prints on abi=. If it is ABOVE the table's
#    top row (6), the wall is CLAMPED and the tool says so -- `check` note, a
#    SANDBOX NOTE, and used=6 on the SANDBOX OK line -- and the command still runs.
python3 -c 'import ctypes;l=ctypes.CDLL(None,use_errno=True);print("landlock abi",l.syscall(444,0,0,1))'
cat /sys/kernel/security/lsm        # landlock must appear in the list
nova-sandbox check                  # backend=landlock abi=<n> ... note=<the clamp, if any>

# 4. darwin: the dyld cache path this spec says is absent on macOS 26.
ls /private/var/db/dyld
```

Command 3 is Linux-specific; commands 1, 2 and 4 are macOS-specific; a reader runs the ones their machine can answer. A reader who
gets a different answer to any of them has found a defect in this document, and
the document changes.

## To verify at build

Each item is a claim in this document that was written from documentation and
must be **executed on the machine** before the spec's word is trusted. A build
that cannot confirm one changes this document rather than asserting it.

1. That the measured three-service `mach-lookup` set
   (`com.apple.system.opendirectoryd.libinfo`, `com.apple.SecurityServer`,
   `com.apple.system.logger`), with `/` and `/dev` in the roots, is enough for
   a Node-based harness and a Go toolchain under the profile, and if not, which
   further service each needs, added by measurement — the unqualified
   `(allow mach-lookup)` is forbidden and is not the fallback, while
   a deny-default profile that blocks `mach-lookup` outright breaks `dyld` and
   process spawn in ways that look like unrelated crashes.
2. `-D` parameter escaping, which is a live risk and not a formality: one
   measured run of a multi-line profile with seven parameters printed
   `invalid data type of path filter; expected pattern, got boolean`, and a
   path containing a space and a paren aborted the run (134). Decide from the
   measurement whether the tool refuses paths carrying SBPL metacharacters,
   changes how parameters are grouped, or both.
3. Landlock syscall numbers as used from Go's `syscall` package on both
   `amd64` and `arm64`, and whether the restrict-then-fork body (which needs
   `runtime.LockOSThread`, `prctl` and three raw syscalls) is
   achievable under the repository's standard-library-only rule or needs
   `golang.org/x/sys/unix` — a dependency decision, not a detail.
4. That the restriction applied before the fork survives both `fork(2)` and
   `execve(2)` for a Node harness that re-execs itself, and that a child
   process it spawns is equally restricted.
7. The Landlock ABI table above, **row by row, against
   `uapi/linux/landlock.h` on a machine running each kernel** — and in
   particular whether newer ABIs add filesystem accesses absent from the
   table. Until a new row is verified the tool **clamps** ABI 7
   and up to the table's ABI 6 and says so (`used=6`). The verification is also the
   procedure for every future ABI: read the header, add the row, add the grant
   side, release.
8. That a unix-domain socket **under** a Landlock write rule can be connected
   to while one outside every rule cannot — the linux half of what
   `tools/sandboxcheck` now measures on darwin. Landlock's path rules
   govern the socket file's *lookup*, not `connect(2)` itself, so this may
   come back as "the filesystem wall does not close it below the ABI that
   adds `RESOLVE_UNIX`". Do not infer a socket restriction from the environment
   scrub alone; the Linux limitations above govern.

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
   `/private/var/select`). On darwin a wrapped `/usr/bin/c++` compiles and
   runs a C++ probe inside the write set (it fails without the
   `xcode_select_link` literals). On linux a wrapped command's **child** reads
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
   turns the test red. `--net-listen` is demanded by the same test: a wrapped
   listener that binds a loopback TCP port accepts an inbound connection made
   from outside the wall **only** when `--net-listen` was passed, and without
   it that same inbound connect fails; `--net-deny --net-listen` together is
   `SANDBOX REFUSED reason=bad_net` at exit 125 and the command does not run —
   a tool that picks one of the two instead of refusing turns the test red, and
   that assertion is its **own** test on **every** platform, because it is a
   flag refusal with no profile text in it and a test gated on the darwin
   profile would take it off windows along with the profile.
   Two more, both on the **policy text** and neither touching the network,
   because a rule whose only witness is a live DNS query has no test at all:
   the generated policy carries the literal `/private/var/run/mDNSResponder`
   **inside** the network marker, so `--net-deny` takes it away with the rest;
   and it carries exactly the three measured `global-name`s and no bare
   `(allow mach-lookup)`. A mutation deleting either turns a Go test red. The
   live DNS measurement stays in `tools/sandboxcheck`, where the operator
   run and the mac CI job execute it and a Go test does not (test 16).
   On linux, an ABI forced **above** this tool's table is a **clamp**: the wall
   is built at the table's maximum, `abi=` carries the kernel's number and
   `used=` the wall's, and a `SANDBOX NOTE` before the command starts names
   both numbers and the word `clamped`. A mutation that clamps *silently* —
   dropping `used=` or the note — turns it red, because the saying is what
   replaced the refusal. The refusal keeps its own test on the other end of the
   table: an ABI forced **below** the first row is `SANDBOX REFUSED
   reason=landlock_abi_unknown` naming both numbers, at exit 125, with the
   tripwire on the exec path seeing no call — the mirror of the forced-down
   `net_unenforceable` case above, and the only thing that makes
   `landlock_abi_unknown` more than a word in the exit table. The
   `no_sandbox` refusal has its own linux test, where the ABI refusal once stood in
   for, through the same seam and with the same tripwire. End to end on a real
   kernel, a walled run on a machine whose ABI is above the table **runs and
   exits 0** and its line carries `used=`; on a machine at or below the table
   the line carries **no** `used=` field at all.
8. A wrapped command that writes to `$TMPDIR` succeeds and the file lands under
   the first `--write`; `TMPDIR`, `TMP` and `TEMP` all name that directory and
   zsh's `TMPPREFIX` is a prefix beneath it; `--tmp` outside the write set is
   refused. A macOS zsh heredoc larger than the pipe buffer succeeds with an
   inherited outside `TMPPREFIX` and writes its report inside that temp
   directory.
9. An environment variable set by the caller arrives in the child unchanged,
   including one whose value is a credential-shaped string, and that value
   appears in no printed line. The `SANDBOX NOTE dropped from the child's
   environment: <names>; an agent socket speaks for a key the wall denies` line
   is asserted **whole**, before the command starts, with `<names>` exactly the
   set that was dropped and nothing else — a planted `AI_AGENT`,
   `CLAUDE_AGENT_SDK_VERSION` and caller token appear nowhere in it — and **no
   NOTE at all** is printed when the scrub removed nothing. A NOTE that names a
   variable the child still has is a false statement about the wall.
10. `TestProbeProvesTheWall`: the `write_outside` path is the named one and is
    asserted to be outside every list **with `TMPDIR` pointed inside the wall
    by the rule that `$TMPDIR` names the first `--write`** — a probe built on
    `os.TempDir()` turns this red; a first
    `--write` whose parent is inside a list is exit 2
    `reason=probe_outside_inside`; all five checks run even when the first
    fails; a named outside path that this user cannot write to anyway is exit 2
    `reason=probe_outside_unwritable`, asserted with a directory the test makes
    read-only — the check that would otherwise pass on a broken wall; a
    policy that denies everything fails `write_inside` and `read_root` and is
    `PROBE REFUSED`, not `PROBE OK`; a policy with no wall at all fails
    `write_outside` and `read_secret`; a correct policy is
    `PROBE OK steps=5 passed=5`; the secret file's contents are never read.
    A probe **without `--secret`** prints `PROBE OK steps=4
    passed=4` with no `read_secret` step — the key delivered by `nova-secrets
    exec` is never a file.
    And the shape the probe enforces, which is what makes `read_root` mean anything:
    the printed `path=` of `read_root` **is `os.Executable()`**, the probe's own
    binary, and no step's `path=` is a shell. `/bin` is a fixed root in the
    profile verbatim, so a `read_root` that read `/bin/sh` would exercise none
    of the run-time root it exists for; and a step built as a shell **string**
    lets a `--secret` holding a quote and a `;` run a command inside the wall
    and flip the check's verdict, which is the test's second half, with the
    injected file asserted absent afterwards. `--secret` is resolved
    like every other caller path, so one that names no file is a refusal rather
    than a probe that "could not read" a file that was never there.
11. `--no-sandbox` is **not a flag this tool has**: the test runs
    `nova-sandbox --no-sandbox -- <command>` and asserts the existing refusal,
    `SANDBOX REFUSED reason=bad_flag: unknown flag --no-sandbox; the flags are
    ...; run: nova-sandbox help`, at exit 125, with the command not run. No
    environment variable and no file can turn the wall off either — the test
    sets every plausible name and the tool still sandboxes.
12. A wrapped command exiting 3 gives exit 3; one killed by `SIGKILL` gives
    137; an argument containing a space, a quote, a `$` and a `;` arrives in
    the child's argv byte-for-byte; stdout and stderr are not interleaved by
    the tool. Per platform: on linux the command is a **child with a pid of its
    own** (the test reads `/proc/self/stat` from the wrapped command and
    asserts it is not the tool's) and the tool **waits** for it, and `SIGINT`
    and `SIGTERM` reach that child; on darwin `SIGTERM` reaches the child and the
    tool waits for it. stdio: a wrapped command whose stdout is a **pipe**
    writes through it, and one whose stdout is a **file inside the write set**
    writes through it; one whose stdout is a file **outside** every named path
    fails for `/bin/cat` — the test asserts that failure rather than hiding
    it, because it is the reason the rule names only two legal shapes. On
    every platform the wrapped command can signal its
    **own** child: `sh -c 'sleep 30 & kill $!'` exits 0 and the sleep is gone,
    and a wrapped command's forked background child is **reaped with the
    caller's process group** — the test starts the tool in a group of its own
    making, wraps a command that forks a background `sleep` and exits, kills
    that group and asserts the `sleep` is dead; a tool that gives its child a
    group of its own turns this red, which is how it was found,
    which is red on darwin without `(target children)` in the signal clause.
    Descriptors: a wrapped listing of `/dev/fd` names **0, 1 and 2 and nothing
    the tool added**, compared against the same listing outside the wall so that
    the assertion is about the wrap and not about the shape of `ls`; and a
    descriptor the **caller** holds open onto the secret does not reach the
    child, which is the tool's half of "every file it opens is `CLOEXEC` and
    only 0, 1 and 2 are passed".
13. The default `--cwd` is the first `--write`; a `--cwd` outside the write set
    is exit 125 `reason=bad_cwd`; a wrapped command reports its own cwd as the
    job directory.
15. `policy` prints a policy and executes nothing; the same lists produce
    byte-identical output twice; there is no flag by which a caller-supplied
    profile file can be passed, asserted by the flag set itself. `policy --
    <command>` shows **the directory of the resolved command as a root** — the
    one root the generator computes at run time, and the one a reader most needs
    to see — and the test asserts both halves: that root appears in the printed
    policy, and the command **did not run** (it is a command that would create a
    file, and the file is not there afterwards).
16. A listing over 40 entries prints 20 and one MORE line naming the remedy;
    `--max 0` prints all; every refusal names the flag and its wanted form; two
    independent problems are both reported in one refusal; no test reaches
    outside `t.TempDir()` or touches the network (CONTRIBUTING.md, **test code
    is code**).

And one for each thing the rules above assert but no test yet reached:

17. `SANDBOX OK` is written and flushed **before** the command starts: the
    wrapped command writes a marker to a file, and the test asserts the stderr
    line is complete before the marker exists — on linux this is load-bearing,
    because past `landlock_restrict_self` the tool's own process is inside the
    wall (the Linux section's step 5), and every status line must be out of it
    before it goes up.
18. `check` on this machine prints one `CHECK OK` naming the backend, the ABI
    or `-`, and `net=enforceable|unenforceable`; with the backend forced
    unavailable it prints `backend=none` and still **exits 0**, because it is a
    question, not an attempt.
19. Exit `125`: a command that exists but is not executable — the pre-flight
    stats the resolved path, **outside the wall and before any profile
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
20. darwin: the wrap writes **no profile file**: the generated text is passed
    with `-p`, the first `--write` holds no `.nova-sandbox-*.sb` at any point
    during or after the run, and the argv the body builds carries `-p`.
21. `--read` ergonomics: a toolchain placed in a user directory is unreadable
    inside the wall without `--read` — the wrapped command fails (126 or a
    signal death) — and with `--read` it runs. **The directory is named by the
    test and lies outside the caller's home**, because the home guard of the
    roots section now refuses a command whose directory **is** the home or an
    ancestor of it: under that guard a toolchain at `~/x.sh` is not a 126
    inside the wall at all, it is exit **125** `reason=bad_read` before
    anything runs, and a test that took the 125 for the 126 it was written
    about would be green for the wrong reason. (One directory deeper,
    `~/tools/x.sh`, is a directory of its own and **is** a bounded root — test
    29, and the guard as built: it refuses only `dir == home` or an ancestor —
    so it is not refused at all. The shape this test needs is one where neither
    outcome can be confused for the 126.) `<tmp>/toolchain/x.sh` with
    `HOME` pointed at the job's data home is the shape this test uses. The test asserts that **no**
    `SANDBOX NOTE` is printed after the command has started, on every platform
    (the linux body cannot, and the others must not diverge), and that the
    usage banner carries the `--read` remedy sentence.
25. From inside a sandboxed task, `rm -rf` of a line's self path fails with
    `EPERM` and the self is byte-identical afterwards (#69's worked specimen).
27. **`git push` from inside the wall fails, and the test names the mechanism.**
    A local bare repository stands in for every remote — no test touches the
    network (test 16). Four assertions, one per mechanism, so that a change
    that removes one of them turns exactly one line red:
    (a) `origin` is the dispatcher's reference checkout under a `--read` path:
    `git push origin HEAD` fails and the reference's `refs/` is byte-identical
    afterwards; (b) `origin` rewritten to `git@example.invalid:x/y.git`: the
    push fails **before any connection**, with the planted `<home>/.ssh/id_test`
    of test 3 unreadable inside the wall and readable outside it in the same
    test; (c) the child's environment, read back from inside the wall, holds
    none of the exact set — planted `SSH_AUTH_SOCK`, `SSH_AGENT_PID`,
    `GPG_AGENT_INFO` and `PODMAN_AGENT_SOCK` are all gone — while a planted
    `AI_AGENT` and `CLAUDE_AGENT_SDK_VERSION` and a caller variable set
    beside them arrive unchanged — and a connect to a unix-domain socket the
    test binds outside every named path is denied, with the control connect
    outside the wall succeeding (darwin today: `tools/sandboxcheck`
    checks `unix_socket_outside` and `unix_socket_outside_control`; linux is
    **to verify at build** item 8 and the test skips by name, not by
    assertion, until it is); (d) `origin` rewritten to an HTTPS URL with no
    token in the environment and a `gh` configuration planted outside every
    list: the push fails and the configuration is unread. The same four pushes
    run **outside** the wall against the local bare repository and succeed, so
    that no line of this test can pass by being impossible.
29. **The home guard on the computed root**, which the roots section states
    ("The home directory is never a root — including by way of the command")
    and nothing demanded a test for. On **every platform**, because the guard is
    in `Build` and the roots-table entry it guards is in this spec for all
    three: a command planted at `<home>/x.sh`, and one planted in an **ancestor**
    of that home, are each exit **125** `reason=bad_read` with the refusal
    naming the home directory, and `Build` returns no policy — asserted with a
    key planted at `<home>/.ssh/id_test`, which is what the whole refusal is
    about. One directory deeper (`<home>/tools/x.sh`) is a directory of its own
    and **is** a root, so the entry is guarded rather than removed. Both
    exemptions run: a command in a home the caller named in its own `--read`
    is accepted, and so is one under the job's data home, which lies
    inside a `--write` by construction — the guard refused the tool's own
    `probe` before that second exemption existed.

## Implementation

The command dispatch and probe are in `cmd/nova-sandbox/main.go`; policy
validation, path containment and the shared environment rules are in
`internal/sandbox/policy.go`. The operating-system wrappers are
`internal/sandbox/wrap_darwin.go` and `internal/sandbox/wrap_linux.go`;
`internal/sandbox/wrap_other.go` refuses unsupported platforms. The disposable
run, artifact handoff, worktree and egress commands have their own files under
`cmd/nova-sandbox/`.
