# The release gates — specification

`nova-update release` is specified in [SPEC-UPDATE.md](SPEC-UPDATE.md), under **The release verb**: what
`cut`, `build`, `install`, `adopt` and `pull` do, where `adopt` runs from, and the list of things `adopt`
will never do. This file is the part of it a person must be able to read **without reading Go** — the three
decisions Johnny made on #1337, after the first real release went round the fleet on 2026-09-18.

They are here rather than in SPEC-UPDATE because each one is a gate rather than a step: something the verb
**refuses** until a condition holds, and a gate whose condition lives only in code is a gate nobody outside
the code can check.

## 1. A range that touched the sensitive paths needs Johnny's read

A release is the moment work stops being a diff somebody can revert and becomes binaries on every bench in
the fleet. Most ranges are ordinary. Some touch the parts of this estate a mistake cannot be taken back
from: the secret store, the sandbox that holds a worker, the image every bench boots, and the scripts the
coordinator runs unattended. Those are not cut on the judgement of whoever is at the keyboard.

**`release cut` classifies the range `<previous tag>..<head>` against the list below.** If any path the
range touched sits under one of these prefixes, the cut **refuses** — naming the paths, because *something
sensitive changed* sends a person back to the compare view to work out what — until `--security-read <note
id or the url of the pull request comment>` names Johnny's read. It then prints, above its own receipt:

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
range whose file list reaches the ceiling is refused with the same remedy as a sensitive one — Johnny's
read — or cut again from a nearer tag so the list fits. `cut` never classifies a prefix of the truth and
calls it clean.

### What `--security-read` may be

A note id (`johnny-4b9200ddc994`) or the url of the comment carrying the read. It is held to the field law
before anything is tagged — no whitespace, no `=`, one token — because it travels into the one-line receipt
above, and a read nobody could print is a read nobody could look up in six months.

## 2. The tag is annotated, and the annotation carries the SUMS digest

`cut` used to `POST git/refs` alone. That creates a **lightweight** tag: a name pointing straight at a
commit, carrying nothing. So the only place a release's `SHA256SUMS` digest lived was the CHANGELOG entry,
and `adopt` had to be handed it by a person retyping it off the file.

**A tag is now two calls, in this order:**

1. `POST repos/<repo>/git/tags` — the tag **object**, whose message is

   ```
   <version>

   Cut from <sha>.
   sums=<sha256 of SHA256SUMS>
   ```

2. `POST repos/<repo>/git/refs` — the ref `refs/tags/<version>`, pointing at **that object**, never at the
   commit. A ref pointing at the commit is the lightweight tag again, with the annotation orphaned.

The `sums=` line is written only when `cut --sums <file>` names the checksum file the build wrote. It is
read back by an **anchored** match on its own line: a sha mentioned in prose inside a release note is not
the digest the release was cut with, and a reader that took the first 64 hex characters it found would
sometimes be right, which is the worst way for a check like this to be wrong.

### What `adopt` does with it

`adopt --from <host>:<dir>` fetches a release from another machine and must check it against a digest that
did **not** travel with the bits — anybody who could change the bits could change the `SHA256SUMS` beside
them. There are now two ways to give it one:

- **`--repo <owner/name>`** reads it off the tag. A tag object is a git object, so its message reached the
  adopting host through the repository rather than through the machine whose bits are being checked. This
  is the way that needs no transcription.
- **`--expect-sums <sha256>`** names it outright, from the CHANGELOG entry. It stays for a release cut
  before the tags were annotated, and it **wins** when both are given: a digest a person typed deliberately
  is a decision, not a default.

A mismatch **refuses and names both** — the digest of what was fetched, the digest the tag says the release
was cut with, and which of the two the expectation came from. Which one is wrong is the whole question, and
a refusal that shows one of them cannot answer it. Nothing is pushed to any machine.

A tag with no annotation, or an annotation with no `sums=` line, is said plainly with the remedy
(`--expect-sums`) rather than treated as a release that verified.

## 3. A leaked release is pulled

**Case H.** A release is found to have shipped something that should never have left this estate. The
artifacts have to go — here and on every machine that holds them — and the record has to stay.

```
nova-update release pull --version <v> --out <dir> --changelog <path> [--machines <file> --ssh <path> --dest <dir>] [--reason <text>] [--platform <goos-goarch>] [--dry-run]
```

- **The tag stays.** A tag that vanishes is a history that cannot be read, and this estate has never
  force-moved or deleted one. The CHANGELOG section is marked instead, with the date and `--reason`, and
  the mark is **idempotent**: a pull run twice — which is what happens when the first run refused on one
  machine — does not stack two notes. A version the changelog has no section for is a refusal, because that
  is somebody pointing `--changelog` at the wrong file.
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
  release is adopted over it; `pull` deletes the stamp, which is what a re-install or a rollback reads.
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

## The fourth dogfood's lessons

Decisions 1 to 3 above are Johnny's, made before the first release went round the fleet. What follows
is what the **fourth** release dogfood found by running the verbs against the real fleet on
2026-09-18 (receipts `20260918T1736*`, `rowan-child-release-4`). Each is numbered so it can be
referred to, each has a test named beside it, and each is a thing the tools now refuse rather than a
thing a person has to remember.

## 4. A truncated compare is named first, and a read does not get past it

The forge names at most 300 files for one compare. That cut's range really touched **58** paths on
the sensitive list; the forge answered with exactly 300 files and `cut` refused naming **24** of
them. It looked like the gate working. It was the gate being lucky — the hits it named were the ones
that happened to fall inside the prefix it could see, and a range whose only sensitive file sat past
file 300 would have been cut clean.

So the truncation is decided **before** the classification and said **before** anything is said about
what was found inside the list:

```
RELEASE CUT REFUSED reason=compare-truncated files=300 range=<base>...<head> remedy="classify from a local `git diff --name-only <base>...<head>` with --paths-from <file>, produced by `release cut --local-diff <checkout>`"
```

It is a field line rather than the usual `CUT REFUSED: <prose>` because this is the one refusal a
person or a script has to be able to tell apart from every other reason a cut can refuse.

**`--security-read` does not get past it.** Johnny's read is a read *of a list*, and the list is the
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
RELEASE CUT PATHS source=local-diff|paths-from files=<n> range=<base>...<head> ...
```

*Tests: `TestCutLocalDiffClassifiesTheCompleteListItProduced`,
`TestCutLocalDiffWritesThePathsFileItClassified`, `TestCutRefusesAPathsFileNobodyProduced`.*

## 6. `--platform` is repeatable, comma-separable, and refuses before it builds

Three forms were tried on the fleet's binary and all three were wrong:
`--platform darwin-arm64,darwin-amd64` was handed to the compiler whole and failed at tool 1 of 21
with `unsupported GOOS/GOARCH pair`, leaving an **empty directory of that name** in the release tree;
`--platform darwin-arm64 --platform darwin-amd64` silently kept the **last** flag, built one
platform and printed one cheerful receipt.

`--platform` is now repeatable **and** comma-separated; every value is resolved and every pair is
held against `go tool dist list` **before the first compile**, so an unsupported pair is a refusal
and not a directory somebody finds later. Every platform gets its own receipt line, and one line
names them all:

```
RELEASE BUILT version=<v> platform=<goos-goarch> tools=<n> verified=<n> out=<dir> sums=<sha256> digest=<path>
RELEASE BUILD OK version=<v> platforms=<a,b,c> tools=<n> sums=<sha256,sha256,sha256> out=<dir>
```

`platforms=` and `sums=` are the same list in the same order, one token each.

*Tests: `TestBuildRefusesAnUnsupportedPairBeforeBuildingAnything`,
`TestBuildBuildsEveryPlatformAndNamesEachInTheReceipt`.*

## 7. No tag, still a digest: `SUMS.digest`

Decision 2 gives `adopt` a digest that did not travel with the bits — off the annotated tag, or out
of the CHANGELOG. Both belong to a **tagged** release. A dev build has no tag, so the fourth dogfood
had to compute `--expect-sums` **on the machine being adopted from**, which is that machine vouching
for its own bytes and is not evidence at all.

`release build` now writes the digest of the `SHA256SUMS` it has just verified, beside it, on the
machine that did the build:

```
<release-dir>/SUMS.digest
```

and `adopt --expect-sums-from <that file>` reads it there. The file is **local by rule**: a
`--expect-sums-from host:path` is refused by name, and no verb in this package ever asks a machine to
hash anything — not `sha256sum`, not `shasum`, not `openssl dgst`. `SUMS.digest` is not listed in the
`SHA256SUMS` it is the digest of, or its own value would depend on the last time the directory was
built — and `pull` names it alongside the listed artifacts, because the `rmdir` that ends a pull
refuses a directory that is not empty and one file this tool wrote itself must not be what stops it.

Precedence when more than one is given: `--expect-sums` (a digest a person typed deliberately is a
decision), then `--expect-sums-from`, then `--repo`.

*Tests: `TestBuildWritesTheSumsDigestBesideTheArtifacts`,
`TestAdoptExpectSumsFromReadsTheCoordinatorsDigestFile`, `TestAdoptRefusesADigestFileOnTheFarSide`.*

## 8. Install on the coordinator first, then adopt

`adopt` is not a courier. It is **this host's** `nova-update` reading a release, verifying it, and
running **that release's** install on every machine. So a Studio that is not yet on the release
cannot adopt the fleet onto it — and the flag it needs to try (`--from`) ships inside the release it
has not installed. The order is:

1. `release build` on the machine with the cores;
2. `release install` **here**, on the coordinator;
3. `release adopt` from here, with the new binary.

A coordinator whose own version is behind the release it has been asked to fan out **refuses**,
naming both versions and the `release install` that fixes it, before it touches a single machine. A
binary with no readable stamp does not refuse: a gate that fires on a value it cannot read is a gate
that stops the work it exists to protect.

*Test: `TestAdoptRefusesWhenTheLocalToolPredatesTheRelease`.*

## 9. A lookup says where it looked

`nova-pulse fleet survey` refused with `tools/bench-standard.sh not found above the working
directory`, which reads as *the script is missing* and means *this verb wants a nova-tools checkout
as its working directory*. It now names the first directory it tried, the last, and what it wanted to
find there. `fleet survey` also takes `--machines <file>` — the machines registry, which is a
different file from its `--benches` fleet file — and an unnamed registry surveys every bench, exactly
as before the registry existed.

`nova-version snapshot` requires both `--bin` and `--out` and does not default either: SPEC-UPDATE
rule 1 is that no path is guessed from the cwd or `$HOME`. The pair is written out in
[CLI.md](CLI.md#nova-version).

*Tests: `TestTheStandardScriptLookupSaysWhereItLooked`,
`TestTheStandardScriptLookupWalksUpToTheCheckout`.*

## 10. The release verbs are in the command reference

The five verbs that put binaries on every bench in the fleet were declared in this spec, in
SPEC-UPDATE, in the help string — and in nobody's command reference. `docs/CLI.md` is what the
dogfood ledger reads, so a verb missing from it is a verb nothing asks to have been run by a
non-author. `### The release verb` under `## nova-update` is that section, and a test holds it
against `internal/release.Verbs` so a sixth release verb fails on the day it is added.

*Tests: `TestTheCommandReferenceDeclaresEveryReleaseVerb`,
`TestTheFourthDogfoodsLessonsAreInTheReleaseSpec`.*

## 11. The windows bench is a target like any other

The Ryzen Threadripper is the fleet's first Windows bench (Emma's standard,
[BENCH-WINDOWS.md](BENCH-WINDOWS.md)), and everything about releasing to it is a **decision** rather
than a default. Five of them, all made before the machine arrived so that `nova-update release build
--platform windows-amd64` and the fan-out behind it work on the day it is plugged in.

**The shell on the far side is POSIX, and that is Emma's decision, not a new one.** BENCH-WINDOWS.md
names the bench's ssh shell as Git Bash (`C:\Program Files\Git\bin\bash.exe`), or native OpenSSH with
Bash in `sshd_config`, and `internal/pulse/fleetstandard.go`'s windows checks are POSIX shell that
reach for `powershell.exe -NoProfile -Command '...'` only for the questions only PowerShell can
answer. `adopt` and `pull` match it: they compose `mkdir -p`, `tar -C`, `cat`, `test -d` and `rm -f`
there exactly as on a Linux bench, and reach for no PowerShell at all.

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
`release build --platform windows-amd64` on the Studio or on hulk produces a `nova-update.exe` this
host cannot execute, so the build cannot ask it whether it answers `version`. What the build promises
is the checksum round trip — written, read back, verified, `verified=<n>` on the receipt — and nothing
more; the receipt carries no claim about anything having been run. The version stamp is asserted where
the binary can actually run: by `install` on the bench, which probes every file it is about to replace
through its own `version` verb, and by CI's windows leg. `GOOS=windows go vet ./...` is clean, which
is the whole of what a non-windows host can say about windows code before the machine exists.

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

## What this file does not cover

The verbs themselves, the machines file, the retire rule, where `adopt` runs from and Johnny's read of
`adopt` are all in [SPEC-UPDATE.md](SPEC-UPDATE.md). SPEC.md's **Conventions** govern throughout — exit
codes, the one-line grammar, the field law, no guessed paths — and no unit test of any of this reaches the
network or a real machine.
