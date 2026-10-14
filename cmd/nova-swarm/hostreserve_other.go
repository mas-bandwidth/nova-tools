//go:build !darwin

package main

// setBackgroundQoS is nothing where the platform has no Darwin QoS: the policy is
// the zero value there, and a server on Linux would need cgroups.
func setBackgroundQoS(pid int) error { return nil }
