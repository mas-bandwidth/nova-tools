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
from: the secret store, the sandbox that holds a worker, the image every bench boots, and the scripts the
coordinator runs unattended. Those are not cut on the judgement of whoever is at the keyboard.

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
`internal/secrets` without it also catches `internal/secretsanta/`. Matching is by prefix and by nothing
else — no guessing from a file name, no substring anywhere in the path.

<!-- release-sensitive-paths -->
```
cmd/nova-sandbox/
cmd/nova-secrets/
infra/image/
internal/sandbox/
internal/secrets/
scripts/coordination/
```

**The list lives in `internal/release/sensitive.go`, and this block is the same list in the same order.**
`internal/ci`'s `TestTheSensitivePathListIsTheSameInTheCodeAndInTheSpec` reads both and fails when they
disagree, because two copies of a security list drift and the copy that drifts is always the one nobody is
running. A path is added to the Go file and to this block in the same commit.

### A range too big to classify

The forge names at most **300 files** for one compare. A file list at that number is a list that **may be
short**, and a gate that reads a truncated list is a gate that passes the one file it did not see. So a
range whose forge file list reaches the ceiling is refused, and rule 4 says how. `cut` never classifies a
prefix of the truth and calls it clean.

### What `--security-read` may be

A note id (`johnny-4b9200ddc994`) or the url of the comment carrying the read. It is held to the field law
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
- **`--expect-sums-from <file>`** reads it from the digest file this host's build wrote (rule 7).

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
retention rule (`internal/release/prune.go`), run last by `build` on `--out` and by `install` on
`--from`: of the directories whose names are versions, it keeps the one just built or installed,
the one the machine had installed before it, and the `KeepBesides` (3) newest of the rest by
modification time, and removes the others through `safepath.RemoveUnder`. A removal that fails is
counted in `prune-failed=` and never fails the verb. Install also preserves any release
directory containing the bin path or its resolved target, compared by filesystem identity
so symlinks and case aliases cannot cause newly installed binaries to be pruned.

*Tests: `TestBuildRefusesAnUnsupportedPairBeforeBuildingAnything`,
`TestBuildBuildsEveryPlatformAndNamesEachInTheReceipt`.*

## 7. No tag, still a digest: `SUMS.digest`

Rule 2 gives `adopt` a digest that did not travel with the bits — off the annotated tag, or out of
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
SPEC-UPDATE rule 1 is that no path is guessed from the cwd or `$HOME`, because a guessed path makes
two runs of one command mean different things. The command is written out in
[CLI.md](CLI.md#nova-version). The one default in the release verbs, and why it is the exception, is
in rule 12.

*Test: `TestSnapshotRefusesAMissingFlag`.*

## 10. The release verbs are in the command reference

The five verbs that put binaries on every bench in the fleet are declared in this spec, in
SPEC-UPDATE, in the help string — and in the command reference. `docs/CLI.md` is what the dogfood
ledger reads, so a verb missing from it would be a verb nothing asks to have been run by a
non-author. `### The release verb` under `## nova-update` is that section, and a test holds it
against `internal/release.Verbs` so a sixth release verb fails on the day it is added.

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
running file be renamed aside, so `install` falls back to moving the old one out of the way and
renaming the new one into place. The name it moves aside to is dot-prefixed, which keeps it out of
`nova-version snapshot` and out of `--retire`, both of which take `nova-*` only; the old image may
survive until the process ends, and has to be inert while it does. A rename that fails for a real
reason still fails, with the old binary put back under its own name.

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
compiled. The gate is `internal/dogfood.Gate` in process rather than a shell out to `nova-check` — one
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
`~/rowan-working/dogfood` **when that directory exists** — the single exception to SPEC-UPDATE rule 1,
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
machine from the installed build's and sends only the files whose `SHA256SUMS` line differs.
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
`TestCycleRefusesBeforeAnyPlay`, `TestToolsPlaySendsOnlyTheFilesTheInstalledBuildLacks`.*

## What this file does not cover

The verbs themselves, the machines file, the retire rule, where `adopt` runs from and the security rules
for `adopt` are all in [SPEC-UPDATE.md](SPEC-UPDATE.md). SPEC.md's **Conventions** govern throughout — exit
codes, the one-line grammar, the field law, no guessed paths — and no unit test of any of this reaches the
network or a real machine.

## Tests this spec demands

The release tests run entirely against fakes and temp dirs — a `fakeForge`, a `fakeSSH`, a `fakeToolchain`, a `fakeGit` — and never reach a network or a real machine; the sensitive-list, command-reference and windows-spec parity checks live in `internal/ci` and read the spec file directly.
One numbered line per test; where one test holds several behaviours, they share its line, and lines 37 and 38 also name, in parentheses, a second test holding the other side of the same behaviour. The dogfood gate's tests are named under rule 12. The behaviours this spec demands that no test proves yet follow, unnumbered.

1. `TestCutRefusesASensitiveRangeWithoutJohnnysRead` — a cut whose range touched a sensitive prefix is refused (exit 2) and names the paths, until `--security-read` is supplied.
2. `TestCutWithJohnnysReadSaysSoOnItsOwnLine` — with `--security-read` the cut prints `RELEASE CUT SENSITIVE paths=<n> read=<id>` above its receipt.
3. `TestCutOfAnOrdinaryRangeSaysNothingAboutSensitivePaths` — an ordinary range prints no `RELEASE CUT SENSITIVE` line.
4. `TestSensitiveClassifiesByPrefixAndNothingElse` — classification is by directory prefix (trailing slash load-bearing) and nothing else, never by filename or substring.
5. `TestTheSensitivePathListIsTheSameInTheCodeAndInTheSpec` — the list in `internal/release/sensitive.go` and the spec block stay the same list in the same order.
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
35. `TestTheCommandReferenceDeclaresEveryReleaseVerb` — `docs/CLI.md` declares every release verb, held against `internal/release.Verbs`.
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
46. `TestInstallMovesARunningFileAsideWhenTheRenameIsRefused` — `install` moves a running binary aside (dot-prefixed) when its rename is refused, and restores the old binary if the fallback also fails.
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

Demanded, and proven by no test yet (8):

- (absent) — when `--repo` and `--expect-sums` are both given, `--expect-sums` wins.
- (absent) — local deletion goes through `safepath.RemoveUnder` only for regular files; a non-regular file is left alone.
- (absent) — `pull` does not touch an installed binary; it deletes the release's artifact directory only.
- (absent) — a machine that never held the release says so (`held=no`) rather than refusing.
- (absent) — artifacts are deleted first, the record last; if the changelog cannot be written the receipt is `PULL FAIL … the artifacts are deleted; mark the section by hand`.
- (absent) — no verb in this package ever asks a machine to hash `SHA256SUMS` itself as evidence (`sha256sum SHA256SUMS`, `shasum`, `openssl dgst`); the bench's `sha256sum -c` of the artifacts is a different check.
- (absent) — precedence when more than one digest source is given: `--expect-sums`, then `--expect-sums-from`, then `--repo`.
- (absent) — an adopt whose local binary has no readable stamp does not refuse.
