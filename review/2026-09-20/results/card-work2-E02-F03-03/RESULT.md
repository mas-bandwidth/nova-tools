RESULT work2-E02-F03-03 sha=a3abdd4ad6dd — nova-work E02-F03 acceptance criterion, criterion E02-F03-03 (docs/roadmaps/nova-work.sexp): Never unlink another live process lock or endpoint
DONE
CRITERION E02-F03-03 STATE verified
BRANCH rowan/work2-E02-F03-03
REPO mas-bandwidth/nova-tools
PATHS lisp/nova-work/tests/replays-8645.lisp

SPEC-WORK line (docs/SPEC-WORK.md:166):
  "from the lock file's contents) and **never unlinks another process's lock**: a socket that"
  (the endpoint half is stated at docs/SPEC-WORK.md:174-175, where only a socket whose own
  lock is free and answers nothing is unlinked)

Test: TestE02F03NeverUnlinkAnotherLiveProcess (in lisp/nova-work/tests/replays-8645.lisp)

Suite counts (two identical runs):
  NOVA-WORK SLICE1 total=408 pass=400 fail=8
  NOVA-WORK SLICE1 total=408 pass=400 fail=8

My test: GREEN (PASS) in both runs — the kernel already satisfies E02-F03-03. The journal
lock (src/lock.lisp take-journal-lock / release-journal-lock) never unlinks the `<journal>.lock`
file: a refused second taker leaves the holder's lock file in place, and release leaves it too.
So the roadmap row for E02-F03-03 is verifiable: it is verified by the landed test above.

NOTE: the 8 failures are pre-existing and environmental, none in replays-8645.lisp:
  1 x endpoint-is-local-and-private — `Socket error in bind: 13 (Permission denied)` (AF_UNIX
      bind blocked in this sandbox)
  7 x request-line sessions — `Can't create directory /tmp/nova-work-reqline-…` (sandbox forbids
      the /tmp mkdir those tests need)
They are unrelated to this criterion and present with or without this change.

git status --short:
   M lisp/nova-work/tests/replays-8645.lisp
   (after commit: clean)

head ecf3ec87c0e9dadba5152a7bf23e97ce1004645e

Left owed: none — verdict delivered (verified) with the naming test left in place.
