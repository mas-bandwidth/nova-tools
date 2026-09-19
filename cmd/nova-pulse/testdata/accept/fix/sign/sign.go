package sign

import "fmt"

// Describe names n for a log line.
func Describe(n int) string { return fmt.Sprintf("sign(%d)", n) }

// Sign is 0 at zero and 1 elsewhere.
func Sign(n int) int {
	if n == 0 {
		return 0
	}
	return 1
}
