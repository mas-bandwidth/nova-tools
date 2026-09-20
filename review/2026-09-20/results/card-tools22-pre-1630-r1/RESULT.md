RESULT tools22-pre-1630-r2 sha=5298f6be12ea — a pre-read of mas-bandwidth/nova-tools#1630 at head 6c568977e7ea: nova-work: make the kernel load without muffling every warning (#1612)
PREREAD 1630 claims=7 proven=7 unproven=0 defects=0 high=0

PR 1630
HEAD 6c568977e7eafcbe469af8107eb0a7b69f42bce4
BASE dev
MERGE-BASE d5666ab96a6e2f41c241021f07d1861dc386064f
BEHIND 25
FILES 7 production, 2 test
LINES +276 -158

CLAIMS

1. run-tests.sh narrows `(handler-bind ((warning #'muffle-warning)) ...)` to `((style-warning
   #'muffle-warning))` so that full WARNINGs (duplicate definitions, clobbered constructors) are
   no longer swallowed during kernel load. PROVEN-BY lisp/nova-work/run-tests.sh:15 the single form
   changed from `warning` to `style-warning`, which would cause any remaining full-warning defect to
   produce a non-zero exit from `asdf:load-system`.

2. The duplicate `machine` defstruct in fleet.lisp (defined again in the folded #1102 section with
   identical slots and constructor names) is consolidated into one struct with two `(:constructor
   ...)` options, eliminating SBCL's "Duplicate definition for COPY-MACHINE found in one file".
   PROVEN-BY lisp/nova-work/src/fleet.lisp:25-40 one struct with both `%make-machine` and
   `make-machine` constructors at the top of the file; lisp/nova-work/src/fleet.lisp:1503 the
   old second definition replaced by a comment.

3. The dead bundle-intake block in operations.lisp (`request-bundle` struct,
   `read-request-bundle`, `%bundle-request`, `%bundle-apply`, `%replay-verdict`, `session-replay`)
   is removed because `src/transport.lisp` defines all six and loads after operations.lisp in the
   serial asd, making this copy never reached. PROVEN-BY lisp/nova-work/src/operations.lisp:1724
   replacement comment states location of live definitions; the entire block (147 lines) removed.

4. Two entries in `*acceptance-slices*` are deduplicated: `slice-09-state-export-replays.lisp`
   appeared three times, now once; `slice-10-fleet.lisp` appeared twice, now once. PROVEN-BY
   lisp/nova-work/tests/acceptance.lisp:66-95 the list body in the diff — two `-` lines for the
   extras removed, plus a positional reorder to maintain continuity.

5. A Go-based CI test detects any file named in `nova-work.asd` or listed in
   `*acceptance-slices*` that contains two top-level definitions (`defun`, `defmacro`,
   `defgeneric`, `defparameter`, `defvar`, `defstruct`) of the same name within that file,
   skipping read-time conditional pairs. PROVEN-BY internal/ci/lispduplicate_class_test.go:10
   `TestNoKernelFileDefinesTheSameNameTwice` — parses each kernel file with a regex matching
   top-level forms, tracks namespace-scoped names (fn/type/var), and errors if `seen[name]` exists
   on a non-conditional repeat. Read-conditional detection via previous-non-blank-line prefix
   check handles the `#+sbcl`/`#-sbcl` pairs in transport.lisp.

6. A second Go test ensures `*acceptance-slices*` is a set: no slice filename appears more than
   once, preventing duplicate `load` calls and double-registered `deftest` cases. PROVEN-BY
   internal/ci/lispduplicate_class_test.go:140 `TestNoAcceptanceSliceIsLoadedTwice` — scans the
   parameter binding for all slice filenames, counts occurrences per name, and errors if any count
   exceeds one.

7. The `lispduplicate` class is documented in docs/SPEC-CI.md with rule, hurt, test names,
   allowlist, remedy line and narrowings, and AGENTS.md is updated with the new entry. PROVEN-BY
   docs/SPEC-CI.md:1466-1503 four-line description covering rule, hurt (#1612 with specific error
   messages and statistics), test names, allowlist (none), remedy line text and narrowings.

DEFECTS none

QUESTIONS FOR THE REVIEWER

1. run-tests.sh:15 narrows muffle-scope from `warning` to `style-warning`. The comment explains
   ~83 undefined-function style-warnings are normal due to serial loading with forward references.
   Is it safe to assume future serial-load additions will not introduce non-style WARNINGs that
   would also get exposed? Could something else (e.g., `type-conflict`, `program-error`) appear
   under the new regime?

2. The acceptance.slices order changed during dedup: `slice-09-state-export-replays.lisp` moved
   from positions 2/5/7 to position 5 only, which shifted downstream indices. The comment says
   "No case is removed here: the 314 that ran still run." Does the downstream slice ordering matter
   for anything in the suite (state carried between slices, cross-slice expectations)?

3. transport.lisp:766 comment references `docs/SPEC-WORK.md:5340` for determining which grammar
   shape `session-identity-line` should use. That line number may be stale or the section may have
   moved. Should this reference be verified or updated?

Left owed — nothing. I read the full diff (550 lines), all changed files in their entirety, the
merge-base commit message context, and traced the flow of each removal through the asd ordering.

git status --short

git rev-parse HEAD
d576bf6bbabb39068096a97b4560de9b5e245970
