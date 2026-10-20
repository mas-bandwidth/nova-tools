//go:build !windows

package main

import "github.com/mas-bandwidth/nova-tools/pkg/swarm"

// benchMSVCEnv is Windows' alone (msvc_windows.go): elsewhere there is no Visual Studio
// developer environment to add, and nothing is run.
func benchMSVCEnv(string) (swarm.MSVCEnv, string) { return swarm.MSVCEnv{}, "" }
