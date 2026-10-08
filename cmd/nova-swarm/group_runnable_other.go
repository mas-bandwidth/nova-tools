//go:build !linux

package main

import "github.com/mas-bandwidth/nova-tools/internal/swarm"

func groupRunnable(pgid int) bool { return swarm.GroupAlive(pgid, "") }
