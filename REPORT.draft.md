Verdict: LAND
Head: bfbd23d5cd48ac58324f023df03f7d43c1c487df

## Gate Lines

1. nova-sprint -h: URGENT - help is long and hard to scan; no quickstart verb
2. nova-sprint help add: NEXT - very verbose; harder for a stranger to find what they need
3. nova-sprint help land: NEXT - too verbose
4. nova-sprint help friend: NEXT - verbose; lacks a quickstart for friends
5. nova-sprint where --json: NEXT - large JSON; useful but could be better organized
6. nova-sprint help rules: NEXT - short but lacks context
7. nova-sprint help set: NEXT - too many flags; hard to parse
8. nova-sprint help goal: NEXT - basic but minimal examples
9. nova-sprint help clear: NEXT - help is brief, but could warn more strongly
10. nova-sprint where: NEXT - clear but no guidance on how to initialize
11. nova-sprint add --stream s1 --count 1 --one: URGENT - refusal does not explain what the actor flag is used for
12. nova-sprint inbox: NEXT - minimal output; helpful but terse

READ 6/10: Readability is uneven; the help pages are extremely verbose, making it hard for a stranger to find what they need. Some verbs have clear help (like clear), but others like add and land are 60+ lines.

USE 5/10: Usability is hampered by the lack of a quickstart workflow. You need to manually initialize a sprint with init, then you can explore other verbs. The mem: store works well for testing.

urgent=2 next=10
