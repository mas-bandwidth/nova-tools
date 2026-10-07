# nova-cairn dogfood — opencode, 2026-10-06

Stranger run of `nova-cairn` at sprint/mechanical-2026-10-02 tip 8076dfdfcd8593f1fb3f3156310a2ec61344be97
(`nova-cairn version` reads `nova-cairn v1.0.1-0.20261006184440-8076dfdfcd8593f1fb3f3156310a2ec61344be97 linux/amd64 go1.26.6`).
Nothing was read but the tool's own help (`nova-cairn help`, `<verb> -h`, `help <verb>`) and its page in
docs/CLI.md. Every verb ran with its real flags against a scratch store in a temp directory: the nested
shape (open, append, index, receipt, duplicate, conflict, re-open, --dry-run, --json, --now, --max,
--session, --file and --file -) and the flat shape (the b9395d11.md fixture: append, duplicate, conflict,
index, receipt, open as no-op). The refusals were probed too: bare command, unknown verb, unknown flag,
missing required flags (one run names all three at once), a bad policy word, an empty id, a `../` id and
an id with a slash (both refused with the full id rule), a --file that is missing, a store path under a
file. Every refusal's remedy was pasted back and ran. No code was changed; findings are recorded only.

## Findings

1. **A re-open that changes nothing prints a fresh clock stamp and no marker, so it reads as a first open.**
   - Command: `nova-cairn open --store ./cairns --session s1 --publish manual` (second time, same policy)
   - Printed (first 3 lines):
     `OPEN OK session=s1 store=./cairns source=- publish=manual stamp=2026-10-06T18:47:47.980780394Z`
   - Expected: a marker that the record already stood (duplicate=true) and/or the record's stored stamp —
     `log.jsonl` gained no line, so the fresh stamp is the call's clock and the line is indistinguishable
     from the first open's.
   - Grade: NEXT

2. **A duplicate append prints `persisted=true` where nothing was written.**
   - Command: `nova-cairn append --store ./cairns --session s1 --entry e1 --text "the words to keep"` (repeat)
   - Printed (first 3 lines):
     `APPEND OK session=s1 entry=e1 source=- persisted=true published=false publish=manual duplicate=true stamp=2026-10-06T18:47:40.2978752Z`
   - Expected: per the CLI.md page a duplicate is "nothing written", so `persisted=true` on the same line
     reads as a contradiction (the help defines neither fact's tense: is persisted about this call or the
     store?).
   - Grade: NEXT

3. **A missing `--file` refusal echoes the OS error and does not say what the flag wants.**
   - Command: `nova-cairn append --store ./cairns --session s1 --entry e4 --file ./no-such-file.txt`
   - Printed (first 3 lines):
     `APPEND REFUSED: open ./no-such-file.txt: no such file or directory; run: nova-cairn help`
   - Expected: the house refusal grammar that names the want ("--file wants a readable file of words, or -
     for stdin"), as every other flag refusal in this tool does.
   - Grade: NEXT

4. **The NOTE about --publish sits inside the usage block of `nova-cairn help`, dressed as a command line.**
   - Command: `nova-cairn help`
   - Printed (first 3 lines of the usage block):
     `usage:` / `  nova-cairn open --store <dir> --session <id> [--source <ptr>] --publish <never|manual|deferred|immediate> [--now <rfc3339-utc>] [--dry-run]` / `  nova-cairn NOTE: --publish is a recorded word, nothing more: never, manual, deferred and immediate are the four this tool accepts and it acts on none of them; ...`
   - Expected: the NOTE after the usage lines (or indented under open's line), not wedged between open's
     and append's usage lines with a `nova-cairn ` prefix that makes it read as a third usage line.
   - Grade: NEXT

5. **`receipt --text` renders the words with control escapes, so the line is not the prose that was kept.**
   - Command: `nova-cairn receipt --store ./cairns --session s1 --entry e2 --text` (entry appended from
     stdin: "words from stdin\nsecond line")
   - Printed (first 3 lines):
     `RECEIPT OK session=s1 entry=e2 stamp=2026-10-06T18:48:07.108846261Z bytes=28 source=- persisted=true published=false publish=manual text="words from stdin\nsecond line"`
   - Expected: either a readable rendering of the kept words on the line, or the `--text` flag help to say
     the quoting escapes control characters; today only `--json` gives the plain string.
   - Grade: NEXT

6. **The tool's page in docs/CLI.md has no `### First run` subheading; every other tool's section has one.**
   - Command: (page read) docs/CLI.md `## nova-cairn`
   - Printed (first 3 lines):
     `## nova-cairn` / (blank) / `Keeps a session's words as local checkpoints: the exact words, their source`
   - Expected: the section to open with `### First run` — the one or two commands, the transcript shape,
     how to read it and what a first run gets wrong — as the other seventeen sections in the same file do.
   - Grade: NEXT

## What held

- Every verb's `-h` and `help <verb>` exits 0 and lists its flags with what each wants; the bare command
  refuses in one line naming the verbs and the door.
- One run reports every independent problem at once (a bare `append --store ./cairns` names --session,
  --entry and the words in three lines).
- Ids are validated hard: `../`, `/`, whitespace, Windows device names all refused with the whole rule;
  no traversal landed.
- `open --dry-run` and `append --dry-run` make the same checks as the write (a conflicting re-open and a
  conflicting append both fail at exit 1 under --dry-run) and write nothing.
- Refusals carry remedies that pasted back and ran (`open first: nova-cairn open --store <dir> --session
  <id> --publish <policy>` for an append to a missing session); the conflict line names the exact
  `receipt --text`.
- `--json` gives the one result/facts/items shape on every verb, refusals included (status failed, remedy,
  why), on stderr, so a script cannot mistake a refusal for a result.
- The flat store interop worked as the page says: append lands a dated section, duplicate adds nothing,
  conflict refuses at exit 1, open on a flat record writes no second record, receipt reads the trimmed
  body with publish=unknown.
- Empty sessions are listed by index (`INDEX SESSION session=empty entries=0`); a session with no entries
  is discoverable, which older rating rounds could not see.
- `--now` replays cleanly and the stamps land in the records; UTF-8 words round-trip byte for byte.

No urgent finding: no wrong result, no lost data, no refusal without a remedy, no help that lies. The six
above are wording and marking frictions a next release can smooth.

READ 9/10 — the help and the CLI.md page answered every question cold and never lied; the wedged NOTE
inside the usage block and the missing `### First run` on the page are the only dents.

USE 9/10 — every verb ran first try, every refusal's remedy pasted back green, and the only friction was
reading lines that cannot tell a re-open or a duplicate from a first write.

urgent=0 next=6
