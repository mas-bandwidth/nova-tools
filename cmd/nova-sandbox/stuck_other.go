//go:build !darwin

// The other platforms: `sample` and the unified log are darwin's, so the verb
// says so and guesses nothing. These seams exist only to compile; the verb does
// not call them off darwin.
package main

import "time"

func psText() (string, error)      { return "", nil }
func sampleText(int) string        { return "" }
func logText(time.Duration) string { return "" }
func swVersText() string           { return "" }
