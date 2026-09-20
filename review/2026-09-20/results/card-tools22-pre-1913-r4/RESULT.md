RESULT tools22-pre-1913-r1 sha=5298f6be12ea — a pre-read of mas-bandwidth/nova-tools#1913 at head 137baa099325: nova-play: the help examples run as pasted (#1455)
PREREAD 1913 claims=1 proven=1 unproven=0 defects=0 high=0

PR 1913
HEAD 137baa099325eb4cb308e36f02a1ec4a426a7a2c
BASE dev
MERGE-BASE 485050e30543e816f4adcc6328fe717bcd1f1248
BEHIND 16
FILES 2 production, 0 test
LINES +7 -0

1. The help examples shown in nova-play's usage banner are verified to run as pasted by a test that executes each line.

DEFECTS none

QUESTIONS FOR THE REVIEWER
1. The merge base is 485050e30543e816f4adcc6328fe717bcd1f1248, not the expected 5298f6be12ea - is this expected given how the card was cut?
2. The PR is 16 commits behind dev - is this expected or a sign the card is stale?

Left owed
docs/TESTS.md not read in full; only the nova-play section was examined.

git status --short
git rev-parse HEAD d576bf6bbabb39068096a97b4560de9b5e245970
