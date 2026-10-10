package friend

import (
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bounded"
)

// PeersMaxRemedy is the remedy every MORE line names (internal/tool MaxRemedy).
const PeersMaxRemedy = "--max <n> raises the ceiling, --max 0 lists all"

// Peer carries the presence facts of one friend (docs/SPEC-FRIEND.md, Presence).
type Peer struct {
	Name    string   `json:"name"`
	State   string   `json:"state"`
	AgeS    *float64 `json:"age_s"`
	Seen    string   `json:"seen"`
	Harness string   `json:"harness"`
	Route   string   `json:"route"`
	Queue   int      `json:"queue"`
	Working int      `json:"working"`
	Width   int      `json:"width"`
	ProvedS *float64 `json:"proved_s"`
	Version string   `json:"version"`

	AgeText    string `json:"-"`
	ProvedText string `json:"-"`
}

// PeerReport is the outcome of evaluating peers across records and clock.
type PeerReport struct {
	Status string `json:"status"`
	Word   string `json:"word"`
	Peers  []Peer `json:"peers"`
	Up     int    `json:"up"`
	Asleep int    `json:"asleep"`
	Down   int    `json:"down"`
	Of     int    `json:"of"`

	MoreLine string `json:"-"`
}

// Line renders the single-line PEER fact.
func (p Peer) Line() string {
	return fmt.Sprintf("PEER name=%s state=%s age=%s seen=%s harness=%s route=%s queue=%d working=%d width=%d proved=%s version=%s",
		p.Name, p.State, p.AgeText, p.Seen, p.Harness, p.Route, p.Queue, p.Working, p.Width, p.ProvedText, p.Version)
}

// SummaryLine renders the PEERS OK summary line.
func (r PeerReport) SummaryLine() string {
	return fmt.Sprintf("PEERS OK up=%d asleep=%d down=%d of=%d", r.Up, r.Asleep, r.Down, r.Of)
}

// Lines renders the peer lines, optional MORE line, and summary line in order.
func (r PeerReport) Lines() []string {
	lines := make([]string, 0, len(r.Peers)+2)
	for _, p := range r.Peers {
		lines = append(lines, p.Line())
	}
	if r.MoreLine != "" {
		lines = append(lines, r.MoreLine)
	}
	lines = append(lines, r.SummaryLine())
	return lines
}

// EvaluatePeers is the pure function of presence records and a clock (docs/SPEC-FRIEND.md, Presence).
func EvaluatePeers(records map[string]map[string]string, extraNames []string, now time.Time, max int) PeerReport {
	nameSet := make(map[string]bool)
	for k := range records {
		name := strings.TrimPrefix(k, "bus2:presence:")
		if name != "" {
			nameSet[name] = true
		}
	}
	for _, n := range extraNames {
		n = strings.TrimSpace(n)
		if n != "" {
			nameSet[n] = true
		}
	}

	names := make([]string, 0, len(nameSet))
	for n := range nameSet {
		names = append(names, n)
	}
	slices.Sort(names)

	allPeers := make([]Peer, 0, len(names))
	var nUp, nAsleep, nDown int

	for _, name := range names {
		fields := records[PresenceKey(name)]
		if fields == nil {
			fields = records[name]
		}

		state := PresenceState(fields, now)
		switch state {
		case BusUp:
			nUp++
		case BusAsleep:
			nAsleep++
		default:
			nDown++
		}

		p := Peer{
			Name:       name,
			State:      state,
			Seen:       "-",
			Harness:    "-",
			Route:      "-",
			Version:    "-",
			AgeText:    "-",
			ProvedText: "-",
		}

		if len(fields) > 0 {
			if s := fields["seen"]; s != "" {
				if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
					p.Seen = s
					ageSec := now.Sub(t).Seconds()
					if ageSec < 0 {
						ageSec = 0
					}
					ageRound := math.Round(ageSec*10) / 10
					p.AgeS = &ageRound
					p.AgeText = fmt.Sprintf("%.1f", ageRound)
				}
			}
			if h := fields["harness"]; h != "" {
				p.Harness = h
			}
			if r := fields["route"]; r != "" {
				p.Route = r
			}
			if q, err := strconv.Atoi(fields["queue"]); err == nil {
				p.Queue = q
			}
			if w, err := strconv.Atoi(fields["working"]); err == nil {
				p.Working = w
			}
			if wd, err := strconv.Atoi(fields["width"]); err == nil {
				p.Width = wd
			}
			if pr := fields["proved"]; pr != "" {
				if t, err := time.Parse(time.RFC3339Nano, pr); err == nil {
					prSec := now.Sub(t).Seconds()
					if prSec < 0 {
						prSec = 0
					}
					prSecRound := math.Round(prSec)
					p.ProvedS = &prSecRound
					p.ProvedText = strconv.Itoa(int(prSecRound))
				}
			}
			if v := fields["version"]; v != "" {
				p.Version = v
			}
		}

		allPeers = append(allPeers, p)
	}

	total := len(allPeers)
	shown := total
	var moreLine string
	if max > 0 && total > max {
		shown = max
		moreLine = bounded.MoreLine("PEERS", "PEER", shown, total, PeersMaxRemedy)
	}

	shownPeers := allPeers[:shown]
	if shownPeers == nil {
		shownPeers = []Peer{}
	}

	return PeerReport{
		Status:   "ok",
		Word:     "OK",
		Peers:    shownPeers,
		Up:       nUp,
		Asleep:   nAsleep,
		Down:     nDown,
		Of:       total,
		MoreLine: moreLine,
	}
}
