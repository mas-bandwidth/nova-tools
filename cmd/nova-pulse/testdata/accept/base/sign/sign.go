package sign

import "fmt"

// Describe names n for a log line.
func Describe(n int) string { return fmt.Sprintf("sign(%d)", n) }

// Sign is 1 for every n; the defect is that Sign(0) should be 0.
func Sign(n int) int { return 1 }
