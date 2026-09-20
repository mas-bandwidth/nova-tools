RESULT work4s-E02-F06-03 sha=5298f6be12ea — nova-work E02-F06: does the contract say it? criterion E02-F06-03: Provide reproducible build/install and small startup/status/shutdown smoke tests
DONE
CRITERION E02-F06-03 SPEC PARTIAL
REPO mas-bandwidth/nova-tools
NO-BRANCH
grep -n "reproducible" docs/SPEC-WORK.md | head -20 (7 hits)
grep -n "startup" docs/SPEC-WORK.md | head -20 (12 hits)
grep -n "smoke" docs/SPEC-WORK.md | head -20 (0 hits)
grep -n "install" docs/SPEC-WORK.md | head -20 (6 hits)
grep -n "shutdown" docs/SPEC-WORK.md | head -20 (2 hits)
grep -n "status" docs/SPEC-WORK.md | head -20 (13+ hits)
docs/SPEC-WORK.md:2463: "Session startup, identity, bounds and shutdown must be explicit and inspectable."
docs/SPEC-WORK.md:303-305: "session status with its own SESSION OK … build=<identity> field"
Contract clause with no provision: "smoke tests" and "reproducible build/install"
E02-F06 :evidence (empty in sexp at line 646-657)
E02-F06 test name from line 79: protocol-version-negotiated-or-refused (exists in lisp/nova-work/tests/acceptance/slice-08-replays-late.lisp:700)
git status --short (empty)
Noticed E02-F06 :state is "missing" in sexp; smoke tests not mentioned in spec; startup/shutdown status covered but not as smoke tests
