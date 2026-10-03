# nova-redis READ rating, nova-tools 1.1.0

Rater: Grok
Build: eb80e19c25ae
Score: 7.5/10
README: 6.5/10

## Reasons

README.md:27 says the tool runs a local store and keeps short-lived named values, and prints a live spill as the first command. That sentence is the same one the banner opens with, which is the right shape. The trial is not.

Before any code, three stops. Confusion is README.md:27: the same cell says the tool runs the store, then says to use a separate running instance, and says serve also needs the server binary. The printed command does neither; it writes a value. Boredom is AGENTS.md:13: the map embeds the whole build standard, and none of it is this tool, so the first long read teaches the house and not the verbs. The doubt is README.md:48, which calls the table the 1.0.0 commands and points the install at that tag, while the spill row is offered as the command to run now. The page the README points to first, docs/USAGE.md:84, never names this tool at all.

The banner's first lines do the job the standard asks. cmd/nova-redis/main.go:70 states what it does, how serve, spill, recall and the function verbs work, where the password comes from, and that the dry-run needs no store. Verbs are listed. Refusals name every problem on the line and end in the verb's own help. A missing owner or a TTL that is not above zero never reaches a dial. A spill whose reply is lost is unconfirmed, exit 1, and the next command is a recall, not a second write. acl render needs no store. Those are the properties that make a tool usable cold, and the scratch rules are mapped to named tests in docs/SPEC-REDIS.md:112. redisconn is one dial, bounded, and it does not print a password. The function-library comment explains why each file is its own block. Names like spill, recall and serve are plain.

It is not a 9, and not an 8. The normative spec's verb list at docs/SPEC-REDIS.md:18 stops at help and never mentions the acl verbs the banner and docs/CLI.md both document, so an AI that obeys "read the spec first" learns the wrong tool. Help is a second copy of that contract: the example sits at cmd/nova-redis/main.go:151, after the rules that start at cmd/nova-redis/main.go:70. Success is three grammars, not one: SPILL OK, RECALL MISSING, and a leading OK or LOADED with the library name, plus ACL APPLY REFUSED at exit 1. docs/CLI.md:2026 has no First run heading; the only transcript, docs/TESTS.md:769, is three refusals. main.go holds dispatch, login, spill, recall and shell quoting, and only the JSON value comes from the shared output type. The entry comment omits the acl verbs that dial. A package comment still tells a past incident by ticket number.

A 10 is the same verbs in the spec, the banner and the code; help cut to the five-line how-it-works, the usage, the exit table and the example; every line the verb, then OK, REFUSED or FAILED; the README trial equal to the dry-run; and a choosing section that says serve needs the server binary and a password variable. The scratch contract can stay as it is.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | docs/SPEC-REDIS.md:18 | The normative verb list names serve, spill, recall, fn load, fn check, version and help. acl render, acl check and acl apply are absent, and the test list never cites the acl tests. An AI that reads the spec first learns a tool the binary is not. | Add the three acl lines, their exits and the tests that pin them, beside the other verbs. | M |
| 2 | cmd/nova-redis/fn.go:142 | Success is not one grammar. fn check leads with OK and the library name. fn load leads with LOADED, UNCHANGED or REPLACED (fn.go:130). spill leads with SPILL OK (main.go:333) and recall with RECALL MISSING (main.go:392). acl apply prints ACL APPLY REFUSED and exits 1 (acl.go:304), not the invocation refusal that exits 2. | Lead every line with the verb, then OK, REFUSED or FAILED. Use exit 2 when the verb refuses before it writes. | L |
| 3 | cmd/nova-redis/main.go:70 | Help is a second contract essay. how-it-works is five lines, then usage, then the login, scratch, function, acl and serve rules, and the example is at main.go:151. docs/CLI.md:2040 repeats the essay. The first minute goes to hunting the example. | Keep what, how, usage, the exit table and the example in help. Move the rest to the spec and link it. | L |
| 4 | README.md:27 | The trial command is a live spill against a store the caller must already run. The banner's first run is version, then spill --dry-run, which dials nothing. docs/USAGE.md:84, the page the README points to first, has no section for this tool. | Put the dry-run in the table. Add a short choosing note that serve needs the server binary on PATH and a password variable. | S |
| 5 | docs/CLI.md:2026 | The section has no First run heading. Neighbor tools open with one. The only transcript is three refusals in docs/TESTS.md:769, so a success line is not where the standard says to show it. | Open with First run: version, spill --dry-run and acl render, and the lines those print. | M |
| 6 | cmd/nova-redis/acl.go:306 | When several new users lack a password variable, missing= lists every one and the remedy command names only the first. The next paste still refuses. | Repeat --password-env-for once per missing user in that single remedy. | S |
| 7 | cmd/nova-redis/main.go:17 | The entry comment says the dialing verbs are spill, recall, fn load and fn check. acl check and acl apply dial too. The same file is dispatch, login checks, spill, recall and shell quoting (690 lines). report.go:19 only borrows the JSON value. | Name every dialing verb. Move spill and recall out so main is dispatch. Take flags and refusals from the shared runner. | L |
| 8 | internal/redisfn/redisfn.go:72 | The package doc tells a past incident, issue 3620, inside the LoadMissing rule. docs/CLI.md:2086 repeats the ticket. The rule is clear without the story, and the story is not present tense. | State only the rule: LoadMissing loads when the name is absent and never replaces. | S |

## Good, keep

A spill with no owner, or a TTL that is missing, zero or negative, is refused before any dial, and one run names every problem on the line. A lost spill reply is SPILL UNCONFIRMED at exit 1, and the remedy is a recall with the same login, never a second spill (cmd/nova-redis/main.go:571). docs/SPEC-REDIS.md:112 maps each scratch and serve rule to the test that holds it, which is how an AI checks a claim instead of trusting the prose.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| a hand-rolled skeleton | STILL THERE | cmd/nova-redis/main.go:204 still dispatches, parses and refuses on its own. report.go:19 only borrows the JSON value type. |
| a 50-line prose wall in help | STILL THERE | cmd/nova-redis/main.go:70 opens the banner and the example is cmd/nova-redis/main.go:151. |
| ticket numbers in the package doc | STILL THERE | internal/redisfn/redisfn.go:72 still names issue 3620, and docs/CLI.md:2086 repeats it. |
| three line grammars | STILL THERE | cmd/nova-redis/fn.go:142 leads with OK, cmd/nova-redis/main.go:392 leads with RECALL MISSING, and cmd/nova-redis/acl.go:304 is ACL APPLY REFUSED at exit 1. |
| onboarding and output grammar still have rough edges | STILL THERE | docs/CLI.md:2026 still has no First run heading, and docs/TESTS.md:769 shows refusals only. |
| README scored 6.5 to 7 and 8.4 | STILL THERE | README.md:27 is still a live spill, so this read scores the README 6.5. |
