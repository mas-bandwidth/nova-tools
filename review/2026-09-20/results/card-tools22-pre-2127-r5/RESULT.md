"RESULT tools22-pre-2127-r1 sha=5298f6be12ea — a pre-read of mas-bandwidth/nova-tools#2127 at head 389fa4700151: Johnny Grok: bound read-card turns and cap reasoning (#855)
KIND: transcript-test
DEADLINE: 2100
LEG: go
PATHS: the files the pull request changes, and nothing else
FILES: 0
TEST: none
MODE: read
TURNS: 35
SOURCE: mas-bandwidth/nova-tools#2127
BASE: dev@5298f6be12eaa0f7e6622334d2b6a1eb427649e3
ROUTE: jev=flash why=- eligible=rules

ABSTAIN network unreachable — git clone to https://github.com/mas-bandwidth/nova-tools.git failed with 'early EOF' and 'fetch-pack: unexpected disconnect while reading sideband packet' after multiple attempts at various depths and timeouts up to 600s. Cannot clone repo, cannot fetch PR head, cannot produce a reading.

No repo to show git status from. Job root status:
?? .lease
?? .nova-sandbox-tmp/
?? RESULT.md
?? harness-output.log
?? opencode.json
Job root is not a git repository (no HEAD).===FILE=== card-tools22-pre-2127-r1/usage.tsv
job	attempt	started	ended	rc	provider	model	tokens_in	tokens_out	cache_write	cache_read	reasoning	usd
card-tools22-pre-2127-r1	1	2026-09-20T19:19:29Z	2026-09-20T19:45:35Z	0	openrouter	deepseek/deepseek-v4-flash	52149	2489	0	96256	710	0.0064
