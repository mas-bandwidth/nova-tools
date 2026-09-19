## Layout

One file per spec section so parallel PRs stop conflicting (nova-tools #560):
each `## ` section of this file lives in its own file under `docs/spec-pulse/`
and this top file includes them — every section file's body appears here
verbatim, and `internal/pulse/layout560_test.go` checks both directions, that
this file names each section file and carries its body. An amendment edits one
section file (and this top file's copy of that section with it, keeping the
two identical), never two sections at once. The replay slices follow the same
rule on the Lisp side: `lisp/nova-work/tests/acceptance/<slice>.lisp`, loaded
by `lisp/nova-work/tests/acceptance.lisp` (WORKER-CARDS.md practice 26).

- [preamble](00-preamble.md)
- [The loop, in words](01-the-loop-in-words.md)
- [The rules, numbered](02-the-rules-numbered.md)
- [Fleet](03-fleet.md)
- [The verbs](04-the-verbs.md)
- [Handoff](05-handoff.md)
- [Status](06-status.md)
- [Progress](07-progress.md)
- [Rate and convergence](08-rate-and-convergence.md)
- [Exit codes and the output grammar](09-exit-codes-and-the-output-grammar.md)
- [The card, as `cut` writes it](10-the-card-as-cut-writes-it.md)
- [What this draft does not do](11-what-this-draft-does-not-do.md)
- [The manager tier](12-the-manager-tier.md)
- [Tests this spec demands](13-tests-this-spec-demands.md)
- [Open questions](14-open-questions-each-with-a-default-and-the-default-stands-un.md)
- [CI wall](15-ci-wall.md)
- [Layout](16-layout.md)
- [The beat](17-the-beat.md)
- [The learned admission checklist](18-learned-admission-checklist.md)
