RESULT tools22-pre-2147-r1 sha=5298f6be12ea — a pre-read of mas-bandwidth/nova-tools#2147 at head 3cac2c1daa7d: hygiene: pin KindDeclared and drive-letter PATHS so their reverts go red
PREREAD 2147 claims=4 proven=4 unproven=0 defects=0 high=0
PR 2147, HEAD 3cac2c1daa7d8094ecf9df18b3e25c9909ab932e, BASE dev, MERGE-BASE a7611c8189d33979923064f4d10eb07fb9957730, BEHIND 1, FILES 0 production, 1 test, LINES +66 -0

1. ValidatePaths refuses a Windows drive-letter path and a leading backslash.
   PROVEN-BY internal/hygiene/hygiene_test.go:624 TestValidatePathsRefusesAWindowsDriveLetterAndALeadingBackslash — asserts error != nil and error contains "absolute" for `C:/…`, `C:\…`, `d:/…` and `\…`.

2. KindDeclared returns true for every kind named in kinds.txt and false for an undeclared kind.
   PROVEN-BY internal/hygiene/hygiene_test.go:646 TestKindDeclaredHoldsTheEmbeddedNameSet — iterates parsed kinds.txt entries, asserts true for each and false for "not-a-declared-kind" at line 674.

3. Kinds() returns the kinds.txt name set in order.
   PROVEN-BY internal/hygiene/hygiene_test.go:646 TestKindDeclaredHoldsTheEmbeddedNameSet — asserts strings.Join(Kinds(),",") equals strings.Join(want,",") at lines 665-667.

4. Every kind named in stray.txt's exception column is a declared kind.
   PROVEN-BY internal/hygiene/hygiene_test.go:646 TestKindDeclaredHoldsTheEmbeddedNameSet — iterates StrayKinds() and asserts each is `KindDeclared` at lines 677-681.

DEFECTS none

1. Were these tests run on a Windows target, or only on linux/darwin? A CI runner's separator does not affect the lexical rule, but a Windows runner would be the stronger proof.

2. The KindDeclared test reads kinds.txt with `os.ReadFile("kinds.txt")` — was embedding the same file in a _test.go (`//go:embed kinds.txt var testKinds string`) considered instead of the runtime CWD dependence? It would match the production code's own pattern and remove the failure mode of a test runner that changes directory.

3. The test parses kinds.txt by splitting on `\t` and trimming space, mirroring the production parse in loadKinds. Did you consider sharing the parser — e.g., exporting `parseKinds` — to guarantee the two cannot drift apart?

Left owed: nothing. The entire diff is 66 lines in one test file; I read it and the production functions it exercises in full.

a7611c8189d33979923064f4d10eb07fb9957730===FILE=== card-tools22-pre-2147-r1/usage.tsv
job	attempt	started	ended	rc	provider	model	tokens_in	tokens_out	cache_write	cache_read	reasoning	usd
card-tools22-pre-2147-r1	1	2026-09-20T19:28:13Z	2026-09-20T19:39:59Z	0	openrouter	deepseek/deepseek-v4-flash	60158	6126	0	830464	7782	0.0090
