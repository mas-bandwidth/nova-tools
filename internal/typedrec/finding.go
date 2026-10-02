package typedrec

import "regexp"

// FindingPattern is what a line of a broken read's finding must hold for the finding to
// name its defect (docs/SPEC-CARD-CONTRACT.md section 3): a file (a name with an
// extension, so a path, or any name at file:line), a line (`line <n>`), or the rule the work breaks
// (the card's `STEP <n>`, or its RULES). It is one POSIX extended expression, read the
// same by Go's regexp and by `grep -E` in the card's gh shim, so the shim refuses at the
// review what the member would hand back after it.
const FindingPattern = `[A-Za-z0-9_-][A-Za-z0-9_-]+[.][A-Za-z][A-Za-z0-9]*|[A-Za-z_][A-Za-z0-9_./-]*:[0-9]+|[Ll]ine [0-9]+|[Ss][Tt][Ee][Pp] [0-9]+|RULES?|[Rr]ules?:`

var findingRE = regexp.MustCompile(FindingPattern)

// NamesADefect says a broken read's finding names what is wrong: some line of it names a
// file, a line or the rule the work breaks. "Request changes." alone, or an approval's
// words under a broken verdict, names none.
func NamesADefect(finding string) bool { return findingRE.MatchString(finding) }
