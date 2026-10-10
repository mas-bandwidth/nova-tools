# nova-card dogfood, 2026-10-06

One friend (opencode harness, abliteration-ai/abliterated-model-large-v2), using `nova-card`
cold from `nova-card -h`, `nova-card help`, `nova-card <verb> -h` and the tool's page under
`docs/` (docs/CLI.md `## nova-card`, docs/TESTS.md `## nova-card`) only, at nova-tools
`sprint/mechanical-2026-10-02` tip 0760eac79, built on the Linux bench
(`nova-card devel darwin/arm64 go1.26.6`, cross-compiled; every generate ran against scratch
directories and the staged checkout, nothing outside them, no store, no server). Every verb
ran at least once: generate (all three sources: ledger, findings, help), lint, template,
version, help; the refusals ran too, and each exit code 0, 1 and 2 was hit and matched the
banner's table. One run of the use was retried because I mistyped the example's 40-hex sha —
the example itself runs exactly as printed, from the root of a checkout, as docs/TESTS.md
says. Roughly thirty minutes of use.

## Findings

1. `nova-card help`
   - `nova-card: writes a directory of pre-linted briefs from a ledger, a findings file or a tool's help` / `nova-card is pre-alpha: not ready for production use.` / (how it works block)
   - Expected: a usage block I can read in a terminal. The three usage lines run 229, 242 and
     246 characters (the tool's own count, from generating a card about its own help: lines
     16, 17, 18, 48 and 57 run over 100 characters); they wrap to noise in any normal
     window, and the concrete `example:` block at the bottom — the part a stranger actually
     types — is what works. The tool cuts exactly this finding in other tools' help; its own
     help carries it.
   - Grade: NEXT.

2. `nova-card help nosuchverb`
   - `nova-card: writes a directory of pre-linted briefs from a ledger, a findings file or a tool's help` / `nova-card is pre-alpha: not ready for production use.` / (how it works block), exit 0
   - Expected: one line saying `nosuchverb` is not a verb and naming the verbs there are —
     the bare `nova-card` refusal does exactly that ("one of generate, lint, template,
     version, help"). `help <unknown>` prints the whole banner at exit 0 without naming the
     word I typed, so a stranger learns nothing about the mistake; help never refuses, but
     the unknown verb goes unreported.
   - Grade: NEXT.

3. `nova-card generate --from help --tool nosuchtool --bin-dir ./bin --repo-dir ./repo --out ./use/help-cards-nosuch`
   - `nova-card generate REFUSED: the source yields no card; nothing to write; run: nova-card help generate`, exit 2
   - Expected: per the banner's "what it prints" — `CARDS NOTE <what was skipped: ... a tool with no help>` — a NOTE naming `nosuchtool`, or at least a refusal that names the tool
     it could not run. The refusal names neither the tool nor which `--tool` of possibly
     several was the problem, so with three tools on the line the caller has to bisect.
   - Grade: NEXT.

4. `nova-card generate --from weblog --file x --out ./use/x` (no `--repo`/`--repo-dir`)
   - `nova-card generate REFUSED: no repository: --repo <owner/name>, or --repo-dir a checkout whose origin names one; run: nova-card help generate`, exit 2
   - Expected: every problem the run can find, in one answer. My `--from weblog` is also
     wrong, but it is only named (`--from "weblog"; want ledger, findings or help`) once a
     repository is supplied; the first refusal sent me to fix the wrong flag first. The
     onboarding standard asks a refusal to name every problem at once.
   - Grade: NEXT.

5. `nova-card generate --from ledger --ledger dead-code --repo-dir ./repo --out ./use/dead-cards --max 2 --name sandbox`
   - `LINT DRIFT card=dead-code-cmd-nova-sandbox check=personal-name line=6: names sandbox outside the owner's quoted words; write the role (the owner, a friend, a bench), never the name` (4 DRIFT lines, then `nova-card generate FAILED: 4 red line(s) above; nothing written to ./use/dead-cards`, exit 1)
   - Expected: `--name` guards person, friend and machine names in prose. A name that is also
     a path word reds every card whose PATHS or TEST names the path (`sandbox` in
     `cmd/nova-sandbox`, `internal` in `pkg/bus`), and the remedy — "write the role
     (the owner, a friend, a bench), never the name" — cannot be applied to a PATHS line,
     so the tool's own generated content can never pass its own `--name` check for such
     words. This fleet runs machines named like tools, so the collision
     is a real one.
   - Grade: NEXT.

6. `nova-card generate --from findings --file ./repo/cmd/nova-card/testdata/findings.tsv --repo example/repo --base dev --sha 0123456789abcdef0123456789abcdef01234567 --out ./use/cards-dryrun2 --dry-run`
   - `id	file	test	wave	deps` / `finding-internal-bus-send	pkg/bus/send.go	pkg/bus TestReceiptIsFsynced	1	-` / `finding-cmd-nova-bus-main	cmd/nova-bus/main.go	cmd/nova-bus TestFindingMain	1	-` (then `CARDS OK dir=./use/cards-dryrun2 cards=2 waves=1 tier=pro dry-run=yes (nothing written)`, exit 0)
   - Expected: the help documents "CARDS OK dir=... then manifest" — the CARDS line first,
     as in the real run, with the manifest after it. The dry-run prints the manifest first
     and the CARDS line last, so a caller reading the first line (or a script grepping the
     summary) gets manifest rows instead. Verified the dry-run really writes nothing (the
     out dir was absent afterwards).
   - Grade: NEXT.

7. `go test -count=1 -timeout 600s ./internal/ci/` (the docs-tree gate, on the Linux
   bench; a report quoting nova-card's example output must pass it)
   - `--- FAIL: TestEveryNamedRepoPathExists (2.01s)` / `Error: docs/dogfood/2026-10-06/antigravity/card.md:64: pkg/bus/send.go names no file or directory in this tree; a friend following it finds nothing -- correct the path, or add it to testdata/namedpaths_allowlist.txt with the reason it is not a real path` (exit 1)
   - Expected: recording the documented first run's manifest (this card's own form) should
     not red the docs tree. The findings fixture the example reads names
     `pkg/bus/send.go`, a file that does not exist in this repository, so a report
     quoting the example's output needs a `testdata/namedpaths_allowlist.txt` row (added,
     section 2, with the reason) before the gate goes green; a stranger recording the
     example hits the red and has to find the list. A fixture naming only real paths
     would keep the example self-contained.
   - Grade: NEXT.

READ 8/10 — honest effect lines per verb, an exit table that matched every code I hit,
examples that run exactly as printed, and refusals that name a remedy and even the near-miss
flag ("did you mean --name?"); held back by the 229-246 character usage lines and the
silent `help <unknown verb>`.

USE 8/10 — every verb ran, all three generate sources produced real linted briefs first try
(the first-run example included), the dry-run wrote nothing, and LINT DRIFT's red-brief path
exits 1 with named checks and remedies; held back by the `--name` path-word collision, the
refusals naming one problem at a time, and the missing-tool refusal that names no tool.

Not tried: `nova-sprint add --brief-dir` on the generated directory (the flow's second line)
— it needs a sprint store and the card forbids starting a server, so the manifest's promise
that the add accepts the briefs is taken from the lint, not from an add; the `CARDS NOTE`
path for "a row the ledger did not read" was not triggered (every ledger I read yielded
cards); `--bin-dir`'s PATH default was not exercised (`--bin-dir` was passed explicitly
throughout).

urgent=0 next=7
