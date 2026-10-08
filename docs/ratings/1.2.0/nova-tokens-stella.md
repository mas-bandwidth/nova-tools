# nova-tokens READ and USE rating, nova-tools 1.2.0

Rater: gpt-5.6-terra via Codex CLI
Build: 78e40bbe949f
READ: 6/10
USE: 5/10

This rates the release-candidate source at `origin/sprint/mechanical-2026-10-02`,
`78e40bbe949fed8bf1f7512fb1e31413884438fe`; the forge has no v1.2.0 tag or
published asset. The real binary used was exactly identified as
`nova-tokens v1.2.0-dev.0d56536c darwin/arm64 go1.27.1`, not relabelled as the
candidate. `git diff 0d56536c..78e40bbe -- cmd/nova-tokens internal/tokens
docs/SPEC-TOKENS.md README.md` is empty, so the command observations apply to
the candidate's unchanged tool and spec paths. I read `help` and every verb's
`-h`, then used only a copied `example-bench` in a throwaway directory. No
live store was dialled, no server was started, and no Go build ran on this
machine. Exact command stdout, stderr and exits are retained in the job's
`tool-logs/` and `scratch/nova-tokens-trial.Emg1Yu/` evidence.

## Reasons

READ. The command says what it accounts for, requires every data path to be
named, separates unmeasured values from zero, and gives each verb an effect
statement. The specification clearly distinguishes a report from a gate, and
the copied fixture makes a cold local first run possible. The source labels,
per-type totals and `check` line make the resulting data inspectable.

READ stops at 6 because the command's own help, normative specification and
supporting documents conflict on important behavior. A reader cannot safely
infer the provider syntax, exit contract, defaults, environment reads, or
whether local `report` reads an existing day file. The first runnable example
is buried after a long global policy block, while its own follow-on `session`
example can corrupt the apparent accounting result.

USE. `fold`, `check` and `sum` on the copied fixture each exited 0 and agreed
on the initial three rows. The output retained dashes for unknown dimensions,
named the sources, and the missing-source invocation refused at exit 2 without
guessing. Those are strong mechanics for an AI caller.

USE is 5 because the documented `session --out` flow accepts the same Claude
messages already folded, writes another row, and leaves `check` and `sum` green.
The exact trial changed the Claude totals from input=1338, output=1593,
cache_write=1200 and cache_read=246000 to 2676, 3186, 2400 and 492000. An
accounting tool must not present that duplicated spend as a successful ledger.

## Findings

| # | where | finding | fix |
|---|---|---|---|
| 1 | `nova-tokens session --claude-session ./transcripts/window.jsonl --out ./out` after the fixture fold | Session adds the same Claude messages as a fourth `unattributed` row; the following `sum` doubles the Claude totals and still returns `SUM OK`. | Refuse a session whose message ids are already represented in the day file, or make the example use a distinct transcript or output. |
| 2 | `cmd/nova-tokens/main.go:48` | The banner says `check, sum and report read the day files back`, but local `report` takes declared live sources and has no day-file input. | Describe `check` and `sum` as day-file readers and local `report` as a source fold. |
| 3 | `cmd/nova-tokens/main.go:105-106` | It calls `--timeout` the one default although `session --weights` and `session --day` also default. | List every default or scope the claim to fold. |
| 4 | `docs/SPEC-TOKENS.md:413` | The spec says `--provider <label>=<file>` while help requires `<kind>:<label>=<file>`. | Use the binary's kind-prefixed syntax throughout the spec. |
| 5 | `docs/SPEC-TOKENS.md:443` | Fold is described as not checking overlapping sources although the binary refuses same-provider duplicate message ids before writing. | State the implemented same-provider overlap refusal and its limits. |
| 6 | `nova-tokens sources -h` and `cmd/nova-tokens/sources.go:15-18` | The shared help exit text says an unparsed bus note is exit 1, but `sources` intentionally exits 0 whenever it ran. | Give `sources` its own exit contract or qualify the shared fold-only failure. |
| 7 | `nova-tokens fold --out ./out --day 2026-09-11 --repos ./repos.tsv` | The refusal directs the caller to the 162-line global help instead of `nova-tokens fold -h`. | Point verb-specific refusals at that verb's help. |
| 8 | `nova-tokens version --json` and `cmd/nova-tokens/version.go:67-72` | The refusal reports only that it got one argument, not the rejected `--json` flag. | Name the rejected flag and direct the caller to `nova-tokens version -h`. |
| 9 | `nova-tokens report -h` | Local `report` synopsis omits accepted `--bus` and `--swarm` source flags. | Include every accepted local source flag in the synopsis. |
| 10 | `docs/USAGE.md:185-187` | It says there are no defaults and no environment variables, contradicting timeout, weights, session day and the documented Redis/PATH reads. | Name the exceptions and distinguish path defaults from all defaults. |
| 11 | `README.md:86-92` | A 1.2.0 candidate still sends a new reader to the 1.0.0 release and install command. | Publish and link the matching release, or identify this tree as a candidate without a matching install command. |
| 12 | `cmd/nova-tokens/main.go:50-51` | `first run` points more than a hundred lines down to ten setup lines instead of showing one short runnable sequence near the usage synopsis. | Move a compact first run directly below usage and leave detailed rules below it. |

## Good, keep

The fixture fold, check and sum form a real, local path with explicit sources;
the initial fold produced a typed line for each source and `check` named the
three-row day. Unknown values stay `-` rather than becoming invented zeroes.
The missing-source refusal names the permitted source flags and says it is
refusing to guess. Every help invocation completed with exit 0.

## Compared with earlier ratings

| earlier | now | evidence |
|---|---|---|
| Session can double a folded sample | STILL THERE | This trial's three-row file became four rows and doubled the Claude totals under `SUM OK`. |
| Global help is used as a refusal remedy | STILL THERE | The missing-source fold refusal ends `run: nova-tokens help`. |
| `--timeout` is called the only default | STILL THERE | `main.go:105-106` conflicts with session's documented defaults. |
| Provider syntax drifts in the spec | STILL THERE | `SPEC-TOKENS.md:413` omits the required provider kind. |
| JSON count values as strings | FIXED | The earlier 1.2.0 record notes numeric JSON counts; this review found no contrary evidence. |
| Same-provider duplicate sources were silently counted | FIXED for fold | Help and source describe a pre-write duplicate-id refusal for two declared same-provider sources. |
