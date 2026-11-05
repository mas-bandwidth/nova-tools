//go:build functional

package main

import (
	"os"
	"strconv"
	"strings"
)

// alive says pid is a running process: a zombie, waiting for its new parent
// to reap it, is not.
func alive(pid int) bool {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return false
	}
	f := strings.Fields(string(b[strings.LastIndexByte(string(b), ')')+1:]))
	return len(f) > 0 && f[0] != "Z"
}
