package width

import (
	"context"
	"fmt"
	"strconv"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
)

// Sample is one tick's reading for one friend: at (unix ms, Redis TIME),
// the ACKed working count and the desired count.
type Sample struct {
	At      int64
	Working int
	Desired int
}

// Utilisation is the fold's number for one friend or the sprint:
// working-seconds over desired-seconds (#3071 item 2). Samples are
// integrated as steps: each sample's counts hold until the next sample.
// It returns the two integrals in milliseconds and their ratio (0 when
// nothing was desired).
func Utilisation(samples []Sample) (workingMS, desiredMS int64, ratio float64) {
	for i := 0; i+1 < len(samples); i++ {
		dt := samples[i+1].At - samples[i].At
		if dt <= 0 {
			continue
		}
		workingMS += int64(samples[i].Working) * dt
		desiredMS += int64(samples[i].Desired) * dt
	}
	if desiredMS == 0 {
		return workingMS, desiredMS, 0
	}
	return workingMS, desiredMS, float64(workingMS) / float64(desiredMS)
}

// ReadSamples reads friend's samples from width:log in tick order. An empty
// friend reads every friend's samples summed per tick (the sprint line).
func ReadSamples(ctx context.Context, st *store.Store, friend string) ([]Sample, error) {
	if st == nil {
		return nil, fmt.Errorf("width samples: nil store")
	}
	msgs, err := st.Client().XRange(ctx, LogKey, "-", "+").Result()
	if err != nil {
		return nil, fmt.Errorf("width samples: %w", err)
	}
	var out []Sample
	for _, m := range msgs {
		f, _ := m.Values["f"].(string)
		if friend != "" && f != friend {
			continue
		}
		at, err1 := strconv.ParseInt(fmt.Sprint(m.Values["at"]), 10, 64)
		w, err2 := strconv.Atoi(fmt.Sprint(m.Values["working"]))
		d, err3 := strconv.Atoi(fmt.Sprint(m.Values["desired"]))
		if err1 != nil || err2 != nil || err3 != nil {
			return nil, fmt.Errorf("width samples: malformed entry %s", m.ID)
		}
		if friend == "" && len(out) > 0 && out[len(out)-1].At == at {
			out[len(out)-1].Working += w
			out[len(out)-1].Desired += d
			continue
		}
		out = append(out, Sample{At: at, Working: w, Desired: d})
	}
	return out, nil
}

// Lease is one child's ACKed interval: from its start-ack to its close.
type Lease struct {
	Start, End int64
}

// LeaseMS sums lease durations in milliseconds; the control compares it with
// the working integral the tick samples.
func LeaseMS(leases []Lease) int64 {
	var sum int64
	for _, l := range leases {
		if l.End > l.Start {
			sum += l.End - l.Start
		}
	}
	return sum
}
