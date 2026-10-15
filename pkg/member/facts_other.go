//go:build !linux && !darwin

package member

import "runtime"

func realGOOS() string   { return runtime.GOOS }
func realGOARCH() string { return runtime.GOARCH }

func realCPUInfo() string { return "" }

func realHWCap() (uint, uint) { return 0, 0 }

func realSysctl(string) (int64, bool) { return 0, false }

func realStat(string) bool { return false }

func realProvisioned() bool { return false }

func XDPNote() string { return "" }
