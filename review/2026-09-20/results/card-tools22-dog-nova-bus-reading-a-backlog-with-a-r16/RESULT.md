RESULT tools22-dog-nova-bus-reading-a-backlog-with-a-r16 sha=5298f6be12ea — read `nova-bus Reading a backlog with a typed decision` against docs/CLI.md:556-559 and say CLEAN or DRIFT
SKIP section contains only descriptive prose (heading + description paragraph), no fenced command blocks to execute; zero provider calls possible without JEV_API_KEY and a target inbox/repository anyway
TOOL nova-bus
VERB Reading a backlog with a typed decision
DOC docs/CLI.md:556-559
REPLICA 16 of 24
BUILD v0.16.0-dev.c839379e.0.20260920175628-5298f6be12ea linux/amd64 go1.26.6 (built from source; installed ~/.local/bin/nova-version had Permission denied/exit 126 despite 0755 permissions)
| n | command | exit | status |
|---|---------|------|--------|
(No command blocks present in lines 556-559.)
RAN 0
SKIPPED 0
Left owed claims about --decide behaviour (output grammar INBOX NOTE / INBOX DECIDED, structured-signal rule for STOP:/HOLD:, .public marker refusal, key-env/base-url handling) because no example output was provided in the specified section to compare against the tool. To judge those claims would require running `nova-bus inbox --decide ...` which needs JEV_API_KEY, a git clone with an inbox, and network access — none of which the document instructs us to set up within these lines.
