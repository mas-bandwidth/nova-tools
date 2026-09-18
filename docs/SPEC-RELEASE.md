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

## What this file does not cover

The verbs themselves, the machines file, the retire rule, where `adopt` runs from and Johnny's read of
`adopt` are all in [SPEC-UPDATE.md](SPEC-UPDATE.md). SPEC.md's **Conventions** govern throughout — exit
codes, the one-line grammar, the field law, no guessed paths — and no unit test of any of this reaches the
network or a real machine.
