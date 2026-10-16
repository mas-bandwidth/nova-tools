//go:build !linux

package main

import "errors"

// kvmOpen has no KVM to reach off linux: the "kvm" fact is a linux kernel's alone.
func kvmOpen(string) (int, error) { return 0, errors.New("no KVM off linux") }
