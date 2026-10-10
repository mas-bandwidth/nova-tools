# nova-card dogfood

On the source-matched build, the documented `--name` and `--dropped` flags are present and accepted; my first-pass unknown-flag result came from an older installed binary and is deployment-version skew, not a current source finding. The source-matched run completed all five verbs, exercised ledger, findings, and help generation plus lint against job-local scratch outputs, and found one remaining edge case: `--max -1` silently generates all 13 cards in a dry run.

Run metadata: source `0bc5f275dcee8fc0b5f4b62f42047b7d4b501588`, branch `sprint/dogfood-codex-card-b.w2.g1.e15`; built as `nova-card v1.0.1-0.20261007162517-0bc5f275dcee linux/amd64 go1.26.6`. The first pass used an installed `nova-card v1.2.0-dev.7a1152a darwin/arm64 go1.26.6`; it refused a generation call with `unknown flag --name` and another with `unknown flag --dropped`. Rebuilding current source showed both flags in `generate -h` and `lint -h`, and a successful generation and lint run with both flags. I do not count the earlier version-skew refusals as findings. The host-specific binary and scratch paths in the command below are represented by `$BIN` and `$OUT`; its flags and values are the literal invocation arguments.

1. Command: `"$BIN" generate --from ledger --ledger serial-tests --repo-dir . --repo mas-bandwidth/nova-tools --base sprint/mechanical-2026-10-02 --sha 0bc5f275dcee8fc0b5f4b62f42047b7d4b501588 --out "$OUT/cards-negative-max-current" --max -1 --dry-run`

   Output (first 3 lines):

   ```text
   id	file	test	wave	deps
   serial-tests-internal-nsprint-store-store	internal/nsprint/store/store_test.go	internal/ci TestEveryTestOpensWithTParallel	1	-
   serial-tests-internal-secrets-place	pkg/secrets/place_test.go	internal/ci TestEveryTestOpensWithTParallel	2	serial-tests-internal-nsprint-store-store,serial-tests-internal-nsprint-store-open-sends-no-command
   ```

   The dry run listed all 13 cards. The help says `--max` is an integer and documents `0` as “all”; it does not define negative values.

   Expected: refuse a negative limit with a remedy, or document the behavior. As implemented, a typo or generated negative limit silently becomes unbounded; with a real output directory it would create all 13 cards.

   Grade: NEXT

READ 9/10 — The help and CLI page agree on the source modes, flags, output shape, and basic flow; the negative-limit behavior is unspecified.

USE 8/10 — All source modes and all verbs worked on scratch data, and the documented name and dropped-card checks passed; the negative limit was accepted as unbounded.

urgent=0 next=1
