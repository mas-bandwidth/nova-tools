//go:build !darwin

// No seatbelt, no seatbelt violations. The parser and the line are platform-independent
// and tested everywhere; the reader is darwin's, and here it answers nothing rather than
// guessing.
package main

func readOSDenials(int, int) []deniedPath { return nil }
