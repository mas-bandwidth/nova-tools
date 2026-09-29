RESULT: card-alpha sha=00112233445566778899aabbccddeeff00112233
SCHEMA: v2
ID: card-alpha
TITLE: Reject an empty queue name
KIND: fix-red
PATHS: internal/queue/name.go, internal/queue/name_test.go
DEPENDS-ON: -
TIER: flash
TEST: internal/queue TestNameRefusesEmpty
DONE-WHEN: The test named on the TEST line fails at the base and passes at the head.
DOORS: none
PROBES: Call the constructor with an empty name and with a name of spaces; both return the refusal.

# Reject an empty queue name

The queue constructor accepts an empty name today. It returns a queue that no
lookup can find. It refuses the empty name and the name of only spaces, and it
names the refused value in the error.

```
KIND: read
ID: card-other
```

> KIND: quoted text declares nothing either
