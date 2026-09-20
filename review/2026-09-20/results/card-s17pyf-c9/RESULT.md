RESULT: s17pyf-c9 sha=<no sha - no commits>

**BLOCKED-TOOLCHAIN**

The base-repo bundle at `/tmp/schema14-ftf.bundle` does not exist (`/tmp` returns `Operation not permitted` from the sandbox). The `.git/` directory is freshly initialized with zero objects, zero refs, and no commits — the harness never fetched the bundle into the working tree.

Without the C++ reference corpus (`test/tables/fixedform_main.cpp`, `build/fixedform-corpus`) and the supporting docs (`docs/FIXED-FORM-ALGORITHM.md`), no step beyond STEP 1 is possible. The bench must deliver the bundle before this card can run.