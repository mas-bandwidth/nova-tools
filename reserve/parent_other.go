//go:build !linux && !darwin

package main

// parentCommand has no reader here (the BSDs, Windows): a hold with no --why
// is unlabelled.
func parentCommand() string { return "" }
