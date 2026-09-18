# nova-version — specification

`nova-version moved` writes the TOOLS MOVED note from two revisions' binaries, and
`nova-update apply --sha` builds the whole `cmd/*` set at one revision into one stamped
bin. SPEC.md's **Conventions** govern — exit codes, the one-line grammar, the field law,
no guessed paths — and [SPEC-UPDATE.md](SPEC-UPDATE.md) holds the manifest verbs this file
does not restate. `help` prints these four lines, byte for byte:

```
nova-version moved --from <sha> --to <sha> --repo <dir> --out <path>
nova-update apply --sha <sha> --repo <dir> --bin <dir> [--timeout <d>]
nova-version snapshot --bin <dir> --out <file.tsv>
nova-version diff --from <a.tsv> --to <b.tsv>
```

## nova-version moved and nova-update apply --sha

1. **Every path and revision comes from a flag.** `--repo` is the checkout, `--out` the
   note and `--bin` the binary directory; a missing one is *refusing to guess*, exit 2. A
   revision resolves in `--repo` alone — never the cwd, never `origin/HEAD`.
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
   exit 0, never a refusal.
4. **`moved` refuses, exit 2, one remedy each.** A missing flag is *refusing to guess*,
   naming it; a revision that is not a commit in `--repo` names the revision and the
   `git fetch` that would bring it; a `cmd/*` that builds but answers no help names the
   tool, the revision and the build to repair there.
5. **`apply --sha` is `apply` in its build mode, and it publishes the whole set
   atomically.** With `--sha` it reads no `--file` and takes no name: it builds `./cmd/...`
   at the revision under that revision's one stamp into a new staging directory, reads
   every built binary's `version` back, and verifies the exact postflight set — every
   expected tool present at the one stamp, and every tool the prior set held but this
   revision no longer builds recorded by name as absent; it then switches the `--bin` link
   to the staged set in one step, and records the set's full source metadata — repository,
   revision, build host, go version, time — in the manifest; on any failure it performs one
   verified rollback to the prior set. `--sha` given with `--file` is a refusal naming both
   flags.
6. **Every stamp verification also verifies source metadata.** Every place this spec reads
   a binary's version stamp — `apply --sha`'s postflight, the snapshot, and `moved`'s
   per-revision readback — also reads that binary's source metadata (repository, revision,
   dirty flag, build host) and verifies it against the manifest that recorded the build; a
   binary whose source metadata is missing or disagrees is refused, exit 2, naming that
   binary and the field. A build from the wrong checkout that carries the requested linker
   stamp cannot pass the gate.
7. **`apply --sha` prints one line, every field named.** `APPLY SHA sha=<sha> bin=<dir>
   stamp=<stamp> built=<n> took=<d>`; `built=` is the number of binaries installed and
   `stamp=` the identity every one of them reports.
8. **`apply --sha` refuses a mixed set, naming the pair.** Two binaries at different
   stamps — a lost linker symbol answers `devel` while the rest answer the revision — are
   refused, exit 2, naming both binaries and both stamps, and nothing is installed; the
   staged build is discarded, so `--bin` is never left mixed.
9. **The rest of the `apply --sha` refusals.** A missing `--sha`, `--repo` or `--bin` is
   *refusing to guess*, naming it; an unresolved revision names it and the fetch; a
   `cmd/*` that does not build names the package and the revision.
10. **The mistake `moved` removes, in one sentence.** The ADOPT EVERYTHING note named four
    `--decide` flags still on open PRs (#1141), and `moved` cannot, because every flag it
    announces was read off the binary's own help.
11. **The mistake `apply --sha` removes, in one sentence.** Friends' bins were mixed
    across stamps, and one `apply --sha` builds the whole set under one stamp and refuses
    a directory that is already mixed.
12. **Both are bounded and clockless.** Each prints one line, caps every child through
    `internal/bounded`, takes its clock from the injected seam, and touches the network
    only for the `git fetch` a named missing revision asks for.

### Red tests this section demands

Numbered, one sentence each, every fake standing where the real thing is a bench, a
network or a clock; `git`, `go` and every built binary are fakes on `PATH`.

1. `TestMovedReadsTheBuildNeverAList`: a fake `<tool> help` at `--from` without `--decide` and at `--to` with it yields `added=decide`, and a flag a hand list would name but neither help prints is absent — the mutation that matters.
2. `TestMovedRefusesAMissingFlag`: no `--repo`, and separately no `--out`, is exit 2 printing `refusing to guess`, naming the flag, and starts no build.
3. `TestMovedRefusesAnUnresolvedRevision`: a fake `git` answering "not a commit" for `--from` is exit 2 naming the revision and the fetch remedy, and writes no note.
4. `TestMovedRefusesABinaryWithNoHelp`: a fake `<tool> help` that exits non-zero, and one that prints nothing, are each exit 2 naming the tool and the revision.
5. `TestApplyShaBuildsOneStampOnAFakeBench`: a fake `go` records exactly one `-X main.version=` and every fake binary echoes it, so one `APPLY SHA` line carries `stamp=` of the revision and `built=` of the `cmd/*` count.
6. `TestApplyShaRefusesAMixedSetNamingThePair`: a fake `--bin` holding two binaries that answer two stamps is exit 2 naming both names and both stamps, and starts no build.
7. `TestApplyShaRefusesALostStamp`: a fake build whose one binary answers `devel` while the rest answer the revision is refused naming that binary and both stamps, and installs nothing.
8. `TestApplyShaIsBoundedByTheClock`: an injected clock and a fake build sleeping past `--timeout` is exit 2 with the timeout named and no partial `--bin` directory.
9. `TestMovedNeverInfersARename`: a tool present only at `--from` and a differently named tool present only at `--to` whose helps are identical yield `deleted=1 added=1 renamed=0`, and the same pair with the rename stated by the commit message or a `MOVED` file yields `renamed=1` — help text alone never makes a rename.
10. `TestApplyShaPublishesTheWholeSetAtomically`: a fake `--bin` link to a prior set and a fake build whose last binary fails verification is a non-zero exit with the prior set still linked and untouched, and a successful run swaps the link once and writes repository, revision, build host, go version and time into the manifest.
11. `TestStampCheckAlsoVerifiesSourceMetadata`: a fixture whose binaries all answer the same version stamp but one carries source metadata that is missing or disagrees with the manifest — another repository, revision, dirty flag or build host — is exit 2 naming that binary, and installs nothing.

## nova-version snapshot and nova-version diff

1. **Every path comes from a flag, and neither verb takes a positional argument.**
   `--bin` is the directory holding the binaries and `--out` the TSV `snapshot` writes;
   `--from` and `--to` are two such TSVs `diff` reads. A missing one is *refusing to
   guess*, exit 2, naming the flag and `run: nova-version help`.
2. **`snapshot` reads each binary's own `version`, never the file's name.** It lists every
   `nova-*` regular file in `--bin`, runs each one's `version`, and parses the Conventions
   line with `internal/buildinfo`'s `Parse` — the package that also WRITES that line — so
   the four mandatory tokens are read and a tool's named `key=value` extras (`nova-merge`'s
   `build=`, `nova-sandbox`'s `backend=`) are metadata rather than a broken binary (#1297);
   `name` is the executable's name, and its `stamp`, `revision` and `platform` are read off
   that line, so a renamed stub cannot forge a row and a non-`nova-` file is never one.
3. **`snapshot` writes one row per binary and prints one line.** The `--out` file is the
   header `name<TAB>stamp<TAB>revision<TAB>platform` and then one row per binary, sorted by
   name; `stamp` is the build identity, `revision` the twelve-hex commit when the identity
   carries one and `-` otherwise, and `platform` the `goos/goarch`. Stdout carries `SNAPSHOT
   OK bin=<dir> out=<path> tools=<n> stamp=<stamp>`, `tools=` the row count and `stamp=` the
   one identity every binary reported.
4. **`snapshot` refuses a mixed set, naming the pair.** Two binaries reporting two different
   stamps are refused, exit 2, naming both binaries and both stamps, and no `--out` is
   written — so a friend's bin cannot be recorded as one set when it is four. The remedy is
   one `nova-update apply --sha` to rebuild the set under one stamp, or a `--bin` per set.
5. **The rest of the `snapshot` refusals.** A `--bin` that is unreadable, or holds no
   `nova-*` regular file, names the directory and the readable `--bin` to supply; a binary
   whose `version` exits non-zero, hangs past its deadline, or prints no parseable line
   names the tool and the build to repair there.
6. **`diff` reads two snapshots and prints one line per changed binary.** For every `name`
   whose row differs — stamp, revision or platform, or a name present on one side only — it
   prints `DIFF CHANGED name=<name> from=<stamp|-> to=<stamp|->`; an unchanged binary prints
   no line, and the closing `DIFF OK from=<a.tsv> to=<b.tsv> tools=<n> changed=<n>` counts
   the state, not the output.
7. **`diff` refuses what it cannot read.** A file that is not a snapshot — a missing or
   wrong header, or a row of the wrong arity — names the file and the `snapshot` that writes
   one, exit 2, and prints no changed line; the two files are read, never written.
8. **`version` and `--version` are one spelling.** Every binary, `nova-wake` among them,
   answers both with the identical `<tool> <stamp> <goos>/<goarch> <go version>` line, exit
   0; a second argument, or a spelling that differs between the two flags, is a refusal at
   exit 2.
9. **The mistake this section removes, in one sentence.** A friend's bin held sixteen
   binaries at four stamps — one of them a `nova-wake version` that answered in a syntax of
   its own — and neither fact was visible in one command.
10. **Both are bounded and clockless.** Each prints one line beyond the changed-binary rows
    `diff` exists to print, caps every child through `internal/bounded`, takes its clock
    from the injected seam, and touches no network.

### Red tests this section demands

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
10. `TestVersionAndDoubleDashVersionAgree`: on every fake binary, `version` and `--version` print the identical `<tool> <stamp> <goos>/<goarch> <go version>` line, `nova-wake` among them.
11. `TestSnapshotIsBoundedByTheClock`: an injected clock and a fake binary sleeping past its deadline is exit 2 with the deadline named and no partial `--out`.
