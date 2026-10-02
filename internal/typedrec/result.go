package typedrec

// Defects named in the specification.
const (
	DefectMissing   = "missing"
	DefectMalformed = "malformed"
)

func isValidKind(k string) bool {
	for _, valid := range Kinds {
		if valid == k {
			return true
		}
	}
	return false
}
