# nova-work USE rating, nova-tools 1.1.0

Rater: Grok
Build: 3a1cbd4f7245
Score: 6.5/10

## Reasons

`nova-work help` exits 0 and answers the three questions in one screen: one tree file, import writes it, verify prints one MISSING, EXTRA or DRIFT line per difference. `nova-work version` exits 0. `nova-work version --json` exits 0 and prints one result object. That is the first successful run, and it is not the job.

The job was not finished. Help offers no recorded conversation and no store-free fixture. The first-run line requires a logged-in client. import and verify are the verbs not tried to a result: both need that client, and this sitting does not call one. `nova-work import --org acme --repo acme/demo --dry-run --page-size 15 --gh ./notgh` still executes the named program, prints `GH OK` on standard output, then `IMPORT FAILED` on the error stream, and exits 2. No tree file is written. `--dry-run` on the flag line says it prints what the verb would write and writes nothing; the paragraph above that line says it reads every issue. The flag line is the one a reader trusts, and it is the wrong one.

Flag refusals are the strong part. A missing required flag names the flag, what it wants, and the next command, and says it refuses to guess. An unknown flag lists the flags of that verb. An unknown verb lists import, verify and version. Several bad values that parse are all reported in one run. A value the flag parser itself rejects stops the run, so the other problems stay hidden.

The failure past the flags is a different language. `nova-work verify --tree ./trial/bad.lisp --gh ./notgh` exits 2 with one `VERIFY FAILED` line whose reason encodes blanks and equals signs as `\x20` and `\x3d`. The remedy is a quoted `remedy=` field, not the `run:` line the flag refusals use. `--json` on import and on verify is an unknown flag, so the banner's promise of one JSON object is true only of version.

A 10 would finish both example commands on a recording the help names, say in the dry-run flag that the client still runs, print failure reasons as plain words in the same refusal line, and accept `--json` on import and verify.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | `nova-work import --org acme --repo acme/demo --dry-run --page-size 15 --gh ./notgh` | The dry run executes the named program and exits 2. Help offers no recorded conversation, so import and verify cannot be finished without a live login, and the flag line does not say the client still runs. | Name a recording in the help, and say on the dry-run flag that the client is still called and no file is written. | M |
| 2 | `nova-work verify --tree ./trial/bad.lisp --gh ./notgh` | Exit 2 prints one VERIFY FAILED line, reason=workfile:\x20tree\x20file\x3d./trial/bad.lisp:\x20trailing\x20bytes\x20after\x20one\x20form,\x20at\x20byte\x3d4, and a quoted remedy. The words of the reason are not readable, and the line is not the refusal grammar. | Print the reason as plain words on one REFUSED line that ends with the next command. | M |
| 3 | `nova-work import --org acme --dry-run --json` | Exit 2: unknown flag --json, and the flags listed do not include it. The same refusal meets verify --json. The banner says every other verb prints one JSON object, which leaves import and verify, the verbs that matter, with no machine-readable result. | Accept --json on both verbs and print the same lines as one object. | M |
| 4 | `nova-work import --dry-run --page-size 0 --max-calls 0 --timeout 0s --repo nope --out ./trial/tree.lisp` | Exit 2 names only the bad --repo value. The missing organization, the bad page size, the bad budget and the bad deadline are not reported in that run. A later command with those checks and no bad --repo prints each of them. | When a repeated flag rejects a value, keep checking the other flags and print every problem. | S |
| 5 | `nova-work verify -h` | The --repo line says the default is every repository of --org. This verb has no --org flag. Its required flag is --tree. | Say the default is the organization stored in the tree. | S |

## Good, keep

`nova-work import --org acme --page-size 0 --max-calls 0 --timeout 0s --dry-run` exits 2 and prints three IMPORT REFUSED lines, each ending `run: nova-work help`. `nova-work import --org acme --dry-run --shaped` lists every flag of import and ends `run: nova-work import -h`. `nova-work bake` lists the three verbs and ends `run: nova-work help`.

A tree that does not parse is refused before the named program runs. `nova-work import --org acme --out ./no-such-dir/tree.lisp` names the missing directory and writes nothing.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| import cannot be tried without a gh login | STILL THERE | nova-work help says the first run needs a logged-in client, and nova-work import --org acme --repo acme/demo --dry-run --page-size 15 --gh ./notgh exits 2 |
| value shapes undocumented | STILL THERE | nova-work help names a (work-tree ...) record and the words MISSING, EXTRA and DRIFT, and names no field of that record |
| tree errors one per run | STILL THERE | nova-work verify --tree ./trial/bad.lisp --gh ./notgh prints one VERIFY FAILED line and exits 2 |
| fake import and offline drift verification work | CHANGED | help offers no recording; the dry-run command above exits 2 and writes no tree file |
