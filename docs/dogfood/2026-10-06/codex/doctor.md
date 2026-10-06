# nova-doctor dogfood — Zhi (deepseek/deepseek-v4), 2026-10-06

Read as a stranger: only `nova-doctor -h`, `nova-doctor help`, `nova-doctor <verb> -h`, and
the tool's pages under `docs/` (`docs/SPEC-DOCTOR.md`, the nova-doctor and gosdk sections of
`docs/SETUP.md`, and the command reference `docs/CLI.md`). Built from
`sprint/mechanical-2026-10-02` at `d762f545478f8c5118ed9342f69fd23d8b2d5042` and used as
`nova-doctor v1.0.1-0.20261006205236-d762f545478f linux/amd64 go1.26.6`. Every verb (`run`,
`version`, `help`) ran with its real flags against a scratch `HOME`, a scratch `PATH` of fake
`nova-*` tools and a fake `go`, and then the real `go` 1.26.6 from a checkout; the refusals
ran too. `strace` was used to see which files the check opens. Nothing was written outside
the scratch directories.

## Findings

### 1. `gosdk` never reads the seat, so every machine is judged a bench — URGENT

Command: `nova-doctor` (no `go` and no `nova-*` on `PATH`)

Printed (exit 2):

    DOCTOR gosdk fail no go on PATH and a bench builds and tests here fix: install the Go toolchain go.mod's toolchain line names on this bench (docs/SETUP.md, dep-go-sdk-b.w2)
    DOCTOR self fail no nova-* tool is on PATH fix: nova-update apply --file <manifest> <tool>, or put the directory holding the nova tools on PATH

Expected: `docs/SETUP.md`'s gosdk section says "The gosdk check reads the role from the
machine's seat. On the coordinator's machine, `go` on PATH is a warn naming the bench rule,
with the released install as its fix; with no `go` there it is ok." Neither happened. A
scratch `HOME` whose `nova/seat.env` held `NOVA_ROLE=coordinator` (and the same file with
`NOVA_SEAT_ROLE`, `NOVA_MACHINE_ROLE`, `NOVA_SEAT`, `ROLE`, `SEAT_ROLE`, `NOVA_KIND` and
more) printed the identical line, and `strace` shows the process never opens or stats
`.../nova/seat.env` at all. With the real `go` on PATH but no checkout, the same check fails
`no go.mod here` and the fix says to run it in a checkout. A coordinator is told to install
Go, the one thing the page says its machine must not do. Grade: URGENT.

### 2. The banner advertises `--max`, which no verb has, and miscounts the `--json` verbs — URGENT

Command: `nova-doctor version --max 0`

Printed (exit 2):

    VERSION REFUSED: unknown flag --max; the flags of version are --json; run: nova-doctor version -h

Expected: `nova-doctor -h` says "Every verb but run takes `--json`" and "A verb that lists
takes `--max <n>` (default 20, 0 lists all) and says MORE for the rest". But `run -h` lists
`--json` for `run`, so the first sentence is false, and no verb accepts `--max`
(`nova-doctor --max 5` refuses with "the flags of run are --check, --json, --local,
--strict"). A stranger is sent after a flag that does not exist. Grade: URGENT.

### 3. `--json help` runs the checks, and `help --json` is not JSON — URGENT

Command: `nova-doctor --json help`

Printed (exit 2, one line cut at the third field):

    {"exit":2,"results":[{"check":"gosdk","dependency":"the Go toolchain","status":"fail","evidence":"no go.mod here: the bench's toolchain is not named","fix":"run nova-doctor in a checkout of this repository, whose go.mod names the bench's toolchain (docs/SETUP.md, dep-go-sdk-b.w2)"},{"check":"self",...

Expected: `help` prints help (`nova-doctor help [<verb>]`), or the tool refuses the flag
order; instead the flag before the verb swallowed `help` and the checks ran. The mirror
`nova-doctor help --json` printed `run`'s text help and ignored `--json`, although the banner
says only `run` lacks `--json`. The help door is not a door when a flag comes first.
Grade: URGENT.

### 4. A word after the verb is silently ignored instead of refused — URGENT

Command: `nova-doctor run extra`

Printed (exit 2):

    DOCTOR gosdk fail no go.mod here: the bench's toolchain is not named fix: run nova-doctor in a checkout of this repository, whose go.mod names the bench's toolchain (docs/SETUP.md, dep-go-sdk-b.w2)
    DOCTOR self ok 1 tools on PATH, all v1.2.0

Expected: a refusal, the way `nova-doctor version extra` gives one: `VERSION REFUSED: takes no positional arguments, got "extra" (flags come before arguments)`. `run` takes the bogus
argument and runs the checks instead. `nova-doctor --check self gosdk` and
`nova-doctor --local help` drop the extra word the same way (exit 0 and 2 respectively).
Grade: URGENT.

### 5. The refusal advertises a file input, and an existing file is ignored — URGENT

Command: `nova-doctor ./note.txt` (the file exists in the working directory)

Printed (exit 2):

    DOCTOR gosdk fail no go.mod here: the bench's toolchain is not named fix: run nova-doctor in a checkout of this repository, whose go.mod names the bench's toolchain (docs/SETUP.md, dep-go-sdk-b.w2)
    DOCTOR self ok 1 tools on PATH, all v1.2.0

Expected: the tool's own refusal for a name that is no verb says `the verbs are run, version, and a file is given by its path (./nosuchverb)`, so a real path should be read as a file (or
the message must not offer a file at all). `note.txt` is never named, opened or mentioned;
the checks just run. An input the tool announces is silently dropped. Grade: URGENT.

### 6. A tied `self` split is reported as one release — URGENT

Command: `nova-doctor --check self` (`nova-alpha` and `nova-beta` at v1.2.0, `nova-gamma` and `nova-delta` at v9.9.9)

Printed (exit 2):

    DOCTOR self fail the tools are not one release: nova-alpha=v1.2.0, nova-beta=v1.2.0 differ from v9.9.9 (2 tools) fix: nova-update apply --file <manifest> nova-alpha --version v9.9.9

Expected: `docs/SPEC-DOCTOR.md` says the evidence names every tool that differs "from the
version most of the tools report". A 2-to-2 split has no most, so the line should say it is
a tie, or name both sides. Instead the check silently picks v9.9.9 and its fix moves the two
v1.2.0 tools, with no hint that either answer is a coin toss. Grade: URGENT.

### 7. `--local` prints no skipped line and `--json` has no `skipped` field — NEXT

Command: `nova-doctor --local --json`

Printed (exit 2, one line cut):

    {"exit":2,"results":[{"check":"gosdk","dependency":"the Go toolchain","status":"fail","evidence":"no go.mod here: the bench's toolchain is not named","fix":"run nova-doctor in a checkout of this repository, whose go.mod names the bench's toolchain (docs/SETUP.md, dep-go-sdk-b.w2)"},{"check":"self",...

Expected: `docs/SPEC-DOCTOR.md` says `--local` "skips the checks only a fleet needs and says
which, in one line: `DOCTOR local skipped=<name,name> (...)`", and that `--json` prints
`"skipped":[...]`. Neither the line nor the field appears, and the text output is byte for
byte the same with and without `--local`; the flag never says what it did. Grade: NEXT.

### 8. `run -h` names the check flag's value `<help run>` — NEXT

Command: `nova-doctor run -h`

Printed (exit 0):

    usage: nova-doctor run [flags]
    checks: gosdk, self
    example: nova-doctor --local

Expected: the help two lines above prints the real names `gosdk, self`, and the flag's
placeholder should be `<name>`. Instead the flags block reads
`--check <help run>  run only this check (repeatable); the names are in help run`, and
`nova-doctor --check "help run"` is refused with `no check named "help run"; the checks are gosdk, self`. The placeholder is a phrase, not a value. Grade: NEXT.

### 9. A `self` fix with several broken tools names only the first — NEXT

Command: `nova-doctor --check self` (three `nova-*` that exit 2 for `version`, one that answers)

Printed (exit 2):

    DOCTOR self fail nova-loop, nova-loop-migrate, nova-push-credential did not answer `version` with a version line fix: nova-update apply --file <manifest> nova-loop

Expected: the evidence names all three tools, and the page promises "the one line that fixes
each"; the fix names only `nova-loop`, so the other two are left to the reader. Grade: NEXT.

### 10. The command reference has no nova-doctor page, and the spec omits `gosdk` — NEXT

Command: `grep -n nova-doctor docs/CLI.md docs/TESTS.md` (run against `nova-doctor run -h`, which lists `checks: gosdk, self`)

Printed: no match in either file.

Expected: the standard's onboarding point 3 wants every binary's section in `docs/CLI.md`,
opening with `### First run` and a transcript, and `docs/SPEC-DOCTOR.md`'s checks section
names only `self` while the help, `docs/SETUP.md` and a bare run all name `gosdk` too. The
tool has a README row and a spec, and no page in the reference. Grade: NEXT.

### 11. The `example:` block has one line, not the three the standard wants — NEXT

Command: `nova-doctor -h`

Printed (end of the banner, exit 0):

    example:
      nova-doctor --local

Expected: the standard's onboarding point 6 asks for three to six runnable command lines in
the `example:` block; the banner's whole example is one line, and `run -h` shows the same
single `example:` line. Grade: NEXT.

## What held

`version` and `version --json` print the one version line. `self` is `ok` when the tools on
PATH are one release, and a 2-to-1 split names the odd tool and the fix moves it. An unknown
check and an unknown flag name the real set with a `run:` line, and `--check` with no value
says what it wants. In a checkout, `gosdk` is `ok` when `go` is 1.26.6, `GOCACHE` is
writable and `GOFLAGS=-mod=readonly`; without the flag it is a `warn` and exit 0, and
`--strict` makes that warn exit 1. A wrong `go` version and an unwritable `GOCACHE` each
fail with the version or an export as the fix. A `fail` does not stop the other check.
Nothing in the scratch `HOME`, the `PATH` tools or `GOCACHE` was modified by a run.

READ 5/10 — the banner's three questions, the check list, the exit table and most refusals
are clear and paste-ready, but the banner advertises a `--max` no verb has and misstates
which verbs take `--json`, `run -h` prints a phrase as a flag value, and the tool's
reference page under `docs/` does not exist.

USE 4/10 — the bench path (a checkout, the right `go`, `GOFLAGS=-mod=readonly`, one release
of the tools) does what the page says and writes nothing, but the gosdk check never reads the
seat so a coordinator is told to install Go, `--json help` runs the checks, extra words and a
file path are silently dropped, and a tied release is reported as one.

urgent=6 next=5
