# nova-redis READ rating, nova-tools 1.1.0

Rater: a large language model, cold to this tool
Build: 99e4a903966c
Score: 8.5/10
README: 8/10

## Reasons

The README line at README.md:24 says the tool is "run a local Redis store, and keep short-lived named values in it" — three lines later the table row gives the exact first command, and the code does that. The first place I was confused: docs/SPEC-REDIS.md:18 lists the verbs as serve, spill, recall, fn load, fn check, version and help, but the tool also ships acl render, acl check and acl apply; the normative spec never mentions them, so the spec and the CLI disagree about the surface. The first place I was bored: docs/SPEC-REDIS.md:44, the bind/auth/persistence section, restates rules the code comment at cmd/nova-redis/serve.go:1 already states almost verbatim. The first claim I doubted: docs/CLI.md:2045 describes the JSON envelope as `{"result":{"verb","status",...}}` with bare field names, which is not a JSON shape; a cold reader cannot tell what a real `--json` answer looks like from the prose alone.

The writing is otherwise strong. The package comment at cmd/nova-redis/main.go:1 says what each verb owns and where it lives (serve.go, fn.go, acl.go), the banner at cmd/nova-redis/main.go:118 is four lines and the exit table at main.go:126 is one sentence per exit. The code is one file per idea: acl.go for ACL verbs, serve.go for the instance, fn.go for the library verbs, main.go for dispatch and scratch verbs. The names a stranger understands on first read: spill, recall, owner, TTL, login, function library. The comments are present-tense and say why: cmd/nova-redis/main.go:12 explains why the password is never an argument, cmd/nova-redis/acl.go:329 explains why a missing user is created only with a password. The tests are extensive and pin the spec's demanded behaviours one by one (serve_test.go, spill_test.go, fn_test.go, status_grammar_test.go), and pkg/redisconn is the single dial path the package comment promises.

What keeps it from a 10: the normative spec omits the ACL verbs, the JSON envelope in the prose is not a real example, and the acl apply dry-run declaration at cmd/nova-redis/acl.go:205 is not honoured before the store is opened at acl.go:339, so the flag help's "write nothing" promise is weaker than it reads.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | docs/SPEC-REDIS.md:18 | the normative spec's verb list omits acl render, acl check and acl apply; a cold reader cannot learn the ACL surface from the spec | add an ACL section to SPEC-REDIS.md or point to docs/CLI.md as the ACL norm | S |
| 2 | docs/CLI.md:2045 | the JSON envelope is shown as `{"result":{"verb","status",...}}` with bare field names, not a shape a reader can parse | show one real --json object with values, or point to a checked example | S |
| 3 | cmd/nova-redis/acl.go:205 | acl apply declares DryRun, but the branch that honours it sits at acl.go:339 after the store is opened and compared, so the flag help's "write nothing" reads stronger than the code | move the dry-run branch above the dial and print the would-set plan | S |

## Good, keep

The one-file-per-idea layout and the present-tense comments that name the rule each verb enforces. The single connection path through pkg/redisconn. The spec-demanded tests that make the contract executable.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| a hand-rolled skeleton | FIXED | cmd/nova-redis/main.go:103 calls pkg/tool's Main |
| a 50-line prose wall in help | FIXED | cmd/nova-redis/main.go:120 is four lines |
| ticket numbers in the package doc | FIXED | pkg/redisacl/redisacl.go:1 package doc clean; ticket numbers remain only in test-file comments |
| three line grammars | CHANGED | cmd/nova-redis/coldread_test.go:40 pins one refusal grammar |
