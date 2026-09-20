RESULT work4s-E04-F04-04 sha=5298f6be12ea — nova-work E04-F04: does the contract say it? criterion E04-F04-04: Maintain eager W membership, its counter and per-friend reverse indexes in the same mutation envelope; normal reads never rebuild W lazily
DONE
CRITERION E04-F04-04 SPEC PARTIAL
REPO mas-bandwidth/nova-tools
NO-BRANCH
grep -n "eager" docs/SPEC-WORK.md | head -20 → 4 hits
grep -n "membership" docs/SPEC-WORK.md | head -20 → 20 hits
grep -n "reverse" docs/SPEC-WORK.md | head -20 → 27 hits
grep -n "envelope" docs/SPEC-WORK.md | head -20 → 15 hits
grep -n "mutate\|mutation" docs/SPEC-WORK.md | head -30 → 30 hits
grep -in "lazy rebuild\|rebuild.*lazily\|lazy.*read\|rebuilt on read\|rebuild.*on.*read" docs/SPEC-WORK.md → 0 hits
docs/SPEC-WORK.md:1699: "**W is materialised eagerly, never rebuilt on a read** *(Stella, `4e800fb`; on Glenn's rule of
docs/SPEC-WORK.md:1700: 2026-09-13 that reading W must never walk O)*. W stays exactly what the paragraph above says —
docs/SPEC-WORK.md:1701: `(working O)`, derived, written by no verb — and this paragraph says only **how it is held**: a
docs/SPEC-WORK.md:1702: resident materialised view of the canonical open ids that hold a live lease, **updated inside the
docs/SPEC-WORK.md:1703: accepted mutation envelope** that carries the item and the lease, beside the canonical task, the
docs/SPEC-WORK.md:1704: lease and the per-friend indexes, so a reader at a published revision sees one consistent state
docs/SPEC-WORK.md:1705: and **no read ever rebuilds it**. A pending offer alone is not in W, and two live attempts on one
docs/SPEC-WORK.md:1706: id count that id once; an assignment, a `take`, a renew, a `release`, an expiry, a settle, a
docs/SPEC-WORK.md:1707: `:cancel`, a reassignment, an undo and a replay each leave the materialisation equal to
docs/SPEC-WORK.md:1708: `(working O)` at the revision they publish (**W1**; suite `materialized-working-set`).
clause with no contract: the word "reverse" modifying "per-friend indexes" appears nowhere in the spec's W section. The spec says "per-friend indexes" (line 1704) but does not call them "reverse indexes". The "reverse assignment index" (line 3322) exists elsewhere in a different context and is not stated as being maintained in the same mutation envelope alongside W membership. All other clauses are covered: eager W membership (lines 1699-1708), its counter `|W|` (lines 1710-1715: "`|W|` is a counter carried and read, never computed"), in the same mutation envelope (lines 1702-1704: "updated inside the accepted mutation envelope ... beside ... the per-friend indexes"), and normal reads never rebuild W lazily (lines 1704-1705: "no read ever rebuilds it").
:by-feature tests for E04-F04 from sexp line 89: "indexes-and-counters; reverse-dependency-index-is-bounded; ready-names-the-blocker-and-the-resolver; ready-needs-every-dependency-settled; materialized-working-set; working-is-a-view; history-grows-startup-does-not"
indexes-and-counters → lisp/nova-work/tests/replays-8664.lisp:141 (in tree)
reverse-dependency-index-is-bounded → lisp/nova-work/tests/acceptance/slice-11-dependencies.lisp:110 (in tree)
ready-names-the-blocker-and-the-resolver → lisp/nova-work/tests/acceptance/slice-01-reader.lisp:41 (in tree)
ready-needs-every-dependency-settled → found via grep pattern, needs check
materialized-working-set → lisp/nova-work/tests/replays-8646.lisp:62 (in tree)
working-is-a-view → lisp/nova-work/tests/acceptance/slice-02-close-and-counters.lisp:553 (in tree)
history-grows-startup-does-not → lisp/nova-work/tests/acceptance/slice-04-doing-and-journal.lisp:446 (in tree)
git status --short → (empty)
Noticed: The spec has extensive coverage of eager W maintenance and the mutation envelope concept but consistently omits the word "reverse" when describing per-friend indexes in the W section. The sexp evidence lists "reverse-dependency-index-is-bounded" which relates to dependency indexing, not per-friend reverse indexing specifically.
