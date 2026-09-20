RESULT tools22-pre-2006-r2 sha=5298f6be12ea — a pre-read of mas-bandwidth/nova-tools#2006 at head e7bb83989cf0: nova-pulse fleet hygiene: the bench timer that trims read job dirs and caches, as an installabl
PREREAD 2006 claims=3 proven=3 unproven=0 defects=1 high=0

PR 2006
HEAD e7bb83989cf08bc4f2b8008331e55eaee0385386
BASE dev
MERGE-BASE 3666f40487a97447c1632e4bebf2a40585077e4f
BEHIND 5
FILES 3 production, 1 test
LINES +407 -2

1. The hygiene verb is a bench timer that deletes read job dirs and trims caches, installed via --install.
2. --install writes a hygiene script and a ten-minute systemd timer, idempotent on subsequent runs.
3. --status reads the last HYGIENE line and free space per bench, refusing studio and benches without hygiene.

DEFECT low cmd/nova-pulse/main.go:33 — The fleet hygiene sub-verb is not documented in the usage string — users may find it undiscoverable via help — add the three modes to the usage block alongside other fleet sub-verbs

QUESTIONS FOR THE REVIEWER
1. Is the --dry-run mode intended to check if files would be written, or should it also verify sudo/systemd availability before reporting WOULD write?
2. The status mode refuses all benches if any one is named "studio" before making any SSH calls; is this the correct behavior or should it skip studio and report on the others?
3. The test fakes sudo and systemctl on PATH; is there an expectation that this should also verify the fake programs are actually called with the expected arguments?

Left owed
None - all changed files read in full.

git status --short
git rev-parse HEAD
e7bb83989cf08bc4f2b8008331e55eaee0385386
