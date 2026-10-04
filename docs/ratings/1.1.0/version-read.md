# nova-version READ rating, nova-tools 1.1.0

Rater: glm-5.3-flash
Build: 2be828b0c114
Score: 8/10
README: 8/10

## Reasons
A cold reader gets the tool's shape in a minute: the banner's how-it-works lines name the manifest, the snapshot, the diff and the moved note, say this binary is one of two sharing the manifest reader, and end in a first run the binary performs alone (internal/update/versiontool.go:23-27). The spec states every rule numbered and names the tests it demands, and those tests exist under the names it gives (docs/SPEC-VERSION.md:47-54, internal/update/inventory_spec_test.go:76). The writing carries its reasons: the thirty-second bound is argued in place with the warm-up alternative rejected by arithmetic (internal/update/snapverb.go:21-39), a spent deadline is refused naming both readings of it (internal/update/snapverb.go:186-199), and the inventory verbs sit one file to a family. The code meets the standard's shape: one skeleton, an exit table in the banner, effects on every verb, and a refusal grammar that always names the next command.

What holds the score under 10 is where the reading happens and how the reference speaks. The whole tool lives inside the package of its sibling binary, two of its verbs sharing the sibling's report body through a seam the code itself calls temporary (internal/update/versiontool.go:10,134-135), so the reader of one binary walks the other's check, apply, watch and release code to reach its own. The command reference states adopted counts as constants no reader owns and argues one bound from one bench's afternoon, timings and an issue link included (docs/CLI.md:1736,1759). Two comments cite a rule number the rule does not sit at (internal/update/snapverb.go:216). A 10 needs the verbs on their own ground, a reference that argues from the rule instead of one machine's numbers, and citations that point where the rule is.

First places, read cold before any code: confused at README.md:50, where the trial commands are introduced as the 1.0.0 commands in a tree whose docs and notes say 1.1.0, leaving this reader to guess whether the table's commands still hold; bored at README.md:26, where the nova-work row runs five sentences and a warning inside one table cell, the densest cell in a table that is otherwise one thought per row; doubtful at README.md:104, whose promise that the tests execute the transcripts line by line I did not believe until docs/TESTS.md:666 held the transcript and cmd/nova-version/firstrun_test.go ran it — the claim stands.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | internal/update/versiontool.go:10 | nova-version's every verb lives inside internal/update, its sibling's package: the reader of one binary meets the other's check, apply, watch and release code on the way, and two main paths share one package, the Tool path here and the flag path at internal/update/cli.go:285 | lift the manifest reader and the report body into a lower package both binaries import, and leave each tool's verbs in its own home | L |
| 2 | docs/CLI.md:1736 | the reference states adopted counts as constants, "the adopted 16" and "the 32 nova-* executables" at docs/CLI.md:1777, numbers the manifest's owner owns, not the tool; the tree already holds nineteen commands, so both numbers read stale to a cold visitor | state the rule without counts: the tools the manifest names, never the executables a directory or PATH holds | S |
| 3 | docs/CLI.md:1759 | the thirty-second default is argued from one bench's afternoon: a machine model named, timings in milliseconds, an issue link, so the general doc carries evidence a stranger cannot reproduce and a machine name its own text rule refuses | keep the measurements in the code comment where they already live (internal/update/snapverb.go:27-39) and let the doc state the rule: a first run of a never-executed binary is charged to this deadline | S |
| 4 | internal/update/cli.go:716 | moved runs about 195 lines inside cli.go, the sibling's file, beside its parse helpers; the earlier pass cut it from 345 and the rest of its family each got their own file | give moved its own file beside snapverb.go and diffverb.go | M |
| 5 | internal/update/snapverb.go:216 | the source gate's comment cites SPEC-VERSION item 6, and so does the row struct's at internal/update/snapverb.go:68, but item 6 is diff's rule; the mixed-source rule sits in item 4 of docs/SPEC-VERSION.md | cite item 4 in both comments | S |
| 6 | cmd/nova-version/main.go:3 | the entry comment says the contract is SPEC-VERSION.md while docs/CLI.md:1711 says SPEC-UPDATE.md; each names half the tool, and a cold reader meets two different contract lines before either file | name both in one line at each site: SPEC-VERSION.md for the inventory verbs, SPEC-UPDATE.md for the manifest verbs | S |

## Good, keep
The thirty-second bound's comment is the best writing in the package: it states the rule, measures it, rejects the warm-up alternative with arithmetic, and ends on one honest bound, reachable by flag (internal/update/snapverb.go:21-39). Every refusal names what it wants and the next command, and a spent budget is never reported as a slow binary (internal/update/snapverb.go:176-199). The spec and the tests hold each other: the spec names its tests, and the tests exist under those names.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| lives inside package update | STILL THERE | cmd/nova-version/main.go:22 builds the whole tool from internal/update's VersionTool |
| a 345-line verb in cli.go | CHANGED | the verb is moved at internal/update/cli.go:716-910, about 195 lines, still in the sibling's file |
| a doc section explained by fleet numbers | STILL THERE | docs/CLI.md:1757-1763 argues the bound from one machine's timings and links an issue |
| stale fixed-count prose | STILL THERE | docs/CLI.md:1736 "the adopted 16"; docs/CLI.md:1777 "the 32 nova-* executables" |
| split verb homes | STILL THERE | report and send are defined at internal/update/versiontool.go:100-121 and their body is internal/update/report.go:26, shared with the sibling binary |
| README then 6.5-7 from one rater and 8.4 from another | CHANGED | now 8: the version row's first command runs on the checkout alone (README.md:41,55) while the trial section still pins the 1.0.0 release (README.md:50) |
