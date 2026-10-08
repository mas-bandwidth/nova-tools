//go:build !linux && !windows

package procgroup

import "github.com/mas-bandwidth/nova-tools/internal/swarm"

func GroupRunnable(pgid int) bool { return swarm.GroupAlive(pgid, "") }
func processZombie(int) bool      { return false }
