RESULT tools22-rule-swarm-7-L3986 sha=5298f6be12ea
CONFORMS internal/swarm/result.go:138
SPEC docs/SPEC-SWARM.md:3986 rule 7
PKG internal/swarm
ASK An implementation must parse a RESULT.md into its sections (Head, Per item, Gates, Left owed, One line, Findings), accept exactly three item states, carry each finding line's `dup:` mark and verbatim backtick-quoted rule beside its normalized file:line, match findings against the owed list, classify the report ok/clean/plan-only/no-result/malformed from the head's `findings: <n>`/`repo:`/`rev:` lines, and quarantine any report that does not parse into a line number with zero findings.

Deciding lines:
- internal/swarm/result.go:41 `var itemStates = map[string]bool{"red": true, "green": true, "not done": true}` (the three states)
- internal/swarm/result.go:138 `func ParseReport(data []byte) Report {` (the parser, sections read at :222-261)
- internal/swarm/result.go:183-205 the head's `findings:`, `repo:`, `rev:` lines; ClassOK/ClassClean at :192-195
- internal/swarm/result.go:304-308 `func malformed(...)` sets ClassMalformed, MalformedLine, and nils FindingLines/Items/Gates/LeftOwed (the quarantine)
- internal/swarm/result.go:373-375 `cutPrefixFold(text, "dup:")` sets `f.Dup` (the dup mark)
- internal/swarm/result.go:405 `lastBacktickSpan` (verbatim quote)
- internal/swarm/result.go:448 `OwedMatch` (owed-list match)
- internal/swarm/result.go:32-37 the ClassOK/ClassClean/ClassPlanOnly/ClassNoResult/ClassMalformed constants

GUARDED-BY internal/swarm/swarm_test.go:55 TestTheParserClassifiesWithoutAnOpinion (asserts a fourth state word is malformed with its line and yields no finding; findings:0 with a head is clean; a plan with no head is plan-only). Also guarded by internal/swarm/swarm_test.go:118 TestTheHeadsFirstLineIsTheFindingCount, internal/swarm/swarm_test.go:180 TestAFindingCarriesItsQuoteOnTheSameLineOrTheNext, internal/swarm/swarm_test.go:220 TestAPathIsNormalizedBeforeTheCompare.

Greps run:
- `grep -rn "func Test" --include='*_test.go' internal/swarm/`
- `grep -n "Left owed\|One line\|Per item\|Gates\|findings:\|dup:\|plan-only\|no-result\|malformed" docs/SPEC-SWARM.md`

Left owed: none — the parser implements every noun rule 7 names and the demanded behaviour is pinned by unit tests.
