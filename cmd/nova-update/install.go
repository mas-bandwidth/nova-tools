package main

import (
	"io"

	"github.com/mas-bandwidth/nova-tools/internal/update"
)

// InstallUsage documents the one-step install command (#4321).
// `nova-update install` installs a built release directly to the target bin
// directory without requiring the `release` prefix, ensuring every friend's
// "install the release" is one command that works across all bench kinds
// (linux-amd64, darwin-arm64, and WSL2).
const InstallUsage = "nova-update install --from <dir> --version <v> --bin <dir> [--retire <dir>] [--platform <goos-goarch>] [--timeout <d>]"

// Install dispatches the one-step install command directly to the release
// installer without requiring the `release` prefix.
func Install(args []string, stamp string, out, errs io.Writer) int {
	return update.Main("nova-update", append([]string{"install"}, args...), stamp, out, errs)
}
