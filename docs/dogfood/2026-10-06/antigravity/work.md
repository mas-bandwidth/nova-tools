# nova-work dogfood — Antigravity

Source: `df30ce088344588bf75a86168057f468710ae943`. I built `cmd/nova-work` from this staged source with Go 1.26.6 for Linux/amd64 probes and darwin/arm64 read-only GitHub use. All local tree files were in scratch.

1. Command: `./nova-work verify --tree a.lisp --against b.lisp`

   Printed:

   ```text
   VERIFY FAILED tree=a.lisp sha256=8e8ea14a999d45fbd8060485fbe22217eb934595e8fbd45f79a443ba09fba8ab against=b.lisp against_sha256=f89b279a981e8c7a5f41c0dcbe811ba283f5fbce0e527e8aacd8546c50d7f7b6 repos=1 issues=0 comments=0 seconds=0.0 differences=1 missing=0 extra=0 drift=1
   VERIFY DRIFT path=repos/acme/widgets field=archived want="true" got="false"
   ```

   Expected: the docs/CLI.md "Try it with no login" and "Output" sections call this status `VERIFY FAIL`; the actual output is `VERIFY FAILED`. The binary and its help agree with each other, so the CLI reference's promised status is stale. Grade: NEXT.

READ 8/10 — The help gives the verbs, flags, effects, exits, output shapes, and an offline tree example; the CLI example's status typo weakens the reference.

USE 9/10 — Scratch-file equality, drift, JSON, and size-refusal paths worked as described, and a bounded public-repository dry run, import, and verify completed successfully.

Coverage: help, help import/verify/version, version, import output and dry-run refusals, offline verify equal and drift, JSON output, missing-tree and max-bytes refusals, and top-level unknown-verb refusal. On the staged-source darwin/arm64 binary, a bounded dry-run and import against one public repository each returned IMPORT OK (1 repo, 1 issue, calls=2); verify against that scratch tree returned VERIFY OK (differences=0). These GitHub operations were read-only; the import wrote only under job scratch. The separate Linux bench's unauthenticated import refusal was recorded in the job transcript; no login was attempted. Hands-on use ran about 18 minutes, from 20:31 UTC to 20:49 UTC.

urgent=0 next=1
