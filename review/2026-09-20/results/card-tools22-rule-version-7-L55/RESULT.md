RESULT tools22-rule-version-7-L55 sha=5298f6be12ea
ABSENT
SPEC docs/SPEC-VERSION.md:55 rule 7
PKG cmd/nova-version (feature lives in nova-update)
ASK For rule 7, apply --sha must build cmd/* at a given revision into a single stamped set, verify all binaries agree on one stamp, install them atomically under --bin, and print exactly one line: APPLY SHA sha=<sha> bin=<dir> stamp=<stamp> built=<n> took=<d>.

The APPLY SHA line does not exist anywhere in the source tree. No code implements the apply --sha verb described by SPEC-VERSION.md rules 5–9.

Searches run:
  grep -rn "APPLY SHA" --include='*.go' .           => no results
  grep -rn "--sha\|\"sha\"" --include='*.go' internal/update/  => no results (only snapverb:189 mentions it in a human-readable string)
  grep -rn "postflight\|stagingDir\|stageDir\|rollback\|built=\|took=" --include='*.go' .    => nothing matching apply --sha behaviour
  ls cmd/nova-version/                                 => main.go delegates to update.Main; no moved or apply --sha handler
  cat internal/update/cli.go:298-300                   => verb=="apply" calls apply(entries, name, ...) which processes ONE manifest entry, NOT cmd/* at a revision
  grep -rn "moved" --include='*.go' internal/update/   => no results; moved verb also absent
  grep -rn "TestApplySha\|TestMoved" --include='*_test.go' . => no results; none of the "red tests" from SPEC-VERSION.md exist

What IS implemented vs what spec says help should print:
  Spec (lines 9-14) says help prints:
    nova-version moved --from <sha> --to <sha> --repo <dir> --out <path>
    nova-update apply --sha <sha> --repo <dir> --bin <dir> [--timeout <d>]
    nova-version snapshot --bin <dir> --out <file.tsv> [--timeout <d>] [--budget <d>]
    nova-version diff --from <a.tsv> --to <b.tsv>
  Code (cli.go updateVerbs/versionVerbs constants) only has:
    nova-update check/apply/report/watch/adoption/release verbs
    nova-version snapshot/diff/report/send/help
  The "moved" and "apply --sha" lines are missing from both the help text and the dispatch code.

Left owed
git status --short
?? .lease
?? .nova-sandbox-tmp/
?? RESULT.md
?? harness-output.log
?? opencode.json
?? repo/
