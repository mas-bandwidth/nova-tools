# The release — specification (draft 2, Johnny's read folded in)

Glenn, 2026-09-18: *"a tool is not finished until it is tested, dogfooded by a
non-author on real work with the edges filed, the feedback applied, documented
and released."* That sentence names seven states and the last one had no
document. `nova-update release` is the machinery ([SPEC-UPDATE.md](SPEC-UPDATE.md)
holds its flags); this file says what the machinery is FOR — what a release IS,
what it may not be cut without, how it reaches a bench, how the bench proves it
arrived, how a bad one is taken back, and what the number on the front means.

This spec is normative where it describes the code on `dev` at `220c05d7`, and
**every rule that describes something not yet built says so in its own text**.
[SPEC.md](SPEC.md)'s **Conventions** govern throughout — exit codes, the
one-line grammar, the field law, `internal/oneline`, `internal/bounded`, no
guessed paths — and are not restated. Where this file and the code disagree,
one of them has a bug and the tests decide which. Draft 2 folds in the security
lane's read (`johnny-860d359211aa`, 2026-09-18) and the coordinator's answers
(`rowan-a13a1678d61b`): what those two settled is recorded under **Settled since
draft 1**, and one question is still open under **Still open**.

It exists because of the pit stop of 2026-09-17: four Linux benches ran
six-hour-old tools while the coordinator believed they were current, and the
reason that could happen is that the last mile was a shell script — build,
cache, copy, install and verify inside one nested `ssh` quoting, with no test of
any of it and no step that could SAY what it had done.

| the failure it closes | the rule that closes it |
|---|---|
| "released" meant whoever built last on the Studio | 1, 2 |
| a tag on a commit no check run ever judged | 5 |
| binaries nobody can tie back to the tag | 3, 4 |
| a build host holding keys to every bench | 13 |
| bits verified only against a checksum that travelled with them | 4, 14 |
| a bench believed current because the coordinator said so | 19, 20 |
| a bad release with no way back but a rebuild | 21, 22, 23 |
| a version number that meant whatever the cutter felt | 24, 25, 26 |
| a tool called done by the person who wrote it | 27 |

---

## What a release IS

1. **A release is a tag on a green commit on `main`, and nothing else is one.**
   Not a build somebody kept, not a `bin/` a friend is happy with, not a branch.
   The tag is created at the sha `--from` resolved, **after** that sha's binaries
   exist (rule 4), and it is created and never force-moved, because a tag that
   can be moved is a tag whose binaries and whose source can disagree. A version
   that is already a tag in the repository is refused before anything else
   happens.
   **Red test:** `a-tag-is-never-moved-only-created`.
2. **A release is a set of per-platform binaries, every one of them stamped with
   the version.** `release build` compiles every `cmd/nova-*` found by walking
   the checkout — never from a list, so a tool added today ships today — with
   `-trimpath` and one stamp composed once, `-s -w -X main.version=<version>`,
   into `<out>/<version>/<goos>-<goarch>/`. The set is per platform and the
   platform is in the path, so one artifact root holds every leg of one release
   at once. A tool that does not compile means **no checksum file at all**.
   **Red test:** `a-failed-compile-writes-no-SHA256SUMS`.
3. **The set is closed by `SHA256SUMS`, written last, over the whole set.** The
   file is in `sha256sum -c` format so a person can verify a copy by hand
   without this tool, it never lists itself, and it is the whole of what
   `install` trusts about a directory it did not build: **the names come from
   the file**, so a binary dropped into the directory afterwards is not part of
   the release and is never installed. A name carrying `/` or `\` is refused as
   a path.
   **Red test:** `a-binary-dropped-beside-the-set-is-not-installed`.
4. **The digest of `SHA256SUMS` is recorded in the tag's annotation, one line
   per platform** — `<sha256>  <goos>-<goarch>/SHA256SUMS` — so that the
   question *are these the release's bytes?* is answered against something the
   adopting host got from the **forge**, not against a file that travelled with
   the bits. A checksum file carried beside the binaries proves only that the
   directory agrees with itself.
   **The order is: build from the sha, then tag** (Johnny, 2026-09-18, settling
   open question A). The digest cannot exist before the binaries, and the
   binaries are built from the sha, so `build` runs first against the resolved,
   gated commit and the tag is created **last**, carrying one digest line per
   platform in its message. A build that fails therefore leaves no tag, which is
   the property `cut` already has and the reason a tag is the last mutation of a
   release rather than the first.
   **The tag must be an ANNOTATED object, and today's is not: that is a bug.**
   `GH.Tag` POSTs `git/refs`, which creates a **lightweight** ref with no
   message, while the comment beside it calls it "annotated-by-reference"
   (Johnny, 2026-09-18: *lightweight `git/refs` is a bug*). A lightweight tag has
   nowhere to put a digest, so rule 4 cannot stand on one. The fix is a `git/tags`
   POST creating the tag object with its message, then `git/refs` pointing at
   that object, and it is filed for the release lane as the first item of this
   rule's work.
   **NOT BUILT at `220c05d7`:** the annotated tag, the digest inside it, the
   build-then-tag order, and `install`'s check against it.
   **Red tests:** `install-refuses-a-SUMS-whose-digest-is-not-the-tags`;
   `the-tag-is-an-annotated-object-not-a-bare-ref`;
   `a-failed-build-leaves-no-tag`.

## What gates it

A release is cut only when every gate below is green **on the sha being
tagged**. Each is a command with an exit code; none of them is a judgement
somebody makes in prose.

5. **CI green on that commit, judged by the forge, not by memory.** `cut` reads
   every check run recorded against the sha and refuses unless all of them have
   COMPLETED and none concluded in a failure. `success`, `skipped` and `neutral`
   pass; `queued` and `in_progress` are *not finished*, a separate refusal with
   a separate remedy. **A commit no check run has ever judged is not green: it
   is unjudged**, and unjudged is the state that let four benches run stale
   tools. A tag cannot be amended, so the evidence is read before it is written.
   **Red tests:** `cut-refuses-an-unjudged-commit`;
   `cut-refuses-a-pending-run-differently-from-a-failed-one`.
6. **`nova-check dogfood gate --cli docs/CLI.md --receipts <dir> --require-all`
   exits 0.** Every verb in the command reference has been run on real work by
   somebody who did not write it, and every edge filed against it is cleared.
   This is the gate's stated purpose — *the line a release calls* — and
   `--require-all` is the release's spelling of it, not the ledger's.
   **Red test:** `the-release-lane-calls-the-dogfood-gate-with-require-all`.
7. **The docs are current, and "current" means pinned to the binaries.**
   [CLI.md](CLI.md) is the inventory the dogfood gate reads, so a verb missing
   from it is a verb no gate can demand. Before a cut, `nova-version moved
   --from <previous tag> --to <sha>` is run and its added and deleted entries
   must already appear in `CLI.md` — the note is read off the binaries' own
   `help`, never a hand list, so this is a mechanical comparison and not a
   reviewer's impression.
   **Red test:** `cut-refuses-when-moved-names-a-verb-CLI-md-does-not`.
8. **Anything touching secrets, the sandbox or the card image is read by the
   security lane before the cut, and what "touching" means is this path list.**
   Johnny, 2026-09-18: that sitting reads those three surfaces, and it does not
   merge. A range touches them when any file it changes is under one of these
   prefixes:

   ```
   internal/secrets/
   cmd/nova-secrets/
   internal/sandbox/
   cmd/nova-sandbox/
   infra/image/
   scripts/coordination/
   ```

   **The list lives here, in the spec, and changing it is a pull request the
   security sitting reads** (Johnny, settling open question B). It is not a label
   on a pull request — labels drift, and the thing that must not drift is what
   counts as security-relevant — and it is not a note id in the changelog, which
   is after the fact: a note id records that a read happened, and the gate has to
   be able to say a read is *required* before anybody has done one. `cut` reads
   the compare range it already computes for the changelog, classifies each
   changed path against the list, and refuses a cut whose range touches the list
   with no recorded read for that sha.
   **Red tests:** `cut-refuses-a-range-touching-internal-secrets-with-no-read-recorded`;
   `the-classification-list-is-read-from-the-spec-not-from-a-label`;
   `a-range-touching-nothing-on-the-list-needs-no-security-read`.
   **NOT BUILT at `220c05d7`:** `cut` reads the compare range but does not
   classify it.
9. **The gates are ANDed and each one names itself when it says no.** A cut
   blocked by three gates prints three lines, not the first one: a refusal that
   names one of several sends somebody round the loop once per gate. This is the
   same argument the flag validator already makes — every missing flag at once.
   **Red test:** `a-cut-blocked-by-three-gates-names-three`.

## How it is cut, built, installed and adopted

Four verbs, each of which can refuse, and the four are the four steps of the
shell script they replace. Rule 1 of [SPEC-UPDATE.md](SPEC-UPDATE.md) governs
throughout — every path is a flag, no default path, no cwd, no `$HOME` — and its
rule 3 governs the child processes: argv, never a shell.

10. **`cut` decides and records.** It resolves `--from` on the forge, applies the
    gates of rules 5–9, reads the highest existing version tag by **semantic**
    order (`v0.15.10` after `v0.15.3`, which a string sort gets backwards and
    which would put seven releases' work in one patch's changelog), writes a new
    `--changelog` section from the pull requests merged since — their numbers,
    their titles, and for an integration batch the members named in its own body,
    so a batch does not hide ten pieces of work behind one number — and creates
    the tag. `RELEASE CUT version=… sha=… prs=… previous=… changelog=… dry-run=…`.
    `--dry-run` decides everything and writes nothing. A version that could not
    survive `-X main.version=`, a printf format or the field law — whitespace,
    `%`, `=`, a missing `v`, not three dotted numbers — is refused here, before
    anything is built or tagged.
    **`--changelog` names `docs/RELEASE-NOTES-<version>.md`**, one file per
    release, and what `cut` writes into it is the **mechanical** list: the pull
    request numbers, their titles, the batch members. The prose release notes —
    the page a friend reads to find the row that is their actual problem today —
    are Stella's, written beside it under the same version's name. Two documents
    with one flag was the confusion; one file per release, mechanical list first
    and prose above it, is the settlement.
    **Under rule 4's settled order the tag step moves to the end**: `cut` gates,
    decides the version and writes the changelog section, `build` produces each
    platform's set, and the annotated tag is created last with the digests in it.
    At `220c05d7` `cut` still tags before anything is built.
    **Red tests:** `cut-dry-run-writes-nothing-and-tags-nothing`;
    `previous-tag-is-semantic-not-lexical`;
    `the-changelog-file-is-named-for-the-version`.
11. **`build` compiles one platform's set from one checkout.** `--source` is a
    nova-tools checkout, `--platform <goos>-<goarch>` cross-compiles (this host
    when it is absent). The file names are decided for the **target**, never for
    the host: `ToolFile` puts `.exe` on a windows release wherever it was built,
    and reading `runtime.GOOS` at each such site instead is the defect that is
    right on the host that happens to match and silently wrong everywhere else.
    The caller's `GOFLAGS` does not reach a release build.
    **Red test:** `a-windows-release-built-on-darwin-names-exe-files`.
12. **`install` verifies the whole set before the first rename.** Every artifact
    is hashed against `SHA256SUMS` — and, under rule 4, the SUMS against the
    tag's digest — and only then does the first file move. A set checked file by
    file as it installs puts good binaries beside a bad one and leaves the box in
    a state no version answers for. Each file is written beside its target and
    **renamed over it**, so a process already running keeps its own inode and no
    reader ever sees a half-copied binary. A tool whose installed binary already
    answers the version is skipped, and the question is asked of the **binary**,
    by the name it was installed under — never of a marker file, which says what
    somebody meant to install. `RELEASE INSTALLED version=… tools=… skipped=…`.
    **On windows the rename trick does not exist**: a move *over* a running
    `.exe` raises `ERROR_SHARING_VIOLATION`, so the live file is renamed **aside**
    first and the new one moved in, and the aside copy goes on the next install —
    the *what cannot be promised on Windows* list in
    [SPEC-SANDBOX.md](SPEC-SANDBOX.md) is where that platform fact is recorded.
    **Red tests:** `install-verifies-the-whole-set-before-the-first-rename`;
    `install-asks-the-binary-not-a-marker`;
    `install-on-windows-renames-the-running-exe-aside`.
13. **`adopt` is run from the adopting host, and the build host is never given
    the fleet's trust.** Johnny, 2026-09-18: *do not grant hulk an identity the
    others trust; a compromised build host must not reach vision, space or mini;
    the Studio already has that trust, keep it there.* So the topology is fixed:
    **hulk builds, the Studio adopts.** The Studio pulls the stamp directory from
    the build host, verifies it against the digest it already holds from the tag
    (rule 4), and only then reaches each bench.
    **`--from` learns a remote form: `--from <host>:<dir>`** (Johnny,
    2026-09-18, settling open question C), so the pull is inside the verb and has
    the verb's refusals rather than being a step somebody remembers. The host
    half is the same narrow name rule 15 already enforces, the directory half is
    an absolute path, and the pull is a copy, never a remote command composed by
    interpolation. **`ForwardAgent` is never set on it**, on this edge or any
    other: an agent forwarded to a build host is the build host holding the
    fleet's trust by another route, which is the thing this rule exists to
    prevent. A local `--from <dir>` keeps working unchanged.
    **Red tests:** `adopt-refuses-to-run-on-a-host-that-is-not-the-adopting-host`;
    `from-host-dir-pulls-before-it-verifies-and-verifies-before-it-sends`;
    `no-ssh-argv-this-package-composes-carries-ForwardAgent`.
    **NOT BUILT at `220c05d7`:** `adopt --from <dir>` reads a **local** artifact
    root only.
14. **The release installs itself, by absolute path, on the far side.** The
    `nova-update` that runs the remote install is the one `adopt` just sent — so
    a bench provisioned this morning adopts with the same command as one a
    version behind — and it is invoked as `<dest>/<version>/<goos>-<goarch>/nova-update[.exe]`,
    an absolute path, **never a bare name on `$PATH`**. The far-side binary is
    the one just verified. A release carrying no `nova-update` for the target
    platform is refused before the first machine, because no machine could run
    the install.
    **Red tests:** `the-remote-argv-is-an-absolute-path-never-a-bare-name`;
    `adopt-refuses-a-release-that-carries-no-nova-update`.
15. **Machine names are narrower than what `ssh` accepts.** One name per line in
    `--machines`, blanks and `#` comments skipped, `[A-Za-z0-9_.@-]+` and nothing
    else, checked before `ssh` is reached — a name that cannot carry a space, a
    quote, a semicolon or a `$` cannot be half of what went wrong. Remote paths
    are slash paths whatever the adopting host is.
    **Red test:** `a-machine-name-with-a-semicolon-is-refused-before-ssh`.
16. **A machine that refuses does not stop the fleet.** Each machine gets its own
    `RELEASE ADOPTED machine=… version=… tools=… skipped=… bin=…` or its own
    `RELEASE REFUSED machine=… version=…: <cause> (<remedy>)`, and the verb ends
    with `RELEASE ADOPT <OK|FAIL> machines=… adopted=… refused=… version=…`. Exit
    1 if any machine refused; the others are still reported, because the shape
    this replaces reported the whole loop as one line.
    **Red test:** `one-refusing-bench-does-not-hide-the-four-that-adopted`.
17. **The nevers, and they are rules, not advice.** No step of a release may:
    `ForwardAgent yes`; `eval` remote output; put a key on argv or anywhere `ps`
    shows it; `scp` a `.key`; interpolate an unvalidated host or path into
    `ssh bash -c`; install before the checksum; `rm` the last-good stamp on a
    retire; run a card with `--network=host`; mount `~/.config/nova-secrets`;
    bake a credential into an artifact; print a key; widen the darwin sandbox
    profile; skip the parent-guard. Keys never move between machines: a
    credential the far side needs it already has, reached with `nova-secrets
    exec` there. `gh` and `ssh` each carry their own credential and this package
    reads, logs and passes none of it.
    **Red test:** `the-release-package-touches-no-secret` — a scan of every
    argument every edge is handed, plus every byte the verbs write.

## How adoption is proven

18. **A receipt is read from what the remote SAID, never from its exit code.** A
    shell that could not find the binary exits non-zero for the same reason a
    full disk does, and only one of those has the same remedy. `adopt` reads the
    remote's `RELEASE INSTALLED version=… tools=… skipped=…` line; **no such line
    is a refusal** even if the exit status was 0, and a line naming a different
    version is a refusal naming both.
    **Red test:** `a-silent-remote-that-exits-zero-is-still-a-refusal`.
19. **`nova-version snapshot` on each machine after adoption is the proof, and a
    mixed set is a failure.** Per [SPEC-VERSION.md](SPEC-VERSION.md): the
    snapshot reads each binary's own `version`, refuses two stamps in one `--bin`
    naming both, and writes one row per binary. A release is adopted on a machine
    when that machine's snapshot reports one stamp and it is the release's, and
    the fleet has adopted when every machine's does.
    **Red test:** `a-bench-with-two-stamps-is-not-adopted`.
20. **The receipts are kept, per machine, per release.** One directory per
    version holding one snapshot TSV per machine, so `nova-version diff --from
    <previous>/<machine>.tsv --to <this>/<machine>.tsv` is the answer to *what
    moved on that bench* without asking the bench. The coordinator's belief is
    never the record.
    **`adopt` takes the snapshot itself**, in the same remote turn as the install
    (Johnny and Rowan, 2026-09-18, settling open question D): the receipt and the
    proof are then one transaction, and the path of the snapshot it wrote is a
    field on the receipt line —
    `RELEASE ADOPTED machine=… version=… tools=… skipped=… bin=… snapshot=<path>`.
    A snapshot that refuses — a mixed set on that bench — makes that machine a
    `RELEASE REFUSED` even though the install itself returned, because a bench
    that cannot say what it is running has not adopted. One more remote call per
    machine is the price, and the alternative is a step something has to remember.
    **Red tests:** `a-release-with-a-machine-missing-its-snapshot-is-not-complete`;
    `a-mixed-set-after-install-makes-the-machine-refused-not-adopted`.
    **NOT BUILT at `220c05d7`:** the snapshot is a separate verb a person runs.

## How a bad release is undone

21. **The previous set is kept, and going back is an install, not a rebuild.**
    Each version installs into its own stamp directory and `bin/` is pointed at
    it, the shape [SPEC-VERSION.md](SPEC-VERSION.md) rule 5 already uses for
    `apply --sha`. Undoing a release is therefore `nova-update release install
    --version <previous> --from <dir> --bin <dir>` — the same verb, the same
    verification, a version that is already on the disk — and it is one step, on
    a bench, with no forge and no build host in the path.
    **Red test:** `install-of-the-previous-version-restores-it-without-a-build`.
    **NOT BUILT at `220c05d7`:** `install` renames into `--bin` directly and
    keeps no stamp directory of its own, so today the previous binaries are gone
    once the rename lands. Open question **E** is whether `release install`
    adopts `apply --sha`'s staged-directory-and-link shape or keeps the rename.
22. **`--retire` deletes stale copies and refuses the live one.** Eighteen stale
    `nova-*` copies in `~/go/bin` shadow an adopted release on `$PATH`, and the
    remedy is a verb, not an `rm` somebody types: `release --retire <dir>`
    removes the copies under a validated path below a validated root, prints one
    line per file and a count, and **refuses outright when `<dir>` is the live
    stamp or the directory `bin/` points at** — never `rm` the last-good stamp.
    Deletion goes through the one guarded helper; CI refuses any other.
    **It is a `release` verb and the security lane reads it** (Johnny,
    2026-09-18, settling open question F): the stale copies came from `go
    install` rather than from a release, but the question *which directory is
    live* is a release's to answer, and a deletion verb is his read wherever it
    sits.
    **Red tests:** `retire-refuses-the-live-stamp`;
    `retire-refuses-a-path-that-is-not-below-its-root`.
    **NOT BUILT at `220c05d7`.**
23. **A tag is never deleted; a leaked release has its artifacts pulled.** The
    bad version keeps its tag and its changelog section, the next one supersedes
    it, and the changelog says what was wrong with it — a tag deleted and
    recreated is the one state the cut gate spends its whole length refusing.
    **But a release that leaked a secret is not undone by superseding it**
    (Johnny, 2026-09-18, settling open question H): *leaving the bits
    downloadable is the leak continuing.* So the remedy has three parts and all
    three happen — **the GitHub release assets are pulled**, binaries and
    `SHA256SUMS` both, so the artifacts are no longer downloadable; **the tag
    stays, as history, annotated superseded**, naming the version that replaces
    it and why; and **if a key moved, it is rotated**, which is the security
    lane's step and not this tool's. An `install` against a pulled release finds
    nothing and refuses; it never falls back to a cached copy.
    **Red tests:** `cut-refuses-a-version-that-is-already-a-tag`;
    `a-pulled-release-cannot-be-installed-from-a-cache`.

## What a version number means for us

24. **`v0.MINOR.PATCH`, and the `0` is honest.** No tool here has promised a
    stable interface across a sprint; the major stays `0` until one does, and the
    day one does is a decision on the bus, not a cutter's. The scheme in rules
    24–26 was proposed in draft 1 and carried on the bus without objection
    (Johnny, 2026-09-18); it is the estate's numbering until somebody proposes
    another.
    **Red test:** `cut-refuses-a-major-above-zero`. **NOT BUILT at `220c05d7`:**
    `ValidVersion` accepts any three numbers after the `v`.
25. **MINOR is the sprint; PATCH is the batch.** A sprint has a fixed scope and a
    finish line, so it gets a number: the sprint that follows `v0.15.2` opens
    `v0.16.0`. Each integration batch that lands on `main` inside that sprint is
    a PATCH — `v0.16.1`, `v0.16.2` — because a batch is the unit that goes green,
    gets adopted and can be rolled back to. It is the unit the changelog already
    groups by: a batch commit names its members in its own body and `cut` reads
    them.
    **Red test:** `cut-refuses-a-version-that-is-neither-the-next-patch-nor-the-next-minor`
    — a version that skips `v0.16.1` to reach `v0.16.4`, or that raises MINOR
    twice in one sprint, is a number nobody can read backwards.
    **NOT BUILT at `220c05d7`:** `cut` reads the previous tag but does not
    compare the new version to it beyond refusing an exact duplicate.
26. **A dev build is `v0.16.0-dev.<sha>` and is never a tag.** The next sprint's
    MINOR, `-dev.`, and the short sha of what was built. It survives every reader
    in the estate, measured: `release.ValidVersion` accepts it (the patch may
    carry a `-` suffix and the number before it is a number);
    [SPEC-UPDATE.md](SPEC-UPDATE.md) rule 4's read takes the whole token
    `0.16.0-dev.220c05d7`, so two dev builds that differ only in the sha are two
    builds and not one; and the stamp assertion's whole-token match does not
    mistake it for `v0.16.0`. A dev build is stamped, installed and adopted like
    any other set — it is just never tagged, and `cut` never sees one.
    **Red test:** `a-dev-stamp-round-trips-through-validversion-and-the-update-read`.

## The release's definition of done

27. **Six states, in this order, and none of them is skipped.** Glenn,
    2026-09-18:
    1. **tested** — the red tests each rule names exist and were seen red first;
    2. **dogfooded by a non-author** — on real work, with the edges filed, and
       the receipt is a file (rule 6), not the last speaker's memory;
    3. **fixes** — the feedback from (2) applied, and the edges closed;
    4. **documented by Stella** — [CLI.md](CLI.md) and the release notes, in the
       house tone, by somebody who did not write the tool;
    5. **released** — a tag on a green `main` sha, with the binaries and the
       digest (rules 1–4);
    6. **adopted** — every machine's receipt and snapshot (rules 18–20).
    A release is done at (6), not at (5). A tag nobody is running is a tag.
    **Red test:** `the-done-check-refuses-at-released-without-adopted`.

---

## Says NO when

Each a named line, exit 1 — the tool ran and the answer is no:

- a check run on the sha is failing, or has not finished, or there is none;
- `nova-check dogfood gate --require-all` names a verb no non-author has run;
- `nova-version moved` names a verb or flag `CLI.md` does not carry;
- a machine refused during `adopt` (the others are still reported);
- a machine's post-adoption snapshot reports a stamp that is not the release's,
  or reports two stamps;
- a release whose tag exists and whose machines have not all adopted it.

## Refuses when

Each a `RELEASE REFUSED: <reason> (<remedy>)`, exit 2 — the tool could not run:

- any required flag is missing (**all** of them named at once), or a positional
  argument was given, or `--timeout` is not positive;
- the version carries whitespace, `%` or `=`, has no leading `v`, or is not three
  dotted numbers after it;
- the version is already a tag in the repository;
- `--source` holds no `cmd/nova-*` directory, so the build would ship an empty
  set;
- `--platform` is not `<goos>-<goarch>`;
- there is no `SHA256SUMS` for that version and platform under `--from`; or a
  line of it is not a `sha256sum` line; or it lists no artifact; or it names a
  path rather than a file;
- an artifact's bytes do not match the recorded sum — or, under rule 4, the SUMS
  file's digest does not match the tag's;
- the release carries no `nova-update` for the target platform;
- `--machines` names no machine, or a line is not a machine name;
- `--from <host>:<dir>` carries a host that is not a machine name, or a
  directory that is not absolute;
- `--retire` names the live stamp, or the directory `bin/` points at, or a path
  not below its root.

## Deliberately does not

- **It does not decide the version.** `--version` is argv, where `ps` shows it.
  Rules 24–26 are what a person types, not what a tool computes; a tool that
  guessed the next number would be guessing the scope of a sprint.
- **It does not judge whether the release is any good.** The gates are
  mechanical; the read is Stella's and Johnny's.
- **It does not move a key, read one, log one or pass one.** `gh` and `ssh` carry
  their own; the far side reaches its own through `nova-secrets exec`.
- **It does not build on the machine that adopts.** Benches install; they do not
  compile a release. (Glenn, 2026-09-17: prebuilt binaries only.)
- **It does not run a shell**, locally or remotely. Every child is argv.
- **It does not touch the network in a unit test.** The forge, `ssh` and the Go
  toolchain are three interfaces with fakes behind them; a real cut, a real
  build and a real adopt are soak and nightly work (Glenn's hard rule,
  2026-09-17).
- **It does not delete a tag, a changelog section or the last-good stamp.**
- **It does not have a config file.** There is no file any path can arrive from.

## Settled since draft 1

Johnny's read of #1337 (`johnny-860d359211aa`, 2026-09-18) and Rowan's answers
(`rowan-a13a1678d61b`) settled eight of the nine. Each is folded into the rule
it belongs to and recorded here so a reader knows the decision was made rather
than assumed:

- **A** — build from the sha, then an **annotated** tag carrying the digest; the
  lightweight `git/refs` tag `GH.Tag` creates today is a bug, filed for the
  release lane (rule 4).
- **B** — the security classification is a **path list in this spec**, six
  prefixes, and changing it is a pull request the security sitting reads; not a
  label, not a note id (rule 8).
- **C** — `adopt --from <host>:<dir>`, and `ForwardAgent` never (rule 13).
- **D** — `adopt` takes the `nova-version snapshot` itself, in the same remote
  turn, and the receipt carries its path (rule 20).
- **F** — `--retire` is a `release` verb and the security lane reads it (rule 22).
- **G** — `--changelog` names `docs/RELEASE-NOTES-<version>.md`, one file per
  release: the mechanical list is `cut`'s, the prose above it is Stella's
  (rule 10).
- **H** — a leaked release has its **GitHub assets pulled**, its tag kept as
  history and annotated superseded, and any key that moved rotated (rule 23).
- **The version mapping** — `v0.MINOR.PATCH`, MINOR per sprint, PATCH per batch,
  dev builds `v0.16.0-dev.<sha>`, carried without objection (rules 24–26).

The Windows section's three are settled in its own text: `--place` defaults to
`job` and `wsb` opts in; `--memory` and `--cpu` live on `run` only; and a `.wsb`
that cannot report an exit status **refuses** rather than reporting a clean
close as 0.

## Still open

One, and it is Stella's:

- **E. Stamp directory or rename (rule 21).** `release install` renames into
  `--bin`; `nova-update apply --sha` stages a set and swaps a link
  ([SPEC-VERSION.md](SPEC-VERSION.md) rule 5). The rollback story wants the link,
  because a version that is still on the disk is a version `install --version
  <previous>` can reach. The rename is what makes an install safe while work is
  in flight, because a running process keeps its own inode. Two shapes do one
  job today, and a flag that picked between them per machine would be worse than
  either. Until it is settled, rule 21 describes the stamp directory and marks
  itself NOT BUILT.
