package pr_test

import (
	"time"
)

// #3156's policy, passed as arguments (the predicates hold no thresholds).
const (
	staleAfter = 3600 * time.Second
	behindN    = 3
)

var gatedTypes = []string{"code"}

func at(sec int64) time.Time { return time.Unix(sec, 0) }
