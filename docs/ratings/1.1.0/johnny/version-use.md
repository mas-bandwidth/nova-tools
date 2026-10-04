# nova-version USE rating, nova-tools 1.1.0

Rater: Grok
Build: 3a1cbd4f7245
Score: 8/10

## Reasons

The banner's example runs as printed, from the binary alone. `nova-version example --out versions.tsv` exits 0, writes a one-tool manifest, and the NOTE is the next command. `nova-version snapshot --file versions.tsv` exits 0 with checked=1 known=1 unknown=0. `nova-version report --file versions.tsv` exits 0 and prints the Go toolchain line it ran. `nova-version version` exits 0 with the same line as `--version`.

Two jobs after that. A directory of 18 regular binaries: `nova-version snapshot --bin ./bin --out before.tsv --max 1` exits 0, tools=18, one row on screen, MORE shown=1 total=18, and the file holds the header plus every row. A second directory with one of those binaries, snapshotted to after.tsv, then `nova-version diff --from before.tsv --to after.tsv` exits 0 with tools=18 changed=17 and one DIFF CHANGED line per missing name, to=-. The same file on both sides is changed=0. A different job, a hand-written manifest whose installed column is the argv `nova-version version`: `nova-version report --file local.tsv` exits 0 and names that binary's version.

Refusals. Missing flags: `nova-version snapshot` names both --bin and --out and the --file shape, in one line, and says run help. Unknown flag: `nova-version diff --form before.tsv --to after.tsv` names --form, lists the flags, says did you mean --from, and says run diff -h. Unknown verb: `nova-version nosuch` lists the verbs and says run help. Bad value: `nova-version snapshot --bin ./bin --out bad.tsv --timeout 0s` says the bound must be positive. A bad manifest names every bad line in one refusal. Two bad snapshot files are both named in one diff refusal. `nova-version report --send` names all six missing flags at once.

--json matches the lines on example, snapshot --file, diff and version. --dry-run on snapshot prints dry_run=true, a NOTE that the file was not written, and leaves no file. report and send refuse --json as an unknown flag and list the flags they do take.

Guesses. The dry-run sentence "write no --out" reads as if --out may be omitted; the usage line still requires it, and the refusal names it. A directory of links looks like a bin and is refused as empty. The word regular is in that sentence; the remedy still says to supply a readable directory of executables, which is what was supplied. send is not tried: it needs a bus, a remote and a branch, and its help offers no --dry-run. `nova-version report --draft --as trial --to reader --file versions.tsv` is the local stand-in and prints the note without a delivery. `nova-version moved --from 3a1cbd4f7245 --to HEAD --repo ../../repo --out ./moved-note.md --dry-run` exits 0, prints added=0 deleted=0 renamed=0 verbs=205 with dry_run=true, and writes no file. verbs=205 on an empty diff is the number the line does not explain. A non-checkout is refused in one line.

A 10 names every binary that fails, not the first, renders report as one object, and says when the directory holds links. The rest of this tool is already close: the first run needs no guess, and most refusals are one line with the next command.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | `nova-version snapshot --bin ./badbin --out badout.tsv --timeout 5s` | Two binaries exit 1 and the refusal names only nova-a, then tells the reader to go build ./cmd/nova-a. The other binary is invisible until the next run. | Name every binary that did not answer, in that one line, and point the repair at the file that failed. | M |
| 2 | `nova-version report --file versions.tsv --json` | The banner says report and send take no --json, and the run refuses --json as an unknown flag. The inventory an AI most wants to parse is the one verb left as prose lines. | Accept --json on report and send and print the same facts the lines already carry. | M |
| 3 | `nova-version snapshot --bin ./linkbin --out link.tsv` | A directory whose only entry is a link to a real binary is refused as holding no regular file. The remedy says to supply a readable directory of executables and does not say to replace the links. | Say the entries are links, and name the copy that would make them regular files. | S |
| 4 | `nova-version` | A bare run is labeled VERSION REFUSED, the same token as the version verb, even though no verb was given. The line does list the verbs and says run help. | Label that line with the tool, not with version. | S |
| 5 | `nova-version moved` | One run with no flags prints four MOVED REFUSED lines, each ending run help, rather than one line that names every missing flag and moved -h. A dry-run that changes nothing still prints verbs=205 with no legend. | One refusal, the verb's own -h, and a verbs field that says what it counts. | S |

## Good, keep

The banner example runs as printed, and the NOTE is the next command: report on the manifest just written.
A missing snapshot names both flags and the other shape; a bad manifest names every bad line; a near flag says did you mean.
diff of two non-snapshots names both files in one line, and snapshot --dry-run prints dry_run=true and writes nothing.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| snapshot --bin names one problem where there are several | STILL THERE | `nova-version snapshot --bin ./badbin --out badout.tsv --timeout 5s` exits 2 naming nova-a only |
| moved's counts are unexplained | CHANGED | `nova-version moved --from 3a1cbd4f7245 --to HEAD --repo ../../repo --out ./moved-note.md --dry-run` prints added=0 deleted=0 renamed=0 verbs=205, and verbs=205 has no legend |
| report --json is refused | STILL THERE | `nova-version report --file versions.tsv --json` exits 2 as an unknown flag and names report -h |
| snapshot, diff, report and the JSON refusal worked | STILL THERE | `nova-version snapshot --file versions.tsv` exits 0, `nova-version diff --from before.tsv --to before.tsv` prints changed=0, and the report JSON refusal names the flag |
