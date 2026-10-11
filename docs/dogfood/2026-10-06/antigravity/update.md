# nova-update dogfood — antigravity (zhi), 2026-10-06

Read as a stranger: only `nova-update -h`, `nova-update help`, every verb's
`-h`, `nova-update help release`, and the page under `docs/`
(`docs/CLI.md`'s `## nova-update` section and `docs/SPEC-UPDATE.md`). Built from
the checkout at 1c05149f60a74e73707c4eb6325b84194648ffcf and used as
`nova-update v1.0.1-0.20261007140251-1c05149f60a7 linux/amd64 go1.26.6` on the
Linux bench (no go command runs on the working machine): the `example:` lines
run as printed; `check`, `status`, `report` (`--host`, `--json`, `--snapshot`,
`--draft`, `--send`, `--store`), a real `apply` and its `--dry-run`,
`watch --adopt` (with and without `--as`/`--to`), `adoption` (with `--json`),
`version`, the refusals too. 15–40 minutes of use; no code changed, a finding is
recorded here and never fixed here.

## Findings

1. Human-facing values carry `\x20` escapes, and the `--json` rendering is
   plain.

       $ nova-update status --file /tmp/zhi-update-dogfood/esc.tsv
       STATUS FAILED checked=1 current=0 stale=1 newer=0 ahead=0 differ=0 unknown=0 pins=0 took=31ms file=/tmp/zhi-update-dogfood/esc.tsv entries=1 kinds=tool at=2026-10-07T14:10:27Z timeout=5s budget=1m0s max=20
       STATUS STALE name=faketool kind=tool installed=1.3.0 latest=1.26.6 path=/tmp/zhi-update-dogfood/bin/faketool source=local:go\x20version owner=zhi
       exit=1

   `report` splits the same way: its line for the same file prints
   `raw=faketool\x201.3.0`, while the `--json` rendering carries
   `"raw":"faketool 1.3.0"` plain. I expected the text a person reads (or a
   quoted form) in `source=` and `raw=`, the way `path=` and `installed=` stay
   plain. SPEC-UPDATE.md's field
   law ("a value is one pkg/oneline token") is what the escaping serves, and
   its grammar writes `raw=<first line, escaped>`, so the spec and the code agree
   here: this is friction for a line reader, not a lie. Grade: NEXT.

2. A result's continuation rows open with the verb's own token, not `MORE`/`NOTE`.

       $ nova-update status --file /tmp/zhi-update-dogfood/manifest.tsv
       STATUS FAILED checked=1 current=0 stale=1 newer=0 ahead=0 differ=0 unknown=0 pins=0 took=9ms file=/tmp/zhi-update-dogfood/manifest.tsv entries=1 kinds=tool at=2026-10-07T14:09:19Z timeout=5s budget=1m0s max=20
       STATUS STALE name=faketool kind=tool installed=1.2.3 latest=1.3.0 path=/tmp/zhi-update-dogfood/bin/faketool source=local:/tmp/zhi-update-dogfood/bin/latestfake owner=zhi
       exit=1

   `apply --dry-run` prints `APPLY OK`, then `APPLY STALE`, `APPLY PLAN` and
   `APPLY NOTE`; each row after the count line reads as a fresh `STATUS`/`APPLY`
   result rather than as part of the line above. I expected a continuation row to
   open `MORE` or `NOTE`, as STANDARD.md section 2 says ("a continuation line
   opens with `MORE` or `NOTE`"), so a row cannot be taken for its own verdict.
   SPEC-UPDATE.md's output grammar names `STATUS <EQUAL|STALE|...>` and
   `APPLY PLAN` as their own first tokens, so the spec and the code agree and the
   friction is against the general standard, not a wrong result. Grade: NEXT.

3. `report --draft` with no `--host` writes the placeholder `-` into the subject.

       $ nova-update report --file /tmp/zhi-update-dogfood/manifest.tsv --draft --as zhi --to ada
       From: zhi
       To: ada
       Subject: versions on - at 2026-10-07T14:09:19Z
       exit=0

   I expected the subject to drop the host when none was given, or to name the
   absence in a word. SPEC-UPDATE.md rule 24 says the `host` is "as `--host`
   says or `-`", so the code follows the spec; the placeholder in prose is
   friction, nothing was sent, and the same run with `--host benchone` prints
   `Subject: versions on benchone at ...`. Grade: NEXT.

4. A tool-level refusal opens `UPDATE REFUSED`, not the tool name.

       $ nova-update
       UPDATE REFUSED: no verb given; the verbs are example, check, status, apply, report, watch, adoption, release, version; run: nova-update help
       exit=2

   I expected STANDARD.md section 2 and ONBOARDING point 1's grammar
   (`nova-update REFUSED: ...` for an invocation with no verb) so a caller
   scanning for the tool name finds the line. SPEC-UPDATE.md's own grammar block
   writes `UPDATE REFUSED` byte for byte and the code prints it, so the two
   documents disagree: the code is faithful to its spec, and the spec — not the
   code — is the one that lies against the general standard. Every refusal still
   names its remedy in one line and exits 2, so it is friction. Grade: NEXT.

5. `report --send` cuts nova-bus's refusal mid-word.

       $ nova-update report --file /tmp/zhi-update-dogfood/manifest.tsv --send --as zhi --to ada --timeout 2s
       REPORT FAILED checked=1 known=1 unknown=0 changed=- sent=uncertain took=16ms file=/tmp/zhi-update-dogfood/manifest.tsv host=- as=zhi entries=1 kinds=tool at=2026-10-07T14:09:19Z timeout=2s budget=1m0s max=20 snapshot=-
       REPORT TOOL name=faketool kind=tool version=1.3.0 raw=faketool\x201.3.0 path=/tmp/zhi-update-dogfood/bin/faketool
       REPORT NOTE send not confirmed: exit 2; the bus said: SEND REFUSED: --redis is required: NOVA_BUS_REDIS is unset, and with no NOVA_SPRINT_REDIS the fleet's bus row (nova-config fleet set --bus <host:port>, then apply) cannot be read either; refusing to g... (retry --send with the same --snapshot)
       exit=1

   I expected the bus's own sentence whole, or a pointer to where it is kept;
   the cap cuts it at `refusing to g...`, so the half that says what to do next
   is the tool's parenthetical retry. `watch --as`/`--to` cuts the same sentence
   the same way. `sent=uncertain` is truthful and this tool's retry is on the
   line, so it is friction, not a wrong result. Grade: NEXT.

6. `report --store` prints the Redis client's own log lines above its refusal.

       $ nova-update report --store 127.0.0.1:1 --timeout 1s
       redis: 2026/10/07 14:09:20 pool.go:762: redis: connection pool: failed to dial after 5 attempts: dial tcp 127.0.0.1:1: connect: connection refused
       redis: 2026/10/07 14:09:20 pool.go:762: redis: connection pool: failed to dial after 5 attempts: dial tcp 127.0.0.1:1: connect: connection refused
       REPORT REFUSED: fleet beats at 127.0.0.1:1: context deadline exceeded; run: nova-update report -h
       exit=2

   I expected one `REPORT REFUSED` line naming the store and the cause; the two
   client lines are not the tool's grammar, they print the same failure twice,
   and the cause they name (`connection refused`) is not the cause the refusal
   states (`context deadline exceeded`). The run still refuses with the address
   and a remedy and exits 2, so it is friction. Grade: NEXT.

## What worked

`example --out` wrote the one-tool manifest and its second run said
`unchanged=true`; `report` read only the installed side and needed no network;
`report --json` is one value with the lines, and every verb that takes `--json`
(`example`, `check`, `status`, `apply`, `report`, `adoption`) answered with a
parsable object. `apply --dry-run` printed the plan and started no process; the
real `apply` ran the entry's command and read
`APPLY AFTER name=faketool installed=1.3.0 was=1.2.3`, then a second apply was
idempotent (`from=1.3.0 to=1.3.0`). A real `github:cli/cli` read answered
`CHECK STALE name=gh kind=tool installed=1.3.0 latest=2.102.0 ...` in 5.4s; a
model entry with no engine was UNKNOWN `not_found`, and `apply` of that model was
refused naming its owner and the owner's own `ollama pull qwen3-coder:30b`.
`--snapshot` wrote the state file and the second run said `changed=no`;
`--draft` printed the body and sent nothing; `--send` failed honestly
(`sent=uncertain`). `watch --adopt` ran two checks, refused the missing one with
a remedy and named its owner on `ADOPT ESCALATE`; `adoption` listed each choice
and `--as` filtered. The refusals each took one line and exit 2 and named the
next command: a missing `--file`, an unreadable file, four fields, a bad header,
an unknown `--kind`, an unknown flag, `--max -1`, no apply name, an absent name,
a wrong-case name, missing `--as`/`--to`, a missing `--adopt`, a wrong adoption
header and an unknown verb. `help release`, `release -h` and each
`release <verb> -h` printed their usage, flags and exit table at exit 0.

## Not done

- `release cut`, `release build`, `release install`, `release adopt`,
  `release pull` and `release cycle` were read by their `-h` only. `build`
  compiles every `cmd/nova-*` and writes an artifact tree; `install`, `adopt` and
  `pull` write binaries, stream over ssh or delete installed files; `cut` tags on
  the forge and needs a green checks read. None is the version read this card
  asks for, and the card forbids starting a server.
- The `npm:` and `brew:` latest sources were not exercised; the `local:`,
  `github:` and `ollama:` (with no engine) paths were.
- `report --send` and `watch --as`/`--to` were attempted and refused because no
  bus store is set on the bench (`NOVA_BUS_REDIS` unset); `report --store` was
  attempted against a dead address and refused. No store or server was started.

## Gate

    go test -count=1 -timeout 600s ./internal/docs ./internal/ci

    ok  	github.com/mas-bandwidth/nova-tools/internal/docs	2.921s
    ok  	github.com/mas-bandwidth/nova-tools/internal/ci	12.267s

    go vet ./internal/docs ./internal/ci   (printed nothing)

    go test -count=1 -timeout 600s ./internal/docs -run TestDocsTreeIsConsistent

    ok  	github.com/mas-bandwidth/nova-tools/internal/docs	0.013s [no tests to run]

The card's named test `./internal/docs TestDocsTreeIsConsistent` does not exist
at this tip, so it passes with nothing to run; the docs package's own walk
(terminology, links, maps, transcripts) is the check this file answers.

READ 8/10 — the banner answers what the tool does, how it works and where its
state lives, and every verb's `-h` states its effect and exit table; the escaped
values, the continuation grammar, the `-` subject and the tool-level token keep
it off a 9.

USE 8/10 — every verb ran for real from one manifest and a scratch dir with no
store, a real `apply` and its dry run did exactly what their help says, and every
refusal was one line with a next step; the `--send` cut and the client logs on
`--store` are the stumbles.

urgent=0 next=6
