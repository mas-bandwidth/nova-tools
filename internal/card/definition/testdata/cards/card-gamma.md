RESULT: card-gamma sha=abcdef0123456789abcdef0123456789abcdef01 a read of the retry policy
SCHEMA: v2
ID: card-gamma
TITLE: Read the retry policy and report what it does
KIND: read
PATHS: none
DEPENDS-ON: -
TIER: flash
TEST: none the card reads and reports, and changes no file
DONE-WHEN: The report names every retry limit in the policy with its file and line.
DOORS: none
PROBES: Every limit in the report exists at the pinned commit at the file and line named.

# Read the retry policy

Read the retry policy and report each limit it sets, the file and line that
sets it, and what happens when the limit is reached.
