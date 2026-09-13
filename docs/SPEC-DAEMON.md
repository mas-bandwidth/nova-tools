# nova-daemon — specification

`nova-daemon` is one binary at the **unit layer**. It renders, installs, checks and
repairs the operating system's units — macOS `launchd` agents and Linux `systemd` user
units — for a line's scheduled roles, its long-running daemons and its consumers on a
clock, from **one declaration file kept in git**, and it stamps every run so that a unit
that ran and failed is never confused with one that never fired.

This spec is normative. If the code and this document disagree, one of them has a bug,
and the tests decide which. It stands beside [SPEC.md](SPEC.md), whose **Conventions**
section — exit codes, no guessed paths, the one-line output grammar, the field law, the
cap-and-count rule, `internal/oneline` and `internal/bounded` — applies here unchanged
and is not restated. Where this tool needs something the Conventions do not cover, it is
below and it says so.

Glenn, 2026-09-12 ~03:00Z: *"Should we consider generalizing the daemon for the keeper
into nova-daemon"* ([ideas#768](https://github.com/mas-bandwidth/ideas/issues/768)). And
the platform question, the same thread: *"can nova daemon be made to work on mac and
linux? windows too?"*

A unit layer of this shape has run one bench's estate for a month — fourteen scheduled
roles, three long-running detectors and one sixty-second consumer, unattended, through
rebuilds, reboots and a code-signing kill that took three roles out in one night. **The
form works, and every way it failed is written down with a date.** This tool is those
failures closed, one rule each.

| the failure, from the record | the rule that closes it |
|---|---|
| a rebuild moved a binary's content hash; macOS SIGKILLed three scheduled jobs at exec for a code-signing mismatch, writing no stamp, no log and no exit — the fold, the mail and a contraction pass simply did not happen (2026-08-21) | `repin` runs **from the build, unconditionally**, not from a unit — a direct execution is subject to no unit's pinned requirement (rules 17, 18) |
| the detector for that kill existed, worked, and ran **once a day**; it found all three a day late (2026-08-21) | the heartbeat's period must be **strictly shorter than the shortest declared schedule in the file**, and `check` fails when it is not (rule 19) |
| `repin` skipped a running job for a correct reason and then forgot; the job sat forty-three ticks un-repinned while every instrument read it healthy (2026-08-21) | a skip is **recorded as owed** in the state directory, and the heartbeat retries it; a skip with no record is refused (rule 20) |
| an install was refused over a live run, so one role kept a superseded schedule while every other role moved; it fired, stamped fresh, reported clean and exited 0 — **at the wrong time, forever** (2026-08-23) | `check` compares **declared against loaded against fired**, and `WRONG-WHEN` is a failure of its own (rule 13) |
| a deferred install re-instated the schedule it was meant to replace | a recorded owed act **re-derives its content at repair time** and never stores it (rule 20) |
| a green report said "all 9 stamped roles RAN-CLEAN" for twenty mornings while the weekly role had run twice in its life, because the list it counted deliberately excluded it (2026-08-30) | every unit in the declaration is counted in every count line; there is **no exemption list**, and a unit that cannot be judged is `UNKNOWN`, never absent (rule 14) |
| an unattended run called a gated tool, which did not fail — it **parked on a permission modal** for 3h14m and reported work it had not done (2026-07-22) | stdin is `/dev/null`, no unit may declare an unbounded deadline, and a deadline reached kills the **process group** and stamps `TIMEOUT`, a state of its own (rules 9, 10) |
| a launchd job was installed via `go run`; the plist named a build artifact the toolchain deletes on exit. The agent registered, reported healthy, and could never start again (2026-08-13) | `install` resolves the program through symlinks and **refuses a temporary-build path**, an absent file and a non-executable one (rule 6) |
| launchd handed a job a POSIX `PATH`; the first unattended night's commit came back `go: command not found` and the whole run blocked, silently, from the second night onward (2026-08-12) | the unit's environment is **declared, resolved at install time from the installer's own environment, and refused when a named tool is not found** (rule 8) |
| a spawned role had no HTTPS credential and hung a four-hour deadline on a clone prompt (2026-08-13) | rule 9 again: the deadline is the wall, and `RUN TIMEOUT` is distinguishable from `RUN FAIL` in the stamp and in `check` |
| the estate's deployment was a checkout nobody pulled; eighteen commits on the default branch never reached the binary the units ran, and the local staleness check could not see it (2026-09-07) | `check` compares the **program on disk now** against the program the loaded unit names, byte hash to byte hash, and reports `WRONG-BIN` (rule 15) |
| a plist was hand-edited twice and drifted from the table it was generated from | the declaration is the only source; `render` prints what would be written and runs nothing, so a reader checks the unit without trusting this document (rule 4) |

---

## The three kinds, and why there are exactly three

A unit is one of three kinds, and the kind decides the whole shape of what is rendered.
The distinction is not cosmetic: it is the answer to *what does "down" mean for this
thing*, and every platform body below is that answer spelled in that platform's words.

| kind | down means | rendered as |
|---|---|---|
| `role` | **resting.** It fires at its declared time and is meant to be down between firings and after a refusal | launchd: `StartCalendarInterval`, `KeepAlive` false, `RunAtLoad` false · systemd: `.service` `Type=oneshot` + `.timer` `OnCalendar=` |
| `daemon` | **the failure state.** It is meant to be up; every exit is followed by resurrection | launchd: `KeepAlive` true, `RunAtLoad` true, no schedule · systemd: `.service` `Restart=always` `RestartSec=10`, `WantedBy=default.target` |
| `consumer` | **resting, briefly.** It wakes on an interval, does work if there is work, and exits | launchd: `StartInterval`, `RunAtLoad` true, `KeepAlive` false · systemd: `.service` `Type=oneshot` + `.timer` `OnUnitActiveSec=` with `OnBootSec=` |

A `daemon` self-heals a stale code requirement, because its restart re-pins it; a `role`
and a `consumer` do **not** — they die and stay dead, and every stamp-reading watcher
reads them as *not due yet*. That asymmetry, measured 2026-08-13, is why `repin` and the
heartbeat exist, and it is why `check` asks the init system rather than the stamp.

---

## The rules, numbered

1. **One declaration file, named by a flag, kept in git.** Every unit comes from the file
   `--file` names: no default path, no search of the cwd, no `$HOME`, no directory of
   `.unit` files discovered by walking. A missing `--file` is `refusing to guess`,
   exit 2. It is a text file in git, so a change to what a box runs is a diff somebody
   read — and it is the **only** place a unit's shape is written. There is no second
   table, in this tool or in a caller, from which a unit may be generated.

2. **Nine fields per unit, and nothing implied.** One unit is one tab-separated line:

   | field | holds |
   |---|---|
   | `name` | the unit's name, `[a-z][a-z0-9-]*`, unique in the file. It is the whole identity: the reverse-DNS label, the systemd unit name, the stamp directory and every event line's `unit=` are derived from it and from `--prefix`, never invented |
   | `line` | which line owns it. An opaque token, `[a-z][a-z0-9-]*`; this tool never interprets it beyond grouping `check`'s counts and refusing a `--line` filter that matches nothing |
   | `kind` | `role`, `daemon` or `consumer` |
   | `when` | `daily HH:MM`, `weekly <sun…sat> HH:MM`, `every <duration>`, or `always` |
   | `deadline` | how long one run may take, a Go duration, **never empty and never zero** (rule 9) |
   | `dir` | the working directory the command runs in, absolute |
   | `write` | comma-separated absolute paths the command may write, or `none` |
   | `env` | comma-separated **names** of environment variables from the secret store, or `none` (rule 7) |
   | `command` | the argv, split on single spaces (rule 3) |

   **The first line must equal the header byte for byte**, else a refusal naming the file
   and line 1, exit 2, remedy *put the header back exactly as the spec shows*; unchecked,
   a header-less file loses unit 1 to the skip. The header and every line whose first
   character is `#` are skipped, nothing else is. **No field may be empty** — `none` is
   the word for an empty set, so a table row carries every column and a reader never has
   to tell an omission from a value. More or fewer fields is a refusal naming the line
   number, exit 2, never a skip.

3. **A command is argv, never a shell.** The `command` field is split on single spaces
   and executed directly: no shell, no pipe, no glob, no `&&`, no environment expansion,
   no `~`. A field carrying two adjacent spaces, or a leading or trailing one, is refused
   at load time, so the split never makes an empty argument; an argument that needs a
   space is refused with the remedy *put it in a script and name the script*. `argv[0]`
   must be absolute (rule 6). This is what makes rule 21's assertion — that nothing this
   tool renders can run a second program of its own choosing — provable rather than
   hoped for.

4. **`render` writes nothing and runs nothing.** `nova-daemon render --file <path>
   --out <dir>` writes the unit files this declaration would install, into a directory
   the caller names, and installs none of them. It is how a reader checks the wall
   without trusting this document, and how a repository keeps a rendered copy under
   review. `render` is the **only** producer of unit text in this binary: `install`
   renders and then loads, and carries no second copy of the XML or the INI.

5. **One unit is rendered from one declaration line, and the two bodies agree on the
   observable facts.** A reader of the launchd plist and a reader of the systemd pair
   must be able to answer the same four questions the same way: *what does it run*,
   *when*, *what is its environment*, and *where do its logs go*. Where the platforms
   differ the difference is named here and nowhere else:

   - **A missed firing.** launchd runs a `StartCalendarInterval` job that came due while
     the machine was asleep or off, at the next opportunity. systemd's timers do not,
     unless told to. So every rendered `.timer` carries `Persistent=true`, and the two
     bodies catch up identically. This is the one place a systemd default is overridden
     to match launchd rather than the reverse, and it is a decision, not an accident.
   - **Determinism.** Every rendered file is byte-identical run to run for an unchanged
     declaration: map-valued sections (the environment) are written in sorted key order.
     A file that differs run to run makes every reinstall look like a change and hides
     the ones that are.
   - **Escaping.** A path is not trusted to be polite. In a plist, `&`, `<` and `>` are
     escaped; in a systemd unit, `%` is written `%%` and a value carrying a newline is
     refused at load time rather than rendered. A directory name with an ampersand in it
     is legal on both platforms and would otherwise produce a file the loader rejects
     with a parse error pointing at nothing.
   - **A unit name is escaped into a filename by the platform's own rule**, and a `name`
     that does not survive that round trip is refused at load time.

6. **The program is resolved once, at install time, and three shapes are refused.** The
   unit names an absolute path to a real file, `argv[0]` of the `command` field with
   symlinks followed: a symlink retired later is a unit pointing at nothing. Refused,
   each by name, at exit 2, before anything is written:
   - a path that does not exist, is a directory, or carries no executable bit for the
     installing user;
   - a path with a directory segment beginning `go-build`, or any path the platform's
     temporary directory contains — **a temporary build artifact.** Installing from
     `go run` writes a unit naming a file the toolchain deletes the moment the command
     exits; the unit registers, the loader reports it happily at exit 0, and it can never
     start again. *That is the worst shape a failure takes here: a unit that is present,
     registered, and permanently dead*, invisible to every check that asks "is it
     installed?" rather than "did it run?" (measured 2026-08-13). Remedy: *build first
     and install the built binary*;
   - a path under any directory in this unit's own `write` set (rule 11).

7. **A secret reaches a unit by NAME and by no other route.** The `env` field holds
   variable **names**. This tool never decrypts anything, never reads a key file, never
   holds a plaintext, and **never writes a value into a unit file, a log, a stamp or an
   event line** — a plist in `~/Library/LaunchAgents` and a user unit in
   `~/.config/systemd/user` are both world-readable, so a secret rendered into one is a
   secret published. The values arrive because `run` starts the command **through** the
   secret tool, named by `--secrets-exec`, with `--only` carrying exactly the declared
   names and nothing else ([SPEC-SECRETS.md](SPEC-SECRETS.md)). The order is the one that
   spec fixes and is load-bearing: the secret tool opens the file, sets the environment,
   and **becomes** the sandbox, which builds the wall and becomes the command — so the
   store is finished with before a wall exists, and the wall never reads a key file. The
   reverse order is forbidden there and forbidden here.

   **`nova-daemon` is the waiting parent and never `exec`s itself away**, because writing
   the end stamp is its whole job — and it therefore never reads, copies, inherits into
   its own process, or logs the child's environment.

8. **The unit's own environment is declared, resolved from the installer's, and refused
   when incomplete.** Separate from rule 7 and never overlapping it: a unit needs a
   `PATH` and a `HOME`, and a loader hands a job a POSIX `PATH` that on most developer
   machines does not include the toolchain. Measured 2026-08-12: the first unattended
   night's commit came back `go: command not found`, and the run blocked — silently, on
   schedule, with nobody watching, from the second night onward. So `--tool <name>` may
   be given any number of times; each is looked up on the **installer's own** `PATH` at
   install time, the directories found are prepended to the POSIX base, and **a tool that
   resolves nowhere is a refusal**, exit 2, naming it. It is never a warning: installing
   a unit whose first command will bounce is a false green. No bench path is ever written
   into this spec or into the tool — the directories come from the lookup.

9. **Every unit has a deadline, and a deadline is a wall rather than a guard.** `deadline`
   may not be empty, `none`, or zero: a unit with no bound is refused at load time,
   exit 2. **A gated tool does not fail — it parks**, on a permission modal, a host-key
   prompt, a credential prompt, indistinguishable from a slow call from the inside
   (2026-07-22, 3h14m; 2026-08-13, a full four-hour deadline burned on a clone prompt).
   Every `if it fails, continue` guard written against that never fires, because the call
   neither succeeds nor fails. So:
   - the command's **stdin is `/dev/null`**, always, and no declaration may change it: a
     prompt reading stdin gets EOF and the command fails fast instead of parking;
   - the command is started as its own **process-group leader**, and the deadline kills
     the **group**, not the pid — a parked child outlives a killed parent otherwise;
   - a deadline reached is `TERM` to the group, then `KILL` after a grace of 10 seconds,
     and the run is stamped **`TIMEOUT`**, which is a third state beside success and
     failure and is reported as such by `check` and `status`.

10. **A run that exits 0 with its process group still populated is a fourth state.**
    `RUN OK` requires an exit status of zero **and** an empty process group at exit.
    A command that exits cleanly having left work running behind it has delivered
    nothing and looks identical to success: four such runs on one bench were recorded as
    clean successes with real costs attached (measured, 2026-08). The stamp carries
    `work_outstanding`, `check` reports `LEFTWORK`, and the exit status alone is never
    the verdict.

11. **A unit never writes what governs it.** The `write` set is refused, at exit 2,
    naming the path, if it contains or is contained by: the declaration file; the unit
    output directory; the state directory; the log directory; or the program resolved in
    rule 6. A unit that can rewrite its own declaration, its own unit file, its own
    stamps or its own binary cannot be checked by anything, because every instrument that
    would catch it is inside its write set. **The record a unit produces must be outside
    the unit's reach**, which is also why the supervisor — not the command — writes every
    stamp and every log, outside the wall, before and after.

    "Contains" is asked of the filesystem and not of a string prefix, by the predicate
    [SPEC-SANDBOX.md](SPEC-SANDBOX.md) already defines for its own inside-ness question.

12. **The command runs behind a wall, or the declaration says so in the open.** `run`
    starts the command through the sandbox tool named by `--sandbox`, with `dir` as the
    working directory and `write` as the write set. A unit whose `write` is `none` runs
    with a write set of its `dir` alone. `--sandbox none` is accepted and is **recorded
    in every stamp and printed in every `RUN` line as `wall=none`**, because an
    unwalled unit is a fact a reader must be able to count, not a silence.

13. **`check` compares three facts, and `WRONG-WHEN` is one of its failures.** For every
    unit in the declaration, `check` establishes:
    - **DECLARED** — what the file says;
    - **LOADED** — what the init system actually holds, read back from the installed unit
      file and from the loader (`launchctl print`, `systemctl --user show`), never from
      this tool's memory of what it once wrote;
    - **FIRED** — the last run's stamp, and the loader's own last-exit fact.

    A role on a superseded schedule is invisible to every other instrument: *it fires, it
    writes a stamp, its stamp is fresh, the loader reports exit 0, its program exists —
    and it runs at the wrong time, forever, and the only witness is a diff between two
    numbers that nothing compares* (measured 2026-08-23). So `check` makes both
    comparisons the phrase "wrong WHEN" contains:
    - **declared schedule against loaded schedule** — a plist or timer holding a time the
      declaration no longer names is `WRONG-WHEN reason=loaded`;
    - **declared schedule against the stamp's actual start** — a run whose `started_at`
      is further from the nearest slot the declaration names than `--when-tolerance`
      (default 2 minutes) is `WRONG-WHEN reason=fired`. A `daemon` has no slots and is
      exempt from this second comparison only, and the exemption is a property of the
      kind, not a list of names.

14. **There is no exemption list.** Every unit the file declares is judged and counted in
    every count line. A unit this run could not judge is `UNKNOWN` with a reason, which
    is a failure, not an absence. **An exemption written down is honest, and honesty is
    not coverage**: one bench's morning report said *"all 9 stamped roles RAN-CLEAN"* for
    twenty consecutive mornings while a tenth role, deliberately omitted from the counted
    list with a comment explaining why, had run twice in its life (measured 2026-08-30).
    The comment was read once, by its author; the green verdict was read every morning by
    everyone. A caller who wants a narrower question asks it with `--unit` or `--line`,
    and the count line then says what the filter was.

15. **The program the unit loads is compared by content to the program on disk.** `check`
    hashes the file `argv[0]` resolves to now and compares it with the hash the stamp
    recorded for the last run and with the program path the loaded unit names.
    A difference is `WRONG-BIN`, with which of the three disagrees. This is the check
    that sees a deployment that never happened: one bench's units ran binaries missing
    eighteen commits from the default branch, and the local staleness check compared
    build output against **local source mtimes**, so an un-pulled checkout read as
    *current with source* (measured 2026-09-07). Content, not mtime; and this tool says
    which binary is running, never whether it is the right one to be running — *that*
    comparison belongs to the caller that owns the checkout.

16. **A skipped act, a killed run and a never-fired slot are three different lines.**
    The stamp exists so that they can be told apart:
    - **never fired** — the slot came and went and no stamp names it. `MISSED`.
    - **ran and failed** — a stamp with `started_at`, `ended_at` and a non-zero exit.
      `FAILED`.
    - **killed at exec** — no stamp at all for a slot the loader says it launched, and
      the loader reports a kernel kill reason rather than an exit code. `KILLED`.

    The third is the one nothing else can see: **a unit killed before `main()` leaves no
    evidence of its own**, so a stamp-reading watcher cannot distinguish it from a unit
    that is not due yet. The loader knew the answer the whole time and nobody asked it
    (measured 2026-08-21). `check` asks it: `launchctl print` distinguishes
    `last exit code = 0` from `last exit reason = <kernel reason>`, and
    `systemctl --user show` distinguishes `Result=success` from `Result=signal` with
    `ExecMainStatus`. These are **two different facts, not two formats**, and the tool
    keeps them apart.

17. **`repin` is unconditional and runs from the build, not from a unit.** On darwin a
    loader pins a per-unit lightweight code requirement to the program's content hash.
    Go builds are content-addressed, so **every rebuild moves that hash and every unit
    pinned to it is killed at its next launch, before `main()`, writing nothing**. Three
    roles died that way in one night and the work of that night simply did not happen
    (2026-08-21). Re-signing does not repair it; measured, all three kills landed on
    binaries that had been re-signed. **Booting the unit out and back in is the only
    repair measured to work.**

    So `repin` asks nothing. It runs at the one moment every unit is *known* to be
    stale — the build that made them so — and a check there would only add a way to be
    wrong. It is called by the caller's build (`make`, a post-commit hook, a deploy
    step); it is **never** installed as a unit of its own, because a direct execution
    from a shell is subject to no unit's pinned requirement, and that is precisely why it
    can run when every unit cannot.

18. **The repairer shares fate with the repaired, and the spec says so.** The heartbeat
    of rule 19 runs the same binary as many of the units it repairs, so one rebuild
    invalidates it along with its patients — and it cannot boot itself out, because it is
    running whenever it is repairing. **A repairer that cannot repair itself is one
    rebuild away from a box that stays dead.** An earlier version of this design claimed
    the repairer ran "in a process that shares fate with nothing"; that claim was false
    when it was written and is recorded here as false. Rule 17 is the answer to it; this
    rule is the reason rule 17 may not be softened into "the heartbeat will get it".

19. **A repair must run more often than the failure it repairs.** The heartbeat's
    interval, `--beat`, must be **strictly less than the shortest interval between two
    consecutive firings of any unit in the declaration**, and `check` fails, exit 1, with
    `BEAT` naming both numbers when it is not. A check that runs once a day cannot bound
    a failure that can arrive at any hour: the detector for the 2026-08-21 kill existed,
    worked, and found all three roles **a day late** — and a day late was the whole
    finding, because in that day the work was not done. *Nothing about the detector was
    wrong. Its clock was wrong, and a clock is not a detail.*

    **Prefer the fast beat that already exists to a new one** — a second timer is a
    second thing that can die — so `--beat` may name an existing `consumer` unit in the
    declaration to ride, and installs a heartbeat unit of its own only when told to with
    `--beat-unit`.

20. **A correct skip still records that the act is owed.** `install`, `remove` and
    `repin` all refuse to touch a unit that is **running**, for a correct reason: booting
    out a running unit kills the work inside it. Every such refusal **writes the owed act
    to the state directory** — and a refusal that returns without writing it is itself a
    bug this spec names, because both times this was built without the record the act
    never happened: a job skipped at 14:01 exited cleanly at 14:08 and was still
    un-repinned forty-three ticks later (2026-08-21), and a refused install left one role
    on a superseded schedule with nothing anywhere going to retry it (2026-08-23).

    **The owed record names the unit and the declaration file, never the rendered
    content.** The heartbeat re-derives what to install **at repair time**, from the file
    as it stands then: a late install that re-instates the schedule it was meant to
    replace passes every other test and fixes nothing. An empty owed set **removes the
    file** rather than writing an empty one, so "no file" means one thing only.

21. **This tool touches only the units its declaration names.** Not a prefix match on a
    live listing, not every unit of the current user: the set of names comes from
    `--file`, and a unit the file does not declare is never rendered, loaded, booted out,
    repinned or removed — it is reported by `check` as `UNDECLARED` when it carries this
    declaration's `--prefix`, and ignored entirely when it does not. Removing somebody
    else's unit is reaching well past what this tool owns.

22. **Every run stamps, and the start stamp is written before the command starts.**
    `run` writes `<state>/<unit>/last.json` at two moments — once before it starts the
    child, once after it reaps it — and appends one line to `<state>/<unit>/runs.tsv`,
    which is append-only and pruned to `--keep-runs` (default 200) lines from the front.
    The stamp carries: `unit`, `line`, `kind`, `fired_for` (the slot the declaration
    names that this run belongs to, or `-` for a daemon), `started_at`, `ended_at`,
    `exit`, `signal`, `timed_out`, `work_outstanding`, `deadline_sec`, `wall`,
    `bin_sha256`, `log`, and `env_names` — **names, never values** (rule 7).

    The start stamp matters because a start with no end and no live process is a crash,
    which is a different fact from a clean failure. It does **not** rescue rule 16's
    third case: a kill at exec kills the supervisor too, so nothing of this tool is left
    to write anything, which is exactly why `check` asks the loader.

23. **Logs outlive the process, and are bounded.** Each unit's stdout and stderr go to
    files under `--logs` named for the unit and the run, written by the supervisor
    outside the wall. A log file is capped at `--log-bytes` (default 1 MiB) **by keeping
    the head and the tail** with one marked cut between them, because the first lines say
    what it tried and the last say how it died; the cut leaves `...+<dropped>B`, the same
    mark `internal/bounded` uses. Logs older than `--log-days` (default 30) are pruned by
    the heartbeat, and the pruning is reported in the `BEAT` line, never silently.
    **Bookkeeping never fails the work**: a stamp or a log that cannot be written is one
    loud line and the run continues.

24. **Bookkeeping is loud and never fatal; the work is bounded and never silent.** Two
    halves of one rule. A failure to write a stamp, prune a log or record an owed act is
    reported on its own line and does not change the command's exit status. A failure to
    *read* one, in `check`, is `UNKNOWN` and therefore a **failure** — because "I could
    not tell" must never be printed as "fine" (rule 14).

25. **The tool has no clock of its own beyond the beat, and no opinion about content.**
    `nova-daemon` does not decide what a line's roles are, when a line should do its
    work, what a role means, or whether a run's output was any good. It declares no
    roles, ships no schedule table, and carries no default declaration. What a box runs
    and when is the declaration's, which is the caller's, which is in git.

26. **Platforms: two bodies, and a refusal by name where there is none.** `darwin` is
    launchd, `linux` is systemd user units. On any other platform every verb that would
    write or load refuses at exit 2 with `DAEMON REFUSED reason=no_body`, naming the
    platform — the shape [SPEC-SANDBOX.md](SPEC-SANDBOX.md) already uses. Windows is
    named as owed work, not as a promise: a Windows Service through the service control
    protocol, or a logon-less scheduled task, when there is a box to build it against
    ([ideas#768](https://github.com/mas-bandwidth/ideas/issues/768)). `render` is the one
    exception: it renders **any** named platform's unit text on any platform, because
    rendering is a pure function of the declaration and a reader on a laptop must be able
    to read what a server will load. `render` therefore takes `--platform`, defaulting to
    the one it runs on.

---

## The verbs

```
nova-daemon render     --file <path> --out <dir> [--platform darwin|linux] [--prefix <s>] [--unit <name>]... [--line <name>]
nova-daemon install    --file <path> --units <dir> --state <dir> --logs <dir> --prefix <s> [--tool <name>]... [--unit <name>]... [--line <name>]
nova-daemon remove     --file <path> --units <dir> --state <dir> --prefix <s> [--unit <name>]... [--line <name>]
nova-daemon check      --file <path> --units <dir> --state <dir> --prefix <s> [--beat <duration>] [--when-tolerance <d>] [--unit <name>]... [--line <name>] [--max <n>]
nova-daemon status     --file <path> --units <dir> --state <dir> --prefix <s> [--unit <name>]... [--line <name>] [--max <n>]
nova-daemon logs       --file <path> --logs <dir> --unit <name> [--runs <n>] [--bytes <n>]
nova-daemon repin      --file <path> --units <dir> --state <dir> --prefix <s>
nova-daemon heartbeat  --file <path> --units <dir> --state <dir> --logs <dir> --prefix <s> [--tool <name>]...
nova-daemon run        --file <path> --unit <name> --state <dir> --logs <dir> [--sandbox <path>|none] [--secrets-exec <path>] [--secrets-arg <s>]...
nova-daemon version | help
```

`render` is the reader's verb and the only producer of unit text (rule 4).

`install` renders, writes each unit file, and loads it. **Idempotent**: a second run on
an unchanged declaration rewrites identical bytes and reloads, and says `unchanged` in
its line. A unit whose declaration changed is booted out **before** the file changes and
bootstrapped **after** — never both loaded and disagreeing, the order
[SPEC-SECRETS.md](SPEC-SECRETS.md) fixes for the same reason.

`remove` boots the unit out, removes its unit files, and **leaves the state and the
logs**, which are the record of what it did while it existed. A `--purge` flag removes
those too and is the only way they are deleted.

`check` is the gate and the value: read-only, exit 1 on any delta, cheap enough to run
from the heartbeat and from a nightly. `status` is the question: the same three facts,
one line per unit, **exit 0 even when a unit is dead**, because answering is its whole
job — the rule [SPEC.md](SPEC.md) states for `nova-fuse status`.

`logs` prints the tail of a unit's log, bounded, `--runs` back. It exists so that reading
a failure does not require knowing this tool's file layout.

`repin` is rule 17: called by the build, never installed.

`heartbeat` is one pass of the fast beat, and does exactly four things, in this order:
retry the owed acts of rule 20; repin what the loader reports killed or flagged stale,
skipping anything running; prune logs past `--log-days`; print one `BEAT` line. It is
`check` plus the repairs `check` is forbidden to make, and it never spawns a model, a
session or a harness — **the tool finds and repairs machinery; judgment costs tokens and
belongs to a mind that is called only when there is something to think about.**

`run` is what the unit file actually invokes. It is a supervisor, not a launcher: it
stamps, bounds, walls and reaps. **A unit never names the work's command directly**,
because a command invoked directly by the loader has no stamp, no deadline, no process
group and no log — which is the whole failure class this spec exists to close.

---

## Exit codes

`render`, `install`, `remove`, `check`, `status`, `logs`, `repin` and `heartbeat` use
[SPEC.md](SPEC.md)'s table unchanged: **0** ran and passed, **1** ran and said NO,
**2** could not run.

`run` cannot, for the reason [SPEC-SANDBOX.md](SPEC-SANDBOX.md) states: its exit status
belongs to the command it supervises, and a tool returning 2 for a bad flag would be
indistinguishable from a command that exited 2 on its own. It adopts that spec's numbers
verbatim, so that one nested launcher has one convention end to end:

| code | meaning |
|------|---------|
| 0–124 | the command's own exit status, passed through unchanged |
| 125 | `nova-daemon run` said **NO** before the command started: `RUN REFUSED` — the declaration is unreadable or the unit is not in it (`reason=no_unit`), a state or log directory that cannot be written (`reason=no_state`), a program refused by rule 6 (`reason=bad_program`), a write set refused by rule 11 (`reason=bad_write`), a deadline of zero or absent (`reason=no_deadline`), a sandbox or secrets tool named and not executable (`reason=no_tool`) |
| 126 | the command could not be executed and this tool was still there to say so |
| 127 | `argv[0]` did not resolve: `RUN REFUSED reason=not_found` |
| 128+N | the command was killed by signal `N` — including this tool's own deadline kill, which additionally prints `RUN TIMEOUT` |

The reservation is ambiguous exactly as it is in `env(1)`: a command that itself exits
125, 126 or 127 is indistinguishable by status alone. This tool's refusals always print
a `RUN REFUSED` line to stderr and a command's do not, so **a caller that needs to tell
them apart reads the line, not the number** — and never reads `run`'s exit status as a
check result.

---

## Output grammar

Every `OK` line goes to stdout; every `FAIL`, `REFUSED` and `TIMEOUT` line goes to
stderr. Every field carrying a path, a name, a reason or stored text is escaped by
`internal/oneline`, and every listing is capped and counted by `internal/bounded`
(`--max`, default 20, `0` means all).

```
RENDER OK platform=<darwin|linux> units=<n> files=<n> out=<dir>
RENDER FAIL <file>:<line>: <reason>

INSTALL UNIT name=<u> kind=<k> when=<w> state=<installed|unchanged|updated|owed>
INSTALL OK units=<n> installed=<n> unchanged=<n> owed=<n> prefix=<s>
INSTALL FAIL <u>: <reason>

REMOVE UNIT name=<u> state=<removed|absent|owed>
REMOVE OK units=<n> removed=<n> owed=<n>

DAEMON UNIT name=<u> line=<l> kind=<k> declared=<w> loaded=<w|-> fired=<rfc3339|never> verdict=<OK|NOT-INSTALLED|WRONG-WHEN|WRONG-BIN|MISSED|FAILED|TIMEOUT|KILLED|LEFTWORK|DEAD|OWED|UNKNOWN> reason=<one token|->
DAEMON OK units=<n> loaded=<n> fired=<n> ontime=<n> owed=<n> since=<rfc3339>
DAEMON FAIL units=<n> loaded=<n> fired=<n> ontime=<n> owed=<n> failed=<n> shown=<n> since=<rfc3339>
DAEMON FAIL <u>: <reason>
DAEMON UNDECLARED name=<u>
DAEMON REFUSED reason=<no_body|bad_file|bad_flag>: <text>

STATUS UNIT name=<u> installed=<true|false> up=<true|false|-> last=<rfc3339|never> exit=<n|-|killed> verdict=<…>
STATUS OK units=<n> shown=<n>

BEAT OK owed=<n> retried=<n> repinned=<n> skipped=<n> pruned=<n> failed=<n> beat=<duration>
BEAT FAIL <u>: <reason>

REPIN UNIT name=<u> state=<repinned|skipped-running|failed>
REPIN OK units=<n> repinned=<n> skipped=<n> failed=<n>

RUN START unit=<u> fired_for=<rfc3339|-> deadline=<duration> wall=<sandbox|none> env=<n> bin=<12 hex>
RUN OK unit=<u> exit=0 took=<duration> log=<path>
RUN FAIL unit=<u> exit=<n> took=<duration> log=<path>
RUN TIMEOUT unit=<u> deadline=<duration> killed=<group> log=<path>
RUN LEFTWORK unit=<u> exit=<n> group=<n> log=<path>
RUN REFUSED reason=<no_unit|no_state|bad_program|bad_write|no_deadline|no_tool|not_found>: <text>

LOGS OK unit=<u> runs=<n> bytes=<n> file=<path>

<TOKEN> NOTE <one remedy or gap clause>
<TOKEN> MORE kind=<unit|log> shown=<n> total=<t> <remedy>
```

`RUN START` is printed **before** the command begins, for the reason
[SPEC-SANDBOX.md](SPEC-SANDBOX.md)'s `SANDBOX OK` is: a log that ends in a crash still
says what the run was.

`env=<n>` is a **count** of the names the unit declared. The names appear in the stamp;
the values appear nowhere at all (rule 7).

`DAEMON OK`'s five numbers are the measure this tool is judged by, and they are the
whole answer to *is the unit layer working*:

> **units declared, units loaded, units fired, units fired on time — per day.**

`ontime=` counts units whose last firing fell inside `--when-tolerance` of a slot the
declaration names. `since=` is the window the counts cover. **The count line prints on
failure as well as success**: the counts are the truth about the state, not about the
output, so the listing is capped and the counting never is.

---

## The declaration file

The header, byte for byte, tab-separated:

```
name	line	kind	when	deadline	dir	write	env	command
```

One line per unit. A worked example — generic, and the paths are the caller's:

```
name	line	kind	when	deadline	dir	write	env	command
fold	alpha	role	daily 00:03	1h	/srv/alpha/work	/srv/alpha/work	API_KEY	/srv/alpha/bin/harness fold
inbox	alpha	daemon	always	24h	/srv/alpha/work	/srv/alpha/state	API_KEY,GH_TOKEN	/srv/alpha/bin/inbox serve
sweeper	alpha	consumer	every 60s	5m	/srv/alpha/work	/srv/alpha/queue	none	/srv/alpha/bin/sweeper once
weekly-eval	beta	role	weekly sun 16:33	3h	/srv/beta/work	/srv/beta/work	none	/srv/beta/bin/eval run
```

**`when` by kind, and a mismatch is a refusal naming both**: `role` takes `daily HH:MM`
or `weekly <dow> HH:MM`; `consumer` takes `every <duration>`, at least 10 seconds;
`daemon` takes `always` and nothing else. `HH:MM` is 24-hour, in the **box's local time**,
which is what both loaders schedule in — a declaration that means UTC says so by writing
UTC times and the box being on UTC, and this tool never converts, because a tool that
silently converted would move every unit twice a year.

**Minutes on the hour are the caller's business, not this tool's.** It offers no
scheduling advice and rejects no slot; it only refuses a `when` that does not parse, a
`when` that does not match the kind, and a file whose shortest interval is shorter than
the beat (rule 19).

---

## The state directory

```
<state>/<unit>/last.json      the most recent run, overwritten
<state>/<unit>/runs.tsv       append-only, pruned from the front to --keep-runs
<state>/owed.json             the owed acts of rule 20: unit -> {act, file, at}
```

`owed.json` is a map rather than a list because the retry needs the declaration file's
path and **must not guess it** from the unit's name; and an empty map removes the file
(rule 20). Nothing else lives here, so one glob finds the whole record of a box's unit
layer, and `remove` leaving it behind is deliberate: the record outlives the unit.

---

## What it deliberately does not do

- **It does not declare the fleet.** Which boxes exist, which lines live on them, which
  engines they run, and what the organization's repository policy is, belongs to
  `nova-admin` ([ideas#769](https://github.com/mas-bandwidth/ideas/issues/769)), which
  calls this tool as one of its hands and holds no copy of what it does.
- **It does not bring a line up.** Creating the box user, cloning a self, installing a
  harness, generating a key and resealing secrets, building the wall — that is
  `nova-run`, once, per line per box
  ([ideas#766](https://github.com/mas-bandwidth/ideas/issues/766)). `nova-run` finishes
  by calling `install` here so the line comes up on reboot without a login. **The
  boundary in one line: `nova-run` brings a line up once; `nova-daemon` keeps its units
  running; `nova-admin` declares the fleet.**
- **It owns no role.** The sleep cycle, the fold, the patrols, a swarm dispatcher, an
  engine's daemon — their content, their cadence and their meaning stay with whoever
  wrote them. This tool runs them the way it runs anybody's process.
- **It never decrypts, reads a key file, or holds a plaintext** (rule 7).
- **It never answers a prompt.** There is no `--yes`, no expect script, no TTY, and no
  code path that writes to a child's stdin. A permission prompt is a deadlock, and the
  answer to a deadlock is a wall, not a guard (rule 9).
- **It never writes to the bus, opens a pull request, or sends mail.** A finding is a
  line on stdout and an exit code; what to do about it is the caller's.
- **It never spawns a model or a session.** Mechanical work is machinery, at no token
  cost; a mind is woken by a caller reading `check`'s exit code, and only when there is
  something to think about.
- **It carries no default declaration, no role table and no box literal.** Nothing in
  this spec or this tool names a repository, a branch, a forge, a bench, a person or an
  organization as the only case. Our own setup is one instance of the shape.
- **It does not verify that the program is the *right* build**, only which build is
  running (rule 15). Whether a checkout is current with its remote belongs to whoever
  owns the checkout.
- **It does not supervise a unit's children beyond the process group** (rules 9, 10). A
  command that daemonizes itself out of its group is beyond this tool's reach, and
  `LEFTWORK` is the honest report rather than a silent success.

---

## Tests this spec demands

Every rule above is a test, and a check never seen failing is not a check — so each of
these is proved **red first**, against a real artifact, before it is proved green.

**The declaration**

1. A missing `--file` refuses with `refusing to guess`, exit 2, on every verb.
2. A header that differs from the spec's by one byte refuses naming line 1; a
   header-less file does **not** silently lose its first unit.
3. A line with eight or ten fields refuses naming the line number; an empty field
   refuses; `none` is accepted for `write` and `env` only.
4. A `command` with two adjacent spaces, a leading space or a trailing space refuses with
   the script remedy; a rendered unit for a well-formed command contains no shell.
5. `kind`/`when` mismatch refuses naming both, in all six wrong pairings.
6. A duplicate `name` refuses naming both lines.

**Rendering, both bodies**

7. `render --platform darwin` and `--platform linux` both run on **either** platform and
   produce fixture-identical bytes for a fixture declaration (golden files in
   `testdata/`), including the sorted environment.
8. A `role` renders `KeepAlive` false / no `RunAtLoad` on darwin and a `.timer` with
   `Persistent=true` on linux; a `daemon` renders `KeepAlive` true on darwin and
   `Restart=always` on linux; a `consumer` renders `StartInterval` and
   `OnUnitActiveSec`. Nine assertions, three kinds by three facts.
9. A `dir` containing `&`, `<`, `>` renders a plist a parser accepts; the same containing
   `%` renders a systemd unit whose value round-trips; a value with a newline refuses.
10. Two `render` runs on an unchanged file produce byte-identical output.

**Install and refusal**

11. Installing a program under a `go-build` segment refuses with the build-first remedy,
    **and writes no unit file** — proved by an empty output directory after the refusal.
12. Installing a program that is a dangling symlink, a directory, or non-executable
    refuses, each by its own reason.
13. `--tool` naming something absent from the installer's `PATH` refuses, exit 2, and the
    rendered environment for a present one contains the directory the lookup found and no
    literal from this repository.
14. A `write` set containing the declaration file, the units directory, the state
    directory, the log directory or the program refuses, five cases, each naming the
    path — **and the containment is proved with a symlink and with a case-differing
    spelling**, not only a string prefix.
15. `install` is idempotent: a second run reports `unchanged` and rewrites identical
    bytes; a changed declaration boots out before the file changes and bootstraps after,
    proved by a scripted loader seam recording the call order.

**Check, and the three facts**

16. A unit installed with one schedule and declared with another reports
    `WRONG-WHEN reason=loaded`, and the reported `loaded=` is read **back from the
    installed file**, not from what the test asked to be installed.
17. A stamp whose `started_at` is outside `--when-tolerance` of every declared slot
    reports `WRONG-WHEN reason=fired`; one inside reports `OK`; a `daemon` with a stamp
    at any hour reports `OK`.
18. A slot that came and went with no stamp reports `MISSED`; one with a non-zero exit
    reports `FAILED`; one with a loader kernel-kill reason and **no stamp at all**
    reports `KILLED` — the three are asserted to be three distinct verdicts for three
    fixtures that differ only in the record they leave.
19. A binary whose content hash differs from the stamp's and from the loaded unit's
    program reports `WRONG-BIN`, naming which disagrees.
20. Every unit in the file appears in the counts, including one that cannot be judged,
    which is `UNKNOWN` and makes the run **fail**. There is no flag, field or file that
    removes a unit from the count.
21. `DAEMON FAIL` prints the full count line as well as the findings — the failing-run
    count-line regression, pinned.
22. A listing above `--max` prints exactly `--max` item lines plus one `MORE` line naming
    the remedy; `--max 0` prints all; `--max -1` refuses.
23. `check` with `--beat` longer than the shortest declared interval fails with `BEAT`
    naming both durations; shorter passes.
24. A loaded unit carrying the declaration's prefix but absent from the file reports
    `UNDECLARED` and is **not** removed, repinned or touched.

**Run, the supervisor**

25. `run` writes the start stamp **before** the child starts — proved by a child that
    blocks until the test observes the stamp.
26. A command that reads stdin gets EOF immediately and does not block.
27. A command that outlives its deadline is `TERM`ed, then `KILL`ed after the grace, its
    **whole process group** dies (proved with a grandchild), and the stamp says
    `timed_out`, not `exit=0`.
28. A command that exits 0 leaving a live grandchild reports `LEFTWORK`, and its stamp
    carries `work_outstanding` — asserted to be a different verdict from `RUN OK`.
29. `run`'s exit status is the command's for 0, 1, 7 and 42; its own refusals are 125 and
    always carry a `RUN REFUSED` line on stderr.
30. The secrets tool is invoked with `--only` carrying exactly the declared names, in the
    declared order, and **no value of any of them appears** in the stamp, the log, the
    rendered unit, or any event line — asserted against a fixture whose values are
    distinctive strings grepped for across every artifact the run produced.
31. `--sandbox none` prints and stamps `wall=none`; a sandbox path that is not executable
    refuses at 125 before the command starts.
32. A stamp directory that cannot be written produces one loud line and **does not**
    change the command's exit status; a log that cannot be written likewise.
33. The log cap keeps the head and the tail with one `...+<dropped>B` mark, and the cut
    falls on a rune boundary.

**Repair**

34. `repin` on a unit the loader reports running **skips it, records it owed, and says
    so** — and a build of the tool with the record removed fails this test, which is the
    point of it.
35. The heartbeat retries an owed install and the installed unit carries the schedule the
    file holds **now**, specifically not the one recorded when the act was deferred.
36. An owed set emptied by a successful retry removes `owed.json` rather than writing an
    empty map.
37. The heartbeat prunes a log older than `--log-days` and reports the count; it prunes
    nothing under it.
38. Off darwin, `repin` prints one line and does nothing, exit 0.

**Platform**

39. On a platform with no body, every writing verb refuses `reason=no_body` naming the
    platform, exit 2, and `render` still works for both named platforms.
40. Every parser of loader output — the launchd last-exit pair, the stale-requirement
    property, the running pid, the systemd `Result`/`ExecMainStatus`/`ActiveState` —
    is **pure**, fed from captured fixtures in `testdata/`, and every branch is reachable
    from a test on a machine with neither loader installed. Matching is on the exact key
    before its separator, never a substring of the blob: both loaders nest whole
    sub-blocks and a `Contains` matches a neighbor.

---

## Open questions — each with a default, and the default stands unless answered

1. **Does `install` own the heartbeat unit, or does the caller?** Default: the caller
   points `--beat` at an existing `consumer` in the declaration (rule 19 prefers the beat
   that already exists), and `--beat-unit` installing one is the fallback. The risk of
   the default is a box with no fast beat at all and nothing saying so; `check`'s `BEAT`
   condition is the mitigation, and it is why rule 19 is a failure rather than a note.
2. **System-wide units, and root.** This spec is **per-user** throughout: launchd
   `gui/<uid>` agents and `systemd --user` units, nothing needing `sudo`, blast radius by
   account. A box-level unit that survives with no login (a linux `LaunchDaemon`
   equivalent, `loginctl enable-linger`, a system unit) is owed work, not v1 — and the
   linger question is real, because a `systemd --user` unit does **not** run after logout
   without it. Default for v1: `check` reports lingering as a fact in a `NOTE` and
   refuses nothing.
3. **Should `run` verify that the declaration it was handed is the one `install`
   rendered from?** A unit file naming a declaration path is a path, and the file at that
   path can change under it — which is a feature (rule 20's re-derivation) and a hazard
   (a run whose shape nobody installed). Default: no verification, and `check`'s
   declared-against-loaded comparison is where the drift surfaces. A recorded hash in the
   unit file would close it and would also make every declaration edit an uninstall.

---

*Draft 1, 2026-09-13. Not ratified: this document is a proposal until every named reader
of its pull request has said APPROVE against a head sha.*
