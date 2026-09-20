RESULT: s17pr2-1370 sha=<unreachable>

**CANNOT READ**

The base tree in `repo/` (bundle at `/tmp/schema14-ftf.bundle`) is inaccessible due to sandbox restrictions. The `.git` directory at the workspace root contains no objects, and the bundle at `/tmp/schema14-ftf.bundle` cannot be opened (`Operation not permitted`). Consequently:

- `test/elixir-fixedform/main.exs` at base — cannot read. The insertion point (after line 938), the plan tuple format (`{:ordinal, 0, 0, 8, 8, 1}`), the `Leg` module API, and the `Tblfu1.FixedRuntime.run/4` signature are all unverifiable against existing code.
- The law documents (`docs/FIXED-FORM-ALGORITHM.md`, `docs/SPEC.md`, `docs/PORTING.md`) are all absent from the workspace. No law can be quoted to validate the behavioural claims in the diff.
- The make target for the elixir leg cannot be checked (no `Makefile`, `make/` directory, or `.exs` files exist in the workspace).

The diff itself (one hunk, 38 added lines) is not truncated and appears internally self-consistent: the plan `{:ordinal, 0, 0, 8, 8, 1}` with body `<<0, 0, 0, 0, 1, 0, 0, 0>>` (2^32 in little-endian) would, if the test framework is as described in the comments, read an 8-byte ordinal value through a 64-bit temporary, land the clamped value 0 for a single-variant ordinal, and count one clamp. But this reading depends on assumptions about the plan tuple arity and semantics that cannot be confirmed without the base file and the law documents.