package sandbox

// VisibleRoots is the recursive data reach of the completed policy, including
// backend system and executable roots. Metadata-only ancestor/PATH grants do
// not make private cleanup channels readable and are deliberately excluded.
func (p *Policy) VisibleRoots() []string {
	roots := append([]string{}, systemVisibleRoots()...)
	for _, list := range [][]string{p.Reads, p.ReadsNoExec, p.Writes, p.OptRoots} {
		roots = append(roots, list...)
	}
	return append(roots, p.Tmp, p.Home)
}
