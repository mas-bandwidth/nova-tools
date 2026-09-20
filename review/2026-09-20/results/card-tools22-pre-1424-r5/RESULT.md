RESULT tools22-pre-1424-r1 sha=5298f6be12ea — a pre-read of mas-bandwidth/nova-tools#1424 at head 5ed14cae3e7f: bus: --host, the Host header, and host= on the inbox line
PREREAD 1424 claims=7 proven=7 unproven=0 defects=0 high=0

PR 1424
HEAD 5ed14cae3e7f9887e98b2eb749c4c1518ee9d03a
BASE dev
MERGE-BASE 6ad8501220ddaefcb6379dfb88f0fad01a73d3e5
BEHIND 18
FILES 14 production, 4 test
LINES +686 -36

CLAIMS
1. --host flag on send and reply commands writes a Host: line under From: in notes
   PROVEN-BY cmd/nova-bus/host_test.go:14 TestSendHostWritesTheHostLineAndInboxShowsIt - asserts note contains "From: Ada\nHost: air\nTo: Bo\n"

2. inbox prints host=<name> between from= and addr= when a note carries a Host line
   PROVEN-BY cmd/nova-bus/host_test.go:28 TestSendHostWritesTheHostLineAndInboxShowsIt - asserts inbox output contains "from=Ada host=air addr=to"

3. <bus>/.nova-bus/defaults can contain a host=<name> line read when the flag is absent
   PROVEN-BY cmd/nova-bus/host_test.go:39 TestHostComesFromTheDefaultsFileWhenTheFlagIsAbsent - asserts note from defaults file contains "Host: studio\n"

4. A host value is one word: lower-case letters, digits, -, ., _, at most 40 characters
   PROVEN-BY internal/bus/host_test.go:93 TestAHostThatIsNotOneWordIsRefused - asserts "the air", "Air", tabs, and strings >40 chars are refused

5. A draft with its own Host line keeps it; a --host naming a different machine is a refusal
   PROVEN-BY internal/bus/host_test.go:60 TestHostFlagAgainstADifferentHostLineIsARefusal - asserts error contains "--host", "air", "studio", "does not post one machine's note as another"

6. Notes without Host line are unchanged byte-for-byte (compatibility)
   PROVEN-BY internal/bus/host_test.go:136 TestANoteWithNoHostIsUnchanged - asserts rendered output has no Host: and ID is unchanged with/without host

7. The id's preimage is untouched - host is not in the id
   PROVEN-BY internal/bus/host_test.go:144 TestANoteWithNoHostIsUnchanged - asserts hosted.Note.Header.ID equals plain.Note.Header.ID

DEFECTS none

QUESTIONS FOR THE REVIEWER
1. The defaults file format uses key=value lines like receipt-max-words. Was any consideration given to validating that the host key appears only once (no duplicates) before returning an error?
2. The OPEN cache file still says "OPEN v2" even though it now has a 9th field. The code takes either 8 or 9 fields. Why not upgrade to OPEN v3 explicitly to make the change explicit?

Left owed
- cmd/nova-bus/audit_test.go and cursor_test.go: only minor changes (line count updates), did not read in full
- internal/bus/cursor_test.go: only test count update for openFieldsV2
- docs/CLI.md and docs/SPEC.md: read key excerpts on Host line changes but not full text

git status --short
git rev-parse HEAD
d576bf6bbabb39068096a97b4560de9b5e245970===FILE=== card-tools22-pre-1424-r1/usage.tsv
job	attempt	started	ended	rc	provider	model	tokens_in	tokens_out	cache_write	cache_read	reasoning	usd
card-tools22-pre-1424-r1	1	2026-09-20T19:14:23Z	2026-09-20T19:20:10Z	0	inception	mercury-2.5	756604	695	0	217229	4855	0.0320
