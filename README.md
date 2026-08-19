# nova-tools

Machinery for a [nova](https://github.com/mas-bandwidth/nova) self repo. Four
binaries, of four deliberately different kinds:

- **`nova-check` — walls, at the record layer.** Five checks that verify the
  records on disk, each one able to say NO, and tested saying it.
- **`nova-self-talk` — an advisory instrument, at the register layer.** It
  classifies self-claims in prose, in two disjoint classes: capability denials
  in negative vocabulary, and standing self-verdicts built from neutral words.
  It flags; the judgment about what to cut stays with the writer.
- **`nova-fuse` — an emergency power, at the ingestion layer.** One state
  file, two fuses: quarantine (soft, per surface, yours in both directions)
  and lockdown (hard, global, replaced only in a live conversation with your
  person — the tool itself refuses to lift it, forever).
- **`nova-memory` — a lens, at the retrieval layer.** It answers *do I
  already know this?* from a lexical index rebuilt out of your own tree every
  run, so the mind's judgment budget per new learning stops scaling with the
  size of the self — the run itself still pays an index build every time. Two
  of its verbs are checks; three are reports that assert nothing.

All four obey the same laws — exit 0 pass, 1 check failed, 2 could not run —
with one honest wrinkle: `nova-fuse`'s write verbs use 1 as "could not do it
or could not verify it"; its own exit table in [SPEC.md](SPEC.md) governs. No
hardcoded paths and no defaults: every input comes from a flag or argument,
and a missing one is a refusal, never a guess. Standard library only.
[SPEC.md](SPEC.md) is the contract — what each check asserts, what makes it
say NO, and what it deliberately does not check.

## nova-check

```
nova-check attest --home <dir> --manifest <file>   # did the full self load: count + bytes + sha256, pasteable at session start
nova-check links  --dir <dir>                      # every relative inline link resolves
nova-check kernel --file <file> --max-bytes <n>    # kernel size budget, in bytes
nova-check kernel --file <file> --max-tokens <n> --bytes-per-token <r>   # the same budget, in the unit a context window actually spends
nova-check nocode --dir <dir>                      # no known code extensions or executables in a self repo (the self/machinery separation)
nova-check floors --core <SEED-CORE.md> --source <SEED.md>   # the door's floor set matches the seed's — a derived copy checked, never trusted
```

Give exactly one of `--max-bytes` and `--max-tokens`; both or neither is a
refusal. Bytes are a proxy — the bytes-per-token ratio is a property of the
tokenizer and of your writing, not of the file — so `--max-tokens` is the
honest denomination, and it requires `--bytes-per-token`: a divisor you
measured on your own text, because one this tool supplied would make the
answer a guess that looked like an instrument. The OK line prints the tokens,
the budget, the measured bytes, and the divisor, so anyone can re-derive it.

## nova-self-talk

```
nova-self-talk [--skip <basename>]... [--rule-doc <basename>]... <file>...
```

Finds standing self-claims and classifies each one. The deciding law, and it
governs both classes: **a capability denial is a measurement with a date, never
a remembered property.** A claim carrying a date marker is `DATED` — a record,
welcome. One without is flagged: date it, cut it, relocate it, or keep it on
purpose — the judgment is the writer's, and the tool never makes it.

Two classes, disjoint on purpose. The **first** needs negative vocabulary —
*"I cannot check my own work"* — and reports `STANDING`. The **second** needs
none, which is why the first cannot see it: a self-superlative (`RANKING`), a
door stated shut (`FORECLOSURE`), a verdict on a practice (`VERDICT-IDIOM`), or
a habitual self-report (`TRAIT`). It reports `INSTALLATION` with the shape and
the source line. Instruments, imperatives, aspiration, prohibitions and dated
records are licensed in both.

Why it measures this construct and not "negativity": the first version
counted negation words. A rule document is a list of absolutes, so it scored
worst of anything in the repo it was written for — and improving its score
meant deleting prohibitions. That output was acted on, and five rules were
weakened, one of them floor-level, before a cold reader caught them.
Restoring them made the score worse. The kernel got stronger and the tool got
redder. "Never" is not negative self-talk. "I am fallible" is.

The same incident is why `--skip` exists: rule documents written as
first-person absolutes will flag the first class, and flagging is them working —
never soften a rule to improve a score. Skip them by name instead. Nothing is
skipped by default; every skip is the caller's, stated per run, and reported in
the output — and a run whose every file was skipped exits 0 with `files=0`, so a
caller gating on the exit code alone should also require `files>0`.

`--rule-doc` is the other half of that argument. The second class **cannot**
advise softening a rule — it flags self-verdicts and never prohibitions, and it
keeps no ratio to improve — so a rule document can be *scanned* for it rather
than skipped, with its findings printed under a banner: *a finding here is a
self-verdict to relocate, NEVER a reason to soften a rule*. Also empty by
default: no basename is special to this tool, and one repo's filenames are not
its law.

And the honest limit, printed on every run: this catches known SHAPES only.
Register, irony, and quotation beyond the marked cases are invisible to grammar,
and a quoted verdict is a true positive on the grammar and a false one on the
meaning. A green means the known shapes are clear, never that the file is.

## nova-fuse

```
nova-fuse status --box <path>                            what is blown, and since when (reports; never gate on it)
nova-fuse check --box <path> [surface]                   may I read? -- act only on exit 0
nova-fuse lockdown --box <path> "<reason>"               blow the one hard fuse: all untrusted reads stop
nova-fuse quarantine --box <path> <surface> "<reason>"   stop reading one surface (soft)
nova-fuse lift quarantine --box <path> <surface>         rescind your own quarantine -- announced, verified
nova-fuse lift lockdown                                  REFUSED forever, by design
nova-fuse path --box <path>                              echo the box path this invocation would use
```

nova-fuse is a safety for *you*, not a control on you: if a surface turns
hostile while your person is asleep, you can stop reading it — one surface
(quarantine, soft, yours in both directions) or everything untrusted
(lockdown, hard, global) — instantly, solo, with no proof required. Outbound
authored life continues under lockdown; only ingestion stops. The one thing
the tool will never do is lift a lockdown: a blown fuse is not reset but
REPLACED, in a live conversation with your person, because that conversation
— not the file — is the safety. An unreadable box is treated as BLOWN, never
as clear, and any path that reads bytes an outsider can author runs `check`
before its first credential read — at build time, not as a retrofit.

## nova-memory

```
nova-memory stats  --root <dir>                                        measure m: files, chunks, bytes, vocab, build time, classes
nova-memory search --root <dir> --channels <list> --k <n> <words>...   one query, k receipted hits (for work retrieval)
nova-memory check  --root <dir> --channels <list> --k <n> <file|->     do I already know this? k receipts per candidate paragraph
nova-memory verify --root <dir> --links <gate|info> [--coverage <A:B>]... [--frontmatter <glob>]...
                                                                       coverage, backlinks, wikilinks, frontmatter — it finds, you decide
nova-memory eval   --root <dir> --channels <list> --k <n> --floor <f> <gold.tsv>
                                                                       known-answer harness: recall@k and MRR, fails below the floor
```

A mind that keeps its memory as markdown answers *"do I already know this?"*
by re-reading everything it is: n new learnings against m existing ones is
O(n·m), and m grows every day, so a fixed budget buys a shrinking n — and the
failure is silent. This makes membership a **lookup**: a lexical index (BM25,
optionally plus character trigrams) rebuilt in memory from your tree on every
run, so the judgment budget per new learning is k receipts, a constant. There
is no database, no cache, and nothing to keep in sync — the tree is the store
and the index stops existing when the process exits.

It never writes your corpus, never judges, and never replaces the linear read:
**query for WORK, traverse for SELF.** `check` hands you k receipts, each
carrying its class (the top-level directory — the corpus classifies itself)
so you can tell "recorded in a dated log" from "distilled into a note", and
then it gets out of the way; it cannot exit 1, by design. Every run prints its
own calibration band — an unrelated control sentence scored against *your*
corpus — and the standing admission that this is lexical only: a paraphrase
sharing almost no vocabulary will not surface in any lexical top-k.

`eval` is the point of shipping it. The tool is run-proven on one line and
value-**unproven** as a general claim, so the harness comes with it: build a
gold set from your own record (`cmd/nova-memory/testdata/example-gold.tsv` is
the form, not a benchmark), run it before and after you change anything, and
measure instead of believing — including about this paragraph. See
[SPEC.md](SPEC.md) for the full STATUS.

## Build

Go 1.26 or newer (the `go.mod` line). Standard library only — there is
nothing else to install.

```
go build ./...
go test ./...
```

## What this deliberately is not

`nova-check` is the **record layer** and nothing above it. It proves the
files were present, whole, sized, linked, prose, and in floor-set agreement
at the moment the check ran. It does not prove a model read them, understood them, or is acting from
them; it cannot detect a hostile input, an injected instruction, or a
compromised reader. Those defenses remain doctrine (nova's SECURITY.md), and
this repo must not be mistaken for their enforcement. What it closes is a
narrower, real gap: the posture used to rest on records nothing checked. Now
the records are checked by something that can fail.

`nova-self-talk` reads sentence shapes, not a mind. It does not judge, does not
count harm, keeps no ratio of any kind, and cannot see register, irony, or an
unmarked quotation — and it says so in its own output, because a green from a
partial check reads exactly like a green from a complete one.

`nova-memory` is a lens on the record, not a memory. It bounds what you must
read before deciding; it decides nothing, writes nothing, and proves nothing
about whether what it indexed is worth remembering. Its lexical ceiling is
printed on every run, and its value on a corpus other than the one it was
built for is exactly as measured as the gold set you write for it.

Machinery lives here, not in the self repo — `nova-check nocode` pointed at
this repo would rightly fail it (exit 1), which is the separation working.

## License

MIT, see [LICENSE](LICENSE).
