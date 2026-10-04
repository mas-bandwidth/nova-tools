# nova-version — specification

`nova-version moved` writes the TOOLS MOVED note from two revisions' binaries;
`nova-version snapshot` records what a bin holds, and `nova-version diff` compares two
records. SPEC.md's **Conventions** govern — exit codes, the one-line grammar, the field law,
no guessed paths — and [SPEC-UPDATE.md](SPEC-UPDATE.md) holds the manifest verbs
(`snapshot --file`, `report`, `send`) this file does not restate. The lines of
`nova-version help` for the verbs this file specifies:

```
nova-version moved --from <sha> --to <sha> --repo <dir> --out <path> [--timeout <d>] [--budget <d>] [--dry-run]
nova-version snapshot --bin <dir> --out <file.tsv> [--timeout <d>] [--budget <d>] [--max <n>] [--dry-run]
nova-version diff --from <a.tsv> --to <b.tsv>
```

## nova-version moved

1. **Every path and revision comes from a flag.** `--repo` is the checkout and `--out` the
   note; a missing one is *refusing to guess*, exit 2. A revision resolves in `--repo`
   alone — never the cwd, never `origin/HEAD`.
2. **`moved` reads each revision's binaries, never a hand-written list.** For every
   `cmd/*` it builds both revisions, runs each `<tool> help`, and parses the verbs and
   flags the help prints; it inventories each revision separately — the tools and verbs
   present at `--from`, and the tools and verbs present at `--to` — and reports the added
   and deleted entries between them. It never infers a rename from help text: a tool that
   disappears is *deleted* and one that appears is *added*, and a rename is stated only
   when the commit message or a `MOVED` file says so. So a flag on no binary's help can
   never be announced.
3. **`moved` reads the two helps and writes the note to `--out`.** It prints one line to
   stdout naming every field: `MOVED OK from=<sha> to=<sha> added=<n> deleted=<n>
   renamed=<n> verbs=<n> file=<path>`; `renamed=` counts only the renames the commit
   message or a `MOVED` file states, and an empty diff is `added=0 deleted=0 renamed=0`,
   exit 0, never a refusal. `--dry-run` takes the same builds and reads, adds `dry_run=true`
   to that line and prints the note under it instead of writing `--out`.
4. **`moved` refuses, exit 2, one remedy each.** A missing flag is *refusing to guess*,
   naming it; a `--repo` that is not a git checkout is named as such; a revision that is
   not a commit in `--repo` names the revision and the `git fetch` that would bring it;
   a `cmd/*` that builds but answers no help names the
   tool, the revision and the build to repair there.
5. **The mistake `moved` prevents, in one sentence.** A hand-written adoption note can
   announce flags no merged binary has, and `moved` cannot, because every flag it
   announces is read off the binary's own help.
6. **`moved` is bounded and clockless.** It prints one line, caps every child through
   `internal/bounded`, takes its clock from the injected seam, and fetches nothing: a
   missing revision is refused.

### Tests this section demands

`git`, `go` and every built binary are fakes on `PATH`, with an injected clock.

1. `TestMovedReadsTheBuildNeverAList`: a fake `<tool> help` at `--from` without `--decide` and at `--to` with it yields `added=decide`, and a flag a hand list would name but neither help prints is absent.
2. `TestMovedNeverInfersARename`: a tool present only at `--from` and a differently named tool present only at `--to` whose helps are identical yield `deleted=1 added=1 renamed=0`, and the same pair with the rename stated by the commit message or a `MOVED` file yields `renamed=1`.
3. `TestMovedEmptyDiffIsNotARefusal`: an empty diff is `added=0 deleted=0 renamed=0`, exit 0.
4. `TestMovedIsBoundedByTheClock`: every child is capped through `internal/bounded` and the clock is the injected seam.

## nova-version snapshot and nova-version diff

1. **Every path comes from a flag, and neither verb takes a positional argument.**
   `--bin` is the directory holding the binaries and `--out` the TSV `snapshot` writes;
   `--from` and `--to` are two such TSVs `diff` reads. A missing one is *refusing to
   guess*, exit 2, naming the flag and `run: nova-version help`; a bare `snapshot` names
   `--bin` and `--out` and the other shape, `--file <manifest>`, and the two shapes
   together are refused.
2. **`snapshot` reads each binary's own `version`, never the file's name.** It lists every
   `nova-*` regular file in `--bin`, runs each one's `version`, and parses the Conventions
   line with `internal/buildinfo`'s `Parse` — the package that also WRITES that line — so
   the four mandatory tokens are read and a tool's named `key=value` extras (`nova-sandbox`'s
   `backend=` and `platform=`) are metadata rather than a broken binary;
   `name` is the executable's name, and its `stamp`, `revision` and `platform` are read off
   that line, so a renamed stub cannot forge a row and a non-`nova-` file is never one.
3. **`snapshot` writes one row per binary and prints one line.** The `--out` file is the
   header `name<TAB>stamp<TAB>revision<TAB>platform` and then one row per binary, sorted by
   name; `stamp` is the build identity, `revision` the twelve-hex commit when the identity
   carries one and `-` otherwise, and `platform` the `goos/goarch`. Stdout carries `SNAPSHOT
   OK bin=<dir> out=<path> tools=<n> stamp=<stamp>`, `tools=` the row count and `stamp=` the
   one identity every binary reported, then one `SNAPSHOT ROW name= stamp= revision=
   platform=` per row written (capped by `--max`). `--dry-run` takes the same reads, prints
   the same lines with `dry_run=true`, and writes no `--out`.
4. **`snapshot` refuses a mixed set, naming the pair.** Two binaries reporting two different
   stamps are refused, exit 2, naming both binaries and both stamps, and no `--out` is
   written — so a friend's bin cannot be recorded as one set when it is four. The remedy is
   to rebuild the set under one stamp, or a `--bin` per set. **A mixed source is refused the
   same way:** a line that carries the structured source metadata (repository, revision,
   dirty flag, build host) is compared with every other line that carries it, and two that
   differ are refused naming both binaries and both sources; a line with no source
   metadata is recorded and has no say in that comparison.
5. **The rest of the `snapshot` refusals.** A `--bin` that is unreadable, or holds no
   `nova-*` regular file, names the directory and the readable `--bin` to supply; a binary
   whose `version` exits non-zero, hangs past its deadline, or prints no parseable line
   names the tool and the build to repair there. A timeout names the other reading too —
   that the deadline was spent on the platform's assessment rather than on a broken
   binary — and the `--timeout` that answers it; a spent `--budget` is named as
   the budget and never as a slow binary.
6. **`diff` reads two snapshots and prints one line per changed binary.** For every `name`
   whose row differs — stamp, revision or platform, or a name present on one side only — it
   prints `DIFF CHANGED name=<name> from=<stamp|-> to=<stamp|->` under its first line,
   `DIFF OK from=<a.tsv> to=<b.tsv> tools=<n> changed=<n>`, which counts the state, not the
   output; an unchanged binary prints no line.
7. **`diff` refuses what it cannot read.** A file that is not a snapshot — a missing or
   wrong header, or a row of the wrong arity — names the file and the `snapshot` that writes
   one, exit 2, and prints no changed line; both files are read before either is refused,
   so one run names both, and they are read, never written.
8. **`version` and `--version` are one spelling.** Every binary answers both with the identical `<tool> <stamp> <goos>/<goarch> <go version>` line, exit
   0; a second argument, or a spelling that differs between the two flags, is a refusal at
   exit 2.
9. **The mistake this section prevents, in one sentence.** A bin holding binaries at
   several stamps, one of them answering `version` in a syntax of its own, shows neither
   fact in one command.
10. **Both are bounded and clockless.** Each prints one line beyond the changed-binary rows
    `diff` exists to print, caps every child through `internal/bounded`, takes its clock
    from the injected seam, and touches no network.
11. **`snapshot`'s bounds are the caller's, and its per-binary default is thirty seconds
    because its first exec is always a cold one.** `--timeout <d>` bounds one binary's
    `version`, default `30s`; `--budget <d>` bounds the whole run, default `60s`; a
    non-positive either is a refusal naming both flags. Thirty rather than the five every
    other verb in [SPEC-UPDATE.md](SPEC-UPDATE.md) takes: those verbs probe tools a person
    has been running for days, while every binary `snapshot` reads is one the machine has
    never executed — the documented sequence is `go install ./cmd/...` and then
    `nova-version snapshot` — so the platform's one-time assessment of a never-seen
    executable is charged to this deadline on every row of every run, and a five-second
    bound refuses healthy binaries and sends the reader to repair a build that is fine. A
    cold first exec can take seconds on a loaded machine where the warm one takes
    milliseconds. A warm-up exec outside the bound buys nothing: an exec killed early
    leaves the assessment unpaid, and one under `--budget` would turn a genuinely broken
    binary's prompt refusal into a whole-budget wait.

### Tests this section demands

Numbered, one sentence each, every fake standing where the real thing is a bench, a network
or a clock; every `nova-*` binary and every built `version` line is a fake, and nothing below
reaches a network.

1. `TestSnapshotWritesOneRowPerBinary`: a fake bin of stubs each answering a version yields one header and one row per `nova-*` file, with name, stamp, revision and platform all named, and a non-`nova-` file absent.
2. `TestSnapshotReadsVersionNotTheFileName`: a fake binary whose `version` prints a stamp unlike its name records the printed stamp, so a renamed stub cannot forge a row.
3. `TestSnapshotRefusesAMixedSetNamingThePair`: a fake bin holding two binaries that answer two stamps is exit 2 naming both names and both stamps, and writes no `--out`.
4. `TestSnapshotRefusesAMissingFlag`: no `--bin`, and separately no `--out`, is exit 2 printing `refusing to guess`, naming the flag.
5. `TestSnapshotRefusesABinaryWithNoVersion`: a fake `version` that exits non-zero, and one that prints nothing parseable, are each exit 2 naming the tool.
6. `TestSnapshotRefusesAnUnreadableBin`: a `--bin` that is a file, and one holding no `nova-*` file, are each exit 2 naming the directory and the readable `--bin` remedy.
7. `TestDiffNamesOneLinePerChangedBinary`: two fake snapshots differing in one binary print one `DIFF CHANGED` line naming it and both stamps, and the unchanged binary prints none.
8. `TestDiffNamesAddedAndRemoved`: a binary only in `--from` and one only in `--to` are each one line with `-` on the absent side, and `changed=` counts both.
9. `TestDiffRefusesANonSnapshotFile`: a file with the wrong header, and a row of the wrong arity, are each exit 2 naming the file and the `snapshot` remedy.
10. `TestSnapshotIsBoundedByTheClock`: an injected clock and a fake binary sleeping past its deadline is exit 2 with the deadline named and no partial `--out`.
11. `TestSnapshotToleratesTheFirstExecOfANeverSeenBinary`: a fake binary that is slow on its FIRST invocation and immediate on every one after — the platform's assessment made deterministic — is read, not refused, under the default bound, so the verb's own normal case (a `--bin` one `go install` old) is not a refusal.
12. `TestSnapshotTakesItsBoundsFromFlags`: a fake binary sleeping past a given `--timeout` is exit 2 naming that tool and that duration with no partial `--out`; four such binaries under a `--budget` shorter than one of them is exit 2 naming the budget rather than a tool's slowness; a non-positive `--timeout` is exit 2 naming the flags.
