# nova-secrets USE rating, current baseline 0c5803c2de40

Rater: GLM (glm-5.3-flash), a cold reviewer with no memory of this tool
Build: 0c5803c2de40
Score: 8/10

## Reasons
Rated from binaries built on this bench from the staged checkout at exactly 0c5803c2de406c1b0b2b0841f579c9bf73406b1c: the tree was clean at that commit, and the nova-secrets binary's own stamp reads v1.0.1-0.20261003234940-0c5803c2de40 with vcs.revision equal to that SHA and vcs.modified=false. A pre-provisioned nova-secrets on this machine's PATH carries a different stamp and a different toolchain and was set aside. Commands below use $N for the rated binary and $S for the job's scratch dir; every path is job-local, the store's remote is a local bare clone, and the age and sops on PATH are the pinned installed ones (age 1.3.2, sops 3.13.3). No real store, seat or key, no ssh, no gh and no network was touched.

What ran cold: `nova-secrets help`; every verb's help; `version`; `--version`; the advertised first run (keygen, one chmod the refusal itself named, .sops.yaml from the printed rule block, a bare remote, `seal --dry-run`, then a real `seal --stdin --no-pr`); job 1: `names`, `check`, and `exec --only API_KEY --require API_KEY` delivered the sealed value (25 chars) into the child command; job 2: a second name and a rotation of the first, then `exec --only API_KEY,TEST_TOKEN --require TEST_TOKEN` delivered both; job 3: keygen and `seat add` for a second seat, `gate --base main --head <branch> --machines <registry>` APPROVE, merge, then `check` and `exec` under the new seat's key delivered the re-sealed value; `place --dry-run` and `placed` (count=0); `--json` on names and on a refusal; `names --max 2` printed the MORE line with a widening command; the four refusal probes (missing flag, unknown flag, unknown verb, bad value) plus wrong-key, --only mismatch, --require exclusion, bare command and `help <unknown>`.

Could not be run here, judged from help and --dry-run only: a real `place` over ssh (no machine this job may touch), the `seal` and `seat inject` pull-request path (needs a push, gh and a gate approval; the plan says it waits up to 2m for the gate), `placed` over real receipts, and the recovery-key flows. All measurements describe this baseline and this declared coverage, not release completion.

8/10: the local subset and the refusal grammar behave exactly as the help says, and every turn ended in a paste-able next command. It loses the points for: the `place` plan deriving the default remote path from the registry's os/arch field where the help says <home>, so a delivery verb's plan contradicts its own help; the gate naming one rule problem per run, so one new seat costs three gate runs; a wrong key printing only sops exit 128 with the cause withheld; and the bad-value refusal carrying no next command. A 10 needs the plan to follow the help's <home>, the gate to name every rule problem at once, a run: remedy on the bad-value refusal, and a cause named for the wrong-key failure.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | `nova-secrets place --store $S/store --as alpha --key $S/keys/alpha.key --sops <sops> --machine bench-a --secret TEST_TOKEN --machines $S/fleet.tsv --dry-run` | the plan prints path=darwin/amd64/.config/nova-secrets/TEST_TOKEN.env; the help says the default is <home>/.config/nova-secrets/<secret>.env and the registry's home column read /home/bench, so the plan built the path from the os/arch field instead; a reader cannot deliver without guessing | derive the default --path from the registry's home column, as the help states | M |
| 2 | `nova-secrets gate --store $S/store --base main --head seat-gamma --machines $S/fleet.tsv` (three runs) | the gate refused one rule problem per run — first "rule has 1 age recipients; expected exactly two", then "path_regex names 0 seat files", then the unvouched recipient — so one seat-rule branch costs three gate runs; a refusal should name every problem at once | judge each changed rule against every rule it can fail and print one line per failure, per the exit table | M |
| 3 | `nova-secrets exec --store $S/store --as alpha --key $S/keys/beta.key --sops <sops> --only API_KEY -- <cmd>` | with a key that does not open the seat file the only line is "sops failed: exit 128 (transcript withheld: run 'sops -d <file>' to inspect)"; the likely cause (this key does not open this file) is never named, so the reader must run sops by hand to learn it | keep the transcript withheld but name the likely cause: the --key does not open <as>.yaml | S |
| 4 | `nova-secrets names --store $S/store --as alpha --max notanumber` | the bad-value refusal ends "the value given is not one" with no run: line; every other refusal carries a next command, and here the next command is the same call with a number or --max 0 | end the refusal with "; run: nova-secrets names -h" like the other bad-input refusals | S |
| 5 | `nova-secrets keygen --as beta --key $S/keys/beta.key --age-keygen <age-keygen>` | after the tool's own RULE, NOTE, NEXT and OK lines, age-keygen's raw output ("Done. Your new key is at … Nothing failed. Next: send this public key …") prints a second, foreign Next: hint, so two different next steps compete on one screen | capture --age-keygen's output and fold it into the tool's own OK and NEXT lines | S |

## Good, keep
- The refusal grammar: one line naming the problem, the valid names there are, and a paste-able next command — unknown flags and verbs list what exists, the JSON refusal carries a why array with a full remedy, and the store-prerequisite refusal explains the stale-copy danger with exact git remedies.
- --dry-run is a real plan from the real path (write, branch, push, pull request, gate wait) that reads no value and writes nothing, and --json renders the same result value the lines do.
- The local loop works first-try: keygen rule block, .sops.yaml, seal --no-pr, merge, exec delivers only the --only names with --require enforced; seat add re-seals a new seat whose own key then opens it, and the gate vouches the rule against the registry.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| 1aac13259 (7): place accepts the gate's registry format and plans a garbage path | STILL THERE | the PLACE PLAN line above: path=darwin/amd64/.config/nova-secrets/TEST_TOKEN.env while the registry's home column read /home/bench |
| 1aac13259 (7): the gate names one problem at a time | STILL THERE | three GATE REFUSE runs on one seat-rule branch, one problem each (recipients, then the seat file, then the vouch) |
| 1aac13259 (7): a wrong key gives sops failed: exit 128 | STILL THERE | the wrong-key exec line above, exit 125, cause unnamed |
| 1aac13259 (9): the bad-value refusal lacks a next command | STILL THERE | the --max notanumber refusal line ends without a run: remedy |
