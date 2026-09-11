package board

import "time"

type Card struct {
	ID, Text, Hash, Owner, Filer  string
	By, Default, Thing, Leg, Evid string
	Since                         time.Time
	State                         string
	Close                         *Event
	LatestAt, TakenAt             time.Time
	HasTake                       bool
	Conflicts, Quarantined        int
	Conflict, Stale, Overdue      bool
	Row, Probed                   bool
}

type Log struct {
	Lines         []string
	UnparsedFiles int
}

type Board struct {
	Cards                   []*Card
	Unparsed, UnparsedFiles int
	Quarantined, Conflicts  int
	FirstQuarantined        string
}

func Derive(log Log, now time.Time, stale time.Duration) *Board { return &Board{} }
