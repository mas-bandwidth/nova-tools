package typedrec

import "slices"

// Defects named in the specification.
const (
	DefectMissing   = "missing"
	DefectMalformed = "malformed"
)

func isValidKind(k string) bool {
	return slices.Contains(Kinds, k)
}
