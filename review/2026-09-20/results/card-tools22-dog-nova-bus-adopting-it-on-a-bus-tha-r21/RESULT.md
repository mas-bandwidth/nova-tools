RESULT tools22-dog-nova-bus-adopting-it-on-a-bus-tha-r21 sha=5298f6be12ea — read `nova-bus Adopting it on a bus that already exists: the switch day` against docs/CLI.md:542-555 and say CLEAN or DRIFT
SKIP both fenced commands need a bus <dir> and a user <you> that the section never tells the reader how to make; fabricating a fixture is forbidden, so they cannot be tried here
TOOL nova-bus
VERB Adopting it on a bus that already exists: the switch day
DOC docs/CLI.md:542-555
REPLICA 21 of 24
BUILD nova-version version -> "nova-version v0.16.0-dev.c839379e.0.20260920175628-5298f6be12ea darwin/arm64 go1.27.1" (sha 5298f6be12ea == base 5298f6be; bare `nova-version` was not on PATH, built from the pinned checkout into job scratch; bare `nova-version` with no verb prints "VERSION REFUSED: a verb is required" exit 2, its normal behaviour)
| n | command | exit | verdict |
| 1 | nova-bus check --bus <dir> --full --legacy-before "$(date -u +%Y-%m-%dT%H:%M:%SZ)" | n/a | SKIP |
| 2 | nova-bus inbox --bus <dir> --as <you> --receipt-max-words 40 --full --legacy-now --advance --remote origin --branch main | n/a | SKIP |
RAN 0
SKIPPED 2
Left owed: the commands' printed output (the one INBOX LEGACY line, the INBOX SWITCH line, and the refusal that hands back the exact line to run) could not be judged because the section supplies no bus directory or user identity to run against and I was not allowed to invent one. The documented flags were verified present via `nova-bus help` (check --full --legacy-before; inbox --as --receipt-max-words --full --legacy-now --advance --remote --branch) -- no flag mismatch found.
git status --short (below) prints nothing from the repository.