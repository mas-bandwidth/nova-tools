# nova-self-talk READ and USE rating, nova-tools 1.2.0

Rater: minimax/minimax-m3 in opencode, a sprint worker on a friend's re-rate card
Build: 7acb90e18a76
READ: 8/10
USE: 8/10

Built and run on a Linux machine, in a scratch directory made for the trial; no live store, no server, no network. Every verb was exercised: scan (bare and named), shapes, example (with and without --dry-run), version, and help, over the example pages, stdin, and a missing file.

## Reasons

READ. The banner answers the three questions in its first lines: what the tool flags, how it reads (sentence by sentence against a table of shapes, a dated claim never flagged), and the first run (example ./pages, then scanning journal.md). The two classes are glossed where named. The output contract is stated line by line with its stream, and the exit table is stated once. The shapes verb prints the live detector table with a sentence each finds and a near miss it passes.

What keeps READ at 8. The banner is still long (94 lines, cmd/nova-self-talk/main.go:28-121), with a print-line inventory and flags essay between the usage block and examples. The per-verb help is thinner than the banner and in places wrong: scan -h and example -h drop the required <file>... and <dir>; every verb's -h ends with the scan's exit table; --skip and --rule-doc print <value> where the banner says <basename>; shapes -h loses the indent of its continuation line. The banner says which lines go to stderr and which to stdout but never places SELFTALK MORE, which goes to stderr, so the banner's own 2>/dev/null recipe loses the only line that says findings were cut. The spec has drifted: its "Says NO" grammar differs from the binary; it opens a paragraph with "the checks above are walls" when nothing above is a check; it cites "Specimen 8" without context; and its test index points at stale line numbers. The internal map row still calls the package an "agent self-talk journal stream" and the README's example command scans a path a reader holding only the binary does not have.

USE. The first run is clean. example --dry-run says what it would write; the real write lays down two pages; a second run writes nothing and reports kept=; a changed page is refused. Each pasted example line exits 1 with findings on stderr in line order. Standard input works as -. --max limits findings per class with a MORE line. --json carries the same counts, items, and more. A missing file refuses the whole run. A bare word that is no file shows the verbs. A flag after files is refused with why. A refusal under --json is one JSON object.

What keeps USE at 8. The tool correctly refuses files with NUL bytes (not valid UTF-8). However: --max abc refuses in the flag library's voice (parse error); --bogus --max abc names only the unknown flag; help frobnicate prints the whole banner at exit 0; example --dry-run /proc/nope says EXAMPLE OK with would-write when the real run cannot write; example --dry-run on existing pages says would-write=- but still offers run: nova-self-talk example ./pages which would do nothing; version extra says got 1 without naming what it got and points at version -h instead of help version.

A 10 would make per-verb help show real usage lines with their own exit tables, make help <unknown> exit 2 with the list of verbs, name every invocation problem in one refusal in the tool's own voice, make the dry run check that the directory exists and is writable, and bring the spec's grammar, line references and map row into line with the binary.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | cmd/nova-self-talk/main.go:28-121 | The banner is 94 lines; the print-line inventory and flags essay sit between the usage block and examples. | Keep the three questions, usage and examples in the banner; move the print-line inventory to scan -h and docs/CLI.md. | M |
| 2 | scan -h, example -h | The usage lines drop the required <file>... and <dir>. | Print each verb's real usage line, as the banner's usage block has it. | S |
| 3 | version -h, shapes -h, example -h | Each ends with the scan's exit table, which is not true of those verbs. | Give each verb its own exit line: 0 done; 2 could not run. | S |
| 4 | scan -h | --rule-doc and --skip print <value> where the banner says <basename>. | Name the value basename in the flag usage text. | S |
| 5 | shapes -h | The banner excerpt loses the indent of its continuation line. | Keep the continuation's indent, or excerpt the usage line alone. | S |
| 6 | main.go: stream table | The stream table never names SELFTALK MORE, which goes to stderr (cmd/nova-self-talk/scan.go:383); the shared output puts MORE on stdout (pkg/tool/out.go:134). | Say MORE prints on stderr after its findings in the table, or print MORE on stdout as the family does. | S |
| 7 | --bogus --max abc | Only the unknown flag is named; the bad --max is never named. | Collect flag-parse errors into the same problems list before refusing. | S |
| 8 | --max abc | Refuses with invalid value "abc" for flag -max: parse error, the flag library's words. | Say --max must be a whole number of zero or more (got "abc"). | S |
| 9 | help frobnicate | Prints the whole banner, exit 0; a caller cannot tell a fallback from an answer. | Print frobnicate is not a verb; the verbs are scan, shapes, example, version, help, exit 2. | S |
| 10 | example --dry-run /proc/nope | Says EXAMPLE OK with would-write when the real run cannot make the directory. | Check in the dry run that the directory exists and is writable, or can be made, and refuse as the real run would. | S |
| 11 | example --dry-run on existing pages | Says would-write=- but still offers run: nova-self-talk example ./pages which would do nothing. | When nothing would be written, offer the scan: run: nova-self-talk pages/journal.md. | S |
| 12 | version extra | Says got 1 without naming what it got, and points at version -h instead of help version. | Quote what it got, and point at nova-self-talk help version. | S |
| 13 | docs/SPEC.md:1870 | The "Says NO" grammar is SELFTALK FAIL <file>:<line>: STANDING: <claim> where the binary prints STANDING match="...": <sentence>. | Write the grammar as the binary prints it. | S |
| 14 | docs/SPEC.md:1751 | "The checks above are walls" opens a paragraph with no check above it in this section. | Say "nova-check's checks are walls". | S |
| 15 | docs/SPEC.md:1810 | "Specimen 8 is the shape it must keep" names a specimen the reader cannot find from here. | Quote the sentence, and keep the number in parentheses after it. | S |
| 16 | docs/SPEC.md:3080 | The test index cites stale line numbers that now hold another tool's text. | Cite the section heading, not a line number, or regenerate the numbers. | S |
| 17 | internal/docs/catalog.go:111 | The internal map calls the package an "agent self-talk journal stream"; it classifies self-claims in prose. | Reword the row: self-claim classifier for prose: STANDING/DATED and INSTALLATION. | S |
| 18 | README.md:40 | The row's first command scans a path only a repo checkout has; the banner's first run is example ./pages. | Show nova-self-talk example ./pages && nova-self-talk ./pages/journal.md, and say exit 1 means findings. | S |

## Good, keep

- shapes: the live detector table, each row with a sentence it finds and a near miss it passes. A pattern tool that shows its patterns is the right answer to "what will this flag".
- Refusal for non-text: files with NUL bytes are refused before scanning.
- example never replaces a page: a changed page is refused, an identical one is kept and reported as kept=.
- The every-run NOTE saying what a green clears and what it does not.

## Compared with earlier ratings

| earlier | now | evidence |
|---|---|---|
| --bogus --max abc names only --bogus (1.1.0 use) | STILL THERE | finding 7 |
| --max abc in library's voice (1.1.0 use) | STILL THERE | finding 8 |
| help <unknown> exits 0 (1.1.0 use) | STILL THERE | finding 9 |
| per-verb usage omits required args (1.1.0 use) | STILL THERE | finding 2 |
| long banner (1.1.0 read) | STILL THERE | finding 1 |
| internal map row (1.1.0 read) | STILL THERE | finding 17 |
| numbered references in spec (1.1.0 read) | STILL THERE | findings 13, 15, 16 |
| scores | CHANGED | READ 8 and USE 8 here; per-verb help defects and dry-run defects are new findings |
