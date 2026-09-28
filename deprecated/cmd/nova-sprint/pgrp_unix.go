//go:build unix

package main

import "syscall"

// currentPgrp is this process's group, the default --pgid a capacity debit carries.
func currentPgrp() int { return syscall.Getpgrp() }
