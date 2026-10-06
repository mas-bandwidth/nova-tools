package card

// Tokens is a text's token count by the measure a brief is weighed in: four bytes to a
// token, rounded up, the measure every harness's own count is close to for this prose
// (docs/SPEC-CARD-CONTRACT.md section 7). The cost record carries a brief's (sprint
// Consumer.BriefTokens), and the daemon's record of each card it hands says it.
func Tokens(text string) int { return (len(text) + 3) / 4 }
