# nova-memory READ and USE rating, nova-tools 1.2.0

Rater: gpt-5.6-terra via Codex CLI
Build: 0d56536c3d61bfeadcb70dfb614861367f5dcd51
READ: 7/10
USE: 7/10

This rates the installed `nova-memory v1.2.0-dev.0d56536c` artifact, whose
embedded VCS revision is the Build above. I read `nova-memory help`, every
advertised verb's `-h`, and the matching source/spec cold, then used only a
throwaway markdown corpus. The public release list had no v1.2.0 asset, so I
did not substitute a v1.0.0 artifact or attribute this build's behavior to the
newer checkout head.

## Reasons

READ. The first-run path is unusually usable: the banner explains the
in-memory, write-nothing model; each verb help states its effect, flags and
exit behavior; and `quickstart` prints the commands it actually ran. Required
flags state why the caller must choose them, which is particularly good for an
AI that must not silently select a corpus, channel or reading budget. The
source/spec also name the lexical boundary and the bounded corpus limits.

READ stays at 7 because the artifact's help and its cold CLI document give
opposite instructions for the same CAL value. The help says CAL is context,
not a cutoff; `docs/CLI.md:416` says a hit only means something clearly above
CAL. My successful scratch search returned two ordinary hits at 0.81 while
CAL was 1.51. A reader who starts from the checked-in documentation is told to
discard a result the tool returned without such a verdict. `verify -h` also
says its root is repeatable even though the verb refuses two roots.

USE. The scratch quickstart, search and check all completed without a store or
server, returned addressable `root=file:line` receipts, and the multi-problem
refusal reported all three independently bad inputs in one run. The explicit
lexical caveat and the non-judging `check` output are good boundaries.

USE stays at 7 because `check` will index its candidate when the candidate is
under `--root`, then receipt that candidate as its own top match without a
warning. The first-run command using a draft inside the corpus demonstrated
that self-match. An AI using the tool as a duplication check can therefore
mistake the candidate's presence for evidence that an earlier record says the
same thing.

## Findings

| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | `docs/CLI.md:416` at build `0d56536c3d61` and `nova-memory help` | The cold CLI document says a raw score below CAL means nothing, while the artifact help says CAL is context rather than a cutoff. The scratch search returned hits at `score=0.81` under `CAL=1.51`, so the two entry points give incompatible advice about a normal result. | Make the document use the help's non-cutoff rule, add one below-CAL transcript, and state any per-hit decision rule on the HIT line if one exists. | M |
| 2 | `nova-memory check --root scratch-memory --channels bm25 --k 2 scratch-memory/draft.md`; `cmd/nova-memory/check.go:45-85` | A candidate located inside a root is indexed before retrieval and is returned as its own receipt. The tool neither refuses nor warns, even though that makes a duplication check self-confirming. | Canonicalize the candidate and roots; refuse or warn when the candidate is inside an indexed root, with a command showing how to place it outside the corpus or exclude it. | M |
| 3 | `nova-memory verify -h`; `cmd/nova-memory/verify.go:20,59-64` | Help inherits `--root` as repeatable, but verify refuses anything except exactly one root. A cold caller can follow its help and receive a failure for a supported-looking invocation. | Give verify a one-root flag description and state the one-tree constraint in its synopsis. | S |

## Good, keep

Keep the write-nothing, per-run index; explicit roots, channels and k; and the
receipts carrying class, frontmatter, root and file:line. Keep quickstart's
echoed commands and its reminder that its channel and k are demonstrations,
not defaults. Keep the refusal shape: the deliberate bad search named bad k,
bad channel and missing query together, then exited 2.

## Compared with earlier ratings

| earlier | now | evidence |
|---|---|---|
| 1.1.0 ratings found CAL hard to interpret | CHANGED, but not resolved | This artifact's help now says CAL is context, not a cutoff; its checked-in `docs/CLI.md:416` still teaches the cutoff rule. |
| Earlier ratings asked for direct, verb-specific help | IMPROVED | Every advertised v1.2 artifact verb answered `-h` at exit 0 with synopsis, flags and effect text. |
| Earlier ratings valued explicit refusal reasons | STILL GOOD | The deliberate `search --channels imaginary --k 0` refusal reported the bad k, unknown channel and missing query in one run. |
| Earlier ratings did not name a candidate-under-root trap | NEW | The scratch `check` returned `scratch-memory/draft.md` itself as rank 1, so the candidate can manufacture its own evidence. |
