# nova-card dogfood

Run: 2026-10-07. Tool: `nova-card v1.2.0-dev.7a1152a darwin/arm64 go1.26.6`. The build commit is an ancestor of `sprint/mechanical-2026-10-02` at `6ff31d5b3`; every generated directory and input made for this run stayed under the job's `scratch/` directory. I read the tool's help and its `docs/CLI.md` and `docs/SPEC-CARD-CONTRACT.md` page before trying the verbs.

1. Command: `nova-card generate --from findings --file ./cmd/nova-card/testdata/findings.tsv --repo example/repo --base dev --sha 0123456789abcdef0123456789abcdef01234567 --out scratch/cards-name-dry --name DogfoodReviewer --dry-run`

   Output (first 3 lines):

   ```text
   nova-card generate REFUSED: unknown flag --name; the flags of generate are --base, --bin-dir, --dry-run, --file, --from, --ledger, --max, --minutes, --out, --prefix, --repo, --repo-dir, --sha, --tier, --tool; did you mean --base?; run: nova-card help generate
   ```

   Additional command: `nova-card generate --from findings --file ./cmd/nova-card/testdata/findings.tsv --repo example/repo --base dev --sha 0123456789abcdef0123456789abcdef01234567 --out scratch/cards-dropped-dry --dropped abandoned-card --dry-run`

   Additional output (first 3 lines): `nova-card generate REFUSED: unknown flag --dropped; the flags of generate are --base, --bin-dir, --dry-run, --file, --from, --ledger, --max, --minutes, --out, --prefix, --repo, --repo-dir, --sha, --tier, --tool; run: nova-card help generate`

   `nova-card lint --card scratch/cards-help/dogfood-help-nova-card.md --name DogfoodReviewer` also refuses `--name`. `docs/CLI.md` lists both `--name` and `--dropped` for generation and lint, and the spec says they exclude named people and dropped card ids from generated briefs.

   Expected: accept the documented flags and apply them, or remove the claims from the docs.

   Grade: URGENT

2. Command: `nova-card generate --from ledger --ledger serial-tests --repo-dir . --out scratch/cards-max-negative --max -1 --dry-run`

   Output (first 3 lines):

   ```text
   id	file	test	wave	deps
   serial-tests-internal-nsprint-store-store	internal/nsprint/store/store_test.go	internal/ci TestEveryTestOpensWithTParallel	1	-
   serial-tests-internal-secrets-place	internal/secrets/place_test.go	internal/ci TestEveryTestOpensWithTParallel	2	serial-tests-internal-nsprint-store-store,serial-tests-internal-nsprint-store-open-sends-no-command
   ```

   The dry run produced all 13 cards. The help says `--max` is an integer and only documents `0` as “all”.

   Expected: refuse a negative limit with a remedy, rather than silently treating it as no limit; with a real output directory this could write far more cards than the caller intended.

   Grade: NEXT

READ 8/10 — The help and CLI page explain the three input sources, output shape, and the basic flow, but the docs advertise flags this build refuses.

USE 7/10 — Ledger, findings, and help generation plus repeated lint worked on scratch outputs, while the negative limit was accepted as an unbounded run.

urgent=1 next=1
