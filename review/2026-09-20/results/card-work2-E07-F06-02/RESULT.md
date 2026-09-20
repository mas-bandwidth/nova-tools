RESULT work2-E07-F06-02 sha=a3abdd4ad6dd — nova-work E07-F06 acceptance criterion, criterion E07-F06-02 (docs/roadmaps/nova-work.sexp): When a public view reaches private work through a parent, print only the private count
DONE
CRITERION E07-F06-02 STATE unmet

BRANCH rowan/work2-E07-F06-02
REPO mas-bandwidth/nova-tools
PATHS lisp/nova-work/tests/replays-e08-render-file.lisp

SPEC-WORK line (docs/SPEC-WORK.md:948-949):
  "  a private node or its descendants into an output file, and a public view that reaches one"
  "  through a parent prints `private=<n>` and nothing of it (5653982211). A task has no stored worker; who is working"

TEST: TestE07F06WhenAPublicViewReaches (docs/SPEC-WORK.md:948-949)

Suite runs (both identical):
  NOVA-WORK SLICE1 total=408 pass=399 fail=9
  NOVA-WORK SLICE1 total=408 pass=399 fail=9

Findings:
- The test is RED. It asserts that render-node on a descendant of a private node
  (reached through a private parent) prints only the private count ("private=1"),
  and that none of the descendant's own data leaks.
- Observed failure:
  "a public view that reaches private work through a parent prints only the
   private count: expected \"private=1\" got \"a secret title http://x/secret\""
- The direct case is already satisfied: render-node on a node marked `:private t`
  itself returns "private=1" (node-verbs.lisp:165-167). The kernel even carries
  effective-private-p (node-verbs.lisp:411) for the inherited/ancestor case, but
  render-node does not call it, so a child under a private parent leaks its title
  and links. The "through a parent" half of E07-F06-02 is genuinely unmet.
- Interpretation for the roadmap row: E07-F06-02 should stay unverified; the
  test I left pins the gap so the next reader does not take my word for it.
- Note: 8 of the 9 failures are pre-existing ("Can't create directory
  /tmp/nova-work-reqline-*"), unrelated to this card and caused by the
  sandbox's /tmp being unwritable; my test adds the 9th.

git status --short (before commit, one file):
  M lisp/nova-work/tests/replays-e08-render-file.lisp

head (commit) 93fbed075e1437b60067bdf43ae103f5dc235f04
base           a3abdd4ad6dd0a0427f131ad6b71a9e10f07308e

Left owed: none
