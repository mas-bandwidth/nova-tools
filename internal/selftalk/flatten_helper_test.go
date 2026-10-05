package selftalk

// flattenWithLines strips markdown and collapses all whitespace to single
// spaces, so a claim spanning a hard wrap is one sentence, while retaining the
// original line for each output byte. Repeated sentences and hard wraps keep
// their own locations; searching the original text for a flattened match cannot.
//
// A HEADING AND A BLANK LINE END A SENTENCE. A hard wrap joins two lines of one
// sentence, but the line break on either side of a heading or a blank line is a
// boundary the writer drew: joining across it glued "# Journal" onto the claim
// under it and reported the claim on the heading's line. A '.' is inserted at
// that break, so the claim pattern, which stops at a terminator, starts after it.
//
// The line of a byte is answered from the per-line start offsets. The returned
// slice is that answer expanded to one entry per output byte, which
// TestLocationFlatteningKeepsTheExistingText pins by length. Scan uses
// flattenLineStarts and does not build this slice.
func flattenWithLines(text string) (string, []int) {
	flat, starts := flattenLineStarts(text)
	if len(flat) == 0 {
		return "", nil
	}
	lines := make([]int, len(flat))
	for i := range lines {
		lines[i] = lineAt(1, starts, i)
	}
	return flat, lines
}
