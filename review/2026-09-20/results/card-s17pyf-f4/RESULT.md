RESULT: s17pyf-f4 sha=<no-commit>

**BLOCKED-BUNDLE**

The base repo bundle at `/tmp/schema14-ftf.bundle` was not placed by the bench harness. The working directory is an empty git repo with no commits. Without this bundle I cannot clone the reference C++ source, read `test/tables/fixedform_main.cpp`, read `test/tables/fixedform_properties.cpp`, use `build/fixedform-corpus`, or read `docs/FIXED-FORM-ALGORITHM.md`. The task cannot proceed. This is the bench's fault — the harness did not create the expected bundle path.

Python3 is available (`Python 3.14.7` at `/opt/homebrew/bin/python3`) — toolchain is not the blocker.