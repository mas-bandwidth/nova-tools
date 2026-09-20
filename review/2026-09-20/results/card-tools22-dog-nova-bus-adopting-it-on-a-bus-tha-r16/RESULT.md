RESULT tools22-dog-nova-bus-adopting-it-on-a-bus-tha-r16 sha=5298f6be12ea
SKIP section requires pre-existing bus directory; neither command can be exercised without a bus that the documentation does not tell you to create
TOOL nova-bus
VERB Adopting it on a bus that already exists: the switch day
DOC docs/CLI.md:542-555
REPLICA 16 of 24
BUILD nova-version v0.16.0-dev.c839379e.0.20260920175628-5298f6be12ea darwin/arm64 go1.27.1

| n | command | exit | verdict |
|---|---------|------|---------|
| 1 | nova-bus check --bus \<dir\> --full --legacy-before "$(date -u +%Y-%m-%dT%H:%M:%SZ)" | N/A | SKIP no bus directory provided |
| 2 | nova-bus inbox --bus \<dir\> --as \<you\> --receipt-max-words 40 --full --legacy-now --advance --remote origin --branch main | N/A | SKIP no bus directory or remote provided |

RAN 0
SKIPPED 2

Left owed: Both commands need a real git-backed bus directory (with notes/commits and a `participants.json`) and, for command 2, a reachable git remote named `origin`. The section title itself ("Adopting it on a bus that already exists") assumes all of this is in place but provides zero setup instructions. Without the documented bus, I cannot judge exit codes, output shape, field names, flag acceptance, or any behavioural claim the section makes. This is not "did not try" — it cannot be tried here because the card gives no path from nothing-to-running-bus within these lines.
