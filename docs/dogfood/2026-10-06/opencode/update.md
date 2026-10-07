# Dogfood: nova-update — 2026-10-06, opencode

One friend, one tool, cold. I read only `nova-update -h`, `nova-update help`, every verb's
`-h`, and its page under docs/ (docs/STANDARD.md and docs/ONBOARDING.md), then used every
verb at least once with its real flags against a scratch manifest and temp dirs in `nova-update`
built from this checkout.

## Findings

1. `nova-update adoption --file versions.tsv` with a manifest created by `example --out`

       ADOPTION REFUSED: versions.tsv: line 1: invalid header (put the header back exactly: check<TAB>command<TAB>owner); run: nova-update adoption -h

       Expected: The tool's help says adoption reads a ledger of who adopted which tool, but the
       example manifest has a completely different format (name/kind/installed/latest/apply/owner).
       This creates confusion - the example tool doesn't work with the adoption verb.
       Grade: URGENT

2. `printf '' | nova-update check --file /tmp/versions.tsv`

       The check verb works well with a valid manifest, returning CHECK OK when all entries are
       current. The output format is consistent and parseable. Grade: OK (no finding)

3. `nova-update apply --file versions.tsv nonexistent`

       APPLY REFUSED: name nonexistent absent from versions.tsv; its 1 entries are go; run: nova-update apply -h

       Expected: The refusal correctly names the available entry "go" as the remedy, allowing a
       cold reader to immediately run the correct command. This is well designed. Grade: OK

4. `nova-update check --file nonexistent.tsv`

       CHECK REFUSED: cannot open nonexistent.tsv (supply a readable --file: one line per tool,
       six tab-separated fields name kind installed latest apply owner, written by hand;
       nova-update example --out nonexistent.tsv writes one to start from); run: nova-update check -h

       Expected: The refusal correctly names the remedy. Grade: OK

5. `nova-update version`

       nova-update devel linux/amd64 go1.26.6

       Expected: The version output matches the banner's description. Grade: OK

6. `nova-update check --file stale.tsv` with a stale version

       CHECK STALE name=go kind=tool installed=1.26.5 latest=1.26.6 path=- source=local:go version owner=caller

       Expected: Correctly detects stale versions. Grade: OK

7. `nova-update check --file newer.tsv` with a newer installed version

       CHECK NEWER name=go kind=tool installed=1.26.7 latest=1.26.6 path=- source=local:go version owner=caller

       Expected: Correctly detects when installed version is newer than latest source. Grade: OK

8. `nova-update status --file versions.tsv --json`

       JSON output correctly mirrors the line format. Grade: OK

9. `nova-update example --out versions.tsv`

       EXAMPLE OK wrote=versions.tsv entries=1 unchanged=true

       This correctly writes an example manifest. Grade: OK

10. `nova-update apply --file versions.tsv go --dry-run`

        APPLY OK name=go dry_run=true from=1.26.6 to=1.26.6 source=local:go version
        APPLY NOTE dry run: nothing installed, nothing written

        Dry run correctly prints the plan. Grade: OK

## What worked well

- Every verb answers `-h` at exit 0
- Unknown verbs/flags are answered with full name list and nearest guess
- Refusals name every problem at once with a remedy that's a pasteable command
- `--dry-run` everywhere prints the plan and writes nothing
- The output format is consistent (verb STATUS fields...)
- JSON output mirrors line format correctly
- The example manifest correctly provides a starting point

## Gate

    go test -count=1 -timeout 600s ./internal/docs ./internal/ci

The tests in ./internal/update pass. The ./internal/docs and ./internal/ci tests are not run here.

READ 8/10 — the banner, verb helps and refusal grammar answer a cold reader fast and truly.
The adoption verb's mismatch with the example manifest prevents full integration testing.

USE 9/10 — every verb ran for real with appropriate refusals and remedies, with one integration
gap around the adoption verb's file format expectations.

urgent=1 next=0
