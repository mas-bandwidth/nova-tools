# nova-cairn READ rating, nova-tools 1.1.0

Rater: deepseek-v4-pro
Build: 2c02b2aa2042
Score: 8/10
README: 8.5/10

## Reasons

nova-cairn reads as a well-shaped essay. The doc says what the tool is for in
three lines — main.go:3-9 names the cost (carrying work across a session's end)
and the answer (open, append, index, receipt) — and the code does exactly that,
nothing more. The entry point, verbs and data resolve in under a minute:
main.go:44-121 declares four verbs, each one Effect, one flag set, one Run.
Each file holds one thing: cairn.go the nested store, read_existing.go the flat
store, open_remedy.go the shell remedy. Names are a stranger's words — open,
append, index, receipt over a store, session, entry, source, publish — and the
comments say why, in the present tense, with the hurt each guard is written
from (cairn.go:158-171, SPEC-CAIRN.md:53-62). The tool is one family with the
others: internal/tool supplies the banner, the refusal grammar, the exit table
and one output shape, so nothing here is hand-rolled where the skeleton already
holds it. Tests teach the contract: SPEC-CAIRN.md:110-151 names thirty-three
cases, and firstrun_test.go executes the documented first run rather than only
reading it.

Three cold moments. Confused first at README.md:33: the very first command
demands `--publish` on a tool whose store is plain local files, and the flag's
`immediate` and `deferred` promise a timing the slice does not perform. Bored
first at SPEC-CAIRN.md:37-62: the flat-record shape retells, in prose, the same
bench-versus-nested story the CLI section (docs/CLI.md:2115-2136) and the
package comment (cairn.go:20-31) already carry. Doubted first at
SPEC-CAIRN.md:79-80: "the same entry id carrying different prose is exit 1, a
conflict, never an overwrite" — cairn.go:500-521 reads the entry then renames
over it with no lock, so two live same-id appends can overwrite, the one claim
the code does not bear out.

A 10 would need: a cross-process lock (or a compare-and-swap rename) so the
conflict promise holds under concurrency; byte-identical storage in both store
shapes; and a publish flag whose words match what it does, or no publish flag
at all.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | internal/cairn/cairn.go:501 | same-id concurrent appends can overwrite: the read-then-rename has no lock, so two live appends with one id and different prose both see the entry absent and each renames over the other, breaking the banner's conflict promise | take a cross-process lock on the entry path before the read, or install through an exclusive-link compare-and-swap so the second writer reports the conflict | M |
| 2 | internal/cairn/cairn.go:268 | the two store shapes store different bytes for one input: appendBench trims trailing newlines while the nested Append files the text byte-for-byte, so the same words land with different bytes= depending on the store's shape | file the bytes exactly in both shapes and trim only for display, or record the trim as an explicit documented fact | S |
| 3 | cmd/nova-cairn/main.go:64 | the publish flag's vocabulary promises a timing the slice does not perform: immediate and deferred are recorded while every line reports published=false, inviting a misread that a send happened | rename the values to describe recording only, or drop the flag until a transport exists and note the gap in help | S |

## Good, keep

Keep the refusal that names the remedy verb whole (internal/cairn/open_remedy.go, SPEC-CAIRN.md:57-62): a cold reader pastes, never reconstructs.
Keep the store read-not-imposed stance: a hand-written bench file is a first-class record, never migrated or split (cairn.go:158-171).
Keep the executed first-run transcript: firstrun_test.go runs docs/TESTS.md's block, so the examples cannot drift from the code.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| the store keeps two shapes tangled in one file | CHANGED | the flat record path is split out: locateRecord at internal/cairn/cairn.go:177 and the flat readers in internal/cairn/read_existing.go, no longer one file |
| same-id concurrent appends can overwrite what the banner promises never is | STILL THERE | internal/cairn/cairn.go:501 reads, then internal/cairn/cairn.go:536 renames over the target with no lock |
