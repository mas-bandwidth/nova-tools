// Package ci holds the checks that are about the REPO rather than about any one
// binary: properties every command under cmd/ must have, asserted by walking the
// directory rather than by listing the commands, so that a binary added tomorrow
// is held to them on the day it appears rather than on the day somebody
// remembers to add it to a list.
//
// It has no exported API. See onboarding_test.go, and docs/ONBOARDING.md for the
// standard it enforces.
package ci
