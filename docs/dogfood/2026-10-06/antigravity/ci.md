# nova-ci dogfood — antigravity (zhi), 2026-10-06

Read as a stranger: only `nova-ci -h`, `nova-ci help`, `nova-ci <verb> -h`, and
the page under `docs/`. Built from the staged checkout at
c02c00769c229a6e012b0dff03ef8929016f3b23 and used as
`nova-ci v1.0.1-0.20261006191814-c02c00769c22 darwin/arm64 go1.27.1`: every verb
once with its real flags (the built-in `--example` stream, a scratch dir), the
refusals too.

## Findings

1. `nova-ci bench -h`
   Printed:
   ```
   usage: nova-ci bench <run> [flags]
     nova-ci bench run --host <h> [--fallback <h>] --dir <tree> [--root <dir>] [--cache <dir>] [--with-git] -- <go command>
   `nova-ci bench <verb> -h` lists a verb's flags.
   ```
   then, on the next line, the exit table's `exit codes:` runs straight into the
   banner: `exit codes: nova-ci: test-time budgets over go test -json output, and
   this repository's own CI steps`, followed by the whole banner. I expected the
   group's `-h` to print the group usage and its exit table and stop, the way
   `nova-ci slowtests -h` does.
   Grade: NEXT.

2. `nova-ci slowtests < /dev/null` (an empty event stream)
   Printed:
   ```
   CI-SLOW FAILED packages=0 slowest=none: looked at nothing; run: nova-ci slowtests --allow-empty
   CI-LOAD load=10.45 cpus=32 per-cpu=0.33: measured, not a verdict
   ```
   exit 1. I expected the verdict line to be led by the verb's own status word
   (`nova-ci slowtests REFUSED:` or `FAILED`), or exit 0 for a stream with
   nothing to judge; `CI-SLOW FAILED` reads like a finding prefix, not the run's
   verdict. The remedy (`--allow-empty`) is present, so this is friction, not a
   wrong result.
   Grade: NEXT.

3. `nova-ci local --dry-run` (from the staged checkout, whose shared Go cache is
   read-only for this reader)
   Printed:
   ```
   nova-ci local REFUSED: the package selection against ca8bb8cc36d5c8297aae0c082105a54dbb0c75b4 failed (ERROR select-packages: go list failed; failing the job rather than testing nothing (re-run on a healthy runner):\x0apattern ./cmd/...: open /Volumes/nova/ai/shared/cache/go-build/ff/fff6...-d: operation not permitted\x0a...
   ```
   exit 2. I expected one line naming the failed step and the remedy; the refusal
   is a raw multi-line `go list` error with `\x0a` escapes and a machine-local
   cache path. The trigger was my sandbox, but the refusal shape is the finding:
   a reader cannot act on the escaped blob.
   Grade: NEXT.

## What the tool got right

- `nova-ci` bare and `nova-ci bogus` each refuse in one line and name every verb.
- `nova-ci slowtests --nosuchflag` names every flag of the verb.
- `nova-ci github receipt --dry-run` with nothing set names every missing
  required flag at once, each with what it wants (ONBOARDING point 2).
- `nova-ci version -h` (and every verb's `-h`) carries the exit table and the
  effect, and exits 0 before reading anything.
- `nova-ci slowtests --example --json` renders the same value as the lines.
- `--max 0` really prints every finding, as its help says.

READ 8/10 — the banner says what the tool does, how it works in five lines and
where its state lives, and every verb's effect is explicit; the group `bench -h`
formatting and the `CI-SLOW FAILED` verdict word are what keep it from a 9.

USE 8/10 — `nova-ci slowtests --example` runs with nothing set up and the
refusals name a remedy a cold reader can paste; `local` needs a healthy checkout
and its failure shape is noisy, so a first real use can stumble.

urgent=0 next=3
