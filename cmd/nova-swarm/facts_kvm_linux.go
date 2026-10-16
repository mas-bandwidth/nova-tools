//go:build linux

package main

import "syscall"

// kvmGetAPIVersion is KVM_GET_API_VERSION, _IO(KVMIO, 0x00) with KVMIO 0xAE (linux/kvm.h).
const kvmGetAPIVersion = 0xAE00

// kvmOpen opens path read-write, as the member's user, and asks it KVM_GET_API_VERSION, whose
// answer is the ioctl's return value.
func kvmOpen(path string) (int, error) {
	fd, err := syscall.Open(path, syscall.O_RDWR|syscall.O_CLOEXEC, 0)
	if err != nil {
		return 0, err
	}
	defer syscall.Close(fd) // ignored: a read-only probe; the close of a descriptor only asked for its API version loses nothing
	v, _, errno := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), kvmGetAPIVersion, 0)
	if errno != 0 {
		return 0, errno
	}
	return int(v), nil
}
