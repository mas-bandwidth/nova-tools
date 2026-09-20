RESULT: s17pyf-f5 sha=no-commits

**BLOCKED-TOOLCHAIN**

No commits exist in the repo — it is a fresh `git init` with no objects. The base-repo bundle at `/tmp/schema14-ftf.bundle` is inaccessible because the macOS sandbox-exec profile blocks all access to `/tmp` (`ls: /tmp/: Operation not permitted`, `stat: /tmp/schema14-ftf.bundle: stat: Operation not permitted`). Without the bundle there is:

- No C++ reference code (`test/tables/fixedform_main.cpp`, `test/tables/fixedform_properties.cpp`)
- No byte oracle (`build/fixedform-corpus`)
- No `docs/FIXED-FORM-ALGORITHM.md`
- No existing make/CI structure to inspect

Python 3.14.7 is present and usable. The bench failed to make the bundle available in a sandbox-accessible location. This is the bench's fault, not the row's.