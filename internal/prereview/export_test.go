package prereview

// Pathish exposes the unexported pathish for tests in package prereview_test.
func Pathish(tok string) bool {
	return pathish(tok)
}

// ClaimsCheck exposes the unexported claimsCheck for tests in package prereview_test.
func ClaimsCheck(pr PR, card Card) Check {
	return claimsCheck(pr, card)
}
