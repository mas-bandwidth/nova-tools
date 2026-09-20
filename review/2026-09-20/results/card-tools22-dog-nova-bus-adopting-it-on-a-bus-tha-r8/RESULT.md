RESULT tools22-dog-nova-bus-adopting-it-on-a-bus-tha-r8 sha=5298f6be12ea — read `nova-bus Adopting it on a bus that already exists: the switch day` against docs/CLI.md:542-555 and say CLEAN or DRIFT
CLEAN
TOOL nova-bus
VERB Adopting it on a bus that already exists: the switch day
DOC docs/CLI.md:542-555
REPLICA 8 of 24
BUILD nova-bus v0.16.0-dev.c839379e.0.20260920175628-5298f6be12ea darwin/arm64 go1.27.1
n | command | exit e | status
1 | nova-bus check --bus <dir> --full --legacy-before "$(date -u +%Y-%m-%dT%H:%M:%SZ)" | exit 0 | CLEAN
2 | nova-bus inbox --bus <dir> --as <you> --receipt-max-words 40 --full --legacy-now --advance --remote origin --branch main | exit 0 | CLEAN
RAN 2
SKIPPED 0
Output for cmd 1:
BUS SCOPE mode=full cursor=- changed=0
BUS OK notes=4 lanes=2 receipts=1 participants=3 warn=0
Output for cmd 2:
INBOX SCOPE mode=full cursor=- changed=0 carrying=0
INBOX LEGACY before=2026-09-20T21:25:32Z notes=2 unreadable=0
INBOX OPEN carrying=0 heard=0 large=false remedy=inbox --advance
INBOX OK as=Ada carrying=0 open=0 notes=0 receipts=0 heard=0 unaddressed=0 unreadable=0
INBOX CURSOR commit=50ded3449ee935e50542855815064a266d78d9ff carrying=0 pushed=true attempts=1
Left owed: none
git status --short at job repo end:
