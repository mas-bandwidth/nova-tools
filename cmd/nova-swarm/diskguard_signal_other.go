//go:build !unix

package main

import "errors"

func signalPid(int) error { return errors.New("no signal on this operating system") }

func pidAlive(int) bool { return false }
