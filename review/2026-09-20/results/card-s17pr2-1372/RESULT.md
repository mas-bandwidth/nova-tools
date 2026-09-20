RESULT: s17pr2-1372 sha=CANNOT_READ

**CANNOT READ** — the base repo (expected at `repo/`, cloned from `/tmp/schema14-ftf.bundle`) is absent from this sandbox. Every attempt to access it failed: `cd repo` returned No such file or directory, `ls /tmp/schema14-ftf.bundle` returned Operation not permitted, and globbing for `**/*.md` found zero matches. The law documents named by the card (`docs/FIXED-FORM-ALGORITHM.md`, `docs/SPEC.md`, `docs/PORTING.md`) could not be read, so:

- Rule 1–7 numeric bounds (65536 for Rule 6; 64 for Rule 7) are unverifiable against the spec texts.
- Error atom names (`:layout_count_mismatch`, `:layout_kind_unknown`, etc.) are unverifiable against any document defining them.
- §5.6 retirement of runtime-walk layout checks vs the kept parse_layout validations cannot be confirmed.
- PORTING.md matrix cells touched by this leg cannot be checked.
- The pre-existing bindings (`bad_kind`, `bad_size`, `bad_root`, `bad_tree`, `entries`, `layout`) that the new test references at line ~1385 depend on hunks above the truncation point; without them I cannot confirm the bad inputs are constructed per-spec.

This is not a truncated *diff* (the diff is shown in full); the missing context is the **law documents and base tree** that the instructions require for verification. They are not present in this environment.
