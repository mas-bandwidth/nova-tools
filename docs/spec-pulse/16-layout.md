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

- [preamble](spec-pulse/00-preamble.md)
- [The loop, in words](spec-pulse/01-the-loop-in-words.md)
- [The rules, numbered](spec-pulse/02-the-rules-numbered.md)
- [Fleet](spec-pulse/03-fleet.md)
- [The verbs](spec-pulse/04-the-verbs.md)
- [Handoff](spec-pulse/05-handoff.md)
- [Status](spec-pulse/06-status.md)
- [Progress](spec-pulse/07-progress.md)
- [Rate and convergence](spec-pulse/08-rate-and-convergence.md)
- [Exit codes and the output grammar](spec-pulse/09-exit-codes-and-the-output-grammar.md)
- [The card, as `cut` writes it](spec-pulse/10-the-card-as-cut-writes-it.md)
- [What this draft does not do](spec-pulse/11-what-this-draft-does-not-do.md)
- [The manager tier](spec-pulse/12-the-manager-tier.md)
- [Tests this spec demands](spec-pulse/13-tests-this-spec-demands.md)
- [Open questions](spec-pulse/14-open-questions-each-with-a-default-and-the-default-stands-un.md)
- [CI wall](spec-pulse/15-ci-wall.md)
- [Layout](spec-pulse/16-layout.md)
