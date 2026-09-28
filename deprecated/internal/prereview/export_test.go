package prereview

// Pathish exposes the unexported pathish for tests in package prereview_test.
func Pathish(tok string) bool {
	return pathish(tok)
}

// ClaimsCheck exposes the unexported claimsCheck for tests in package prereview_test.
func ClaimsCheck(pr PR, card Card) Check {
	return claimsCheck(pr, card)
}

// FirstTell exposes the unexported firstTell: the tell's sentence, and whether
// one fired.
func FirstTell(added string) (string, bool) {
	t, ok := firstTell(added)
	return t.says, ok
}
