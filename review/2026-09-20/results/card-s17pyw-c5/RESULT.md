RESULT: s17pyw-c5 sha=NO-COMMITS

**BLOCKED-TOOLCHAIN**

The base-repo bundle `/tmp/schema14-ftf.bundle` specified in the harness does not exist at that path or anywhere else on this bench. The working directory is a bare `git init` with zero commits. Without the bundle, there is no `docs/SPEC.md`, no C++ oracle under `bench/paired/corpus`, no `make/` targets, and no existing test structure to read. No further steps can be taken.

The bench must be re-provisioned with the bundle at `/tmp/schema14-ftf.bundle` before this card can be executed.