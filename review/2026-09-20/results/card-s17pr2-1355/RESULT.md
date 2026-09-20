RESULT: s17pr2-1355 sha=CANNOT READ

**CANNOT READ** — `test/rust-fixedform/src/main.rs`, final hunk of `main()`. The diff truncates at "... DIFF TRUNCATED AT 6000 of 6163 BYTES" immediately after a `+ ` insertion inside `main()`, precisely where `the_hostile_sweep(&dir);` must appear. Whether this new test is called (Step 5 gate execution, Step 4 assertion) is entirely in the truncated region (~163 bytes remaining). The rest of the diff (full ~108-line `the_hostile_sweep` function body) is visible, but the single invocation line determining whether this code runs at all is not.
