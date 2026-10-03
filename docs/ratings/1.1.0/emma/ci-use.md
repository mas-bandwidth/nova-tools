# nova-ci USE rating, nova-tools 1.1.0

Rater: gemini-2.5-pro
Build: 2c02b2aa2042
Score: 8.5/10

## Reasons
The tool is fast, responsive, and predictable across its primary tasks. The store-free example mode allows immediate evaluation, and refusal handling across missing flags and invalid values is exemplary.

First successful run:
Running `nova-ci slowtests --example --budget 60 --load 4 --cpus 16` executed immediately from the compiled binary with zero external dependencies, printing elapsed package timings and load metrics.

Real jobs tried:
1. Measuring test elapsed time on a scratch Go module by piping `go test -count=1 -json ./pkg1 ./pkg2 | nova-ci slowtests --package-budget 0.001` identified both packages exceeding the allocated threshold and displayed slowest tests.
2. Discovering functional tests using `nova-ci functional ./pkg1 ./pkg2` correctly identified `./pkg2` and emitted the exact regex pattern `^(TestFunctionalSlow)$`.

Refusals provoked:
- Unknown verb: `nova-ci bogus` returned exit 2, listed all recognized verbs, and pointed to `nova-ci help`.
- Missing required flags: `nova-ci github receipt` named all missing flags at once (`--from-runner`, `--repo`, `--sha`, `--run-id`, `--workflow`, `--conclusion`, store address) and stated what each expects.
- Unknown flag: `nova-ci slowtests --bogus` identified the offending `--bogus` flag and listed all supported flags.
- Bad value: `nova-ci slowtests --budget abc` indicated that `--budget wants a whole number, got "abc"`.

Flags tested:
- `--json` on `slowtests` rendered complete structured output for successes and refusals alike.
- `--dry-run` on `github receipt`, `new-rule`, `new-verb`, and `local` accurately previewed planned actions without performing network calls or filesystem modifications.

Verbs not tried live:
- `github receipt` without `--dry-run` was not executed against a live database.
- `local` without `--dry-run` was not invoked across the entire test suite.
- `new-rule` and `new-verb` were tested with `--dry-run` to avoid modifying tracked repository files.

Where guessing was needed:
- `nova-ci functional ./...` returned bare package directories like `pkg2` rather than `./pkg2`, requiring manual path adjustments before feeding into the test runner.
- The help text references "the SLEEPS marker" without providing the specific code pattern needed to declare one.

To reach a 10/10:
1. Ensure `functional ./...` preserves relative path prefixes so outputs can be piped directly into `go test`.
2. Document the code-level syntax for skipped tests in the `slowtests` help description.
3. Note in banner examples that piping `go test` masks non-zero test failure exits unless shell pipe failure checks are enabled.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | `nova-ci functional ./...` | Package traversal prints bare package directory paths that the test runner rejects | Retain relative path prefixes such as leading dot slash in output | S |
| 2 | `nova-ci help slowtests` | Help mentions SLEEPS marker without documenting the Go test skip pattern | Include skip call syntax in flag explanation | S |
| 3 | `nova-ci help` | Banner example pipes go test to slowtests without warning that test exit status is masked | Add warning note that test failure status must be verified independently | S |

## Good, keep
Built-in event stream under example flag enabling instant verification with no setup.
Comprehensive refusal diagnostics reporting every invalid or missing parameter in a single invocation.
Consistent structured output representation between plain text lines and json mode.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| functional ./... prints a path the test runner rejects | STILL THERE | `nova-ci functional ./...` prints pkg2 which fails go test pkg2 |
| the allowlist's bound and the SLEEPS marker are not in the help | STILL THERE | `nova-ci help slowtests` describes SLEEPS marker without stating skip syntax |
| the unknown-option refusal omits the offending flag | FIXED | `nova-ci slowtests --bogus` prints unknown flag --bogus |
