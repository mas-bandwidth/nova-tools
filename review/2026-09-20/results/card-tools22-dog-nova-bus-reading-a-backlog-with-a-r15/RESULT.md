RESULT tools22-dog-nova-bus-reading-a-backlog-with-a-r15 sha=5298f6be12ea — read `nova-bus Reading a backlog with a typed decision` against docs/CLI.md:556-559 and say CLEAN or DRIFT
BROKEN installed nova-* tools cannot be executed in this sandbox: every invocation of /home/ubuntu/.local/bin/nova-* returns `Permission denied` (exit 126); no reading of the document against the tool is possible.

- TOOL nova-bus
- VERB Reading a backlog with a typed decision
- DOC docs/CLI.md:556-559
- REPLICA 15 of 24
- BUILD (nova-version could not run: `/usr/bin/bash: line 1: /home/ubuntu/.local/bin/nova-version: Permission denied`, exit 126; no sha printed)

The section docs/CLI.md:556-559 contains **no fenced command blocks** — it is the heading line
556, two blank lines (557, 559) and one prose paragraph (558) describing the `--decide` flag and
its `INBOX NOTE` / `INBOX DECIDED` output fields. There are therefore no documented commands to
run; the section can only be exercised by invoking `nova-bus` against the prose claims, which is
impossible because the tool cannot be executed at all.

Commands I actually ran (STEP 1 gate + attempts):

| n | the command | exit | verdict |
|---|-------------|------|---------|
| 0 | (no fenced command blocks exist in docs/CLI.md:556-559) | - | - |
| gate | `cd repo && git rev-parse HEAD` | 0 | CLEAN (printed 5298f6be12eaa0f7e6622334d2b6a1eb427649e3, matches card) |
| gate | `nova-version` | 126 | BROKEN (Permission denied, no sha) |
| attempt | `nova-bus --help` | 126 | BROKEN (Permission denied) |
| attempt | `nova-bus inbox --as you --decide` | 126 | BROKEN (Permission denied) |

RAN 0 (documented command blocks)
SKIPPED 0 (documented command blocks)

BROKEN failure output, verbatim:

```
$ nova-version
/usr/bin/bash: line 1: /home/ubuntu/.local/bin/nova-version: Permission denied
exit=126
```

```
$ nova-bus inbox --as you --decide
/usr/bin/bash: line 1: /home/ubuntu/.local/bin/nova-bus: Permission denied
exit=126
```

Why BROKEN and not SKIP: the card's BROKEN definition is "the tool could not run at all".
The installed tool exists (0755, owned by ubuntu, 7.2MB) at /home/ubuntu/.local/bin/nova-version
and /home/ubuntu/.local/bin/nova-bus, but the landlock sandbox that wraps this session
(native-argv.log: `nova-sandbox --read <job root> --write <jobdir> --write <data>
--write <tmp> --cwd <jobdir> --read /home/ubuntu/nova-bench/harness-v1.18.20 --read
/home/ubuntu/sdk --read-noexec /home/ubuntu/go/pkg/mod`) grants no read rule for
/home/ubuntu/.local, so the shell cannot even read the binaries: `file` reports
"writable, executable, regular file, no read permission", `head` cannot open it, and exec
fails with `Permission denied` (bash exit 126). The tree itself is the correct pinned base
(git rev-parse HEAD = 5298f6be12eaa0f7e6622334d2b6a1eb427649e3), so the failure is the
installed tool being unreachable, not a wrong tree.

Left owed: every prose claim in docs/CLI.md:556-559 is unverified — the flags `--decide`,
`--floor`, `--key-env`, `--base-url`, `--allow-private`, the `.public` marker refusal, the
`STOP:`/`HOLD:` structured-signal rule (`kind=edge needs_reply=1.00 wake=needs-action`), the
`INBOX NOTE` fields `kind= needs_reply= blocked= conf= wake= owner= ref=` (with `+<n>` over
four refs), and the closing `INBOX DECIDED n=<n> needs_reply=<m> below_floor=<b> wake=<w>`
line, including `needs_reply` meaning at-or-above 0.5 and `--floor` defaulting to 0.9. None
could be checked because no nova-* binary can be executed in this sandbox.

git status --short from <JOBDIR>/repo at the end (must be empty):
```
```