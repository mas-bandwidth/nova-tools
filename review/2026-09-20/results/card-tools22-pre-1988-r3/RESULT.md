RESULT tools22-pre-1988-r2 sha=5298f6be12ea — a pre-read of mas-bandwidth/nova-tools#1988 at head 6b5bc84867b9: nova-work.asd: discover the tests, keep src explicit (#1947)
PREREAD 1988 claims=18 proven=15 unproven=3 defects=0 high=0

PR 1988
HEAD 6b5bc84867b99cc93519568c6c39b6ff0b29dc57
BASE dev
MERGE-BASE a78f3ea93b08a1ac5fd01608ecc61b1040f3a6f2
BEHIND 6
FILES 3 production, 4 test
LINES +820 -52

### CLAIMS

1. The `nova-work/tests` system in `nova-work.asd` no longer lists test components explicitly; it uses `#.(nova-work-asdf:test-components)` to discover them at read time.
   PROVEN-BY lisp/nova-work/nova-work.asd:282 — Go diff line removing all `(:file "tests/...")` entries and replacing with `(asdf:defsystem "nova-work/tests" ... :components #.(nova-work-asdf:test-components))`

2. The test prelude is declared as `+test-prelude+ = '("tests/harness" "tests/acceptance")`, loaded before discovery, because harness defines `deftest` and acceptance provides shared fixtures AND loads per-slice files under `tests/acceptance/`.
   PROVEN-BY internal/ci/lispkernel_class_test.go:136,263-264 — `testPrelude` slice checked via `reflect.DeepEqual(prelude, testPrelude)` against the `.asd` text

3. Everything after the prelude is sorted by canonical name using `string<`, so the list is deterministic across machines and checkouts.
   PROVEN-BY lisp/nova-work/nova-work.asd:485 — `(sort (copy-list names) #'string<)` in `canonical-test-names`

4. The Go CI test `TestEveryKernelSourceIsACompiledComponent` was updated from a single-component-list check to a two-part check: `src/` is still verified named-or-debt, and `tests/` is verified by shape (discovery forms present, prelude declared, no hand-written components, parity file exists).
   PROVEN-BY internal/ci/lispkernel_class_test.go:170-297 — sub-tests `"tests are discovered, not written out"` and `"the discovery is still there"`

5. No test file is hand-written in the `nova-work/tests` `:components` list; only the prelude is declared.
   PROVEN-BY internal/ci/lispkernel_class_test.go:245-250 — sub-test asserts `componentsOf(testsPart)` has length zero

6. The Lisp-side discovery functions are exported from `#:nova-work-asdf` package: `+test-prelude+`, `test-components`, `discovered-test-names`, `canonical-test-names`.
   PROVEN-BY lisp/nova-work/nova-work.asd:399-403 — `(:export #:discovery-error #:+test-prelude+ #:test-components #:discovered-test-names #:canonical-test-names)`

7. File names are validated for safe characters (letters, digits, `-`, `_`, `.`), no leading dot, and no `..` escape sequences.
   PROVEN-BY lisp/nova-work/nova-work.asd:439-452 — `check-file-name` function

8. Symbolic links are refused: if a `.lisp` file resolves to a different location than its directory, a `discovery-error` is signalled.
   PROVEN-BY lisp/nova-work/nova-work.asd:454-468 — `check-not-a-link` function

9. Duplicate component names and case-only collisions are rejected during discovery.
   PROVEN-BY lisp/nova-work/nova-work.asd:470-485 — `canonical-test-names` with hash-table tracking

10. `discovered-test-names` reads the `tests/` directory via `uiop:directory-files`, validates each file, excludes the prelude, and returns sorted names.
    PROVEN-BY lisp/nova-work/nova-work.asd:487-509 — `discovered-test-names` function

11. `test-components` appends the prelude to the discovered set and formats everything as `(:file "name")` forms.
    PROVEN-BY lisp/nova-work/nova-work.asd:511-515 — `test-components` function

12. Documentation was updated in three places: SPEC-CI.md (`kernel-components` rule explains two halves), README.md ("Adding a test file" section with reload caveat), and nova-work.asd header comments (TWO LISTS, ON PURPOSE).
    PROVEN-BY docs/SPEC-CI.md:11-43, lisp/nova-work/README.md:14-42, lisp/nova-work/nova-work.asd:5-94

13. A new Lisp test suite (`asd-discovery.lisp`) proves on every run: parity between components and directory, exactly-once registration, prelude order, no recursion into `tests/acceptance/`, and all refusal behaviors.
    PROVEN-BY lisp/nova-work/tests/asd-discovery.lisp:665-810 — seven `deftest` cases covering each property

14. A shell script (`asd-order-check.sh`) empirically measures that order after the prelude carries no meaning: runs the suite in current (legacy), sorted, and reverse-sorted order in fresh images, comparing test-name sets and outcomes. Also verifies loading from a `git archive` with no Git.
    PROVEN-BY lisp/nova-work/tools/asd-order-check.sh:1-175 — three modes (`orders`, `archive`, `live`) with comparison logic

15. A legacy ordering file (`asd-order-legacy.txt`) preserves the pre-#1947 hand-maintained order for reproducible comparison in the `cur` mode of the order-check script.
    PROVEN-BY lisp/nova-work/tools/asd-order-legacy.txt:1-40 — 28 entries matching the old test list minus prelude

16. The `nova-work` system uses `(asdf:defsystem ...)` instead of plain `(defsystem ...)`.
    UNPROVEN — cosmetic change confirmed by diff but no mechanism requires or verifies this; likely works due to ASDF convention

17. The `notCompiled` map need not contain any `tests/` entries since discovery guarantees every regular test file is compiled.
    PROVEN-BY internal/ci/lispkernel_class_test.go:293-296 — loop checking for `tests/` prefix in notCompiled keys, which currently has none

18. Adding a new test file no longer requires touching `nova-work.asd`; just writing the `.lisp` file registers it automatically.
    PROVEN-BY lisp/nova-work/README.md:14-16 — "Write `tests/<name>.lisp` and run `./run-tests.sh`. Do not add a line to `nova-work.asd`."

### DEFECTS

DEFECTS none

### QUESTIONS FOR THE REVIEWER

1. `check-file-name` restricts test file names to `[A-Za-z0-9._-]`. Is this charset intentionally minimal to ensure compatibility across git, ASDF, and all filesystems — or could it be expanded (e.g., to allow unicode for internationalized project names)? The PR says "characters that mean the same thing to git, to ASDF's pathname parsing and to every filesystem in the fleet," suggesting the constraint is deliberate but the reasoning warrants explicit confirmation.

2. `tools/asd-order-check.sh` is a substantial shell script (158 lines) that creates temp directories, runs SBCL multiple times, compares outputs, and requires both `sbcl` and `git`. Should this be integrated into CI as a gated step, or is it intended as manual/tooling-only? If automated, what is the expected runtime budget given it spawns three fresh SBCL images plus an archive load?

3. `discovered-test-names` uses `uiop:directory-files` which does NOT recurse — nested files like those under `tests/acceptance/` are excluded by design. Is this non-recursion behavior guaranteed by UIOP's `directory-files` implementation, or should there be an explicit guard against future UIOP changes that might introduce recursive globbing?

Left owed

Did not execute `asd-order-check.sh` or `asd-discovery.lisp` (requires running Lisp image); reviewed code textually. Did not vet the Go test binary beyond `go build` succeeding. The Lisp helper functions (`fail`, `name-char-p`, `check-file-name`, etc.) were not executed — their refusal paths rely entirely on the asd-discovery.lisp tests which I did not run.

git status --short

git rev-parse HEAD
d576bf6bbabb39068096a97b4560de9b5e245970

