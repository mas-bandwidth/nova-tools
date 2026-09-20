RESULT tools22-dog-nova-bus-adopting-it-on-a-bus-tha-r15 sha=5298f6be12ea
SKIP both commands require a pre-existing bus directory that the section never tells you how to make; --advance + --remote origin requires network access
TOOL nova-bus
VERB Adopting it on a bus that already exists: the switch day
DOC docs/CLI.md:542-555
REPLICA 15 of 24
BUILD nova-version v0.16.0-dev.c839379e.0.20260920175628-5298f6be12ea darwin/arm64 go1.27.1

n | command | exit | status
1 | nova-bus check --bus <dir> --full --legacy-before "$(date -u +%Y-%m-%dT%H:%M:%SZ)" | exit 2 | SKIP needs <dir>: no bus directory created in this section
2 | nova-bus inbox --bus <dir> --as <you> --receipt-max-words 40 \ --full --legacy-now --advance --remote origin --branch main | — | SKIP needs <dir>, <you>, and --remote/--branch: no bus identity, no remote created in this section

RAN 0
SKIPPED 2

Left owed: none — the section header says "that already exists" but provides zero steps to create or point at one. Both command blocks have valid flag syntax (all flags confirmed present in `nova-bus help` output), but neither can be exercised without a bus directory and the second also needs an identity and git remote connectivity. The rest-of-paragraph prose (`check --full --rebuild-index`, `inbox --as <you> --advance`) inherits the same missing-fixture problem.

git status --short:
?? .lease
?? .nova-sandbox-tmp/
?? harness-output.log
?? opencode.json
?? repo/
