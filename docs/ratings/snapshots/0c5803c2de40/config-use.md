# nova-config USE rating, current baseline 0c5803c2de40

Rater: opencode qwen3.8-flash
Build: 0c5803c2de40
Score: 8/10

## Reasons
The source reviewed is snapshot 0c5803c2de406c1b0b2b0841f579c9bf73406b1c; the staged branch head is 9d7802ac5b52328b06c4d56f79fe1cac7a12cec3 and contains that snapshot; the binary is built only from an isolated detached checkout at the full snapshot sha with `go build -o <job>/bin/ ./cmd/...`, and `nova-config version` prints `nova-config devel linux/amd64 go1.26.6`. Everything below ran on that binary in a scratch directory with the store and provider environment variables cleared; the whole local subset runs against the `--file` store the help offers, hash of try.json tracked between writes.

What ran: `help`, `-h`, `<kind> <verb> -h`; the advertised first-run example block verbatim (migrate, machine add, set, list, history) — it runs exactly as printed and writes only ./try.json; two distinct jobs: a friend row plus a loop row, and a route row, all on the --file store; seven refusal probes; `--json` and `--dry-run` on writes, remove, migrate, apply, status.

What could not run (needs real services, judged from help only): apply into Redis, inventory against Redis, migrate --pg, status --redis. No Redis or PostgreSQL was contacted.

Why 8 and not lower: the help is the strongest use surface — every refusal names every problem at once and ends with the next command to run; `--dry-run` is honest (prints the row it would write, store hash unchanged; apply --dry-run now refuses exactly as apply refuses); per-verb `-h` marks required flags, states the effect and gives a worked example; history rows say who wrote and when with old>new values; exit codes are documented and match what the probes returned. What cost the score: under `--json` every refusal still leaves stdout empty and answers in plain stderr, so an AI told to read JSON gets nothing to parse on any error path; the top-level help leaves one unit (`--deadline`) unqualified and one conditional requirement (`--every` or `--keepalive`) out of its summary, each costing a guessed retry; `status` reports the Redis view absent (`redis=""`) with status ok and no warning. A 10 would need refusals to arrive as one JSON object under `--json` naming the same problems and the next command, `--deadline <seconds>` spelled out in the top help, the loop line naming its xor requirement, and a warning fact when apply has never run. This is a rating of this baseline snapshot and the coverage tried, not of any release.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | `nova-config machine add m9 --as a1 --file try.json --json` | with --json set, a refusal prints plain text on stderr and leaves stdout empty: exit=2, stdout=, stderr=`nova-config machine add REFUSED: --seat is required ...` — an agent reading --json finds no machine-readable error on any path (usage, store miss, machine self) | under --json emit the same result object: status refused, exit, the named problems, and the run: next command | M |
| 2 | `nova-config route add r-flash --tier flash --provider p1 --model m-1 --deadline 30m --tokens 1000 --as a1 --file try.json` | the top help names `--deadline` with no unit, so the natural guess `30m` is refused (`want a non-negative integer`) and the job takes a retry | the top-level usage line prints `--deadline <seconds>` the way `route add -h` does | S |
| 3 | `nova-config loop add w1 --machine m1 --argv '["nova-swarm","member","--as","w1","--reader"]' --as a1 --file try.json` | the loop summary line says add needs `--machine --argv`, but the write also demands exactly one of --every or --keepalive; only the refusal teaches it (`has neither --every nor --keepalive`) | the loop line in the top help adds: and one of --every or --keepalive | S |
| 4 | `nova-config status --file try.json --json` | `redis=""` comes back with status ok: a fleet whose Redis view was never applied is indistinguishable from a healthy one | add a warnings fact naming the absent view when apply has not run or Redis is unreachable | S |

## Good, keep
- refusal grammar: every problem named at once, the wanted shape printed, and `run: <the exact next command>` at the end; the exit-code contract in the top help matches what probes return.
- dry-run honesty: `CONFIG DRY-RUN ... wrote=nothing` with a NOTE suggesting the follow-up set; the store file hash is unchanged after add and remove --dry-run; `migrate --dry-run` lists all 19 pending migrations and creates no file.
- the first-run example block runs verbatim on the --file store with no database, and history rows record actor, time and old>new per change.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| every refusal under --json leaves stdout empty (USE 2026-10-02 at 1aac13259) | STILL THERE | `nova-config machine add m9 --as a1 --file try.json --json` gives exit=2 with stdout empty and the REFUSED text on stderr |
| --json refusals escape as plain stderr (USE 2026-10-02 at 1aac13259) | STILL THERE | `nova-config loop set wX --every 5 --as a1 --file try.json --json` gives exit=2, stdout empty, stderr `REFUSED: name "wX": want lower-case letters, digits and dashes` |
| apply --dry-run passes where apply refuses (USE 2026-10-02 at 1aac13259) | FIXED | `nova-config apply --file try.json --dry-run` gives exit=2 `REFUSED: --redis is required`, the same refusal apply gives without --dry-run |
