# nova-redis READ rating, nova-tools 1.1.0

Rater: gemini-2.5-pro
Build: 2c02b2aa2042
Score: 7.5/10
README: 7/10

## Reasons
The tool provides focused capabilities for operating a local store, managing short-lived scratch values with mandatory TTLs, deploying Lua function libraries, and synchronizing ACL permissions. Scratch isolation with owner prefixes and positive expiry times prevents unbounded key leaks, and store-free dry runs allow verifying calls safely.

First confused: docs/CLI.md:2026 lacks the standard ### First run heading required by ONBOARDING point 3, unlike most other tools in the reference document. Additionally, docs/SPEC-REDIS.md:18 omits the acl verbs from its verbs summary, leaving a reader who encountered them in CLI.md unsure whether they are supported parts of the tool contract.

First bored: cmd/nova-redis/main.go:92 presents a 52-line continuous prose block in the usage text detailing all subverbs, flags, error scenarios, and environment variable fallbacks in a wall of text before reaching exit codes and example commands.

First doubted a claim: cmd/nova-redis/main.go:72 states under how it works that serve runs the instance, spill writes a value, recall reads it back, and fn load and fn check install and verify functions, completely omitting the acl verbs despite them occupying substantial code in acl.go and being listed in the usage syntax.

A 10 would need:
1. Migrate tool dispatch and reporting to internal/tool, retiring the hand-rolled skeleton in main.go and report.go.
2. Split the 52-line prose wall in usage into dedicated verb help screens.
3. Add the missing ### First run section to docs/CLI.md:2026.
4. Update docs/SPEC-REDIS.md:18 to specify the acl verbs and their behavioral contracts.
5. Harmonize output line grammars so that all verbs lead with standard VERB STATUS tokens.
6. Point README.md:27 to docs/CLI.md#nova-redis rather than the specification document.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | cmd/nova-redis/main.go:204 | Hand-rolled tool skeleton and dispatch in main.go and report.go duplicates internal/tool functionality | Migrate tool dispatch, banner, and output reporting to internal/tool | L |
| 2 | cmd/nova-redis/main.go:92 | 52-line prose wall in usage string overloads general help with detailed flags and error semantics | Split detailed prose into individual verb help screens and keep main usage concise | M |
| 3 | docs/SPEC-REDIS.md:18 | Verbs summary and specification text completely omit the acl verbs | Document acl verbs and add tests to the spec checklist | M |
| 4 | docs/CLI.md:2026 | Tool reference section lacks the standard ### First run heading mandated by ONBOARDING point 3 | Add ### First run subsection with runnable command examples and transcript | S |
| 5 | cmd/nova-redis/fn.go:14 | Status lines for fn load and fn check do not follow standard VERB STATUS leading token format | Update line output to begin with FN LOAD or FN CHECK followed by status token | S |
| 6 | cmd/nova-redis/main.go:72 | Banner how it works summary explains serve, spill, recall, and fn but omits acl verbs entirely | Include the acl role and permission management capabilities in how it works | S |
| 7 | README.md:27 | Table links directly to docs/SPEC-REDIS.md instead of docs/CLI.md#nova-redis | Point link to CLI.md section matching other tools in the overview table | S |

## Good, keep
Clean bounded scratch storage with strict owner prefix and mandatory positive TTL preventing unbounded keys.
Store-free dry-run mode for spill and acl apply that validates inputs and prints planned actions without dialing.
Safe credential handling reading passwords from environment variables rather than command-line arguments.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| a hand-rolled skeleton | STILL THERE | cmd/nova-redis/main.go:204 |
| a 50-line prose wall in help | STILL THERE | cmd/nova-redis/main.go:92 |
| ticket numbers in the package doc | FIXED | cmd/nova-redis/main.go:1 |
| three line grammars | STILL THERE | cmd/nova-redis/fn.go:14 |
| onboarding and output grammar still have rough edges | STILL THERE | docs/CLI.md:2026 |
