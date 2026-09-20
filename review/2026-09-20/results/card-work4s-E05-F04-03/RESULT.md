RESULT work4s-E05-F04-03 sha=5298f6be12ea — nova-work E05-F04: does the contract say it? criterion E05-F04-03: Resolve release version through the release task reference
DONE
CRITERION E05-F04-03 SPEC PARTIAL
REPO mas-bandwidth/nova-tools
NO-BRANCH
grep -n "release version" docs/SPEC-WORK.md → 1 hit (line 2123: contextual mention, not the criterion)
grep -n "release task" docs/SPEC-WORK.md → 0 hits
grep -n "Resolve.*version\|version.*resolve\|version.*through\|reference.*version" docs/SPEC-WORK.md → 0 hits
grep -in "resolve\|semantic\|semver\|tag\|v[0-9]" docs/SPEC-WORK.md → 16 hits (none named the criterion; mostly general resolver/version references)
docs/SPEC-WORK.md:710: `rule 2 resolving a name that is in C, \`released=\` reading a settled release task through the`
docs/SPEC-WORK.md:952: A task may carry \` :version "<text>\", the name of the release it ships, **read by the \`released=\` field of the disposition row below**, so *what release contains this fix* is answered from the release task that tracks it and never from a second store.
docs/SPEC-WORK.md:2083: where none — \`released=<version|->\` — the \` :version\` of the **settled** release task that names
docs/SPEC-WORK.md:2084: this item in its \` :deps\`, found through the reverse-dependency index, and \`-\` while that task is
docs/SPEC-WORK.md:2085: still in O, because **a merged fix is not a distributed one** (Glenn, 23:36Z: *a merged fix can
docs/SPEC-WORK.md:2086: be C while the separately tracked release task remains O; do not call distributed just because
docs/SPEC-WORK.md:2087: merged*); **where two settled release tasks name one item, the field prints the version of the
PARTIAL — clause with no contract: the spec never uses the word "reference" to describe how the release task is located. It says "reading a settled release task through the reverse-dependency index" (710) and "found through the reverse-dependency index" (2084). The roadmap phrase "through the release task reference" could mean a direct pointer from the fix task to its release task (an inverse-edge stored as a reference), whereas the spec says the version is looked up via the reverse-dependency index during query time. The mechanics differ: indexed lookup vs. stored reference.
E05-F04 :by-feature :evidence: () — empty, no tests listed
"parent-green-needs-dependencies" — exists in lisp/nova-work/tests/acceptance/slice-11-dependencies.lisp:127
"merged-is-not-distributed" — exists in lisp/nova-work/tests/acceptance/slice-05-durable-journal.lisp:6
git status --short
Noticed: The spec sections listed as source-sections for E05-F04 are "The validator" and "A cell is a reference, not another state store"; neither section heading alone makes "release task reference" a standalone contract line. The `released=` field grammar (2083-2090) is the closest thing, but it describes reverse-index lookup, not a stored reference. Also noticed that ROADMAP.md:588 lists the criterion but the sexp has empty :evidence for E05-F04, suggesting unimplemented criteria remain unmapped.
