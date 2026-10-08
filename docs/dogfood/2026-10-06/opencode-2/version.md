# nova-version dogfood — opencode-2, 2026-10-06

Built on a Linux bench from the staged checkout at
`7acb90e18a764f0e728cd5ed701196a34405a824` with
`go build -o $JOB/bin/nova-version ./cmd/nova-version`, never the installed
binary, and used as
`nova-version v1.0.1-0.20261007211151-7acb90e18a76 linux/amd64 go1.26.6`.
Read cold: the banner from `nova-version -h` and `nova-version help`, every
`nova-version <verb> -h`, and the tool's page under `docs/`
(`docs/SPEC-VERSION.md`, which points at `docs/SPEC-UPDATE.md` for the manifest
verbs). Run in one scratch directory with a made-up actor, `--as boss --to reader`, and a scratch `--bin` of stubs: `example` (both shapes), `snapshot`
(both shapes), `diff`, `report`, `send`, `moved` (dry-run and a real write) and
`version`, with the refusals too.

## Findings

1. `nova-version report --file one.tsv --snapshot s.json`
   Printed:
   ```
   REPORT OK checked=1 known=1 unknown=0 changed=yes sent=- took=4ms file=one.tsv host=- as=- entries=1 kinds=tool at=2026-10-07T21:19:12Z timeout=5s budget=1m0s max=20 snapshot=s.json
   REPORT TOOL name=alpha kind=tool version=1.2.3 raw=nova-alpha\x20v1.2.3\x20linux/amd64\x20go1.26.6 path=/tmp/nvfin/bin-a/nova-alpha
   REPORT CHANGED name=alpha was=- now=nova-alpha\x20v1.2.3\x20linux/amd64\x20go1.26.6
   ```
   I expected a plain report with no `--send` to read and write nothing, as `report -h` says ("without --send, report reads and writes nothing (--draft prints the note)"); the run instead created `s.json` (`-rw-------`, 132 bytes) with `observed` state and printed `changed=yes`, so a preview silently moved the caller's recorded state.
   Grade: URGENT (help that lies)

2. `nova-version send --file one.tsv --as boss --to reader --draft`
   Printed:
   ```
   SEND REFUSED: --draft and --send are exclusive (choose one); run: nova-version send -h
   (one line printed)
   ```
   I expected `send --draft` to print the note only, because `send -h` lists `--draft  print the note only` among send's flags and the unknown-flag refusal repeats them (`the flags of send are --as, --budget, --draft, --file, --host, --kind, --max, --snapshot, --timeout, --to`); a stranger who follows the help is refused for using the flag the help gave, and the reason names an exclusive `--send`, a flag `send` does not list.
   Grade: URGENT (help that lies)

3. `nova-version diff --from ./a.tsv --to ./b.tsv`
   Printed:
   ```
   DIFF OK from=./a.tsv to=./b.tsv tools=2 changed=1
   DIFF CHANGED name=nova-alpha from=v1.2.3 to=v1.2.3
   ```
   I expected the changed line to show what changed: `a.tsv` records `platform=linux/amd64` and `b.tsv` records `platform=darwin/arm64` for `nova-alpha`, but the line prints the same stamp on both sides, so the announcement `changed=1` cannot be read and the platform that moved is invisible.
   Grade: URGENT (wrong result)

4. `nova-version snapshot --file unknown.tsv`
   Printed:
   ```
   SNAPSHOT FAILED checked=1 known=0 unknown=1 file=unknown.tsv
   SNAPSHOT UNKNOWN name=ghost reason=not_found remedy=install\x20definitely-not-a-cmd-xyz\x20or\x20supply\x20its\x20executable\x20path;\x20searched\x20PATH\x3d/usr/bin:/bin
   ```
   I expected plain prose in the line (`remedy=install definitely-not-a-cmd-xyz or supply its executable path; searched PATH=/usr/bin:/bin`), the way `report`'s own reason beside it is plain; the command printed no such characters, so a person reads `\x20` and `\x3d` instead of the sentence that says what to install and where it looked.
   Grade: NEXT (friction)

5. `nova-version report --file one.tsv --draft --as boss --to reader`
   Printed:
   ```
   From: boss
   To: reader
   Subject: versions on - at 2026-10-07T21:19:12Z
   ```
   I expected the subject to drop the host phrase when no `--host` is given (`Subject: versions at ...`), not to print `on -`; `--host` is optional in the usage line, so the no-host case is the common one and its placeholder is the first prose a reader sees.
   Grade: NEXT (friction)

6. `nova-version moved`
   Printed:
   ```
   MOVED REFUSED: --from is required; it wants the revision to compare from; refusing to guess; run: nova-version help
   MOVED REFUSED: --to is required; it wants the revision to compare to; refusing to guess; run: nova-version help
   MOVED REFUSED: --repo is required; it wants the checkout holding both revisions; refusing to guess; run: nova-version help
   ```
   I expected one refusal naming every missing flag at once, the way `snapshot` with no flags prints one line listing `--bin, --out`; four lines repeat the same remedy four times and read as four separate faults, and a reader who fixes only the three shown is refused again.
   Grade: NEXT (friction)

7. `nova-version moved --from 1b2c8aea22f1550def8d252b64e27e3b52958d3b --to 7acb90e18a764f0e728cd5ed701196a34405a824 --repo ./repo --out m.md --timeout 1ns`
   Printed:
   ```
   MOVED REFUSED: cannot run git against ./repo (budget) (supply a --repo and an environment where git answers); run: nova-version help
   (one line printed)
   ```
   I expected the spent deadline to be named with the `--timeout` that answers it: git answered here and the 1ns bound stopped it, so the remedy sends the reader to repair an environment that is fine.
   Grade: NEXT (unclear help)

8. `nova-version bogus`
   Printed:
   ```
   VERSION REFUSED: unknown verb "bogus"; the verbs are example, moved, snapshot, diff, report, send, version; run: nova-version help
   (one line printed)
   ```
   I expected the leading word to be `nova-version` (`nova-version REFUSED: ...`) so a line can be matched to its tool; the status word here is the short verb name `VERSION`, and `snapshot`, `diff`, `send` and `moved` lead with their own short names too, which a reader must translate.
   Grade: NEXT (friction)

## What held

`example` printed a one-tool manifest, wrote it with `--out`, left the same file unchanged on the second run and refused to overwrite a different one; `snapshot --file` counted a known tool and reported an unknown one with a remedy; `snapshot --bin` wrote the four-column TSV from real `nova-*` stubs, listed rows under `--max` with a `MORE` line, refused a mixed set, a binary with no version, an unreadable `--bin` and a non-positive bound, and noted a skipped symlink by name; `diff` read both files before refusing and named a missing file, a wrong header and a wrong-arity row, both files at once when each was bad; `report` carried a three-entry manifest, filtered by `--kind`, bounded with `--max` and a `MORE` line, refused a bad manifest with every problem on one line, and left an unknown command `UNKNOWN` at exit 1; `report --draft` composed the note without sending; `send` with no bus was honest (`sent=uncertain` with the bus's own refusal in the note); `moved --dry-run` read both revisions (314 verbs) and wrote nothing, the real run wrote the note, and a bad revision and a non-checkout `--repo` were each refused; `version` and `--version` are one spelling, `version --json` renders the same value, every verb answers `-h` at exit 0, and a positional after `version` is refused.

READ 8/10 — the banner answers what it does, how it works and where its state lives, every verb's `-h` is complete and the manifest rules live in `report -h`; the two `-h` lines that can never be used, the `on -` subject and the escaped prose keep it off a 9.

USE 8/10 — one manifest carried report, snapshot, diff and send from a scratch directory with no store and no network, `moved` read two real revisions, and the refusals name their remedy; a draft that writes delivery state, a `send -h` flag that can never be used and a `diff` line that cannot show the move are the stumbles.

urgent=3 next=5
