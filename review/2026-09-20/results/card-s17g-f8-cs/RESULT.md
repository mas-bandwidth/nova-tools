RESULT: s17g-f8-cs sha=000000000000

NO SITE

The workspace contains no schema14 codebase whatsoever. The git repo has zero commits,
no source files, no test infrastructure, and no implementation. There is nothing on the cs leg.

GREPS (all empty — no matching files exist):

  $ grep -rn "malformed, ragged tail" test/ internal/codegen/
  (no output)

  $ ls internal/codegen/cstable/ test/cs-* test/cs* 2>/dev/null | head -20
  (no output)

  $ grep -n "malformed, ragged tail" test/tables/fixedform_main.cpp test/tables/fixedform_properties.cpp
  (no output — file not found)

No C++ reference was available to port from. No cs-side implementation exists. No file
`test/cs-tables/src/FixedFormChecks.cs` exists in this workspace. No `docs/FIXED-FORM-ALGORITHM.md` exists.

The audit finding of G (GAP) for F8 on the cs leg is confirmed: there is literally no
code on this leg against which the rule could be asserted. Without any source code present,
Step 3 is moot — the site does not exist because nothing exists. No test can be written,
wired, run, or controlled.
