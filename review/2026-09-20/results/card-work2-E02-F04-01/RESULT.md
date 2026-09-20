RESULT work2-E02-F04-01 sha=a3abdd4ad6dd — nova-work E02-F04 acceptance criterion, criterion E02-F04-01 (docs/roadmaps/nova-work.sexp): Check base tip and owner token before every write
DONE
CRITERION E02-F04-01 STATE verified
BRANCH rowan/work2-E02-F04-01
REPO mas-bandwidth/nova-tools
PATHS lisp/nova-work/tests/replays-8646.lisp

SPEC-WORK line found and quoted:
  docs/SPEC-WORK.md:215-221 (rule 2, "Holding"):
  "Every `--every`, the owner fetches the tip and reconfirms, in this order:
   first that the fetched tip is the session's base, then that OWNER on it still
   carries its generation and token; only then does it push ... the check is one
   predicate, `tip == base`, made before the CAS of every push the session makes."

Test added: TestE02F04CheckBaseTipAndOwner (tests/replays-8646.lisp)

Suite count lines (two runs, identical — not order-dependent):
  NOVA-WORK SLICE1 total=408 pass=400 fail=8
  NOVA-WORK SLICE1 total=408 pass=400 fail=8

My test is GREEN. The kernel already satisfies the criterion: session-reconfirm
(src/session.lisp:300) checks completion-before-until, then tip == base (refusing
with "SESSION RACED expected=<base> found=<tip>"), then OWNER generation/token on
the tip, before any success. The roadmap row E02-F04-01 is met and can be ticked;
every write is guarded by the base and owner token check.

The 8 pre-existing failures are environmental (sandbox): "Socket error in bind:
13 (Permission denied)" and "Can't create directory /tmp/nova-work-reqline-*" —
not criterion failures, and unchanged by this test.

git status --short:
 M lisp/nova-work/tests/replays-8646.lisp

head d65e4b5199d36a15cffbc6f5188a9ed8a3c17da1

Left owed: 0
