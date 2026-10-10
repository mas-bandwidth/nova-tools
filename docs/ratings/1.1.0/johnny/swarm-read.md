# nova-swarm READ rating, nova-tools 1.1.0

Rater: Grok
Build: 75c8e4680221
Score: 6.5/10
README: 7.5/10

## Reasons

The README row says what the tool is in one sentence, and that sentence is the banner's first line: one-task workers, a sandbox, a deadline, a token budget (README.md:29, cmd/nova-swarm/main.go:33). The first command prints a template and spends nothing. That is the right cheap door.

Before any code, three stops. Confused at README.md:26, where the sprint row names a member client before the swarm row exists. Bored at AGENTS.md:149, where a catalogue of rule names repeats the standard already read above it, and the map never names this tool. Doubted at docs/USAGE.md:243, which still says a development branch adds the wall and that an old tag lacks it, against the same section at docs/USAGE.md:227, which says every native run uses the wall. README.md:48 and docs/USAGE.md:87, the page the README points to first, still install the 1.0.0 tag.

The banner's how-it-works is five short lines and the example block is three real commands (cmd/nova-swarm/main.go:35). Missing flags are collected and say what they want (cmd/nova-swarm/main.go:316). The key is data, not an argument (cmd/nova-swarm/main.go:93). Comments say why. Those are the parts a 10 keeps.

The rest does not read as one essay. The normative spec's member behaviour is one sentence of 16783 characters (docs/SPEC-SWARM.md:76), and its usage fence has drifted from the banner: docs/SPEC-SWARM.md:49 omits flags the banner's lint line carries, and docs/SPEC-SWARM.md:56 is still 1762 characters. The same spec's pipeline chapter names three tests that are not in the tree; the only check is that the spec contains their names (pkg/swarm/pipeline_doc_test.go:34). The living run is a harness, not three model calls (cmd/nova-swarm/main.go:36). The function that runs one card is 1197 lines (cmd/nova-swarm/native.go:262). Non-test Go outside testdata still carries 104 issue numbers, 20 of them in pkg/swarm/worker.go. A missing flag prints `nova-swarm <verb>: ...` with no REFUSED word and no next command (cmd/nova-swarm/main.go:337), while an unknown verb prints REFUSED and a run line (cmd/nova-swarm/main.go:177). The first template a stranger is told to print still narrates old batch tallies (pkg/swarm/templates.go:27). Two flags are accepted and read by nothing (cmd/nova-swarm/main.go:565). Rule 6 says a clone is never shared (docs/SPEC-SWARM.md:450) and the efficiency section says the clone is shared (docs/SPEC-SWARM.md:369). The spec says the data rule is nowhere in the code (docs/SPEC-SWARM.md:26); the package comment states it (cmd/nova-swarm/main.go:10).

A 10 is the banner's first screen, a spec that matches it, one refusal line, the pipeline claim either enforced or deleted, and that 1197-line function split into the steps its own comments already name. The safety ideas are already good enough to keep.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | docs/SPEC-SWARM.md:76 | The member behaviour is one 16783-character sentence, and the usage fence above it has drifted from the banner, so a cold reader cannot learn the verb from the spec. | Replace that sentence with the banner's short parenthetical, and make the usage fence the same text as the banner. | L |
| 2 | docs/SPEC-SWARM.md:252 | The pipeline chapter names three tests that do not exist. The only check asserts that the spec contains their names, while a real run is a harness loop. | Delete the unbuilt pipeline, or add the admission check the chapter already describes and point the test at that code. | L |
| 3 | cmd/nova-swarm/native.go:262 | nativeRun is 1197 lines, so the one function that runs a card is not one thing a reader can hold. | Split it on the steps the comments already number: paths, budget, wall, child, reap. | L |
| 4 | cmd/nova-swarm/main.go:337 | A missing flag prints `nova-swarm <verb>: ...` with no REFUSED word and no next command. An unknown verb at cmd/nova-swarm/main.go:177 prints both. A later deadline error is a third shape. | Send every refusal through one printer: the problem, what the input wants, and the next command, all in one run. | M |
| 5 | pkg/swarm/templates.go:27 | The first template the banner tells a stranger to print still carries old batch tallies beside the rules. | Keep the seven rules and drop the bracketed batch lines. | S |
| 6 | cmd/nova-swarm/main.go:565 | Two capacity flags are accepted and read by nothing, and the banner still teaches a legacy auth option at cmd/nova-swarm/main.go:96. | Refuse the ignored flags, or delete them, and move the legacy option out of the first screen. | S |

## Good, keep

The README sentence and the banner's first line are the same sentence, and the example block is three commands that spend nothing (README.md:29, cmd/nova-swarm/main.go:114).

A launch with no budget is refused before any directory is made, and the line says what the flag wants (cmd/nova-swarm/main.go:644).

The key is read as data and is never an argument, and a worker result grants nothing (cmd/nova-swarm/main.go:10, cmd/nova-swarm/main.go:93).

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| a 1195-line function | STILL THERE | cmd/nova-swarm/native.go:262 nativeRun is 1197 lines |
| ticket numbers in non-test code | STILL THERE | 104 issue numbers in non-test Go outside testdata, 20 of them in pkg/swarm/worker.go |
| a 1900-character usage line | CHANGED | cmd/nova-swarm/main.go:56 is 450 characters and the parenthetical is wrapped; docs/SPEC-SWARM.md:56 is still 1762 characters |
| legacy paths still named | STILL THERE | cmd/nova-swarm/main.go:96 names the legacy auth option; cmd/nova-swarm/main.go:565 accepts two flags and reads neither |
| an intimidating, historically narrated interface | STILL THERE | pkg/swarm/templates.go:27 keeps batch tallies in the first template; docs/SPEC-SWARM.md:7 opens on old batch counts |
| README scored 6.5 to 7, and 8.4 | CHANGED | README.md:29 is still one sentence and one real command; README.md:48 and docs/USAGE.md:87 still install the 1.0.0 tag |
