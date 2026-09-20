RESULT work2-E02-F04-02 sha=a3abdd4ad6dd — nova-work E02-F04 acceptance criterion, criterion E02-F04-02 (docs/roadmaps/nova-work.sexp): Admit each request against until and fence on expiry or divergence
DONE
CRITERION E02-F04-02 STATE verified
BRANCH rowan/work2-E02-F04-02
REPO mas-bandwidth/nova-tools
PATHS lisp/nova-work/tests/replays-8647.lisp

SPEC-WORK line found and quoted:
  docs/SPEC-WORK.md:250-252 — "**Admission is checked per request, not per reconfirm**: every read and every write compares the session's clock to `until` at the moment it is admitted, and a request that arrives after `until` is refused `fenced` even if the reconfirm that would have advanced `until` is in flight"
  (fence-on-divergence half: docs/SPEC-WORK.md:419-421 — "**A session whose clip is refused by divergence is fenced**: it is fenced exactly as in rule 2 — every write and every read refused `fenced` at exit 1")

Test added (name): TestE02F04AdmitEachRequestAgainstUntil, in lisp/nova-work/tests/replays-8647.lisp, above-line comment names docs/SPEC-WORK.md:250-252,419-421. It asserts (a) a live session admits each request up to `until` and refuses a post-`until` request `fenced` at exit 1, self-fencing; and (b) a reconfirm whose tip diverged from base fences the session and reports SESSION RACED.

Suite run 1: NOVA-WORK SLICE1 total=408 pass=400 fail=8
Suite run 2: NOVA-WORK SLICE1 total=408 pass=400 fail=8
(The two counts are identical; the suite is not order-dependent for these runs.)

New test status: GREEN — `TEST TestE02F04AdmitEachRequestAgainstUntil PASS`.

The 8 failures are pre-existing environment refusals, none of them this criterion's test: they fail with "Can't create directory /tmp/nova-work-reqline-*" (the sandbox /tmp is not writable, `touch /tmp/...` → Permission denied) and one `endpoint-is-local-and-private` socket bind (Permission denied). They are unrelated to E02-F04 and to this card's single-file change.

Roadmap meaning: the kernel already satisfies E02-F04-02; the row in docs/roadmaps/nova-work.sexp (and its ROADMAP.md view) is unverified because no test pinned it, not because the behaviour is missing. The new green test now pins it.

git status --short (after commit, working tree clean):
  (empty)

head <sha> = dfcc9685e8d0c6e6abdb76a3d8d204596069ffb0

Left owed: none.
