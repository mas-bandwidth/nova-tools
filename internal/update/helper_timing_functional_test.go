//go:build functional || slow

package update

import (
	"os"
	"strconv"
	"time"
)

func init() {
	fakeBusHang = func() { time.Sleep(30 * time.Second) }
	helperTiming = func(a []string) bool {
		switch a[0] {
		case "hang":
			time.Sleep(30 * time.Second)
		case "hold":
			if len(a) > 2 && a[2] != "" {
				_ = os.WriteFile(a[2], []byte(strconv.Itoa(os.Getpid())), 0600)
			}
			d, err := time.ParseDuration(a[1])
			if err != nil {
				os.Exit(6)
			}
			time.Sleep(d)
		case "escaped":
			// Leave a grandchild holding stdout open in its OWN process group, then
			// hang, so the grandchild survives this process's group kill: the
			// escaped-pipe case a deadline must still close.
			readyFile := ""
			if len(a) > 2 {
				readyFile = a[2]
			}
			if err := spawnEscapedHolder(a[1], readyFile); err != nil {
				os.Exit(5)
			}
			time.Sleep(30 * time.Second)
		default:
			return false
		}
		return true
	}
}
