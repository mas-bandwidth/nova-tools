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
| the detector for that kill existed, worked, and ran **once a day**; it found all three a day late (2026-08-21) | the heartbeat is a **declared unit** whose period must be strictly shorter than the shortest declared schedule in the file, and `check` fails when there is none, when it is too slow, and when its own stamp says it has stopped beating (rule 19) |
| `repin` skipped a running job for a correct reason and then forgot; the job sat forty-three ticks un-repinned while every instrument read it healthy (2026-08-21) | a skip is **recorded as owed** in the state directory, and the heartbeat retries it; a skip with no record is refused (rule 20) |
| an install was refused over a live run, so one role kept a superseded schedule while every other role moved; it fired, stamped fresh, reported clean and exited 0 — **at the wrong time, forever** (2026-08-23) | `check` compares **declared against loaded against fired**, and `WRONG-WHEN` is a failure of its own (rule 13) |
| a deferred install re-instated the schedule it was meant to replace | a recorded owed act **re-derives its content at repair time** and never stores it (rule 20) |
| a green report said "all 9 stamped roles RAN-CLEAN" for twenty mornings while the weekly role had run twice in its life, because the list it counted deliberately excluded it (2026-08-30) | every unit in the declaration is counted in every count line; there is **no exemption list**, and a unit that cannot be judged is `UNKNOWN`, never absent (rule 14) |
| an unattended run called a gated tool, which did not fail — it **parked on a permission modal** for 3h14m and reported work it had not done (2026-07-22) | stdin is `/dev/null`, no unit may declare an unbounded deadline, and a deadline reached kills the **process group** and stamps `TIMEOUT`, a state of its own (rules 9, 10) |
| a launchd job was installed via `go run`; the plist named a build artifact the toolchain deletes on exit. The agent registered, reported healthy, and could never start again (2026-08-13) | `install` resolves the program through symlinks and **refuses a temporary-build path**, an absent file and a non-executable one (rule 6) |
| launchd handed a job a POSIX `PATH`; the first unattended night's commit came back `go: command not found` and the whole run blocked, silently, from the second night onward (2026-08-12) | the unit's environment is **declared, resolved at install time from the installer's own environment, and refused when a named tool is not found** (rule 8) |
| a spawned role had no HTTPS credential and hung a four-hour deadline on a clone prompt (2026-08-13) | rule 9 again: the deadline is the wall, and `RUN TIMEOUT` is distinguishable from `RUN FAIL` in the stamp and in `check` |
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

   - **A missed firing, and the two bodies do not behave alike.** `launchd.plist(5)` on
     `StartCalendarInterval`: *"Unlike cron which skips job invocations when the computer
     is asleep, launchd will start the job the next time the computer wakes up. If
     multiple intervals transpire before the computer is woken, those events will be
     coalesced into one event upon wake from sleep."* That is **sleep**; the man page says
     nothing about a machine that was powered off, and the coalescing means several missed
     slots arrive as one run. `systemd.timer(5)` on `Persistent=`: it catches up missed
     runs *"when the system was powered down"*, and it *"only has an effect on timers
     configured with `OnCalendar=`"*. So every rendered **`role`** timer carries
     `Persistent=true` — a `role` is the only kind rendered with `OnCalendar=`, and the key
     is inert anywhere else. A `consumer` catches nothing up on either platform: its
     `OnUnitActiveSec=` timer restarts from the next activation, and `launchd.plist(5)` on
     `StartInterval` says *"If the system is asleep during the time of the next scheduled
     interval firing, that interval will be missed due to shortcomings in kqueue(3). If
     the job is running during an interval firing, that interval firing will likewise be
     missed."* Draft 1 said the two bodies "catch up identically" and that launchd catches
     up after the machine was "asleep or off". Both were false, and a rule built on them
     turned every honest catch-up into a failure — which is what rules 13 and 19 now say
     instead of papering over the difference here.
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

6. **Two programs are resolved at install time, and three shapes are refused for each.**
   A unit file does not name the work's command: it names **this binary**, and this binary
   runs the command (rule 12, and `run` under The verbs). So an install resolves two
   absolute paths, not one, and both go through the same three refusals:
   - **the supervisor** — the `nova-daemon` the unit will invoke. It is the path
     `--self <path>` names; with no `--self` it is the installing process's own
     executable, with symlinks followed. It is a flag because a golden rendering must
     reproduce on a machine whose build lives at another path, and because a check nothing
     can make fail is not a check;
   - **the command** — `argv[0]` of the `command` field, with symlinks followed: a symlink
     retired later is a unit pointing at nothing.

   Refused, each by name, at exit 2, before anything is written:
   - a path that does not exist, is a directory, or carries no executable bit for the
     installing user;
   - a path with a directory segment beginning `go-build`, or any path the platform's
     temporary directory contains — **a temporary build artifact.** Installing from
     `go run` writes a unit naming a file the toolchain deletes the moment the command
     exits; the unit registers, the loader reports it happily at exit 0, and it can never
     start again. *That is the worst shape a failure takes here: a unit that is present,
     registered, and permanently dead*, invisible to every check that asks "is it
     installed?" rather than "did it run?" (measured 2026-08-13). Remedy: *build first and
     install the built binary*. **The supervisor is where that trap now springs**:
     `go run ./cmd/nova-daemon install ...` writes every unit in the file naming one
     temporary supervisor, so the failure that took one unit in 2026-08-13 would take all
     of them at once. That is why both paths are put through this list and not only the
     command's;
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

   **`HOME` is the first path of the unit's `write` set**, or `dir` when `write` is
   `none` — the same path rule 12 hands the wall, and never whatever the loader happened
   to have. [SPEC-SANDBOX.md](SPEC-SANDBOX.md) rule 9 refuses a run whose `HOME` resolves
   outside every `--write` at exit 125, `reason=home_outside`, *"because a wall that lets
   the job start and kills its first git command is the silent sandbox rule 1 exists to
   prevent"*. Draft 1 named the two variables and said where neither came from; with
   `HOME` taken from anywhere else, every walled unit in the worked example below refuses
   at 125 and nothing this spec describes ever runs.

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
   - a deadline reached is `TERM` to the group, then `KILL` after a grace of 10 seconds.

   **What the expiry means is the kind's, and for a `daemon` it is not a failure.** For a
   `role` and a `consumer` the run is stamped **`TIMEOUT`**, a third state beside success
   and failure, reported as such by `check` and `status`. A `daemon` is meant to be up:
   its `deadline` is a **recycle cadence** rather than a bound on one piece of work, the
   expiry stamps **`RECYCLED`**, `run` exits 0, and the loader's `KeepAlive` /
   `Restart=always` brings it straight back. Draft 1 had no such clause, so the worked
   example's `inbox ... daemon always 24h` — a daemon doing its job perfectly — was TERMed
   once a day, stamped `TIMEOUT`, and reported by `check` as a failure once a day, with no
   test covering it. **The floor does not move**: every unit still declares a finite
   bound, stdin is still `/dev/null`, and the kill is still the group's. Only the verdict
   changes, and it changes because for this kind the restart is the design and not the
   damage.

10. **A run that exits 0 with its process group still populated is a fourth state, and
    the supervisor clears it.** `RUN OK` requires an exit status of zero **and** an empty
    process group at exit. A command that exits cleanly having left work running behind
    it has delivered nothing and looks identical to success: four such runs on one bench
    were recorded as clean successes with real costs attached (measured, 2026-08). The
    stamp carries `work_outstanding`, `check` reports `LEFTWORK`, and the exit status
    alone is never the verdict.

    **Then `run` kills the group** — `TERM`, `KILL` after the same 10-second grace — and
    only then exits, reporting how many it killed. It must, because rule 9 put the command
    in a group of its own, and `launchd.plist(5)` says of `AbandonProcessGroup`: *"When a
    job dies, launchd kills any remaining processes with the same process group ID as the
    job."* The job is the supervisor; the leftover work is now in a different group; so
    the loader's own reaper no longer reaches it. Without this clause rule 9's group is a
    way to leak processes rather than a way to stop them.

11. **A unit never writes what governs it.** The `write` set is refused, at exit 2,
    naming the path, if it contains or is contained by: the declaration file; the unit
    output directory; the state directory; the log directory; or either program resolved
    in rule 6. A unit that can rewrite its own declaration, its own unit file, its own
    stamps or its own binary cannot be checked by anything, because every instrument that
    would catch it is inside its write set. **The record a unit produces must be outside
    the unit's reach**, which is also why the supervisor — not the command — writes every
    stamp and every log, outside the wall, before and after.

    **"Contains" is a filesystem question and is asked of the filesystem, here.** Both
    paths are resolved with `filepath.EvalSymlinks` and `filepath.Abs` — the resolution
    [SPEC-SANDBOX.md](SPEC-SANDBOX.md) rule 5 fixes for its own inside-ness question — and
    then the candidate's ancestors are walked to the root, each compared with
    `os.SameFile`. Identity, not text: a string prefix answers neither a symlinked parent
    nor a case-differing spelling of one directory on a case-insensitive volume, and this
    spec's test list demands both. `EvalSymlinks` folds no case, so borrowing the other
    spec's predicate whole left half the test unanswerable.

    **One exemption, and it is a named unit rather than a list.** The heartbeat of rule 19
    is itself a declared unit, and repairing the state and unit directories is its whole
    job — so the unit whose command is this supervisor's `heartbeat` verb may carry the
    state directory and the unit output directory in its `write` set, and no other unit
    may. The exemption is safe for the one reason that matters: **it is judged from
    outside itself.** Its stamps are written by its own supervisor, before and after,
    outside its wall, and `check` reads them as it reads every other unit's — so a
    heartbeat that has stopped repairing is visible exactly where a unit inside its own
    write set would not be (rule 19, `BEAT reason=stalled`).

12. **The command runs behind a wall, or the declaration says so in the open.** `run`
    starts the command through the sandbox tool named by `--sandbox`, with `dir` as the
    working directory and `write` as the write set. A unit whose `write` is `none` runs
    with a write set of its `dir` alone. `--sandbox none` is accepted and is **recorded
    in every stamp and printed in every `RUN` line as `wall=none`**, because an
    unwalled unit is a fact a reader must be able to count, not a silence. **`dir` must
    itself be inside the `write` set** when `write` is not `none`, refused at load time
    naming both, because [SPEC-SANDBOX.md](SPEC-SANDBOX.md) refuses a `--cwd` outside the
    write set at 125 — and a unit that refuses on every run is a unit that never runs.

    **The wall and the secret tool are chosen at install time, not at run time**, because
    `run` is invoked by a unit file and a unit file's argv is written by `render`. So
    `--sandbox`, `--secrets-exec` and `--secrets-arg` are flags of `render`, `install` and
    `heartbeat` as well; what they are given is rendered into the unit's argv verbatim and
    read back by `check`. Draft 1 put them on `run` alone: every installed unit would have
    run unwalled, with no secrets, stamping a `wall=none` no caller had chosen, and no
    test could have caught it because the tests drove `run` directly. `--sandbox` is
    therefore **required** on `render`, `install` and `heartbeat`, refused at exit 2 with
    `reason=no_tool` when absent: the word for "no wall" is `--sandbox none`, typed by
    somebody, and it prints on every line that unit ever writes.

13. **`check` compares three facts, and `WRONG-WHEN` is the comparison it can prove.**
    For every unit in the declaration, `check` establishes:
    - **DECLARED** — what the file says;
    - **LOADED** — what the init system actually holds, read back from the installed unit
      file and from the loader (`launchctl print`, `systemctl --user show`), never from
      this tool's memory of what it once wrote;
    - **FIRED** — the last run's stamp, and the loader's own last-exit fact.

    A role on a superseded schedule is invisible to every other instrument: *it fires, it
    writes a stamp, its stamp is fresh, the loader reports exit 0, its program exists —
    and it runs at the wrong time, forever, and the only witness is a diff between two
    numbers that nothing compares* (measured 2026-08-23). **`WRONG-WHEN` is that diff:
    the declared schedule against the loaded schedule**, a failure, exit 1, with both
    `declared=` and `loaded=` on the line so the reader sees the two numbers.

    **A start time is a count, not a verdict — this is a rule that shrank.** Draft 1 added
    a second comparison: a run whose `started_at` was further from the nearest declared
    slot than `--when-tolerance` was `WRONG-WHEN reason=fired`. It contradicted rule 5.
    Every honest catch-up after a sleep or a power-down starts hours from its slot, and
    `Persistent=true` and launchd's wake behavior exist to produce exactly that run — so
    the tool would have reported the loader working as the loader broken, on a schedule.
    The second comparison is **deleted**, not reconciled, and what it reached for survives
    as the measure:
    - a run whose `started_at` is inside `--when-tolerance` (default 2 minutes) of the
      slot its stamp names in `fired_for` is counted in `ontime=`;
    - a run outside it is verdict **`CAUGHT-UP`**, counted in `caughtup=`, exit 0. It is
      printed, so a box that spends its life catching up is visible; it is not a failure,
      because rule 5 is what produced it and the loader was right;
    - a `daemon` has no slots: `fired_for` is `-` and it is in neither count;
    - a `consumer` has no slots either. Its liveness question is a gap rather than a
      phase: two consecutive stamps more than **twice** its declared `every <d>` apart is
      `MISSED` (rule 16), and it is in neither `ontime=` nor `caughtup=`.

14. **There is no exemption list.** Every unit the file declares is judged and counted in
    every count line. A unit this run could not judge is `UNKNOWN` with a reason, which
    is a failure, not an absence. **An exemption written down is honest, and honesty is
    not coverage**: one bench's morning report said *"all 9 stamped roles RAN-CLEAN"* for
    twenty consecutive mornings while a tenth role, deliberately omitted from the counted
    list with a comment explaining why, had run twice in its life (measured 2026-08-30).
    The comment was read once, by its author; the green verdict was read every morning by
    everyone. A caller who wants a narrower question asks it with `--unit` or `--line`,
    and the count line then says what the filter was.

15. **`check` says which build is running. It does not say whether that is the right
    build, and it does not fail for a rebuild.** For every unit it reports the command's
    `argv[0]` hashed on disk now, the `bin_sha256` the stamp recorded for the last run,
    and the supervisor the loaded unit actually invokes (rule 6) hashed against the
    supervisor on disk now. Content, never mtime.

    - **disk differs from stamp** — `BIN-MOVED`, an informational `NOTE` at exit 0. It is
      the darwin **repin trigger**: rule 17's kill happens because a rebuild moved the
      hash the loader pinned, so this is the one signal available *before* the kill
      instead of after it (measured 2026-08-21).
    - **the loaded program is gone** — absent now, not a regular file, or carrying no
      executable bit. That is `WRONG-BIN`, a failure, exit 1: rule 6's refusal caught
      after the fact, on a unit the loader still reports happily. It is the only case
      here worth an exit code.

    Draft 1 made **any** disagreement among the three `WRONG-BIN`, a failure, and claimed
    it saw *"a deployment that never happened"* — the 2026-09-07 record, where a checkout
    nobody pulled left the units eighteen commits behind. It did not. In that record all
    three hashes **agree**, because nothing was pulled and nothing was rebuilt, so the
    check was green exactly when the failure was present; and it went red after every
    legitimate rebuild until each unit had run once, which for a weekly role is a week of
    red. A check that is green when it should be red is worse than no check, and its row
    has been struck from the table above. What 2026-09-07 needs is a comparison against a
    remote, which belongs to whoever owns the checkout and is named below under what this
    tool deliberately does not do.

16. **A skipped act, a killed run and a never-fired slot are three different lines.**
    The stamp exists so that they can be told apart:
    - **never fired** — a slot inside the window `--since` names came and went and no row
      of `runs.tsv` carries it in `fired_for`. `MISSED`. The window is a flag and the row
      is `runs.tsv`'s, not `last.json`'s, which is overwritten and can answer for one run
      only; without both, the verdict had no way to be computed at all. A `consumer` has
      no slots, so for it `MISSED` is the gap of rule 13.
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

19. **A repair must run more often than the failure it repairs, and the beat is a unit
    like any other.** A check that runs once a day cannot bound a failure that can arrive
    at any hour: the detector for the 2026-08-21 kill existed, worked, and found all three
    roles **a day late** — and a day late was the whole finding, because in that day the
    work was not done. *Nothing about the detector was wrong. Its clock was wrong, and a
    clock is not a detail.*

    So the heartbeat is **one `consumer` declared in the file**, whose `command` is this
    supervisor's `heartbeat` verb. It is installed, walled, bounded, stamped and judged
    exactly like every other unit, with rule 11's one exemption for its write set. There
    is no `--beat` flag and no `--beat-unit` flag. Draft 1 had both, and both were
    unbuildable: a heartbeat unit installed outside the declaration is `UNDECLARED` under
    rule 21 and its write set is refused under rule 11, and a `--beat` duration a caller
    types asserts a period that no unit on the box need actually have.

    `check` therefore takes the beat from the declaration and from the record, and fails,
    exit 1, on any of three:
    - **`BEAT reason=no_beat`** — the file declares no heartbeat unit at all. Draft 1
      named this exact hole as the risk of leaving the beat to the caller and offered an
      optional flag as the mitigation. An optional mitigation for a named hole is a green
      report;
    - **`BEAT reason=too_slow`**, naming both durations — the shortest `every <d>` among
      the declared heartbeat units is not **strictly less than** the shortest interval
      between two consecutive firings of any unit in the file;
    - **`BEAT reason=stalled`**, naming both timestamps — the heartbeat unit's own last
      stamp is older than twice its declared interval. **A declared number is not a number
      the platform honors.** `launchd.plist(5)` on `StartInterval`: *"If the system is
      asleep during the time of the next scheduled interval firing, that interval will be
      missed due to shortcomings in kqueue(3). If the job is running during an interval
      firing, that interval firing will likewise be missed."* A consumer therefore skips
      across sleep and skips whenever the previous tick overran — and the whole repair
      story of rules 17, 18 and 20 rests on this one clock. So the spec asks whether the
      beat *did* beat, and not whether somebody typed a small enough duration.

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
    The stamp carries: `unit`, `line`, `kind`, `fired_for` (the slot the declaration names
    that this run belongs to, or `-` for a `daemon` and for a `consumer`), `started_at`,
    `ended_at`, `exit`, `signal`, `timed_out`, `recycled`, `work_outstanding`,
    `deadline_sec`, `wall`, `bin_sha256`, `self_sha256`, `file_sha256`, `booted_at`,
    `log`, and `env_names` — **names, never values** (rule 7).

    `self_sha256` is the supervisor that actually ran: the fourth fact rule 15 needs, and
    the one `repin` is about. `file_sha256` is the declaration as this run read it, and it
    lives in the stamp rather than in the unit file for the reason open question 3 gave
    and a read confirmed — a hash recorded in the **unit** makes every edit of the
    declaration an uninstall, while a hash in the **stamp** surfaces the same drift in
    `check`, as a `NOTE`, and costs nothing. `booted_at` is the box's boot time read from
    the kernel, which is how a reader tells a `CAUGHT-UP` run after a power-down from one
    after a sleep — the two the platforms report differently (rule 5).

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

    **The supervisor's own lines have a destination too, and it is a different file.**
    Every unit is rendered with `StandardOutPath` and `StandardErrorPath` on darwin, and
    `StandardOutput=append:` and `StandardError=append:` on linux, pointing at
    `<logs>/<unit>.daemon.log`. A `RUN REFUSED` the loader drops on the floor is the same
    silence as no line at all — and `RUN START` is printed before the command begins
    precisely so that a log ending in a crash still says what the run was.

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
    launchd, `linux` is systemd user units. On a platform that is **neither** — windows
    included — every verb that would write or load refuses at exit 2 with
    `DAEMON REFUSED reason=no_body`, naming the platform: the shape
    [SPEC-SANDBOX.md](SPEC-SANDBOX.md) already uses. Windows is
    named as owed work, not as a promise: a Windows Service through the service control
    protocol, or a logon-less scheduled task, when there is a box to build it against
    ([ideas#768](https://github.com/mas-bandwidth/ideas/issues/768)). `render` is the one
    exception: it renders **any** named platform's unit text on any platform, because
    rendering is a pure function of the declaration and a reader on a laptop must be able
    to read what a server will load. `render` therefore takes `--platform`, defaulting to
    the one it runs on.

    **A supported platform is never `no_body`, and a verb with nothing to do there is a
    different fact.** `repin` closes a darwin-only failure (rule 17), so on linux it
    prints one `REPIN OK units=<n> repinned=0` line and exits 0. Draft 1 said that and
    also said every writing verb refuses `no_body` off darwin; on linux the two could not
    both be true. Only one was ever meant: `no_body` is about a platform this tool cannot
    drive, never about a verb with no work to do on a platform it can.

---

## The verbs

```
nova-daemon render     --file <path> --out <dir> --prefix <s> --sandbox <path|none> [--self <path>] [--secrets-exec <path>] [--secrets-arg <s>]... [--platform darwin|linux] [--unit <name>]... [--line <name>]
nova-daemon install    --file <path> --units <dir> --state <dir> --logs <dir> --prefix <s> --sandbox <path|none> [--self <path>] [--secrets-exec <path>] [--secrets-arg <s>]... [--tool <name>]... [--unit <name>]... [--line <name>] [--max <n>]
nova-daemon remove     --file <path> --units <dir> --state <dir> --prefix <s> [--purge] [--unit <name>]... [--line <name>] [--max <n>]
nova-daemon check      --file <path> --units <dir> --state <dir> --prefix <s> [--when-tolerance <d>] [--since <duration>] [--unit <name>]... [--line <name>] [--max <n>]
nova-daemon status     --file <path> --units <dir> --state <dir> --prefix <s> [--since <duration>] [--unit <name>]... [--line <name>] [--max <n>]
nova-daemon logs       --file <path> --logs <dir> --unit <name> [--runs <n>] [--bytes <n>]
nova-daemon repin      --file <path> --units <dir> --state <dir> --prefix <s> [--max <n>]
nova-daemon heartbeat  --file <path> --units <dir> --state <dir> --logs <dir> --prefix <s> --sandbox <path|none> [--self <path>] [--secrets-exec <path>] [--secrets-arg <s>]... [--tool <name>]... [--log-days <n>] [--max <n>]
nova-daemon run        --file <path> --unit <name> --state <dir> --logs <dir> --sandbox <path|none> [--secrets-exec <path>] [--secrets-arg <s>]...
nova-daemon version | help
```

`render` is the reader's verb and the only producer of unit text (rule 4). **`--prefix` is
required on it**, not optional as in draft 1: the label is half of what a unit file is,
and a rendering carrying a different label from the installed unit is precisely the drift
rule 4 exists to let a reader catch.

`--sandbox`, `--secrets-exec` and `--secrets-arg` on `render`, `install` and `heartbeat`
are the **unit's** argv, not this invocation's (rule 12): they are rendered into the unit
file verbatim, read back by `check`, and handed to `run` by the unit file at every firing.
`--secrets-arg` is repeated once per argument the secret tool needs ahead of its `--only`
— its store, its identity, its key, its `sops`, which [SPEC-SECRETS.md](SPEC-SECRETS.md)
spells and which are the caller's paths, so this tool passes them through and interprets
none of them. Draft 1 used the flag in `run`'s grammar and defined it nowhere.

`--self <path>` names the supervisor the unit will invoke; with no flag it is the
installing process's own executable (rule 6).

`install` renders, writes each unit file, and loads it. **Idempotent**: a second run on
an unchanged declaration rewrites identical bytes and reloads, and says `unchanged` in
its line. A unit whose declaration changed is booted out **before** the file changes and
bootstrapped **after** — never both loaded and disagreeing, the order
[SPEC-SECRETS.md](SPEC-SECRETS.md) fixes for the same reason.

**`--units <dir>` stays required, and `check` says whether the loader will read it
again.** A plist bootstrapped from an arbitrary path is live now and gone at the next
login: launchd re-reads `~/Library/LaunchAgents` and nothing else. On linux
`systemctl --user enable <path>` creates symlinks into `~/.config/systemd/user`, so any
directory works. That asymmetry is rule 6's failure one level up — present, registered,
and dead after the next reboot — so `check` reports `NOT-PERSISTENT`, exit 1, naming the
directory this platform's loader does re-read. The flag is not made optional to fix it:
this tool guesses no paths, and a verdict is not a default.

`remove` boots the unit out, removes its unit files, and **leaves the state and the
logs**, which are the record of what it did while it existed. A `--purge` flag removes
those too and is the only way they are deleted.

`check` is the gate and the value: read-only, exit 1 on any delta, cheap enough to run
from the heartbeat and from a nightly. **`--since <duration>` is the window** its counts
and its `MISSED` verdict cover, resolved against `runs.tsv`'s `fired_for` rather than
against `last.json`, which is overwritten and can answer for one run only. It defaults to
one period of the unit being judged: the interval between the two most recent slots the
declaration names, or the declared `every <d>` for a consumer. Draft 1 printed `since=` on
every count line with no flag and no sentence producing it.

On linux, `check` prints one **mandatory** `NOTE linger=<true|false>` for the installing
user, read from `loginctl`. It is a fact rather than a verdict: a `systemd --user` unit
does not run after logout without lingering, so a box meant to be unattended needs it —
and whether this box means that is the caller's, while whether it has it is this tool's to
report on every run rather than when somebody remembers to ask. Draft 1 made the note
conditional, which is the one shape a silently dead unit hides in.

`status` is the question: the same three facts, one line per unit, **exit 0 even when a
unit is dead**, because answering is its whole job — the rule [SPEC.md](SPEC.md) states
for `nova-fuse status`.

`logs` prints the tail of a unit's log, bounded, `--runs` back. It exists so that reading
a failure does not require knowing this tool's file layout.

`repin` is rule 17: called by the build, never installed. On linux it prints one line,
does nothing, and exits 0 (rule 26).

`heartbeat` is one pass of the fast beat, and does exactly four things, in this order:
retry the owed acts of rule 20; repin what the loader reports killed or flagged stale,
skipping anything running; prune logs past `--log-days`; print one `BEAT` line. It is
`check` plus the repairs `check` is forbidden to make, and it never spawns a model, a
session or a harness — **the tool finds and repairs machinery; judgment costs tokens and
belongs to a mind that is called only when there is something to think about.** It is
itself a declared `consumer` in the file (rule 19), so it is installed, walled, bounded
and stamped like anything else, and its own stalling is a `check` failure.

`run` is what the unit file actually invokes. It is a supervisor, not a launcher: it
stamps, bounds, walls and reaps. **A unit never names the work's command directly**,
because a command invoked directly by the loader has no stamp, no deadline, no process
group and no log — which is the whole failure class this spec exists to close.

The consequence of that is load-bearing and draft 1 left it unstated: **the program every
unit loads is this binary**, so on darwin the loader pins its code requirement to
`nova-daemon` and not to the work. One rebuild of the supervisor invalidates every unit in
the file at once; a rebuild of a work binary invalidates none of them. That is the shape
rule 17 is written against, and it is why `repin` is unconditional rather than
per-unit-clever, and why rule 15 compares the command's binary through the stamp rather
than through the loaded program, which is never the command's.

`run` **skips the secret tool entirely when the unit's `env` is `none`.** It does not
invoke it with an empty list: [SPEC-SECRETS.md](SPEC-SECRETS.md) refuses a missing
`--only`, so a wrapper that turned "no secrets" into an empty `--only` would refuse every
unit that needs no secrets.

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
| 71 | **darwin only, and not this tool's.** The sandbox backend's own exec failure, which [SPEC-SANDBOX.md](SPEC-SANDBOX.md) records as the number that platform returns where the other two return 126: *"On `darwin` the backend's own exec failure is 71 and the tool cannot see it"*. Every walled command starts through that tool, so 71 reaches a caller of `run` too. It is listed here rather than left to be discovered, because on darwin row 126 below is otherwise unreachable through the wall |
| 125 | `nova-daemon run` said **NO** before the command started: `RUN REFUSED` — the declaration is unreadable or the unit is not in it (`reason=no_unit`), a state or log directory that cannot be written (`reason=no_state`), a program refused by rule 6 (`reason=bad_program`), a write set or a `dir` refused by rules 11 and 12 (`reason=bad_write`), a deadline of zero or absent (`reason=no_deadline`), a sandbox or secrets tool named and not executable, or `--sandbox` absent altogether (`reason=no_tool`) |
| 126 | the command could not be executed and this tool was still there to say so |
| 127 | `argv[0]` did not resolve: `RUN REFUSED reason=not_found` |
| 128+N | the command was killed by signal `N` — including this tool's own deadline kill, which additionally prints `RUN TIMEOUT` for a `role` or a `consumer` and `RUN RECYCLED` for a `daemon` (rule 9) |

The reservation is ambiguous exactly as it is in `env(1)`: a command that itself exits
71, 125, 126 or 127 is indistinguishable by status alone. This tool's refusals always
print a `RUN REFUSED` line to stderr and a command's do not, so **a caller that needs to
tell them apart reads the line, not the number** — and never reads `run`'s exit status as
a check result.

**A line `run` did not write, `run` does not rewrite.** 125 now carries three tools'
refusals: this one's `RUN REFUSED`, the sandbox's `SANDBOX REFUSED`, the secret tool's
`SECRETS REFUSED`. "Read the line" only tells them apart if the inner line survives, so
`run` forwards it to stderr **verbatim** — unwrapped, unprefixed, unsummarized — and adds
no line of its own for a refusal that was not its own.

---

## Output grammar

Every `OK` line goes to stdout; every `FAIL`, `REFUSED` and `TIMEOUT` line goes to
stderr. Every field carrying a path, a name, a reason or stored text is escaped by
`internal/oneline`, and every listing is capped and counted by `internal/bounded`
(`--max`, default 20, `0` means all). **Every verb that prints one line per unit takes
`--max`** — `install`, `remove`, `repin` and `heartbeat` included, which draft 1 left
uncapped against [SPEC.md](SPEC.md)'s Conventions: *"Every verb that prints one finding
per unit of state takes a `--fail-max <n>` ... or `--max <n>` ... defaulting to 20."* An
estate is exactly the size at which that rule was written.

```
RENDER OK platform=<darwin|linux> units=<n> files=<n> out=<dir>

INSTALL UNIT name=<u> kind=<k> when=<w> state=<installed|unchanged|updated|owed>
INSTALL OK units=<n> installed=<n> unchanged=<n> owed=<n> shown=<n> prefix=<s>
INSTALL FAIL <u>: <reason>

REMOVE UNIT name=<u> state=<removed|absent|owed>
REMOVE OK units=<n> removed=<n> owed=<n> shown=<n>

DAEMON UNIT name=<u> line=<l> kind=<k> declared=<w> loaded=<w|-> fired=<rfc3339|never> verdict=<OK|CAUGHT-UP|NOT-INSTALLED|NOT-PERSISTENT|WRONG-WHEN|WRONG-BIN|MISSED|FAILED|TIMEOUT|RECYCLED|KILLED|LEFTWORK|DEAD|OWED|UNKNOWN> reason=<one token|->
DAEMON OK units=<n> loaded=<n> fired=<n> ontime=<n> caughtup=<n> owed=<n> since=<rfc3339>
DAEMON FAIL units=<n> loaded=<n> fired=<n> ontime=<n> caughtup=<n> owed=<n> failed=<n> shown=<n> since=<rfc3339>
DAEMON FAIL <u>: <reason>
DAEMON UNDECLARED name=<u>
DAEMON REFUSED reason=<no_body|bad_file|bad_flag|no_tool>: <text>

STATUS UNIT name=<u> installed=<true|false> up=<true|false|-> last=<rfc3339|never> exit=<n|-|killed> verdict=<...>
STATUS OK units=<n> shown=<n>

BEAT OK owed=<n> retried=<n> repinned=<n> skipped=<n> pruned=<n> failed=<n> shown=<n> beat=<duration>
BEAT FAIL reason=<no_beat|too_slow|stalled>: <text>
BEAT FAIL <u>: <reason>

REPIN UNIT name=<u> state=<repinned|skipped-running|failed>
REPIN OK units=<n> repinned=<n> skipped=<n> failed=<n> shown=<n>

RUN START unit=<u> fired_for=<rfc3339|-> deadline=<duration> wall=<sandbox|none> env=<n> bin=<12 hex> self=<12 hex>
RUN OK unit=<u> exit=0 took=<duration> log=<path>
RUN FAIL unit=<u> exit=<n> took=<duration> log=<path>
RUN TIMEOUT unit=<u> deadline=<duration> killed=<group> log=<path>
RUN RECYCLED unit=<u> cadence=<duration> killed=<group> log=<path>
RUN LEFTWORK unit=<u> exit=<n> group=<n> killed=<n> log=<path>
RUN REFUSED reason=<no_unit|no_state|bad_program|bad_write|no_deadline|no_tool|not_found>: <text>

LOGS OK unit=<u> runs=<n> bytes=<n> file=<path>

<TOKEN> NOTE <one remedy or gap clause>
<TOKEN> MORE kind=<unit|log> shown=<n> total=<t> <remedy>
```

**A bad declaration file is a refusal, on every verb, with one spelling.** Draft 1 gave
`render` a `RENDER FAIL <file>:<line>` line at exit 2 — a `FAIL` token on a run that could
not run — while the tests that pin it called the same event a refusal. There is one
spelling: `DAEMON REFUSED reason=bad_file: <file>:<line>: <text>`, exit 2, from `render`
as from every other verb.

`BIN-MOVED` and `linger=` are `NOTE` lines and not verdicts (rules 15 and the verbs
above): informational, on stdout, and never the reason a run exits 1.

`RUN START` is printed **before** the command begins, for the reason
[SPEC-SANDBOX.md](SPEC-SANDBOX.md)'s `SANDBOX OK` is: a log that ends in a crash still
says what the run was.

`env=<n>` is a **count** of the names the unit declared. The names appear in the stamp;
the values appear nowhere at all (rule 7).

`DAEMON OK`'s numbers are the measure this tool is judged by, and they are the
whole answer to *is the unit layer working*:

> **units declared, units loaded, units fired, units fired on time — per day.**

`ontime=` counts units whose last firing fell inside `--when-tolerance` of the slot its
own stamp names. `caughtup=` counts the ones that fired late because the box was asleep or
off — the loader working (rule 5), printed rather than hidden, and not a failure.
`since=` is the window the counts cover, and it is `--since`, a flag, rather than a number
the tool invented. **The count line prints on failure as well as success**: the counts are
the truth about the state, not about the output, so the listing is capped and the counting
never is.

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
inbox	alpha	daemon	always	24h	/srv/alpha/work	/srv/alpha/work,/srv/alpha/state	API_KEY,GH_TOKEN	/srv/alpha/bin/inbox serve
sweeper	alpha	consumer	every 60s	5m	/srv/alpha/work	/srv/alpha/work,/srv/alpha/queue	none	/srv/alpha/bin/sweeper once
beat	alpha	consumer	every 30s	2m	/srv/alpha/beat	/srv/alpha/beat,/srv/alpha/state,/srv/alpha/units	none	/srv/alpha/bin/nova-daemon heartbeat --file /srv/alpha/units.tsv --units /srv/alpha/units --state /srv/alpha/state --logs /srv/alpha/logs --prefix com.example.alpha --sandbox none
weekly-eval	beta	role	weekly sun 16:33	3h	/srv/beta/work	/srv/beta/work	none	/srv/beta/bin/eval run
```

**`when` by kind, and a mismatch is a refusal naming both**: `role` takes `daily HH:MM`
or `weekly <dow> HH:MM`; `consumer` takes `every <duration>`, at least 10 seconds;
`daemon` takes `always` and nothing else. `HH:MM` is 24-hour, in the **box's local time**,
which is what both loaders schedule in — a declaration that means UTC says so by writing
UTC times and the box being on UTC, and this tool never converts, because a tool that
silently converted would move every unit twice a year.

**`dir` is inside `write`, and `write`'s first path is `HOME`** (rules 8 and 12). Draft 1's
example gave `inbox` and `sweeper` a `dir` their write sets did not contain, which the wall
refuses at 125 before the command starts: a worked example that cannot run is a worked
example nobody ran.

**`deadline` means one thing for a `role` and a `consumer` and another for a `daemon`**:
a bound on one piece of work for the first two, a **recycle cadence** for the third (rule
9). `inbox ... always 24h` above is a daemon restarted once a day and stamped `RECYCLED`
at exit 0 — not, as draft 1's rule 9 had it, a healthy daemon killed once a day and
reported as a failure once a day.

**The `beat` row is the heartbeat** (rule 19): an ordinary `consumer` whose command is
this tool's own `heartbeat` verb, faster than the fastest thing it repairs, carrying the
state and unit directories in its write set under rule 11's one exemption, installed and
stamped and judged like every other row. A file with no such row fails `check` with
`BEAT reason=no_beat`.

**Minutes on the hour are the caller's business, not this tool's.** It offers no
scheduling advice and rejects no slot; it only refuses a `when` that does not parse, a
`when` that does not match the kind, a `dir` outside its own `write` set, and a file whose
fastest heartbeat is not strictly faster than its shortest interval (rule 19).

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
  ([ideas#766](https://github.com/mas-bandwidth/ideas/issues/766)). `nova-run` finishes by
  calling `install` here. Draft 1 added "so the line comes up on reboot without a login",
  and that is not what per-user units do: a launchd `gui/<uid>` agent needs the user's
  session, and a `systemd --user` unit needs lingering enabled (open question 2). The
  clause is struck; what survives it is `check`'s mandatory linger note and its
  `NOT-PERSISTENT` verdict, which report the truth instead of asserting it. **The boundary
  in one line: `nova-run` brings a line up once; `nova-daemon` keeps its units running;
  `nova-admin` declares the fleet.**
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
  running (rule 15). Whether a checkout is current with its remote belongs to whoever owns
  the checkout. **The 2026-09-07 record is named here rather than in the table above**: a
  checkout nobody pulled left the units eighteen commits behind the default branch, and no
  comparison this tool can make sees it, because on that box the program on disk, the
  program in the stamp and the program the unit loads all agreed. Draft 1 claimed
  `WRONG-BIN` closed it. A rule that claims a hurt it does not close is worse than a hurt
  with no rule, because the next person stops looking.
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
   header-less file does **not** silently lose its first unit. The refusal is
   `DAEMON REFUSED reason=bad_file`, exit 2, on `render` as on every other verb — there is
   no `RENDER FAIL` line to assert.
3. A line with eight or ten fields refuses naming the line number; an empty field
   refuses; `none` is accepted for `write` and `env` only.
4. A `command` with two adjacent spaces, a leading space or a trailing space refuses with
   the script remedy; a rendered unit for a well-formed command contains no shell.
5. `kind`/`when` mismatch refuses naming both, in all six wrong pairings.
6. A duplicate `name` refuses naming both lines.
7. A `dir` outside the unit's own `write` set refuses naming both (rule 12); a `write` of
   `none` with any `dir` is accepted.

**Rendering, both bodies**

8. `render --platform darwin` and `--platform linux` both run on **either** platform and
   produce fixture-identical bytes for a fixture declaration (golden files in
   `testdata/`), including the sorted environment. `--self` and `--sandbox` are pinned to
   fixture paths, because the supervisor's path is rendered into every unit and an
   unpinned one makes the goldens machine-dependent. **The goldens are hand-written and
   reviewed in the pull request that adds them**, never generated by the code under test:
   a golden a program wrote asserts only that the program is deterministic.
9. A `role` renders `KeepAlive` false and `RunAtLoad` false on darwin — both keys present,
   both false, which is what the table of kinds says — and a `.timer` with `OnCalendar=`
   and `Persistent=true` on linux; a `daemon` renders `KeepAlive` true on darwin and
   `Restart=always` on linux; a `consumer` renders `StartInterval` on darwin and
   `OnUnitActiveSec=` **with no `Persistent=`** on linux, the key having no effect off
   `OnCalendar=` (rule 5). Nine assertions, three kinds by three facts.
10. Every rendered unit's argv is `<self> run --file ... --unit ... --state ... --logs ...
    --sandbox ...`, carrying the `--secrets-exec` and every `--secrets-arg` the install was
    given, asserted **against the installed file** and not against what the test passed in.
    A unit rendered with `--sandbox none` says so in its argv; one rendered with a sandbox
    path names it.
11. Every rendered unit names `<logs>/<unit>.daemon.log` for the supervisor's own stdout
    and stderr (`StandardOutPath`/`StandardErrorPath`, `StandardOutput=append:`).
12. A `dir` containing `&`, `<`, `>` renders a plist a parser accepts; the same containing
    `%` renders a systemd unit whose value round-trips; a value with a newline refuses.
13. Two `render` runs on an unchanged file produce byte-identical output.

**Install and refusal**

14. Installing a program under a `go-build` segment refuses with the build-first remedy,
    **and writes no unit file** — proved by an empty output directory after the refusal.
    The same refusal fires for a **supervisor** under a `go-build` segment, whether it came
    from `--self` or from the installing executable: that one takes every unit in the file,
    not one (rule 6).
15. Installing a program that is a dangling symlink, a directory, or non-executable
    refuses, each by its own reason — asserted for the command and for the supervisor.
16. `--tool` naming something absent from the installer's `PATH` refuses, exit 2, and the
    rendered environment for a present one contains the directory the lookup found and no
    literal from this repository.
17. The rendered `HOME` is the first path of the unit's `write` set, and `dir` when `write`
    is `none` (rule 8); a run whose `HOME` would fall outside the wall's write set is
    asserted **not** to happen for any unit the declaration accepts.
18. A `write` set containing the declaration file, the units directory, the state
    directory, the log directory, the command or the supervisor refuses, six cases, each
    naming the path — **and the containment is proved with a symlinked parent and with a
    case-differing spelling on a case-insensitive volume**, against the `os.SameFile`
    ancestor walk of rule 11 and not a string prefix. The heartbeat unit of rule 19 is the
    one unit for which the state and units directories are accepted, and a second unit
    declaring the same write set is refused.
19. `render`, `install` or `heartbeat` with no `--sandbox` refuses, exit 2,
    `reason=no_tool`; `--sandbox none` is accepted and reaches the unit.
20. `install` is idempotent: a second run reports `unchanged` and rewrites identical
    bytes; a changed declaration boots out before the file changes and bootstraps after,
    proved by a scripted loader seam recording the call order.

**Check, and the three facts**

21. A unit installed with one schedule and declared with another reports `WRONG-WHEN`, and
    the reported `loaded=` is read **back from the installed file**, not from what the test
    asked to be installed.
22. A stamp whose `started_at` is inside `--when-tolerance` of the slot it names counts in
    `ontime=`; one outside it is verdict `CAUGHT-UP`, counts in `caughtup=`, and the run
    **exits 0** — the catch-up regression, pinned, with a fixture whose `started_at` is six
    hours past its slot and whose `booted_at` falls between the two. A `daemon` and a
    `consumer` are in neither count.
23. A `consumer` with two consecutive stamps more than twice its declared interval apart
    reports `MISSED`; one inside it reports `OK` (rule 13).
24. A slot inside `--since` that came and went with no `runs.tsv` row naming it in
    `fired_for` reports `MISSED`; one with a non-zero exit reports `FAILED`; one with a
    loader kernel-kill reason and **no stamp at all** reports `KILLED` — three distinct
    verdicts for three fixtures that differ only in the record they leave. A slot outside
    `--since` produces no verdict at all.
25. A binary whose content hash differs from the stamp's reports `BIN-MOVED` as a `NOTE`
    and the run **exits 0**; a loaded unit whose program is now absent or non-executable
    reports `WRONG-BIN` and the run exits 1 (rule 15).
26. Every unit in the file appears in the counts, including one that cannot be judged,
    which is `UNKNOWN` and makes the run **fail**. The unjudgeable fixture is produced at a
    named seam — the scripted loader of test 20, returning an error for one unit — so the
    test has something real to drive. There is no flag, field or file that removes a unit
    from the count.
27. `DAEMON FAIL` prints the full count line as well as the findings — the failing-run
    count-line regression, pinned.
28. A listing above `--max` prints exactly `--max` item lines plus one `MORE` line naming
    the remedy; `--max 0` prints all; `--max -1` refuses. Asserted on `check`, `status`,
    `install`, `remove`, `repin` and `heartbeat`, each of which prints one line per unit.
29. A file with no heartbeat unit fails `BEAT reason=no_beat`; a heartbeat slower than the
    shortest declared interval fails `BEAT reason=too_slow` naming both durations; a
    heartbeat unit whose own last stamp is older than twice its interval fails
    `BEAT reason=stalled` naming both timestamps; a file with a fast, freshly stamped
    heartbeat passes all three.
30. A units directory the platform's loader does not re-read reports `NOT-PERSISTENT`,
    exit 1, naming the directory it does; on linux any directory passes.
31. On linux, `check` prints `NOTE linger=<true|false>` on **every** run, pass or fail,
    from a fixture `loginctl` seam.
32. A loaded unit carrying the declaration's prefix but absent from the file reports
    `UNDECLARED` and is **not** removed, repinned or touched.

**Run, the supervisor**

33. `run` writes the start stamp **before** the child starts — proved by a child that
    blocks until the test observes the stamp.
34. A command that reads stdin gets EOF immediately and does not block.
35. A `role` that outlives its deadline is `TERM`ed, then `KILL`ed after the grace, its
    **whole process group** dies (proved with a grandchild), and the stamp says
    `timed_out`, not `exit=0`.
36. A `daemon` that reaches its deadline stamps `recycled`, prints `RUN RECYCLED`, and
    `run` **exits 0**; `check` counts it and does not fail. The same fixture with
    `kind=role` stamps `timed_out` and fails. One fixture, one field changed, two verdicts
    — the daemon-deadline regression, pinned.
37. A command that exits 0 leaving a live grandchild reports `LEFTWORK`, its stamp carries
    `work_outstanding`, and the grandchild is **dead** by the time `run` exits, with
    `killed=` naming how many (rule 10).
38. `run`'s exit status is the command's for 0, 1, 7 and 42; its own refusals are 125 and
    always carry a `RUN REFUSED` line on stderr; a `SANDBOX REFUSED` or `SECRETS REFUSED`
    line from a tool it started reaches stderr **byte for byte**, with no line of `run`'s
    own added.
39. The secrets tool is invoked with `--only` carrying exactly the declared names, in the
    declared order, and **no value of any of them appears** in the stamp, the log, the
    rendered unit, or any event line — asserted against a fixture whose values are
    distinctive strings grepped for across every artifact the run produced.
40. A unit whose `env` is `none` is started with **no** secrets tool in the chain at all,
    asserted by a secrets seam that records whether it was invoked (rule 12 in the verbs).
41. `--sandbox none` prints and stamps `wall=none`; a sandbox path that is not executable
    refuses at 125 before the command starts; an absent `--sandbox` refuses at 125
    `reason=no_tool`.
42. A stamp directory that cannot be written produces one loud line and **does not**
    change the command's exit status; a log that cannot be written likewise.
43. The log cap keeps the head and the tail with one `...+<dropped>B` mark, and the cut
    falls on a rune boundary.

**Repair**

44. `repin` on a unit the loader reports running **skips it, records it owed, and says
    so**. The mutation is the test: the owed record is written through one interface, the
    suite substitutes a recorder that drops the write, and this test is asserted to **fail**
    under that substitution. A claim that a build with the record removed would fail is not
    a test; this is the substitution that makes it one.
45. The heartbeat retries an owed install and the installed unit carries the schedule the
    file holds **now**, specifically not the one recorded when the act was deferred.
46. An owed set emptied by a successful retry removes `owed.json` rather than writing an
    empty map.
47. The heartbeat prunes a log older than `--log-days` and reports the count; it prunes
    nothing under it.
48. On **linux**, `repin` prints one line, touches nothing, and exits 0 (rule 26) — it is a
    supported platform with nothing for this verb to do, which is not the same fact as
    `no_body`.

**Platform**

49. On a platform that is neither darwin nor linux, every writing verb refuses
    `reason=no_body` naming the platform, exit 2, and `render` still works for both named
    platforms.
50. Every parser of loader output — the launchd last-exit pair, the stale-requirement
    property, the running pid, the systemd `Result`/`ExecMainStatus`/`ActiveState`, and
    `loginctl`'s linger field — is **pure**, fed from captured fixtures in `testdata/`, and
    every branch is reachable from a test on a machine with neither loader installed.
    Matching is on the exact key before its separator, never a substring of the blob: both
    loaders nest whole sub-blocks and a `Contains` matches a neighbor.

---

## Open questions — each with a default, and the default stands unless answered

1. **System-wide units, and root.** This spec is **per-user** throughout: launchd
   `gui/<uid>` agents and `systemd --user` units, nothing needing `sudo`, blast radius by
   account. A box-level unit that survives with no login (a linux `LaunchDaemon`
   equivalent, `loginctl enable-linger`, a system unit) is owed work, not v1 — and the
   linger question is real, because a `systemd --user` unit does **not** run after logout
   without it. Default for v1: `check` reports lingering as a fact in a `NOTE` **on every
   run**, and refuses nothing. Draft 1 made that note optional; both draft-1 reads said the
   same thing about it, and a `systemd --user` unit that stops at logout is precisely the
   silently dead unit this whole spec is about.

**Two of draft 1's three open questions are closed by its reads, and are recorded here as
closed rather than deleted.**

- *Does `install` own the heartbeat unit, or does the caller?* **Neither.** The heartbeat
  is one `consumer` declared in the file like everything else (rule 19), so `install`
  installs it because the file declares it, and a file that declares none fails `check`
  with `BEAT reason=no_beat`. The draft-1 default — a `--beat` flag riding an existing
  consumer, with `--beat-unit` as a fallback — was refused by rules 11 and 21 and asserted
  a period no unit need have.
- *Should `run` verify that the declaration it was handed is the one `install` rendered
  from?* **No verification in the unit; a hash in the stamp.** `file_sha256` in every stamp
  (rule 22) surfaces the drift in `check` as a `NOTE`, while a hash recorded in the unit
  file would make every declaration edit an uninstall — which is the reason the draft-1
  default gave for declining, now carried by a field rather than by a silence.

---

*Draft 2, 2026-09-13. Folds the two cold reads of draft 1 on this pull request. Not
ratified: this document is a proposal until every named reader has said APPROVE against a
head sha.*
