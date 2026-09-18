package pulse

// capacity is the allowed-cards formula every card launcher on the Studio has read over
// ssh -- fill-loop.sh's cap() and flash-native-bench.sh's capacity check -- folded into one
// local verb. allowed = min(cores*3/2 - load1 - cores/8, (freeGB-25)/2, memFreeGB/2), floored
// at zero and never negative. The three arms bound the cards one bench may take by CPU
// headroom with a CI reserve of an eighth of its cores, by free disk above a 25G floor, and
// by free memory. It makes no model call and reads nothing itself: the caller supplies the
// four numbers read from nproc, /proc/loadavg, df and /proc/meminfo.

// AllowedCards returns the allowed cards for one bench from its cores, one-minute load, free
// disk in whole GB and free memory in whole GB. a1 keeps cap()'s integer arithmetic
// (cores*3/2 and cores/8 truncate), so one fleet's number matches the shell it replaces;
// the result floors at 0, never negative.
func AllowedCards(cores, load1, freeGB, memFreeGB int) int {
	a1 := cores*3/2 - load1 - cores/8
	a2 := (freeGB - 25) / 2
	a3 := memFreeGB / 2
	a := a1
	if a2 < a {
		a = a2
	}
	if a3 < a {
		a = a3
	}
	if a < 0 {
		return 0
	}
	return a
}
