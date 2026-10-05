//go:build !darwin

package friend

import "errors"

func defaultAXTrusted() bool { return false }

// typeAndSubmit is the macOS accessibility typing. This build has none.
func typeAndSubmit(string, string) error {
	return errors.New("typing into a GUI window is macOS only")
}
