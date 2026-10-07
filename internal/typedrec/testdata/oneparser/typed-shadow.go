package oneparser

import "github.com/mas-bandwidth/nova-tools/internal/typedrec"

// A value the typed parser returned is typed text: a raw compare on it
// outside typedrec is a parse (cold read of #4429).
func parseTyped(raw []byte) bool {
	msg := typedrec.SplitModel(raw, "").Line1
	return msg == "DONE"
}
