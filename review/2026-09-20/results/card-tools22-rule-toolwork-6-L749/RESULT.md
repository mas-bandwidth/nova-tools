RESULT tools22-rule-toolwork-6-L749 sha=5298f6be12ea — does the code at this base do what docs/SPEC-TOOLWORK.md rule 6 says?
CONFORMS internal/swarm/result.go:85
SPEC docs/SPEC-TOOLWORK.md:749 rule 6
PKG internal/swarm

ASK: A result parser must not extract kind, test, or path values from a RESULT.md; templates must be text-only constants that do not execute; gate selection must come from the original card header's KIND: field alone and nothing the worker wrote.

Deciding lines:

internal/swarm/templates.go:19:
  // A TEMPLATE IS TEXT AND NOTHING ELSE. It is not code, it does not execute, and nothing in
  // this package reads a worker's RESULT.md and acts on it.

internal/swarm/templates.go:22..419 (all template consts):
  Every template is a `const` string literal -- templateReadPR, templateProbeRow, templateFixCard,
  templateResult, templateWorker, templateSetup, templateCapacity, pulseRead, pulseFix,
  pulseText, pulseReplay, pulseDrift, pulseTone, pulseModels -- never executed as code.

internal/swarm/result.go:85-103:
  type Report struct {
  	Class         string
  	MalformedLine int
  	Heading       string
  	HasHead       bool
  	Findings      int
  	NotesRead     int
  	HasNotesRead  bool
  	Repo          string
  	Rev           string
  	Paragraph     string
  	Items         []Item
  	Gates         []Gate
  	LeftOwed      []string
  	OneLine       string
  	FindingLines  []Finding
  	Hash          string
  	Bytes         int
  }
  The struct has no Kind, Test, or Paths fields. ParseReport only parses ## Head (findings, notes
  read, repo, rev, paragraph), ## Per item, ## Gates, ## Left owed, ## One line, and ## Findings.

internal/swarm/contract.go:50-77:
  func CheckResult(resultPath string, c Contract) (Outcome, error) { ... }
  Line 1 of RESULT.md must match the card's contract line exactly, byte for byte. A mismatch is
  refused with "RESULT REFUSED <label> line 1 is <found>". This prevents a RESULT.md from masquerading
  as belonging to a different card/kind.

internal/hygiene/data.go:132-139:
  func KindDeclared(name string) bool { _, set := loadKinds(); return set[name] }
  Reads kinds from embedded kinds.txt data file; there is no default kind, unknown kinds are refused.

GUARDED-BY internal/swarm/contract_test.go:26 TestResultRefusesWrongLine1 -- verifies that a RESULT.md
whose line 1 differs from the card's contract is refused. However, the structural guard in result.go:85
(the absence of Kind/Test/Paths from the Report struct and corresponding absent parsing) is UNGUARDED:
no test asserts that these fields cannot exist, because their absence is guaranteed by the parser being
a struct + switch over section names rather than an explicit block-list.

Grep commands run:
  find . -name 'kinds.go'            # no output
  grep -rn "KIND" --include='*.go' . | head 60
  grep -rn "transcript-test" --include='*.go' . | head 30
  grep -rn "widen\|WIDEN\|template.*kind\|kind.*template" --include='*.go' . | head 20
  grep -rn "\.Kind\b" internal/swarm/*.go | head 30
  grep -rn "ParseReport\|\.Kind.*RESULT\|result.*KIND" --include='*.go' . | head 30
  ls internal/docs/                  # only *_test.go files, no .go source
  find . -name 'cardheader*' -o -name '*kinds*'  # only hygiene/kinds.txt
  grep -n "type Report\|\.Kind\b\|Kind\s" internal/swarm/result.go
  grep -rn "func Test.*Result\|func Test.*result\|func Test.*parse" --include='*_test.go' internal/swarm/ | head 15
  grep -rn "KIND.*RESULT\|widens.*RESULT\|RESULT.*kind" internal/swarm/*.go
  sed -n '729,775p' docs/SPEC-TOOLWORK.md
  git rev-parse HEAD  # 5298f6be12eaa0f7e6622334d2b6a1eb427649e3 ✓

Left owed: No specific test that asserts the Report struct cannot acquire Kind/Test/Paths fields
if someone adds them in the future. The guard is structural (parser design) rather than tested.
A regression test like "a RESULT.md containing KIND: transcript-test inside it is parsed without
extracting a kind" would close this gap.

git status --short
(no output)
