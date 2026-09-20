RESULT tools22-dog-nova-bus-for-harnesses-that-do-no-r13 sha=5298f6be12ea
SKIP section cannot be exercised — no bus fixture or remote documented in this section
TOOL nova-bus, VERB For harnesses that do not wake you, DOC docs/CLI.md:528-541, REPLICA 13 of 24, BUILD nova-version v0.16.0-dev.c839379e.0.20260920175628-5298f6be12ea linux/amd64 go1.26.6
n | command | exit e | status
1 | nova-bus wait --bus ~/bus --as Ada --receipt-max-words 40 --timeout 25m --advance --remote origin --branch main | exit 2 | SKIP needs git-initialized bus directory and named git remote; section never instructs how to create either
RAN 0
SKIPPED 1
Left owed: none — the single documented command requires infrastructure (a bare-git bus at ~/bus populated with a participants roster, plus a functioning git remote named origin) that the section itself does not describe setting up, so nothing could be judged for drift or cleanliness beyond the skip.
