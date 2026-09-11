package board

import (
	"io"
	"time"
)

const Version = "BOARD v1"

type Event struct {
	Verb, ID, Ev, After, As  string
	At                       time.Time
	AtRaw                    string
	AtOK                     bool
	Override                 bool
	Hash, Owner, By, Default string
	Thing, Leg, Evidence, In string
	Tail                     string
	Line                     string
}

func (e Event) Render() string { return "" }

func Parse(line string) (Event, bool) { return Event{}, false }

func NewID(r io.Reader) (string, error) { return "", nil }

func NewEv(r io.Reader) (string, error) { return "", nil }

func HashOf(text string) string { return "" }

func Tail(s string) string { return "" }

func Stamp(t time.Time) string { return "" }
