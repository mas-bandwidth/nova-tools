# nova-memory dogfood, 2026-10-06 (dsh)

Reviewer: Johnny Grok.

Read as a stranger: `nova-memory -h`, `nova-memory help`, `nova-memory <verb> -h`, and the nova-memory page in `docs/CLI.md`. Built on hetzner from 6ec8bb02edc83283630f2e10e21bd035b3336f65, then the branch fast-forwarded to df30ce088344588bf75a86168057f468710ae943. Nothing under `cmd/nova-memory`, and nothing in the nova-memory page of `docs/CLI.md`, changed in that fast-forward. The job copy has no `.git`, so the version line is `nova-memory devel linux/amd64 go1.26.6`. Every verb (quickstart, stats, search, check, verify, eval, boot, version) ran once with its real flags against scratch directories, refusals included. No Redis and no server.

The banner's setup corpus and the fixture quickstart matched the page, including the search transcript in `docs/CLI.md`, apart from the build duration. Findings are only where a run did not.

## 1. `boot` with two `--root` flags loads the last tree and says OK — URGENT

**Command:**

    nova-memory boot --root ./corpus --root ./other2 --pin pin-one.txt

**Printed:**

    BOOT OK files=1 bytes=67

exit 0. The same pin with the roots reversed (`--root ./other2 --root ./corpus`) printed `BOOT OK files=1 bytes=199`. The two files at `notes/jetty.md` are 67 bytes and 199 bytes.

**Expected:** `boot -h` shows one `--root`, and `verify` refuses a second root with `VERIFY REFUSED: --root names exactly one tree for verification, but 2 were given`. The banner instead says `--root` is repeatable and that several roots are indexed together. I expected a refusal, or both trees named in the result. Boot kept the last path, printed no root, and exited 0. The bytes are the other tree's file.

**Grade:** URGENT

## 2. `stats -h` says nothing is excluded by default, and `.git` is — URGENT

**Command:**

    nova-memory stats --root ./corpus

**Printed:**

    STATS OK schema=nova-memory/2 files=1 chunks=1 bytes=19 vocab=3 avg-terms=3.0 build=101.322µs
    STATS OK class=notes chunks=1

The corpus held `notes/jetty.md` (19 bytes) and `.git/hidden.md`.

**Expected:** `nova-memory stats -h` says `--exclude` is "path or glob to skip, repeatable (nothing is excluded by default)". I expected `files=2`. `nova-memory help` says "Nothing is excluded by default except .git", and the run followed that (the 19-byte note only). The verb help is false.

**Grade:** URGENT

## 3. An `--exclude` that matches nothing is a silent success — NEXT

**Command:**

    nova-memory stats --root ./corpus --exclude ./corpus/notes/jetty.md

**Printed:**

    STATS OK schema=nova-memory/2 files=1 chunks=1 bytes=19 vocab=3 avg-terms=3.0 build=74.931µs
    STATS OK class=notes chunks=1

exit 0.

**Expected:** the note skipped, or a refusal that the pattern matched no file. The same note excluded as `notes/jetty.md` (relative to `--root`) did match, and the run then refused with `no markdown files found`. Help calls `--exclude` a path or a glob and never says the path is relative to `--root`. A path written the way `--root` is written does nothing, and the exit stays 0.

**Grade:** NEXT

## 4. An eval miss prints the query with spaces turned into `\x20` — NEXT

**Command:**

    nova-memory eval --root ./corpus --channels bm25 --k 3 --floor 0.5 gold-miss.tsv

**Printed:**

    EVAL MISS query=quantum\x20entanglement\x20of\x20the\x20tide expected=notes/nope.md
    EVAL FAIL recall@3=0.000 below floor 0.500 (0/1, misses=1 shown=1, mrr=0.000, channels=bm25)

exit 1. The gold row was `quantum entanglement of the tide` followed by a tab and `notes/nope.md`.

**Expected:** the query on the miss line as it was written, so it can be read and rerun. The floor failure itself is right, and a row with no tab is refused with the format `query<TAB>expected[,expected]`. The miss line is the part that hides the words.

**Grade:** NEXT

## 5. `--k` given twice keeps the last value and does not say so — NEXT

**Command:**

    nova-memory search --root ./corpus --channels bm25 --k 1 --k 3 lantern

**Printed:**

    SEARCH OK hits=2 k=3 channels=bm25 files=2 chunks=2: query="lantern"
    SEARCH CAL score=1.46 score-channel=bm25 probe=unrelated-control
    SEARCH HIT rank=1 score=0.20 score-channel=bm25 fused=0.01667 class=notes name=- type=- root=./corpus: notes/index-notes.md:1 "[Lantern care](lantern.md) keeps the glazing clean."

exit 0.

**Expected:** a refusal. `--k` is not repeatable on `search -h`. The run used 3 and reported `k=3`, so the value is visible, and the first value was dropped with no word about it.

**Grade:** NEXT

## 6. The page's `check` example does not run — NEXT

**Command:**

    nova-memory check --root ./corpus --channels bm25 --k 3 draft.md

**Printed:**

    MEMORY REFUSED: open draft.md: no such file or directory; run: nova-memory help

exit 2, from the fixture directory the page's first run uses. There is no `draft.md` there.

**Expected:** the transcript in `docs/CLI.md` (scores 13.62, 11.40, 7.56). The candidate sentence on that page is cut with an ellipsis, so it cannot be retyped, and the banner's setup copies a different file to `draft.md`. Quickstart and the hand `search` on that fixture matched the page, apart from `build=` which moves every run.

**Grade:** NEXT

READ 7/10. The banner and the fixture quickstart are enough to run the tool, and that transcript matches `docs/CLI.md`, but verb help denies the `.git` exclusion the banner states and the check example is not a command you can run.

USE 7/10. Stats, search, check, verify, eval, and a single-root boot did what the help said, refusals named a remedy, and the file cap refused an 8388609-byte note, but a second `--root` on boot returns the other tree under `BOOT OK` and a non-matching `--exclude` looks like success.

urgent=2 next=4
