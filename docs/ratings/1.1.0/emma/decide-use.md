# nova-decide USE rating, nova-tools 1.1.0

Rater: gemini-2.5-pro
Build: 4296165394da
Score: 8.5/10

## Reasons
The tool is fast, responsive, and predictable across its core decision workflows. The offline fixed backend allows complete functional evaluation without network requests or keys, and refusal diagnostics report every missing or invalid parameter in a single turn.

First successful run:
Running `nova-decide ask --schema ./testdata/schema.json --state ./testdata/state.txt --backend fixed --answers ./testdata/answers.json --record ./decisions.jsonl --op first` executed immediately from the compiled binary with zero external dependencies, printing evaluated question answers and probabilities.

Real jobs tried:
1. Reading a card and diff, recording the decision, attaching an outcome, and verifying that repeat calls with identical label report changed=false:
   `nova-decide read --card ./testdata/card.md --diff ./testdata/card.diff --backend fixed --answers ./testdata/read-answers.json --record ./decisions.jsonl --op card-1`
   `nova-decide outcome --record ./decisions.jsonl --id card-1 --label ok`
   `nova-decide outcome --record ./decisions.jsonl --id card-1 --label ok`
   `nova-decide calibrate --record ./testdata/record.jsonl --decision read --question defect --positive wrong --negative ok`
2. Scoring landed work, clustering detected defect classes, and classifying red gate test failures:
   `nova-decide score --card ./testdata/card.md --diff ./testdata/card.diff --backend fixed --answers ./testdata/score-answers.json --record ./decisions.jsonl --op card-1@landed`
   `nova-decide findings --record ./testdata/record.jsonl --since 2026-10-01`
   `nova-decide gate --output ./testdata/gate-output.txt --card ./testdata/card.md --diff ./testdata/card.diff --base-red TestPortInUse --backend fixed --answers ./testdata/gate-answers.json --record ./decisions.jsonl --op gate-1`

Refusals provoked:
- Missing required flags: `nova-decide read` reported all missing required flags at once (--card, --diff, --backend, --record), explained what each expects, and stated the remedy.
- Unknown flag: `nova-decide read --bogus` named the unknown flag, printed all supported flags of read, and pointed to `nova-decide read -h`.
- Unknown verb: `nova-decide bogus` named the unknown verb, listed all eleven available verbs, and referenced `nova-decide help`.
- Bad value: `nova-decide calibrate --record ./testdata/record.jsonl --decision read --question defect --positive wrong --negative ok --bars 0.5,2` caught the out-of-range value "2" and explained that probabilities must be between 0 and 1.

Flags tested:
- `--json` on read emitted the complete structured result object containing verb status, facts, and typed item answers.
- `--dry-run` on read, gate, and outcome planned operations safely without performing disk writes or backend requests.
- Dry run over an existing recorded operation reported recorded=existing dry_run=true and displayed the recorded answers.

Verbs not tried live:
- Remote model calls via jev were not executed against the live endpoint because credentials are not configured in this test environment; they were evaluated via --dry-run and refusal tests.

Where guessing was needed:
- In calibrate, passing `--question verdict` without an explicit option refused because choice questions require `<question>=<option>` format, which is explained in the refusal but omitted in flag usage.
- In calibrate, attempting to calibrate a freshly created record with only positive labels refused because both positive and negative labels are required.

To reach a 10/10:
1. Add an explicit syntax example for choice questions (`<name>=<option>`) directly in the help text for calibrate --question.
2. Document in calibrate help that the target record must contain at least one positive and one negative outcome.
3. Include a note in gate help showing the two-value comma-separated format for --bars.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | `nova-decide help calibrate` | Help does not document the question=option syntax required for choice questions | Add syntax note explaining choice questions take name equals option | S |
| 2 | `nova-decide help calibrate` | Help omits requirement that calibration dataset must contain both positive and negative outcomes | State in help that at least one positive and one negative outcome must exist | S |
| 3 | `nova-decide help gate` | Flag description for bars does not highlight that two comma-separated probabilities are mandatory | Add concise format hint in flag summary | S |

## Good, keep
Offline execution via fixed backend allowing full verification without network or keys.
Comprehensive refusal messages reporting all missing or invalid parameters in a single invocation.
Idempotent op handling enabling safe resumption and replay of previously recorded decisions.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| no earlier rating | CHANGED | the first rating of this tool |
