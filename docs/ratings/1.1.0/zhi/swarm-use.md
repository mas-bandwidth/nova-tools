# nova-swarm USE rating, nova-tools 1.1.0

Rater: a large language model, cold to this tool
Build: 99e4a903966c
Score: 7/10

## Reasons

The first successful run was `nova-swarm template --name card`, which exits 0 and prints a card a stranger could fill in. A second, different job: `nova-swarm lint --card card.md` on that template exits 0, reports 40 checks, and prints one NOTE per placeholder with the exact remedy, and `nova-swarm lint --rules` prints the full rule table with a remedy per rule. The four refusals: a missing --result on verify names the missing flag and what it wants; an unknown flag on native is blocked by doctor first with the shadowed-binary remedy; an unknown verb lists the ten verbs; a bad --result path says it wants a readable file. All exit 2.

What costs the score heavily: `nova-swarm verify --result result.md --contract 'RESULT: t sha=0123456789ab' --label t` on a one-line result file panics with `runtime error: slice bounds out of range [2:1]` at internal/swarm/contract.go:82, recovered only into a goroutine dump. The result template's own first line is one line, so the template as printed cannot pass verify. `native` and `member` could not be tried at all: doctor refused the freshly built binary because it shadows the installed one in ~/.local/bin, and putting the installed binary first would test a different build.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | `nova-swarm verify --result result.md --contract 'RESULT: t sha=0123456789ab' --label t` | a one-line result file panics with slice bounds out of range instead of a refusal | guard `len(lines) < 2` in internal/swarm/contract.go before slicing | S |
| 2 | `nova-swarm verify` | the missing-flag refusals name each flag but print no `; run:` remedy, unlike the unknown-verb refusal | add the next command to verify's missing-flag refusals | S |
| 3 | `nova-swarm native -h` | the example cannot be tried with a freshly built binary because doctor refuses the shadowed binary first | doctor could name the fix as an env override for a deliberate test build | S |

## Good, keep

The template and lint pair that lets a cold user write a valid card without a harness. The lint NOTE per placeholder with the exact remedy. The doctor shadow check that refuses to run under the wrong binary.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| the native -h example fails as written | CHANGED | the example now stops at doctor, which names the shadowed binary and the fix |
| the result template fails verify | STILL THERE | `nova-swarm verify` on a one-line result panics at internal/swarm/contract.go:82 |
| two refusal formats | STILL THERE | verify prints `nova-swarm verify: ...` without `; run:`; unknown verb prints `nova-swarm REFUSED: ...; run: nova-swarm help` |
| required-input refusals omit a next command | STILL THERE | `nova-swarm verify` with no flags names the missing flags but prints no `; run:` |
