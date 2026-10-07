# nova-fuse dogfood, 2026-10-06 (opencode-2)

Tool: nova-fuse. Build: `nova-fuse v1.0.1-0.20261007153756-abb9bfecc729 linux/amd64 go1.26.6`
(the tree's `sprint/mechanical-2026-10-02` tip). Run cold, from the binary's own
help (`nova-fuse -h`, `nova-fuse help`, `nova-fuse help <verb>`) and its page
under `docs/` (`docs/CLI.md`, section `## nova-fuse`) only, with no code read.
Every verb ran for real against scratch temp directories: `version`, `init`,
`status`, `check`, `lockdown`, `quarantine`, `lift quarantine`, `lift lockdown`
and `path`, each with its real flags, plus the `--dry-run` forms and the
refusals. The box was hand-edited too, to the shapes the help's own JSON
example and the tool's "repair the box by hand" remedy invite. No file was
changed here except this report.

## 1. `init` makes a missing parent directory and an empty box there, and no line of help says so — NEXT

**Command:**

    nova-fuse init --box ./no-such-dir/box.json

**Printed:**

    INIT OK box=./no-such-dir/box.json: an empty box, no fuse blown (verified by re-reading the box)

exit 0, and the run made `./no-such-dir/` on the way.

**Expected:** `init -h` says "Create an empty box only where nothing exists" and
the banner says the box path is named on every verb with no default; the
`example:` block's first line is `init --box ./fuse-box.json` at a path the
caller set up. A missing directory is a path the caller got wrong, and the one
verb that can make a CLEAR box should refuse it and name the directory (the
tool has that refusal shape), or the help should say `init` makes parents.
Silently making a directory turns a typo into a box that reads clear.

**Grade:** NEXT

## 2. A hand-edited `at` that is not a time is echoed as the `since` — NEXT

**Command:**

    nova-fuse status --box ./badat.json

where the box is
`{"lockdown":null,"quarantine":{"a-forum":{"at":"yesterday","reason":"bad time"}}}`.

**Printed:**

    STATUS OK lockdown=clear quarantines=1
    STATUS OK quarantine=a-forum since=yesterday: bad time

exit 0.

**Expected:** the banner's box example says `at` is "RFC3339 UTC" and `status`
answers "what is blown, and since when"; the same box with `at` absent prints
`since=unrecorded`, and a `null` entry prints `since=unrecorded: NO REASON
RECORDED`. A value that cannot be a time should be reported the same way (or
named invalid), not echoed in the `since` slot as if the box recorded when.
`check` still blocks, so the permission path is safe; the report path is the one
that repeats a value it has not checked.

**Grade:** NEXT

## 3. `check` names a single-dash token as a double-dash flag — NEXT

**Command:**

    nova-fuse check --box ./m.json -weird

**Printed:**

    nova-fuse check REFUSED: unknown flag --weird; the flags of check are --box; run: nova-fuse help check

exit 2.

**Expected:** the token typed was `-weird`; the message says `--weird`, a
spelling the caller never used, so the line a reader greps for their own input
does not hold it. (The parser also takes `-box` for `--box`, which no help or
`docs/CLI.md` line names.) The refusal should quote the token as typed.

**Grade:** NEXT

## 4. `help <verb> <extra>` folds the extra words into the verb name — NEXT

**Command:**

    nova-fuse help status --max

**Printed:**

    nova-fuse help REFUSED: unknown verb "status --max"; the verbs are init, status, check, lockdown, quarantine, lift quarantine, lift lockdown, path, version; run: nova-fuse help

exit 2. `nova-fuse help version extra` refuses the same way.

**Expected:** `help` takes at most one verb; a trailing flag or word is an extra
argument, and the tool already has that refusal (`unexpected argument`). Instead
the words are joined and the line names a verb that never existed, so a reader
who mistyped a flag is sent looking for `status --max`.

**Grade:** NEXT

## 5. `lockdown` prints "blowing lockdown anyway" before a write it then declines — NEXT

**Command:**

    nova-fuse lockdown --box ./link.json "through the link"

where `./link.json` is a symlink to a box elsewhere.

**Printed:**

    LOCKDOWN NOTE box was unreadable (./link.json is a symlink; use the real fuse box file path) and its bytes could NOT be preserved (./link.json is not a regular file); blowing lockdown anyway
    LOCKDOWN FAILED could not write box: atomicfile: target "link.json" is a symlink (the write is temp-file + rename, so a failure cannot leave it torn; stop by hand and tell the person you work with now)

exit 1, and the target is untouched. (Refusing to write through a symlink is
right.)

**Expected:** the NOTE asserts the lockdown is being blown "anyway", but the run
then fails and nothing is blown; a reader who takes the first line as the effect
has been told a false one. The NOTE should say the run will try, or should
follow the write. The exit code (1) is honest and the box is untouched, so this
is a line that lies, not a wrong gate.

**Grade:** NEXT

## 6. Every write but `init` leaves an undocumented `<box>.lock` sibling — NEXT

**Command:**

    nova-fuse init --box ./race.json
    nova-fuse quarantine --box ./race.json r1 "reason 1"
    ls

**Printed** (the directory after the second command):

    race.json
    race.json.lock

exit 0. Twenty concurrent `quarantine` runs against one box all landed (20 of
20 in the box), so the lock works; it is the file that stays.

**Expected:** the banner says "the box is one JSON file you name with --box",
and the page under `docs/` says the one file holds the fuses. The tool leaves a
zero-byte `<box>.lock` beside it that no verb reads, clears or documents; `init`
neither makes one nor removes one. A reader told the state is one file finds
two. Either remove it after the write or name it in the help.

**Grade:** NEXT

## 7. No verb takes `--json`, so a harness needs a second parser — NEXT

**Command:**

    nova-fuse status --box ./b.json --json

**Printed:**

    nova-fuse status REFUSED: unknown flag --json; the flags of status are --box, --max; run: nova-fuse help status

exit 2; `nova-fuse check --box ./b.json --json` and
`nova-fuse quarantine --box ./b.json a r --json` refuse the same way.

**Expected:** the standard says every verb builds one value and renders it as
typed lines or as `--json` of the same value. The help is honest about it
("There is no --json: every verb answers in one-line typed records ... and
check's answer is its exit code"), so this is not a lie; but a harness that
parses the shared envelope for the other tools must grow a second,
line-grammar parser for this one, and the remedy lines it must act on are the
typed text. One `--json` rendering of the same records would keep the set's one
shape.

**Grade:** NEXT

READ 9/10 — the banner answers what, how and how-to; every verb's `help <verb>`
exits 0 and names its effect and its exit codes; a refusal is one line with a
remedy, a missing `--box` is refused rather than guessed, an unreadable box
refuses instead of reading clear, and unknown JSON fields, a directory, a FIFO
and a symlink are all refused; the `example:` block and `docs/CLI.md`'s
transcript matched the binary. The score is held down by `help` joining extra
words into a verb name, `check` requoting a single-dash token, and a
hand-edited `at` echoed as the time.

USE 9/10 — every verb ran for real against scratch directories, refusals
included: `init`, `quarantine`, `lockdown` and `lift quarantine` with `--dry-run`
and their real forms, `status`'s uncapped count with `--max` and its `MORE`
line, `check`'s 0/1/2, `path`, `version`, and `lift lockdown`'s forever
refusal. The paths that matter held: no box, an unreadable box, a directory, a
FIFO, a symlink and unknown JSON all refuse instead of reading clear; twenty
concurrent quarantines lost none; a surface or a box path holding a quote still
printed a remedy that ran; a newline in a reason folded to one line. The score
is held down by `init` making a missing directory, the lockdown NOTE that
announces a blow it does not make, and the stray `.lock`.

urgent=0 next=7
