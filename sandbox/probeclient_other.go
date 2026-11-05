//go:build !linux

package main

import "errors"

// rawClient: the TCP Fast Open and listen acts are Linux probe steps only.
func rawClient(string, string) error { return errors.New("linux only") }
