# nova-sprint USE rating, nova-tools 1.1.0

Rater: gemini-2.5-pro
Build: 2c02b2aa2042
Score: 8/10

## Reasons
The tool provides a rich and capable state machine that coordinates multi-agent work through all stages of execution. Running offline on an in-memory twin makes testing and local verification straightforward, and core workflows for card admission, dealing, execution reporting, reader reviews, and landing function smoothly. Provoked refusals clearly identify missing requirements and supply executable recovery commands.

A score of 10 would require fixing output bounding on inspection verbs like log, ensuring invocation refusals honor requested JSON formatting, rejecting unexpected arguments on version, enabling play simulation to advance twin state by ticking the machine rather than leaving ticks to the hand, and eliminating manual multi-tick guesswork for state settling.

Verbs requiring external daemons or network infrastructure (run, where --watch, inbox --wait, dashboard, fleet sync, and friend sync) were judged from their help and dry-run output rather than live execution.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | `nova-sprint log --max 1` | the --max flag has no effect on output length and prints all lines without bounding | respect --max parameter and bound rendered log lines with a MORE continuation | S |
| 2 | `nova-sprint take --json` | invocation errors on missing required parameters emit plain text and ignore requested JSON output | serialize invocation refusals as JSON when --json is present on the command line | M |
| 3 | `nova-sprint play --ticks 2` | running play on a twin exits with status OK without advancing cards because a twin is ticked only by hand and the driver never runs tick | drive state progression during play on twin backends by ticking the machine rather than leaving ticks to the hand | M |
| 4 | `nova-sprint version --unknown` | version silently ignores unrecognized arguments instead of refusing invalid input | validate arguments and reject unknown flags on version | S |
| 5 | `nova-sprint tick` | state transitions on twin mode require guessing the number of manual ticks needed for card movement | document the exact tick count needed for transitions or provide an auto-settling tick flag | S |

## Good, keep
The twin memory backend offers a frictionless way to experiment with card lifecycles without setting up databases.
Provoked refusals provide helpful actionable hints that specify exactly which parameters are missing.
Structured JSON output is implemented across major query verbs including where, card, inbox, and stats.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| log --max does nothing | STILL THERE | `nova-sprint log --max 1` prints all 8 entries with LOG OK lines=8 of=8 |
| play on a twin reports OK and moves nothing | STILL THERE | `nova-sprint play --ticks 2` prints PLAY OK stopped=ticks while cards remain unmoved |
| usage refusals ignore --json | STILL THERE | `nova-sprint take --json` outputs plain text refusal on stderr |
| version silently accepts unknown options | STILL THERE | `nova-sprint version --unknown` exits 0 printing version string |
