# nova-fuse READ rating, current baseline 0c5803c2de40

Rater: z-ai/glm-5.3-flash, a cold read that ran nothing
Build: 0c5803c2de40
Score: 7.5/10
README: 7.5/10

## Reasons

This read is of source snapshot 0c5803c2de406c1b0b2b0841f579c9bf73406b1c, judged only by
reading: README.md top to bottom, then AGENTS.md and what it points to first (docs/USAGE.md,
docs/CLI.md, docs/SPEC.md, docs/TESTS.md), then the fuse section of docs/CLI.md and the
spec's output grammar, then cmd/nova-fuse from main.go (help.go, lift_remedy.go, version.go,
and the tests), then the internal packages it leans on: internal/fuse, pkg/atomicfile,
pkg/oneline, pkg/bounded. Nothing was run.

The README's line for the tool is one sentence (README.md:36: a recorded decision to stop
reading an untrusted source, checked before every read), and the code does that: the box is
one JSON file named with --box on every verb, check is the gate, and every write re-reads
the box before it says OK. Entry point, verbs and data are found inside a minute from
`nova-fuse help`; each file carries one thing (state in internal/fuse, printing helpers in
oneline, the verbs in main.go); the names — box, surface, quarantine, lockdown, lift — are
a stranger's vocabulary by the end of docs/CLI.md:324-330.

First confused: README.md:36. The row's headline sentence reads as if this tool does the
checking it names; the qualifier (the harness must consult the box for the decision to take
effect) sits one column over in the setup cell of a wide HTML table, so a linear reader
meets the promise before its correction. First bored: README.md:22-42, the tool table —
eleven rows, each cell a compressed essay, the nova-secrets row five clauses deep; as text
it is a slog, and the tool I came for is row twelve of fifteen. First doubted a claim:
docs/CLI.md:328, "any path that reads bytes an outsider can author runs check before its
first credential read, at build time" — a fleet-wide promise the fuse docs themselves cannot
bear out from here, stated in the same paragraph that honestly says the tool enforces
nothing. (A fourth doubt, README.md:48's "these are the 1.0.0 commands" beside a
RELEASE-NOTES-1.1.0.md, was checked against CHANGELOG.md:3 and stands: 1.0.0 is the latest
cut release.)

Why 7.5 and not higher: the safety reasoning is the best writing in this tree — a box that
cannot be read is BLOWN, never CLEAR, argued at every branch (internal/fuse/fuse.go:9-27,
cmd/nova-fuse/main.go:490-496); writes are verified by re-reading (cmd/nova-fuse/main.go:573-583);
`lift lockdown` refuses before any flag or file is read (cmd/nova-fuse/main.go:151-157,
317-328). The cost is the craft around that reasoning. The comments that carry it are
shouted in all-caps bursts, and two of them narrate change history instead of the present
design (cmd/nova-fuse/main.go:122, 744). The verb list is written four times and two of the
copies already disagree on the lift spellings. The box write is an unlocked read-modify-write
that two concurrent writers lose one write of, with this tree's own file lock unused. The
refusals do not speak the family's REFUSED grammar, and no verb renders --json, so the one
result value has one rendering. Unknown box fields decode silently. Each is small; together
they are the difference between admiring the design and trusting the file.

What a 10 would need: one verb list; comments in the present tense at normal weight; the
read-modify-write under the file lock the tree already carries; refusals in the family
grammar and --json from the shared skeleton; a strict decode; and a README headline that
carries its own qualifier.

## Findings

| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | cmd/nova-fuse/main.go:6 | The reasoning a cold reader needs is shouted: comment bodies open in all-caps bursts (LOCKDOWN IS HARD, THE COUNT IS NEVER CAPPED, EVERY FLAG TAKES ONE VALUE) through main.go and internal/fuse/fuse.go:9-63, and two narrate history instead of the design (main.go:122 "It was the whole 32-line banner", main.go:748 "did before this branch too") | Rewrite the comments in the present tense at normal weight, keeping every reason; the essay underneath is already good | L |
| 2 | internal/fuse/fuse.go:33 | Two concurrent writers lose one write: quarantine and lockdown are read-modify-write over the whole box with no lock (cmd/nova-fuse/main.go:606-624, 547-567), the loss is documented as accepted, and pkg/filelock, this tree's process-exclusive lock, goes unused here | Hold the named file lock across each read-modify-write so a racing quarantine and lockdown both land | M |
| 3 | cmd/nova-fuse/main.go:128 | Refusals print `nova-fuse <verb>: <what>; run: nova-fuse help`, not the family's `<TOKEN> REFUSED: <what>; run: <remedy>` that docs/SPEC.md:144 states; a scanner anchored on REFUSED finds no nova-fuse refusal, and the one refusal that does carry the word (main.go:325) spells it mid-sentence | Print refusals through the family shape with a REFUSED word leading, keeping exit 2 | M |
| 4 | cmd/nova-fuse/main.go:195 | Flags, help, the refusal printer and the exit table are hand-rolled here instead of the shared skeleton, and no verb renders --json, so the one result value has one rendering (docs/SPEC.md:140-147) | Take dispatch and the refused -h from pkg/tool's HelpRefused seam and render --json from the same value, keeping exit 0 as CLEAR | M |
| 5 | cmd/nova-fuse/help.go:77 | The verb list is written four times — the usage block (main.go:46-56), the unknown-verb refusals (main.go:173, help.go:77) and docs/CLI.md:289-299 — and the two refusal copies already disagree on the lift spellings | Keep one verb-list constant and print it from every refusal | S |
| 6 | internal/fuse/fuse.go:226 | Unknown JSON fields decode silently, so a hand-edit that misspells lockdown changes nothing and the box still answers clear at exit 0 | Decode with unknown fields refused, so a misspelled key is an unreadable box and reads as blown | S |
| 7 | README.md:36 | The headline "checked before every read" promises the enforcement the tool deliberately does not do; the correction lives one column over in the setup cell | Fold the qualifier into the headline: the harness that consults the box is what does the checking | S |

## Good, keep

- The fail-closed spine is argued at every branch, never asserted: an unreadable or absent box is BLOWN, never CLEAR (internal/fuse/fuse.go:9-27, cmd/nova-fuse/main.go:490-496), and every write re-reads the box before claiming OK (cmd/nova-fuse/main.go:573-583).
- `lift lockdown` refuses before any flag is parsed or any file is read (cmd/nova-fuse/main.go:151-157, 317-328), and the only remedy it names is a live conversation — lever analysis, not slogan.
- The fuse section of docs/CLI.md (docs/CLI.md:287-331) is a complete honest teach: transcript, exit meanings, why -h is refused, and the plain sentence that the tool enforces nothing.

## Compared with earlier ratings

| earlier | now | evidence |
|---|---|---|
| its own output dialect | CHANGED | The per-binary event tokens are now stated in docs/SPEC.md:299-306 and pinned verb by verb in cmd/nova-fuse/status_grammar_test.go:11-38; the refusal line keeps its own non-family shape at cmd/nova-fuse/main.go:128 |
| shouted and historical comments | STILL THERE | All-caps comment bursts at cmd/nova-fuse/main.go:6-17, 226-228, 454-457 and history prose at main.go:120-123, 740-749; internal/fuse/fuse.go:23-33 shouts its five notes |
| three copies of its verb list | STILL THERE | Now four copies: cmd/nova-fuse/main.go:46-56, main.go:173, help.go:77 and docs/CLI.md:289-299, and the two refusal copies already disagree on the lift spellings |
| permissive box decoding | CHANGED | Malformed and mis-typed boxes now fail closed (internal/fuse/fuse.go:23-27, 226-227); unknown JSON fields still decode silently (fuse.go:226-231) |
| lost concurrent updates | STILL THERE | internal/fuse/fuse.go:32-33 records that two writers lose one write, and the read-modify-write at cmd/nova-fuse/main.go:606-624 still takes no lock |
| README then 6.5 to 7 and 8.4 | CHANGED | The trial table and its honest setup columns hold (README.md:22-42); this read gives 7.5, with the fuse headline still promising the checking its setup cell corrects (README.md:36) |
