# The release gates — specification

`nova-update release` is specified in [SPEC-UPDATE.md](SPEC-UPDATE.md), under **The release verb**: what
`cut`, `build`, `install`, `adopt` and `pull` do, where `adopt` runs from, and the list of things `adopt`
will never do. This file is the part of it a person must be able to read **without reading Go**: the gates.

Each rule here is a gate rather than a step: something the verb **refuses** until a condition holds, and a
gate whose condition lives only in code is a gate nobody outside the code can check. Each rule is numbered
so it can be referred to, states the mistake it prevents, and names the tests that hold it.

## 1. A range that touched the sensitive paths needs the security reader's read

A release is the moment work stops being a diff somebody can revert and becomes binaries on every bench in
the fleet. Most ranges are ordinary. Some touch the parts of this estate a mistake cannot be taken back
from: the secret store, the sandbox that holds a worker, and the image every bench boots. Those are not cut
on the judgement of whoever is at the keyboard.

**`release cut` classifies the range `<previous tag>..<head>` against the list below.** If any path the
range touched sits under one of these prefixes, the cut **refuses** — naming the paths, because *something
sensitive changed* sends a person back to the compare view to work out what — until `--security-read <note
id or the url of the pull request comment>` names the security reader's read. It then prints, above its own receipt:

```
RELEASE CUT SENSITIVE paths=<n> read=<id>
```

An ordinary range prints no such line. The line exists to mark the exception, and a line printed every time
is a line nobody reads.

### The list

Each entry is a **directory prefix**, trailing slash included, and the slash is load-bearing:
`pkg/secrets` without it would also catch `pkg/secrets<sibling>/` — a directory whose name
merely begins the same way. Matching is by prefix and by nothing else — no guessing from a file name, no
substring anywhere in the path.

<!-- release-sensitive-paths -->
```
cmd/nova-sandbox/
cmd/nova-secrets/
infra/image/
pkg/sandbox/
pkg/secrets/
profiles/
tools/sandboxcheck/
```

**The list lives in `pkg/release/sensitive.go`, and this block is the same list in the same order.**
`internal/ci`'s `TestTheSensitivePathListIsTheSameInTheCodeAndInTheSpec` reads both and fails when they
disagree, because two copies of a security list drift and the copy that drifts is always the one nobody is
running. A path is added to the Go file and to this block in the same commit.

### A range too big to classify

The forge names at most **300 files** for one compare. A file list at that number is a list that **may be
short**, and a gate that reads a truncated list is a gate that passes the one file it did not see. So a
range whose forge file list reaches the ceiling is refused, and the truncation is named first. `cut` never classifies a
prefix of the truth and calls it clean.

### What `--security-read` may be

A note id or the url of the comment carrying the read. It is held to the field law
before anything is tagged — no whitespace, no `=`, one token — because it travels into the one-line receipt
above, and a read nobody could print is a read nobody could look up in six months.

## 2. The tag is annotated, and the annotation carries the SUMS digest

A **lightweight** tag is a name pointing straight at a commit, carrying nothing; with one, the only place a
release's `SHA256SUMS` digest would live is the CHANGELOG entry, and `adopt` would have to be handed it by a
person retyping it off the file.

**So a tag is two calls, in this order:**

1. `POST repos/<repo>/git/tags` — the tag **object**, whose message is

   ```
   <version>

   Cut from <sha>.
   sums=<sha256 of SHA256SUMS>
   ```

2. `POST repos/<repo>/git/refs` — the ref `refs/tags/<version>`, pointing at **that object**, never at the
   commit. A ref pointing at the commit is the lightweight tag again, with the annotation orphaned.

The `sums=` line is written only when `cut --sums <file>` names the checksum file the build wrote; the same
digest goes into the CHANGELOG section as `SHA256SUMS digest: <sha256>`. `build` writes one `SHA256SUMS`
per platform, under `<out>/<version>/<goos-goarch>/`, and `--sums` takes one file, so the tag carries
**one platform's digest**: the platform whose `SHA256SUMS` `--sums` names. `adopt --repo` verifies that
platform only. Every other platform the release built is adopted with
`--expect-sums-from <out>/<version>/<goos-goarch>/SUMS.digest` on the host that built it, or
`--expect-sums <sha256>` from the `sums=` field of its `RELEASE BUILT` line. The line is read back by an
**anchored** match on its own line: a sha mentioned in prose inside a release note is not the digest the
release was cut with, and a reader that took the first 64 hex characters it found would sometimes be
right, which is the worst way for a check like this to be wrong.

### What `adopt` does with it

`adopt --from <host>:<dir>` fetches a release from another machine and must check it against a digest that
did **not** travel with the bits — anybody who could change the bits could change the `SHA256SUMS` beside
them. The ways to give it one:

- **`--repo <owner/name>`** reads it off the tag. A tag object is a git object, so its message reached the
  adopting host through the repository rather than through the machine whose bits are being checked. This
  is the way that needs no transcription.
- **`--expect-sums <sha256>`** names it outright, from the CHANGELOG entry. It serves a tag that carries no
  annotation, and it **wins** when more than one source is given: a digest a person typed deliberately is a
  decision, not a default.
- **`--expect-sums-from <file>`** reads it from the digest file this host's build wrote.

A mismatch **refuses and names both** — the digest of what was fetched, the digest the expectation says the
release was cut with, and which source the expectation came from. Which one is wrong is the whole question,
and a refusal that shows one of them cannot answer it. Nothing is pushed to any machine.

A tag with no annotation, or an annotation with no `sums=` line, is said plainly with the remedy
(`--expect-sums`) rather than treated as a release that verified.

## 3. A leaked release is pulled

**Case H.** A release is found to have shipped something that should never have left this estate. The
artifacts have to go — here and on every machine that holds them — and the record has to stay.

```
nova-update release pull --version <v> --out <dir> --changelog <path> [--machines <file> --ssh <path> --dest <dir>] [--reason <text>] [--platform <goos-goarch>] [--dry-run] [--timeout <d>]
```

- **The tag stays.** A tag that vanishes is a history that cannot be read, and `pull` never moves or deletes
  one. The CHANGELOG section is marked instead, `**PULLED <date>** this release was withdrawn: <reason>. …`,
  and the mark is **idempotent**: a pull run twice — which is what happens when the first run refused on
  one machine — does not stack two notes. A version the changelog has no section for is a refusal, because
  that is somebody pointing `--changelog` at the wrong file.
- **`--retire` semantics, everywhere.** What is deleted is the release's **own files, by name**: the names
  come from that release's own `SHA256SUMS` under `--out`, so anything else sitting in the directory is
  somebody's and is left alone. Locally it goes through `safepath.RemoveUnder`, only for regular files.
  Remotely it is one `rm -f <dir>/<name> …` per machine and then an `rmdir` — **never recursive**, and
  `rmdir` refuses a directory that is not empty, which is exactly the check wanted. A pull that ran `rm -rf`
  on a path composed from a flag would be one typo away from the fleet.
- **`--out` must still hold the release.** The names are the release's own; a pull that worked them out
  from a directory listing would delete whatever was sitting there. With nothing to read, it refuses.
- **Every path is validated before any remote command is composed**, the same rule `adopt` has:
  `ValidRemotePath` on `--dest` and on each machine's `dest` column, before the first machine is touched.
- **It does not touch an installed binary.** A machine keeps running what it is running until the next
  release is adopted over it; `pull` deletes the release's artifact directory, the last-good copy a
  re-install or a rollback reads.
- **Receipts, one per machine**, and a machine that never held the release says so rather than refusing:

  ```
  RELEASE PULLED machine=<m> version=<v> held=yes|no files=<n> dest=<dir>
  RELEASE PULLED LOCAL version=<v> files=<n> out=<dir>
  RELEASE PULL OK|FAIL version=<v> machines=<n> pulled=<n> refused=<n> local=<n> dry-run=no
  ```

  `--dry-run` asks each machine whether it holds the release, prints `RELEASE WOULD PULL …`, and deletes
  nothing anywhere — including the changelog.
- **The artifacts first, the record last.** Deleting is the urgent half of a withdrawal. If the changelog
  cannot then be written, the receipt says `PULL FAIL … the artifacts are deleted; mark the section by
  hand` rather than leaving somebody to wonder which half happened.

## 4. A truncated compare is named first, and a read does not get past it

The forge names at most 300 files for one compare. If `cut` classified that list first, it would refuse
naming only the sensitive paths that happen to fall inside the part it could see — which looks like the
gate working and is the gate being lucky: a range whose only sensitive file sits past file 300 would be
cut clean.

So the truncation is decided **before** the classification and said **before** anything is said about
what was found inside the list:

```
RELEASE CUT REFUSED reason=compare-truncated files=300 range=<base>...<head> remedy="classify from a local `git diff --name-only <base>...<head>` with --paths-from <file>, produced by `release cut --local-diff <checkout>`"
```

It is a field line rather than the usual `CUT REFUSED: <prose>` because this is the one refusal a
person or a script has to be able to tell apart from every other reason a cut can refuse.

**`--security-read` does not get past it.** The security reader's read is a read *of a list*, and the list is the
thing that may be short: a read of a prefix of the truth vouches for a prefix of the truth. The only
way past a truncated compare is a complete list.

*Tests: `TestCutNamesTheTruncationBeforeTheHitsItFoundInIt`,
`TestCutRefusesATruncatedRangeEvenWithASecurityRead`.*

## 5. The complete list is produced by the verb, never written by hand

`git` in a checkout has no ceiling, so the complete list exists — it just does not come from the
forge. Two flags, and the important one is that **the tool produces the list**:

- **`--local-diff <checkout>`** runs `git -C <checkout> diff --name-only <previous tag>...<head>` and
  classifies that. Three dots, the same range the forge's compare answers.
- **`--paths-from <file>`** is that list as a file. With `--local-diff` it is **written**; without it,
  it is **read back**, which is how the host that has the checkout and the host that does the cut can
  be two different machines.

A path list this verb wrote opens with

```
# nova-update release cut --local-diff <base>...<head>
```

and a file without that line, or with a different range on it, is **refused**. A classification gate
whose input is hand-written is a gate whose answer is whatever somebody remembered. Either way the
cut prints where its list came from, above its own receipt:

```
RELEASE CUT PATHS source=local-diff files=<n> range=<base>...<head> checkout=<dir>
RELEASE CUT PATHS source=paths-from files=<n> range=<base>...<head> file=<path>
```

*Tests: `TestCutLocalDiffClassifiesTheCompleteListItProduced`,
`TestCutLocalDiffWritesThePathsFileItClassified`, `TestCutRefusesAPathsFileNobodyProduced`.*

## 6. `--platform` is repeatable, comma-separable, and refuses before it builds

Two mistakes this rule prevents: a comma list handed to the compiler whole fails at the first tool
with `unsupported GOOS/GOARCH pair` and leaves an **empty directory of that name** in the release tree;
a repeated flag that keeps only its **last** value builds one platform and prints one cheerful receipt.

`--platform` on `build` is repeatable **and** comma-separated; every value is resolved and every pair
is held against `go tool dist list` **before the first compile**, so an unsupported pair is a refusal
and not a directory somebody finds later. Every platform gets its own receipt line, and one line
names them all:

```
RELEASE BUILT version=<v> platform=<goos-goarch> tools=<n> verified=<n> out=<dir> sums=<sha256> digest=<path>
RELEASE BUILD OK version=<v> platforms=<a,b,c> tools=<n> sums=<sha256,sha256,sha256> dogfood=<ok|waived|skipped> out=<dir> pruned=<n> prune-failed=<n>
```

`platforms=` and `sums=` are the same list in the same order, one token each. `pruned=` is the
retention rule (`pkg/release/prune.go`), run last by `build` on `--out` and by `install` on
`--from`: of the directories whose names are versions, it keeps the one just built or installed,
the one the machine had installed before it, and the `KeepBesides` (3) newest of the rest by
modification time, and removes the others through `safepath.RemoveUnder`. A removal that fails is
counted in `prune-failed=` and never fails the verb. Install also preserves any release
directory containing the bin path or its resolved target, compared by filesystem identity
so symlinks and case aliases cannot cause newly installed binaries to be pruned.

*Tests: `TestBuildRefusesAnUnsupportedPairBeforeBuildingAnything`,
`TestBuildBuildsEveryPlatformAndNamesEachInTheReceipt`.*

## 7. No tag, still a digest: `SUMS.digest`

Section 2 gives `adopt` a digest that did not travel with the bits — off the annotated tag, or out of
the CHANGELOG. Both belong to a **tagged** release. A dev build has no tag, and the only other place
to get a digest would be the machine being adopted from, which is that machine vouching for its own
bytes and is not evidence at all.

So `release build` writes the digest of the `SHA256SUMS` it has just verified, beside it, on the
machine that did the build:

```
<release-dir>/SUMS.digest
```

and `adopt --expect-sums-from <that file>` reads it there. The file is **local by rule**: a
`--expect-sums-from host:path` is refused by name, and no verb in this package ever asks a machine to
hash `SHA256SUMS` itself as evidence about a fetch — not `sha256sum SHA256SUMS`, not `shasum`, not
`openssl dgst`. A digest computed where the bits live is the machine vouching for itself.

A destination checking a copy this host already verified is a different question: `adopt` runs
`sha256sum -c SHA256SUMS` (or `shasum -a 256 -c` on darwin) in the artifact directory on the bench,
of the artifacts, never as a substitute for `--expect-sums`. "Already holds" is that verified count,
never an existence check, and the stream lands in `<version>.partial/` until the check passes.

`SUMS.digest` is not listed in the `SHA256SUMS` it is the digest of, or its own value would depend on
the last time the directory was built — and `pull` names it alongside the listed artifacts, because
the `rmdir` that ends a pull refuses a directory that is not empty and one file this tool wrote itself
must not be what stops it.

Precedence when more than one is given: `--expect-sums` (a digest a person typed deliberately is a
decision), then `--expect-sums-from`, then `--repo`.

*Tests: `TestBuildWritesTheSumsDigestBesideTheArtifacts`,
`TestAdoptExpectSumsFromReadsTheCoordinatorsDigestFile`, `TestAdoptRefusesADigestFileOnTheFarSide`.*

## 8. Install on the coordinator first, then adopt

`adopt` is not a courier. It is **this host's** `nova-update` reading a release, verifying it, and
running **that release's** install on every machine. So a coordinator that is not yet on the release
cannot adopt the fleet onto it — the flags it needs may ship inside the release it has not installed.
The order is:

1. `release build` on the machine with the cores;
2. `release install` **here**, on the coordinator;
3. `release adopt` from here, with the new binary.

A coordinator whose own version is behind the release it has been asked to fan out **refuses**,
naming both versions and the `release install` that fixes it, before it touches a single machine. A
binary with no readable stamp does not refuse: a gate that fires on a value it cannot read is a gate
that stops the work it exists to protect.

*Test: `TestAdoptRefusesWhenTheLocalToolPredatesTheRelease`.*

## 9. No path is guessed

`nova-version snapshot --bin <dir> --out <file.tsv>` requires both flags and defaults neither:
The rule is that no path is guessed from the cwd or `$HOME`, because a guessed path makes
two runs of one command mean different things. The command is written out in
[CLI.md](CLI.md#nova-version). The one default in the release verbs, and why it is the exception, is
in the spec.

*Test: `TestSnapshotRefusesAMissingFlag`.*

## 10. The release verbs are in the command reference

The five verbs that put binaries on every bench in the fleet are declared in this spec, in
SPEC-UPDATE, in the help string — and in the command reference. `docs/CLI.md` is what the dogfood
ledger reads, so a verb missing from it would be a verb nothing asks to have been run by a
non-author. `### The release verb` under `## nova-update` is that section, and a test holds it
against `pkg/release.Verbs` so a sixth release verb fails on the day it is added.

*Tests: `TestTheCommandReferenceDeclaresEveryReleaseVerb`,
`TestTheFourthDogfoodsLessonsAreInTheReleaseSpec`.*

## 11. The windows bench is a target like any other

A Windows bench (its standard is [BENCH-WINDOWS.md](BENCH-WINDOWS.md)) takes releases the same way
every other bench does, and everything about releasing to it is a **decision** rather than a default,
so that `nova-update release build --platform windows-amd64` and the fan-out behind it work without a
special case. Five of them.

**The shell on the far side is POSIX.** BENCH-WINDOWS.md names the bench's ssh shell as Git Bash
(`C:\Program Files\Git\bin\bash.exe`), or native OpenSSH with Bash in `sshd_config`. `adopt` and
`pull` match it: they compose `mkdir -p`, `tar -C`, `cat`, `test -d` and `rm -f` there exactly as on
a Linux bench, and reach for no PowerShell at all.

**A path may be written the Windows way and is never sent that way.** `--bin`, `--dest`, `--retire`
and the `--machines` columns take the drive-absolute form — `C:\Users\nova\.local\bin`, which is what
BENCH-WINDOWS.md puts in that bench's runner `.path` and therefore what a person will type — and every
backslash is folded to a forward slash by `release.RemotePath` before any command is composed, giving
`C:/Users/nova/.local/bin`. In Git Bash a backslash is an **escape**: `C:\Users\nova` arrives as
`C:Usersnova`, silently, and the machine then refuses about a path nobody typed. Windows accepts a
forward slash in every API and in every one of its own shells, so the fold costs nothing and removes
the class. The drive form is allowed for the **windows target only** and refused by name for every
other: `C:\...` on a Linux bench is a first token that cannot exist. Drive-*relative* (`C:Users\nova`)
and UNC (`\\server\share`) are refused everywhere, for the same reason a bare name is — they resolve
against something nobody here chose. `--from host:dir` is the one exception to the target rule: that
directory belongs to the **build host**, whose operating system is its own business, so the drive form
is allowed there whatever the target is.

**Every artifact is named for the target, and so is every name derived from one.** `release.ToolFile`
is the only place a tool name becomes a file name: a windows release is a directory of `.exe` files
whoever built it, `SHA256SUMS` lists those names and nothing else, `install` reads the names out of
that file rather than rebuilding them, `adopt` sends and runs `nova-update.exe`, `pull` removes
`.exe` files, and `nova-version snapshot` records the name the file actually has, suffix and all.

**There is no self-verify of a cross-built artifact, and the build says so rather than faking one.** A
`release build --platform windows-amd64` on a non-windows host produces a `nova-update.exe` this host
cannot execute, so the build cannot ask it whether it answers `version`. What the build promises is
the checksum round trip — written, read back, verified, `verified=<n>` on the receipt — and nothing
more; the receipt carries no claim about anything having been run. The version stamp is asserted where
the binary can actually run: by `install` on the bench, which probes every file it is about to replace
through its own `version` verb. On a non-windows host, CI's cross-vet (`make vet-windows`, which is
`GOOS=windows go vet ./...`) type-checks the whole tree for windows, and that is the whole of what such
a host can say about windows code.

**And one thing the bench's own filesystem decides.** Windows will not replace a file that is open for
execution, and the file being replaced is frequently `nova-update.exe` replacing itself — `adopt` runs
the release's own `nova-update.exe` there and that process holds its own image open. It *will* let a
running file be renamed aside, so `install` falls back to moving the existing one out of the way and
renaming the new one into place. The name it moves aside to is dot-prefixed, which keeps it out of
`nova-version snapshot` and out of `--retire`, both of which take `nova-*` only; the existing image may
survive until the process ends, and has to be inert while it does. A rename that fails for a real
reason still fails, with the existing binary put back under its own name.

*Tests: `TestAdoptTakesWindowsDrivePathsForBinAndDest`, `TestAdoptComposesSlashPathsForAWindowsBench`,
`TestAdoptRefusesAWindowsPathForALinuxTarget`, `TestAWindowsPathMayStillCarryNoShellSyntax`,
`TestTheWindowsSumsFileNamesOnlyExeFiles`, `TestAWindowsBuildDoesNotClaimToHaveRunItsOwnArtifacts`,
`TestInstallMovesARunningFileAsideWhenTheRenameIsRefused`,
`TestSnapshotReadsExeNamesAndKeepsTheSuffix`, `TestTheWindowsBenchIsInTheReleaseSpec`.*

## 12. A release is cut only when a non-author has run it

The definition of done: a tool is finished when it has been **tested**, **dogfooded by somebody who
did not write it** on real work, and the **feedback applied**. `nova-check dogfood gate` makes that
mechanical — receipts on disk, read against the command reference, an exit code — and the release
verbs are its caller, so the claim that a release has been dogfooded is never just whatever the last
person said it was. A tag cannot be quietly amended and pushed again.

**`cut` and `build` run the gate FIRST.** Before the forge is asked anything, before a single tool is
compiled. The gate is `pkg/dogfood.Gate` in process rather than a shell out to `nova-check` — one
process, one set of refusals, no shell to get wrong — and it is the same read
`nova-check dogfood gate --cli <reference> --receipts <dir>` does.

**An OPEN EDGE refuses.** An open edge is a verb somebody ran, that did not do what they needed, and
that nobody has run since and said it did. Feedback *filed* is not feedback *applied*, and without the
gate the third step of the definition is the one that goes missing:

```
RELEASE CUT REFUSED reason=dogfood-gate open=<n> remedy="fix the open edges or --no-dogfood-gate --reason <why>"
```

`build` refuses the same way under `RELEASE BUILD REFUSED`, because a dev build has no tag and no
changelog and still reaches every bench through `adopt`. The refusal shows at most ten open edges and
points at `nova-check dogfood ledger` for the rest. The gate asks the question a release turns on, not
the stronger `--require-all` one: a tag held hostage to the last unrun verb in a long reference is a
tag nobody ever cuts.

**The gate judges what ships.** The shipped set is the tools under the checkout's `cmd/` — the
directory beside the reference and the changelog — and a receipt naming any other tool is set aside
before the gate reads it: a tool outside `cmd/` is not built, not tested and not in the release,
so its open edges and its not-ok runs are true about that tool and say nothing about this one. What
is set aside is counted, never dropped in silence:

```
RELEASE CUT NOTE dogfood-gate shipped=<n> outside=<n> cmd=<checkout>/cmd
```

The set is every `cmd/nova-*` directory, the same list `build` compiles: one helper
(`dogfood.CmdTools`) answers both, so what is judged is what is built. A shipped tool's open edge
refuses, and so does a shipped tool's not-ok receipt on a verb the reference does not declare. A
`cmd/` that holds no tool refuses rather than setting every receipt aside, and a tool directory that
cannot be read refuses naming its path: an I/O error is not a tool outside the release.
`nova-check dogfood gate --shipped <cmd dir>` is the same read.

**The two inputs, and the one default in this package.** `--cli` names the command reference and
defaults to `docs/CLI.md` beside the checkout the verb was already given (`--changelog` for `cut`,
`--source` for `build`). `--receipts` names the receipts directory and defaults to
`DefaultReceiptsDir` (in `pkg/release`, under the home directory) **when that directory exists** — the single exception to no path being guessed,
taken because the alternative fails in the direction that lets a tool ship. A run with neither is not a
run that passed: it prints `RELEASE CUT NOTE dogfood-gate=skipped …` naming what was missing.

**The waiver is work, and it outlives the terminal.** `--no-dogfood-gate` without `--reason <why>`
refuses. With one, the reason is printed as `RELEASE CUT DOGFOOD WAIVED reason=<why>`, the receipt line
carries `dogfood=waived`, and the reason is written into the CHANGELOG section as
`Dogfood gate waived: <why>` — in the file that travels by git, because a waiver nobody can find later
is a gate nobody has. The `RELEASE CUT` and `RELEASE BUILD OK` receipts carry
`dogfood=ok|waived|skipped`.

*Tests: `TestCutRefusesOnAnOpenEdgeBeforeItAsksTheForgeAnything`,
`TestBuildRefusesOnAnOpenEdgeBeforeItCompilesAnything`,
`TestCutWaivesTheGateOnlyWithAReasonAndRecordsItEverywhere`, `TestCutNamesASkippedGate`,
`TestCutFindsTheReferenceBesideTheChangelog`, `TestTheRefusalIsBounded`, `TestTheGateIsASeam`,
`TestTheDogfoodGateIsInTheReleaseSpec`, `TestCutJudgesOnlyTheToolsUnderCmd`,
`TestAParkedToolsOpenItemDoesNotBlockAndAShippedToolsDoes`, `TestReadShippedIsEveryNovaDirectoryUnderCmd`,
`TestReadShippedRefusesAToolDirectoryItCannotRead`, `TestTheGateRefusesAToolDirectoryItCannotRead`,
`TestDogfoodGateShippedJudgesOnlyTheToolsUnderCmd`.*

## 13. A machinery install is incremental, reported, and one command

The ask, 2026-10-02: fast iterations, fix and repeat. A fix merged to the foundation
reached five benches in about 25 minutes (nova-tools#5096 item 12): a whole cross-platform build, a
waiver composed by hand, then the tools play's check and apply typed one at a time. Every restamped
binary differed from the bench's copy, so every binary was sent and every loop drained and restarted.

**The build record.** Every `build` writes `<out>/<version>/<goos-goarch>.build` beside the platform
directory, never in it (so it is in no `SHA256SUMS` and reaches no bench): the commit, the Go, the
stamp shape, the base, what was rebuilt and reused, the gate and its reason. A dirty or non-git
checkout records no commit.

**`build --incremental`** finds the newest record under `--out` with a commit, the same Go and the same
stamp shape, verifies that build's artifacts against its `SHA256SUMS`, and asks
`git diff --name-only --no-renames <recorded> <head>` (a tree diff, so the branch does not matter
and a rename is two paths) and one `go list -deps` per platform. A tool is compiled when a changed
path that is not a `_test.go` lies under its own package directory or any package it imports, when
`go.mod` or `go.sum` changed, when the base lacks it, or when `go list` did not answer for it; every
other tool is the base's binary, byte for byte, and answers the version it was built at. Anything
that stops the question being asked honestly — a dirty checkout, no usable record, a base that does
not verify — is a whole build, never a refusal:

```
RELEASE BUILD INCREMENTAL version=<v> platform=<p> base=<v> changed=<n> rebuilt=<tool,...> reused=<n>
RELEASE BUILD WHOLE version=<v> platform=<p> rebuilt=<n> reason=<why>
```

`install` skips a tool whose installed file already holds the artifact's bytes, as it skips one
answering the version: renaming identical bytes over a running loop's binary would only make it drain.

**`build --gate report --reason <why>`** runs the gate, prints its open edges, says
`RELEASE BUILD DOGFOOD REPORTED open=<n> reason=<why>` and builds; the receipt carries
`dogfood=report` and the build record keeps the reason. It is for a machinery install during a
sprint, where the receipts still name tools nova-tools no longer ships. **`cut` has no `--gate` flag**:
a tag is still refused on an open edge, and still waived only with the CHANGELOG line of rule 12.

**`release cycle`** is the fix-land-install cycle from the coordinator in one command: the tools play
(`<source>/fleet/tools.yml`) with `--check`, then the play, limited to `--benches` and `localhost`, the
build `--incremental --gate report --reason <why>`. The play seeds a new version's directory on each
machine from the installed build's, unverified, then measures the sha256 of every file in the stage in
one listing and sends exactly the files whose bytes differ from the release's `SHA256SUMS` (absent,
rebuilt, or corrupt on the machine). Every file in the stage holds the release's bytes before anything
in it runs, a reused one included, and `release install` verifies the whole set again before its first
rename. `tla/BenchStage.tla` is the model: `ReusedByteIdentical`, `NoWrongBinary`,
`NoVerifiedWithWrong` and, with crashes anywhere, `Liveness` hold (`MCBenchStage`); the first cut's
rule, sending the files whose `SHA256SUMS` line differs, breaks `ReusedByteIdentical`
(`MCBenchStageBrokenLines`).
One line per bench, then the cycle:

```
CYCLE BENCH host=<h> platform=<p> version=<v> was=<v> state=INSTALLED|UP-TO-DATE installed=<n> skipped=<n>
CYCLE OK version=<v> benches=<n> changed=<n> check=<d> apply=<d> total=<d> logs=<out>/<version>
```

A failed check applies nothing (`CYCLE FAIL step=check`); a bench with no receipt fails the cycle;
`--dry-run` is the check alone (`CYCLE WOULD …`, `CYCLE DRY-RUN …`).

*Tests: `TestRebuildSetChoosesTheToolsWhoseImportsChanged`,
`TestParsePackagesAnswersEachToolsDirectoriesInsideTheCheckout`,
`TestIncrementalBuildRebuildsOnlyWhatChangedSinceTheRecordedCommit`,
`TestIncrementalBuildIsWholeWhenItCannotTrustTheBase`,
`TestABuildWithoutIncrementalBuildsEverythingAndStillRecords`,
`TestBuildGateReportPrintsTheOpenEdgesAndBuilds`, `TestCutHasNoReportGate`,
`TestInstallSkipsAToolThatAlreadyHoldsTheBytes`, `TestCycleDryRunChecksAndInstallsNothing`,
`TestCycleChecksThenAppliesAndSaysWhatEachBenchRuns`, `TestCycleStopsOnAFailedBench`,
`TestCycleRefusesBeforeAnyPlay`, `TestToolsPlaySendsOnlyTheFilesTheInstalledBuildLacks`,
`TestATransitiveChangeRebuildsTheTool`, `TestToolsPlaySendsEveryStagedFileWhoseBytesDiffer`.*

## 14. A promised recovery journey is proven at the release revision, or the cut refuses

A review of the release lane (item 5): the chaos suite (`internal/sprint/friend_chaos_functional_test.go`: friend against the sprint's store, so it lives with the sprint) turns every part
the landed code cannot meet yet into a named skip (`OWED <card>: ...`), so a red-by-design test does
not block the merge queue, and `go test` reports a parent whose subtests all skipped as a pass. A
green run was read as proof that a friend whose harness closed, whose session went silent, who hit a
usage limit or whose bus credential was revoked is detected, his cards are dealt elsewhere and he
recovers. It proved none of that.

**The promise is the checkout's.** A checkout that ships `internal/sprint` promises the journeys in
`release.PromisedJourneys`, one per chaos subtest; `TestThePromisedJourneysAreTheChaosSuitesSubtests`
(`internal/sprint/journeys_test.go`, beside the suite) holds the list to the suite's own `t.Run` names. A checkout without the package promises nothing and
the receipt says `journeys=none-promised`.

**The evidence is bound to the revision.** `cut --journeys <file>` names a file whose first line is

```
{"evidence":"release-journeys","revision":"<sha being tagged>","functions":"<v>","schema":"<v>","installed":[{"machine":"<m>","build":"<v>","revision":"<sha>"}]}
```

and whose other lines are the `go test -json` of the journeys. Evidence for another revision, an
installed build of another revision, no installed build, no function version or no schema version
refuses, and so does a line that is not JSON: a broken record is not an absent one. The gate runs
once the head is known and before `--dry-run` branches.

**Each journey is read on its own, and only a pass proves it.** One line per promised journey:

```
RELEASE CUT JOURNEY state=<proven|owed|skipped|failed|not-run|platform-unavailable> name=<test> detail=<what the run said>
```

`owed`, `skipped`, `failed` and `not-run` are incomplete. A skip whose output says
`PLATFORM UNAVAILABLE <platform>: <why>` (`release.PlatformUnavailable`) is `platform-unavailable`,
and not incomplete, only for a platform the journey names as optional; the same skip on any other
journey is `skipped`, an unmet promise. No evidence refuses, naming every journey `not-run`:

```
RELEASE CUT REFUSED reason=journey-evidence promised=<n> remedy="<JourneyRemedy>"
RELEASE CUT REFUSED reason=journey-gate incomplete=<n> remedy="<JourneyRemedy>"
```

A cut that passes prints `RELEASE CUT JOURNEYS proven=<n> promised=<n> revision=<sha> functions=<v>
schema=<v> installed=<n>`, the receipt carries `journeys=ok`, and the CHANGELOG section carries
`Recovery journeys proven at <sha>: <n> (functions <v>, schema <v>, installed <machine> <build>, ...).`

**The way past is `--no-journey-gate --reason <why>`.** The reason is required (and shared with
`--no-dogfood-gate`), `RELEASE CUT JOURNEYS WAIVED incomplete=<n> reason=<why>` is printed, the receipt
carries `journeys=waived`, and the CHANGELOG section carries
`Recovery journeys incomplete, gate waived: <why>` followed by one `- <test>: <state> (<detail>)` line per incomplete journey. A waiver is
for an unkept promise, not for evidence about something else: evidence that does not bind still refuses.

*Tests: `TestTheGateRefusesAPromisedJourneyWithoutEvidence`, `TestThePromisedJourneysAreTheChaosSuitesSubtests`,
`TestTheJourneyGateIsInTheReleaseSpec`.*

## 15. The seat adopts an approved build through its tools play

`nova-sprint adopt` runs `fleet/tools.yml` for the seat. Its checks precede the installation window; the
configuration store migrates as its owning role, and a refusal in the window restores the previous tools
and function library while leaving the migration in place. The command is not called by the sprint tick.
The contract is [SPEC-SPRINT.md](https://github.com/mas-bandwidth/nova-sprint/blob/main/docs/SPEC-SPRINT.md), "Adopting a build"; the coordinator procedure is in
[SPRINT-COORDINATOR.md](https://github.com/mas-bandwidth/nova-sprint/blob/main/docs/SPRINT-COORDINATOR.md), "Adopting the seat build".

*Test: `TestAdoptRunsThePlayAndRefusesAHalfMove`.*

## 16. `release check` is the gate, and a release ships when it says OK

### release-check-frame

`nova-sprint release check [--json] [--streams <glob>] [--check <name>]...` is the release gate (docs/SPEC-SPRINT.md, section 11, subsection release-check-frame). It reads and writes nothing, runs every check in the registry (`sprint.ReleaseChecks`), prints `RELEASE CHECK <name> ok|fail <evidence>` for each (on a fail the evidence names what to look at), then `RELEASE OK checks=<n>` or `RELEASE NOT READY failed=<n>`; exit 0 or 1, and 2 for usage. A release is cut when it prints `RELEASE OK`, and not before. The checks and their bars:

| check | the bar |
|---|---|
| `no-stuck-friend` | no friend was stuck at any moment of the last 4 hours: stuck is a working card held past its deadline (her `friend_deadline`, else 2 hours, from its first take), or a card dealt to her and not taken past the dealt bound; read from the store's log, a spell that has ended counting while it overlaps the window; a fail names the friend, the card and the moment |

Each later card of stream sprint-v1-release adds its row here with its check.

### release-check-acceptance-r-b.w3

The release check runs the acceptance sentinel's six checks as well, source: the
coordinator's answer over the bus, 2026-10-06 12:50 ET (message
`01M48ZHQ5ZNWYV5FAKBRTTW038`). They are the checks the coordinator runs by hand
before releasing a stream's acceptance sentinel; each is a pure function over
`sprint.Acceptance` (the stream's rows and log and the git facts), so its unit
tests build a twin of the facts and open no socket. The six are:

| check | the bar |
|---|---|
| `cards-settled` | every card of the stream is landed or dropped with a reason: none is ready, waiting, working, review or merging; a fail names the first card and its state, or the dropped card with no reason |
| `base-gate-green` | the tree gate is green on the base at the stream's last landing: the unit class and the functional class of the packages the cards name, plus `./internal/docs` and `./internal/ci`, each its own recorded result; a fail names the class and the base, or the class with no result recorded |
| `two-ok-reads` | every landed card has the ok reads its tier needs at its final head, one for a flash card and two different readers for a heavier one, none accepted on the coordinator's word alone; a fail names the card, its head, the ok reads it has and what its tier needs |
| `prose-true` | the stream's spec sections and help text are true to the code: `nova-check links` and `nova-check nocode` clean, present tense, no names of people or machines; a fail names the check and the path, or the check with no result recorded |
| `landings-promoted` | the stream's landings are in dev, or a promotion carrying them is queued since its last landing; a fail names the landings with no promotion, or the promotion older than the last landing |
| `no-open-judgment` | no open judgment names the stream: no stale, no brief-defect, no conflict, no returned-to-review; a fail names the judgment, its type and the stream |

Each prints one `RELEASE CHECK <name> ok|fail <evidence>` line and the release
refuses on any fail. With no stream named there is no acceptance to check, so
each passes and says so. Test:
`TestReleaseCheckRunsTheAcceptanceSentinelsSixChecks`.

### release-check-merge-queue-p90-b.w7

`merge-queue-p90` is the merge queue's age check, source: the owner,
2026-10-04, "We cannot let merges get behind like this." Over the last window
(`--window`, default 24 h) it takes the p90, by the nearest rank
(`sprint.PercentileNearestRank`, tested on its own over known lists; an empty
list has no percentile and the check is then ok saying n=0), of the time each
card spent in merging. The merging time is read from the store's log, never
from the merge table's stamps: a work-table move into the `merging` column
opens a spell, and a move out of it, to landed or to any other column, or the
card leaving the table, closes it. A card still merging counts with its age
now, and a card counts when any of its merging overlapped the window, so a
card left in the queue before the window and still there is not hidden by it.
The bar is `--merge-p90`, default 30 minutes: the stream merges in batches and
one card's merge is a push and a green gate, so a longer wait is the queue and
not the card. The check fails when the p90 is over the bar and the evidence
prints the p90, the number of cards and the oldest card still merging, naming
`nova-sprint where --all` and `nova-sprint log --card <id>` to look at; under
the bar it passes with the same numbers. Test:
`TestReleaseCheckFailsWhenTheMergeQueueAgeP90IsOverTheBar`.


## 17. Adopting one machine

`release.OneMachine` is adopt for one machine at a time, as a library: the sprint's
tick adopts the release the coordinator's machine runs onto a fleet member back from
down (SPEC-SPRINT.md section 5, "Back from down: adopt the latest"). It runs adopt
itself three times with a machine list naming the one machine: `--dry-run`, whose
`RELEASE WOULD ADOPT ... installed=` is the version before; the adopt, which must
print `RELEASE ADOPTED machine=<m>`; and `--dry-run` again, the version read back.
The flags are the coordinator's (everything but `--machines`, `--version` and
`--dry-run`), so the certification rule, the stage's digest and every refusal of this
file apply unchanged. A machine has at most one adoption in flight, and an episode is
adopted once. Tests: `TestOneMachineAdoptsOneAtATime`,
`TestOneMachineRefusesANameAdoptRefuses`.

## 18. The spend the store recorded matches each provider's own, or the cut refuses

The owner, 2026-10-05: "We should not make a release without verifying that we capture actual spend,
not < 1/2 of it." and "We must be reliable, and accurate." On 2026-10-04 openrouter's own account
showed about $2,250 spent while the sprint's cost panel showed $836: runs with no result, reads and
retries were not priced. The cost records price every paid call whatever its outcome
(`internal/sprint`, cost.go); this gate is how a release proves they do (`pkg/release/spendcheck.go`).

**The window.** From the previous tag's commit (the forge's `commits/<tag>` committer date), or
`--spend-since <RFC3339>`, taken back to the start of its UTC day (the providers count by the UTC
day), to now. With no previous tag and no `--spend-since` the cut refuses. The gate runs once the
tags are read and before `--dry-run` branches.

**Three readouts, each behind an interface, a fake in the tests.**

- *The store's recorded spend* (`RecordedSpend`): one read of the work and fleet tables and the
  routes of the sprint store at `--spend-store <addr>`, logged in as nova-sprint logs in
  (`NOVA_SPRINT_REDIS_USER` and the variable `NOVA_SPRINT_REDIS_PASSWORD_ENV` names). The dollars of
  a provider are every priced cost record of it (its route's provider, else the provider of the
  model it reported) on every primary that ended in the window, whatever its end
  (`sprint.RecordedSpendIn`); the paid providers are every route's and every priced record's
  (`sprint.RecordedProvidersIn`); a subscription friend's tokens are its subscription records'
  (`billing=subscription`, `sprint.RecordedTokensIn`).
- *Each paid provider's own spend* (`ProviderSpend`): openrouter's is its account activity
  (`GET /api/v1/activity`, the provisioning key in `OPENROUTER_PROVISIONING_KEY`) for the window's
  completed UTC days, plus the key's own count of today (`GET /api/v1/key`, `data.usage_daily`,
  `OPENROUTER_API_KEY`); a window past the activity's 30 days is unread. opencode Zen and Inception
  publish no usage endpoint this build knows, so each is unread with why. Keys come from the
  environment as `nova-secrets exec --only <KEY>` delivers them, and are never printed.
- *The subscription friends' harness receipts* (`TokenReceipts`): `--spend-receipts <file>`,
  `{"evidence":"spend-receipts","from":<RFC3339>,"to":<RFC3339>,"friends":{"<friend>":<tokens>}}`,
  read only when `from` is the window's start and `to` within its last hour.

**One line per comparison, on stderr:**

```
SPEND provider=<p> store=<$> provider_usd=<$> gap=<$> share=<%> verdict=<ok|refuse>
SPEND friend=<f> store_tokens=<n> receipt_tokens=<n> gap=<n> share=<%> verdict=<ok|refuse>
SPEND provider=<p> unread verdict=refuse: <why>
```

The share is the gap over the provider's own figure (1 when that is 0 and the store's is not). **A
gap over 5% refuses**; so does every provider the store knows of with no readout or a readout that
cannot be read, and every friend when the receipts cannot be read: a check that passes when it
cannot look is no check. The refusal names them:

```
CUT REFUSED reason=spend-gate window=<from>..<to> refused=<n> providers=<p,...> friends=<f,...> (<SpendRemedy>)
CUT REFUSED reason=spend-gate window=<from>..<to> unread: <why the store could not be read> (<SpendRemedy>)
```

A cut that passes carries `spend=ok` on its receipt. **The way past is `--no-spend-gate --reason <why>`**: `SPEND GATE WAIVED refused=<n> reason=<why>` is printed, the receipt carries `spend=waived`,
and the CHANGELOG section carries `Spend gate waived: <why> (window <from>..<to>)` followed by one
`- SPEND ...` line per row that did not pass.

**Beside it, in the sprint.** `nova-sprint where --json` carries each provider's latest
reconciliation (`streams[<s>].reconciles`, the sprint's, the same on every stream), and the
per-tier split (`streams[<s>].cost_by_tier`) accounts for every dollar of `total_cost`: a run with
no recorded tier takes its route's (the route row's tier, else the route name's prefix `pro-*`,
`flash-*`, `heavy-*`, `frontier-*`), else the card attempt's; `no tier` only when none exists. The stream total is rounded up once; tiers keep their whole cents and receive
remaining cents by largest fractional remainder, with alphabetical ties, so displayed
tier amounts sum exactly to the displayed total.

*Tests: `TestAReleaseIsRefusedWhenRecordedSpendMissesTheProvidersOwn`, `TestOpenRouterSpendIsTheActivityDaysAndToday`,
`TestReceiptsAreReadOnlyForTheirWindow`, `TestCostByTierTakesTheRouteTierWhenTheRunRecordsNone`,
`TestCostByTierAllocatesFractionalCentsWithoutChangingTheTotal`.*

## 19. A red or missing CI is cut past only with `--waive-ci`, and the waiver is in the tag

A tag is a claim about source, and a release must not make that claim about a commit CI never
vouched for. `cut` reads the check runs on the commit and refuses unless every one has completed
green — a check that has not finished is as unvouched-for as one that failed, and a commit no run
has judged is refused too, because "nobody looked" is not "nobody objected". The refusal names the
checks:

```
RELEASE CUT REFUSED: CI is not green on this commit: ci=failure; fix the red and cut again; a tag cannot be amended
RELEASE CUT REFUSED: no check run has judged this commit; dispatch CI on this commit and cut again when it is green
```

**The way past is the cut's own flag, `--waive-ci "<who, when>"`** — the person who waives the CI
and the date, the pair the owner's v1.2.0 and v1.2.1 waivers had to be hand-made tags to carry.
With it the cut prints `RELEASE CUT CI WAIVED waived=<who> checks=<name,...>` and records the
waiver where the release is read: both the tag annotation and the CHANGELOG section carry

```
CI waived: <who, when>
CI waived checks: <name=conclusion, ...>
```

naming the red checks it let past (or the unfinished ones, or that none judged the commit), so a
person asking in six months why a version shipped on a red commit reads the answer in the tag. The
flag is the cut's and no other verb's, and certification is not waived by it: `--no-certify` is
`adopt`'s, and a waived CI says nothing about a machine's certificate. **An empty value refuses**
(`--waive-ci ""` is not a waiver), and **a green CI refuses a waiver** with `cut without it; nothing here needs waiving`, because a waiver of nothing is a lie in the release's permanent record.

*Tests: `TestACutWithAWaiverRecordsItInTheTag`.*

## What this file does not cover

The verbs themselves, the machines file, the retire rule, where `adopt` runs from and the security rules
for `adopt` are all in [SPEC-UPDATE.md](SPEC-UPDATE.md). SPEC.md's **Conventions** govern throughout — exit
codes, the one-line grammar, the field law, no guessed paths — and no unit test of any of this reaches the
network or a real machine.

## Tests this spec demands

The release tests run entirely against fakes and temp dirs — a `fakeForge`, a `fakeSSH`, a `fakeToolchain`, a `fakeGit` — and never reach a network or a real machine; the sensitive-list, command-reference and windows-spec parity checks live in `internal/ci` and read the spec file directly.
One numbered line per test; where one test holds several behaviours, they share its line, and the tests for drive paths and backslash folding also name, in parentheses, a second test holding the other side of the same behaviour. The dogfood gate's tests are named in the gate section. The behaviours this spec demands that no test proves yet follow, unnumbered.

1. `TestCutRefusesASensitiveRangeWithoutASecurityRead` — a cut whose range touched a sensitive prefix is refused (exit 2) and names the paths, until `--security-read` is supplied.
2. `TestCutWithASecurityReadSaysSoOnItsOwnLine` — with `--security-read` the cut prints `RELEASE CUT SENSITIVE paths=<n> read=<id>` above its receipt.
3. `TestCutOfAnOrdinaryRangeSaysNothingAboutSensitivePaths` — an ordinary range prints no `RELEASE CUT SENSITIVE` line.
4. `TestSensitiveClassifiesByPrefixAndNothingElse` — classification is by directory prefix (trailing slash load-bearing) and nothing else, never by filename or substring.
5. `TestTheSensitivePathListIsTheSameInTheCodeAndInTheSpec` — the list in `pkg/release/sensitive.go` and the spec block stay the same list in the same order.
6. `TestCutRefusesARangeTooBigToClassify` — a range whose file list reaches the compare ceiling (300) is refused rather than classified from a prefix.
7. `TestCutRefusesASecurityReadNoReceiptCouldCarry` — `--security-read` is held to the field law (no whitespace, no `=`, one token) and refused otherwise, even on an ordinary range.
8. `TestTheTagIsAnnotatedAndCarriesTheSumsDigest` — the tag object's message carries the version, `Cut from <sha>.`, and `sums=<sha256>` (written only with `--sums`).
9. `TestTheAnnotatedTagIsTheObjectThenTheRef` — the annotated tag is two ordered calls: the tag object (`POST git/tags`) then the ref (`POST git/refs` pointing at that object, never at the commit).
10. `TestSumsInAnnotationReadsOnlyItsOwnLine` — the `sums=` digest is read by an anchored match on its own line, never a sha drifted into prose.
11. `TestAdoptReadsTheDigestFromTheTagObject` — `adopt --repo` reads the digest off the tag object, travelling by git rather than beside the bits.
12. `TestAdoptRefusesWhenTheTagDigestAndTheBitsDisagree` — a mismatch refuses and names both digests and the source, and pushes nothing.
13. `TestAdoptSaysSoWhenTheTagCarriesNoDigest` — a tag with no annotation, or no `sums=` line, is said plainly with the `--expect-sums` remedy.
14. `TestPullDeletesTheArtifactsHereAndOnEveryMachine` — `pull` deletes the artifacts here and on every machine, keeps the tag, and marks the changelog section with the date and `--reason`; remote deletion is one `rm -f <dir>/<name>` per machine then an `rmdir`, never recursive (`rm -rf` cannot be composed).
15. `TestPullDeletesOnlyWhatTheChecksumFileNames` — deletion is by name from the release's own `SHA256SUMS`; anything else is left alone.
16. `TestPullRefusesWhenItCannotNameTheFiles` — `--out` must still hold the release; with nothing to read it refuses.
17. `TestPullRefusesAPathTheRemoteShellWouldReadAsSyntax` — every path is validated (`ValidRemotePath`) on `--dest` and each machine's `dest` column before any remote command is composed.
18. `TestPullDryRunDeletesNothing` — `--dry-run` asks each machine, prints `RELEASE WOULD PULL`, and deletes nothing (changelog included).
19. `TestMarkPulledIsIdempotentAndRefusesAnUnknownVersion` — the changelog mark is idempotent (a second pull does not stack a note) and an unknown version is refused.
20. `TestCutNamesTheTruncationBeforeTheHitsItFoundInIt` — a truncated compare is decided and named first, as the field line `RELEASE CUT REFUSED reason=compare-truncated files=300 range=… remedy=…`.
21. `TestCutRefusesATruncatedRangeEvenWithASecurityRead` — `--security-read` does not get past a truncated compare; only a complete list does.
22. `TestCutLocalDiffClassifiesTheCompleteListItProduced` — `--local-diff <checkout>` runs `git -C <checkout> diff --name-only <prev>...<head>` (three dots) and classifies that complete list.
23. `TestCutLocalDiffWritesThePathsFileItClassified` — `--paths-from` is written with `--local-diff` and read back without it.
24. `TestCutRefusesAPathsFileNobodyProduced` — a paths file without the verb's header line, or for a different range, is refused.
25. `TestCutLocalDiffWithAReadSaysWhereTheListCameFrom` — the cut prints `RELEASE CUT PATHS source=local-diff|paths-from files=<n> range=…` above its receipt.
26. `TestBuildTakesSeveralPlatformsAtOnce` — `--platform` is repeatable and comma-separated; every value is resolved.
27. `TestBuildRefusesAnUnsupportedPairBeforeBuildingAnything` — an unsupported pair refuses before the first compile and leaves nothing behind.
28. `TestBuildBuildsEveryPlatformAndNamesEachInTheReceipt` — every platform gets its own receipt line and one line names them all (`platforms=`/`sums=` the same list in the same order).
29. `TestBuildWritesTheSumsDigestBesideTheArtifacts` — `build` writes the digest of the just-verified `SHA256SUMS` to `<release-dir>/SUMS.digest`; `SUMS.digest` is not listed in the `SHA256SUMS` it digests.
30. `TestAdoptExpectSumsFromReadsTheCoordinatorsDigestFile` — `adopt --expect-sums-from <file>` reads the coordinator's local digest file.
31. `TestAdoptRefusesADigestFileOnTheFarSide` — `--expect-sums-from host:path` is refused by name.
32. `TestPullDeletesTheDigestFileToo` — `pull` names and removes `SUMS.digest` alongside the listed artifacts, so the final `rmdir` does not find it non-empty.
33. `TestAdoptRefusesWhenTheLocalToolPredatesTheRelease` — `adopt` refuses when the local tool predates the release, naming both versions and the `release install` that fixes it.
34. `TestSnapshotRefusesAMissingFlag` — `nova-version snapshot` requires both `--bin` and `--out` and defaults neither.
35. `TestTheCommandReferenceDeclaresEveryReleaseVerb` — `docs/CLI.md` declares every release verb, held against `pkg/release.Verbs`.
36. `TestTheFourthDogfoodsLessonsAreInTheReleaseSpec` — rules 4 to 10 are in the release spec.
37. `TestAdoptTakesWindowsDrivePathsForBinAndDest` — drive-absolute paths are accepted for the windows target only (refused by name elsewhere: `TestAdoptRefusesAWindowsPathForALinuxTarget`).
38. `TestRemotePathFoldsBackslashesForTheFarSidesShell` — backslashes are folded to forward slashes by `RemotePath` before any command is composed (POSIX shell, no PowerShell: `TestAdoptComposesSlashPathsForAWindowsBench`).
39. `TestAWindowsPathMayStillCarryNoShellSyntax` — drive-relative (`C:Users\…`) and UNC (`\\…`) paths are refused everywhere.
40. `TestAdoptFetchesFromAWindowsBuildHost` — `--from host:dir` allows the drive form whatever the target, since that directory belongs to the build host.
41. `TestTheMachineColumnsTakeAWindowsPath` — the `--machines` columns take the drive form and are normalised the same way.
42. `TestTheWindowsSumsFileNamesOnlyExeFiles` — `release.ToolFile` is the only place a tool name becomes a file name; a windows release's `SHA256SUMS` lists `.exe` names and nothing else.
43. `TestInstallOnAWindowsArtifactDirectoryUsesExeNamesThroughout` — `install` reads names out of `SHA256SUMS` rather than rebuilding them, using `.exe` throughout on windows.
44. `TestAdoptDryRunProbesTheExeOnAWindowsBench` — `adopt` sends/runs `nova-update.exe` (and `pull` removes `.exe`, `snapshot` records the suffix).
45. `TestAWindowsBuildDoesNotClaimToHaveRunItsOwnArtifacts` — a cross-built windows artifact is not self-verified; the build claims only the checksum round trip.
46. `TestInstallMovesARunningFileAsideWhenTheRenameIsRefused` — `install` moves a running binary aside (dot-prefixed) when its rename is refused, and restores the existing binary if the fallback also fails.
47. `TestTheWindowsBenchIsInTheReleaseSpec` — the windows bench is in the release spec.
48. `TestRebuildSetChoosesTheToolsWhoseImportsChanged` — an incremental build compiles a tool when a changed non-test path lies under its package or an import's (an embedded file below one included), when `go.mod`/`go.sum` changed, when the base lacks it or `go list` did not list it; nothing else.
49. `TestParsePackagesAnswersEachToolsDirectoriesInsideTheCheckout` — `go list -deps` is read as each tool's directories inside the checkout; standard and module-cache packages are left out.
50. `TestIncrementalBuildRebuildsOnlyWhatChangedSinceTheRecordedCommit` — the diff runs from the newest record's commit to the head, only the changed tool compiles, every other is the base's bytes, the record names commit and base and sits outside the artifact directory.
51. `TestIncrementalBuildIsWholeWhenItCannotTrustTheBase` — a dirty checkout, no record with a commit, or a base whose bytes no longer verify is a whole build with `RELEASE BUILD WHOLE … reason=`.
52. `TestABuildWithoutIncrementalBuildsEverythingAndStillRecords` — without `--incremental` every tool compiles, and the record is still written.
53. `TestBuildGateReportPrintsTheOpenEdgesAndBuilds` — `--gate report --reason` prints the open edges and `RELEASE BUILD DOGFOOD REPORTED` and builds (`dogfood=report`); without a reason, with `--no-dogfood-gate`, with another value, or by default it refuses before compiling.
54. `TestCutHasNoReportGate` — `cut --gate report` is an unknown flag.
55. `TestInstallSkipsAToolThatAlreadyHoldsTheBytes` — `install` leaves a binary that already holds the artifact's bytes in place (the same file), whatever version it answers.
56. `TestCycleDryRunChecksAndInstallsNothing` — `cycle --dry-run` runs the play once, `--check`, limited to the benches and localhost, with the build `--incremental --gate report --reason`, and keeps its output.
57. `TestCycleChecksThenAppliesAndSaysWhatEachBenchRuns` — `cycle` checks, then applies, echoes the build's lines and prints one `CYCLE BENCH` per bench and `CYCLE OK`.
58. `TestCycleStopsOnAFailedBench` — a failed check applies nothing; a bench the apply has no receipt for fails the cycle.
59. `TestCycleRefusesBeforeAnyPlay` — a `--benches` entry that is not a machine name, an empty list, or a `--source` without `fleet/tools.yml` refuses before any play.
60. `TestToolsPlaySendsOnlyTheFilesTheInstalledBuildLacks` (functional) — the tools play seeds a new version's directory from the installed build's on the machine, sends only the differing files, and the install skips the identical binary.
61. `TestATransitiveChangeRebuildsTheTool` (functional) — the real `go list` on a chain A -> B -> C (a tool, a package it imports, a package that one imports) puts C in A's set (`.Deps` is recursive), and a change under C, an embedded-style file included, rebuilds A and reuses a tool beside it.
62. `TestToolsPlaySendsEveryStagedFileWhoseBytesDiffer` (functional) — a seeded file corrupt on the machine whose `SHA256SUMS` line matches the release's (the binary the play runs, or any other) is sent again in the same run and the install succeeds; an intact reused file is not sent (`tla/BenchStage.tla` `ReusedByteIdentical`).
63. `TestTheGateRefusesAPromisedJourneyWithoutEvidence` — a cut whose checkout promises recovery journeys refuses without `--journeys`, on evidence for another revision or installed build, without a function or schema version, on a broken line, and on any owed, skipped, failed or not-run journey (a green parent proves nothing); an optional platform's `PLATFORM UNAVAILABLE` skip is named and passes; a proven cut binds the revision, versions and installed builds into the section; `--no-journey-gate --reason` enumerates every incomplete journey there.
64. `TestThePromisedJourneysAreTheChaosSuitesSubtests` (`internal/sprint`) — every promised journey names a subtest the chaos suite runs.
65. `TestAReleaseIsRefusedWhenRecordedSpendMissesTheProvidersOwn` — over the window since the previous tag's UTC day, a store figure of $836 against a provider's own $2,250 refuses naming the provider, both figures and the gap; a 3% gap passes (`spend=ok`); a provider whose readout errs, or has none, refuses; subscription friends' recorded tokens are set beside their receipts the same way, and no receipts refuses; no store refuses; `--no-spend-gate --reason` writes every unpassed row into the section.
66. `TestOpenRouterSpendIsTheActivityDaysAndToday` — openrouter's own count is its activity's completed days in the window plus the key's count of today; no key, or a window past 30 days, is unread; opencode and Inception are unread.
67. `TestReceiptsAreReadOnlyForTheirWindow` — a receipts file is read only for the window it covers.
68. `TestACutWithAWaiverRecordsItInTheTag` — a cut over a red or missing CI is refused without `--waive-ci`; with `--waive-ci "<who, when>"` the tag annotation and the CHANGELOG section carry `CI waived: <who, when>` and the red check names; an empty waiver is refused.

Demanded, and proven by no test yet (8):

- (absent) — when `--repo` and `--expect-sums` are both given, `--expect-sums` wins.
- (absent) — local deletion goes through `safepath.RemoveUnder` only for regular files; a non-regular file is left alone.
- (absent) — `pull` does not touch an installed binary; it deletes the release's artifact directory only.
- (absent) — a machine that never held the release says so (`held=no`) rather than refusing.
- (absent) — artifacts are deleted first, the record last; if the changelog cannot be written the receipt is `PULL FAIL … the artifacts are deleted; mark the section by hand`.
- (absent) — no verb in this package ever asks a machine to hash `SHA256SUMS` itself as evidence (`sha256sum SHA256SUMS`, `shasum`, `openssl dgst`); the bench's `sha256sum -c` of the artifacts is a different check.
- (absent) — precedence when more than one digest source is given: `--expect-sums`, then `--expect-sums-from`, then `--repo`.
- (absent) — an adopt whose local binary has no readable stamp does not refuse.
