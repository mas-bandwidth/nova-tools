RESULT: card-beta sha=00112233445566778899aabbccddeeff00112233
SCHEMA: v2
ID: card-beta
ENTRY: work/queue/refuse-empty-follow-up
TITLE: Document the empty queue name refusal
KIND: fix-red
PATHS: docs/queue.md
DEPENDS-ON: card-alpha
TIER: pro
TEST: internal/docs TestQueueDocNamesTheRefusal
DONE-WHEN: The queue guide names the refusal and the test named on the TEST line passes.
DOORS: none

PROBES: Read the guide section on names; it states the refusal and shows the error text.

# Document the refusal

Once the constructor refuses an empty name, the queue guide says so. The guide
section on names gains one paragraph and one example.
