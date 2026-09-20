RESULT: s17pyf-f11 sha=7c29513b0e8f

**BLOCKED-TOOLCHAIN**

The base repo bundle `/tmp/schema14-ftf.bundle` does not exist and `git log --oneline -1` returns
`fatal: your current branch 'master' does not have any commits yet`.  Without the bundle there is
no source code — no `docs/FIXED-FORM-ALGORITHM.md`, no C++ reference in `test/tables/`, no
`make/` directory, and no CI workflow to inspect.  The bench failed to provide the prerequisite
repository content; the python interpreter is present (`Python 3.14.7`), so this is a toolchain
provisioning issue, not a row problem.