RESULT tools22-dog-nova-bus-adopting-it-on-a-bus-tha-r2 sha=5298f6be12ea — read `nova-bus Adopting it on a bus that already exists: the switch day` against docs/CLI.md:542-555 and say CLEAN or DRIFT
SKIP section needs template variables <dir> and <you> with no way to supply real values
TOOL nova-bus
VERB Adopting it on a bus that already exists: the switch day
DOC docs/CLI.md:542-555
REPLICA 2 of 24
BUILD nova-version v0.16.0-dev.c839379e.0.20260920175628-5298f6be12ea darwin/arm64 go1.27.1

n | command | exit | status
1 | nova-bus check --bus <dir> --full --legacy-before "$(date -u +%Y-%m-%dT%H:%M:%SZ)" | n/a | SKIP (no bus dir; <dir> is a template variable never defined in the section)
2 | nova-bus inbox --bus <dir> --as <you> --receipt-max-words 40 \ --full --legacy-now \ --advance --remote origin --branch main | n/a | SKIP (no bus dir or user id; <dir> and <you> are template variables never defined in the section)

RAN 0
SKIPPED 2

Left owed: nothing — the entire section consists of two commands that require a bus directory (<dir>) and a user identity (<you>). The section header begins at line 542 and neither those lines nor any preceding text in the section instructs how to create a bus or obtain a user value. Neither command could be judged against the document's descriptions because no output was produced.

git status --short: (empty — no changes to repo)
