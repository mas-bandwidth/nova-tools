//go:build !unix

package seatcred

import "os"

func seatEnvironmentOwner(os.FileInfo) bool { return true }
