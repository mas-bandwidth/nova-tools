RESULT tools22-dog-nova-bus-reading-a-backlog-with-a-r20 sha=5298f6be12ea — read `nova-bus Reading a backlog with a typed decision` against docs/CLI.md:556-559 and say CLEAN or DRIFT
SKIP the installed nova-* binaries cannot be executed in this sandbox (nova-version exec fails "Permission denied", exit 126) and docs/CLI.md:556-559 is a single prose paragraph with no fenced command blocks, so the section could not be exercised here

TOOL nova-bus
VERB Reading a backlog with a typed decision
DOC docs/CLI.md:556-559
REPLICA 20 of 24
BUILD nova-version could not run: /usr/bin/bash: line 1: /home/ubuntu/.local/bin/nova-version: Permission denied (exit 126). The landlock wall (native-argv.log: --read <swarm-root> --write <job> --write <data> --write <tmp/card> --write <tmp/cache> --read /home/ubuntu/nova-bench/harness-v1.18.20 --read /home/ubuntu/sdk --read-noexec /home/ubuntu/go/pkg/mod) does not include /home/ubuntu/.local/bin, so no nova binary can be read or executed here; the installed build could not be confirmed as 5298f6be12eaa0f7e6622334d2b6a1eb427649e3

STEP 1 verification:
| n | command | exit | verdict |
| 1 | git rev-parse HEAD | exit 0 | CLEAN - printed 5298f6be12eaa0f7e6622334d2b6a1eb427649e3, matches the card's base |
| 2 | nova-version | exit 126 | SKIP - /usr/bin/bash: line 1: /home/ubuntu/.local/bin/nova-version: Permission denied (sandbox wall denies read+exec of /home/ubuntu/.local/bin) |

Section command blocks (docs/CLI.md:556-559):
| n | command | exit | verdict |
| - | (none - docs/CLI.md:556-559 is the heading, a blank, and one prose paragraph describing --decide; the only nova-bus command blocks on this page belong to the previous section, ### Adopting it on a bus that already exists, lines 547-552) | - | - |

RAN 0
SKIPPED 0
(Section has no fenced command blocks to run. STEP 1: git rev-parse HEAD ran clean; nova-version skipped as above.)

Left owed
- Every behaviour documented at docs/CLI.md:556-559 could not be judged: the `INBOX NOTE ... kind=<k> needs_reply=<p> blocked=<p> conf=<c> wake=<w> owner=<lane> ref=<refs>` fields, the closing `INBOX DECIDED n=<n> needs_reply=<m> below_floor=<b> wake=<w>` line, `--floor` default 0.9, `--key-env`/`--base-url` names, the `STOP:`/`HOLD:` structured-signal rule (kind=edge needs_reply=1.00 wake=needs-action), the `.public`-marker refusal and `--allow-private`, and the lazy empty-inbox behaviour. None of it can be exercised because (1) the section contains no command to run and (2) the nova-* tool cannot be executed on this machine at all (landlock wall denies /home/ubuntu/.local/bin). The feature also requires a Jev key from the environment and a network provider, which the section never tells how to provide, so no such fixture was invented.
- The STEP 1 build gate could not be completed: nova-version printed nothing because it could not be executed, so the installed build is unconfirmed.

git status --short at the job clone prints nothing (clean working tree):
```
```