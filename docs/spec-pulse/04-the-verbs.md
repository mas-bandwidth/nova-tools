## The verbs

```
nova-pulse pool    --sources <file> --root <dir> [--out <pool.tsv>] [--timeout <s>] [--max <n>]
nova-pulse cut     --pool <pool.tsv> --templates <dir> --out <dir> --root <dir> [--max <n>]
nova-pulse launch  --cards <cards.tsv> --root <dir> --slots <n> --deadline <s> [--queue] [--max <n>]
nova-pulse harvest --id <pulse id> --root <dir> --sources <file> --templates <dir> [--max-body-bytes <n>] [--max <n>]
nova-pulse beat    --queue <dir> --cairn <file> --title <text> [--resume <text>]
nova-pulse manager --policy <file> --queue <dir> --roots <dirs> --bus <clone> --as <name> --hours <n>
nova-pulse progress --queue <dir> --roots <dirs> [--day <d>]
nova-pulse wake    --bench <name>... --registry <file> [--timeout <duration, default 8m>]
nova-pulse sleep   --bench <name>... [--idle <duration, default 30m>]
nova-pulse install --sha <7-40 hex> --build-bench <name> --benches <a,b,c> [--ssh <path>] [--timeout <duration>]
nova-pulse width   --root <dir> --pool <pool.tsv>  (not yet implemented)
nova-pulse version
nova-pulse help
```

Those lines are the string `nova-pulse help` must print, byte for byte — the parity is a
demand on `internal/pulse/cli.go`'s `pulseVerbs`, which carries the same claim in a comment,
and a replay walks it (replay 36). The rules name verbs and flags the shipped block does not
yet offer — `handoff`, `takeover`, `status`, rule 9's admission gates, `--work`, `--benches`
and `--timeout` on every spawning verb — and each of those gaps is named in the open
questions, not listed here, so a reader who types a line in this block never gets a flag
error. `--timeout <s>` (default 120) bounds every `gh`, `git`, `nova-bus`, `nova-wake` and
`nova-swarm` child, for SPEC-MERGE's reason (SPEC-MERGE.md:458); `pool` is the one verb that
carries it today, and the rest take it when they land. `harvest` takes `--sources` and
`--templates` because its last act is `pool` and `cut` again (rule 15). There is no
`--model`, no `--priority`, no `--retry`. `version` takes no flags and no arguments and
prints the one build-identity line every binary prints (SPEC.md, **Every binary says
which build it is**): `nova-pulse <build identity> <goos>/<goarch> <go version>`, four
tokens, exit 0.
