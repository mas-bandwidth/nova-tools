package sprintfn

import (
	"strconv"

	"github.com/mas-bandwidth/nova-tools/internal/tset"
)

// The bounded sprint-key reads (the errata's addendum to 1.0; item IT30):
// the clock and R, the lease, {p}tick@e, the heartbeat, the dropping marks,
// the parked keys, the scores in {p}missing@e, jopen, and the due count at R.
// Each is a fixed number of Layer 1's checked probes (HMGET, HLEN, ZMSCORE,
// ZCOUNT) over names the query gives or the design fixes; none enumerates a
// hash, since S.read_probe admits no HGETALL and the design lists no bounded
// hash head, so a whole hash (every mark, every parked key) is read by naming
// its fields, which the loop knows.

// fieldsOf reads named fields of a hash key in one HMGET: each one's value, nil
// for a field the hash does not hold. per is the bytes the probe reserves for
// each value.
func (e *qeval) fieldsOf(key string, names []string, per int) (map[string]*string, *Refusal) {
	vals, ref := e.hmget(key, names, per)
	if ref != nil {
		return nil, ref
	}
	out := make(map[string]*string, len(names))
	for i, n := range names {
		out[n] = vals[i]
	}
	return out, nil
}

// clockAt is the clock's fields and R(t) = t - stopped_ms - (stopped_since_ms
// == "" ? 0 : t - stopped_since_ms) at the call's time (1.2). A field that is
// not a whole number of milliseconds, or an R below zero, is DRIFT.
func (e *qeval) clockAt() (ClockFields, tset.Decimal, *Refusal) {
	f, ref := e.fieldsOf(e.bare("clock"), ClockFieldNames, hashFieldBytes)
	if ref != nil {
		return ClockFields{}, "", ref
	}
	out := ClockFields{StoppedMS: f["stopped_ms"], StoppedSinceMS: f["stopped_since_ms"], StopholdMS: f["stophold_ms"],
		DueSinceMS: f["due_since_ms"], StopraisedMS: f["stopraised_ms"]}
	wall, err := strconv.ParseInt(string(e.nowMS), 10, 64)
	if err != nil {
		return out, "", e.fail(codeDrift, tset.RefusalDetail{})
	}
	whole := func(v *string) (int64, bool) {
		if v == nil || *v == "" {
			return 0, true
		}
		n, err := strconv.ParseInt(*v, 10, 64)
		return n, err == nil && n >= 0 && strconv.FormatInt(n, 10) == *v
	}
	stopped, ok1 := whole(out.StoppedMS)
	since, ok2 := whole(out.StoppedSinceMS)
	_, ok3 := whole(out.StopholdMS)
	_, ok4 := whole(out.DueSinceMS)
	_, ok5 := whole(out.StopraisedMS)
	if !ok1 || !ok2 || !ok3 || !ok4 || !ok5 {
		return out, "", e.fail(codeDrift, tset.RefusalDetail{})
	}
	r := wall - stopped
	if out.StoppedSinceMS != nil && *out.StoppedSinceMS != "" {
		r -= wall - since
	}
	if r < 0 {
		return out, "", e.fail(codeDrift, tset.RefusalDetail{})
	}
	return out, tset.Decimal(strconv.FormatInt(r, 10)), nil
}

// evalKey answers one sprint-key read.
func (e *qeval) evalKey(q KeyQ) (QueryResult, *Refusal) {
	switch q.Kind {
	case KeyClock:
		f, r, ref := e.clockAt()
		if ref != nil {
			return nil, ref
		}
		return ClockResult{Kind: q.Kind, WallMS: string(e.nowMS), R: string(r), Clock: f}, nil
	case KeyLease:
		f, ref := e.fieldsOf(e.bare("lease"), LeaseFieldNames, hashFieldBytes)
		if ref != nil {
			return nil, ref
		}
		return LeaseResult{Kind: q.Kind, NowMS: string(e.nowMS), Owner: f["owner"], Name: f["name"],
			UntilMS: f["until_ms"], Gen: f["gen"]}, nil
	case KeyTick:
		f, ref := e.fieldsOf(e.key("tick"), TickFieldNames, hashFieldBytes)
		if ref != nil {
			return nil, ref
		}
		return TickResult{Kind: q.Kind, Cur: f["cur"], BehindN: f["behind_n"]}, nil
	case KeyHeartbeat:
		f, ref := e.fieldsOf(e.bare("heartbeat"), HeartbeatFields, heartbeatFieldBytes)
		if ref != nil {
			return nil, ref
		}
		out := HeartbeatResult{Kind: q.Kind, Fields: map[string]string{}}
		for name, v := range f {
			if v != nil {
				out.Fields[name] = *v
			}
		}
		return out, nil
	case KeyDropping:
		key := e.key("dropping")
		n, ref := e.hlen(key)
		if ref != nil {
			return nil, ref
		}
		out := DroppingResult{Kind: q.Kind, Count: n, Marks: map[string]string{}}
		if len(q.Streams) > 0 {
			f, ref := e.fieldsOf(key, q.Streams, hashFieldBytes)
			if ref != nil {
				return nil, ref
			}
			for s, v := range f {
				if v != nil {
					out.Marks[s] = *v
				}
			}
		}
		return out, nil
	case KeyParked:
		key := e.key("parked")
		n, ref := e.hlen(key)
		if ref != nil {
			return nil, ref
		}
		out := ParkedResult{Kind: q.Kind, Count: n, Notes: map[string]string{}}
		if len(q.Keys) > 0 {
			f, ref := e.fieldsOf(key, q.Keys, hashFieldBytes)
			if ref != nil {
				return nil, ref
			}
			for k, v := range f {
				if v != nil {
					out.Notes[k] = *v
				}
			}
		}
		return out, nil
	case KeyMissing:
		out := MissingResult{Kind: q.Kind, Scores: []*string{}}
		for from := 0; from < len(q.IDs); from += probeChunk {
			got, ref := e.zscores(e.key(indexMissing), q.IDs[from:min(from+probeChunk, len(q.IDs))])
			if ref != nil {
				return nil, ref
			}
			out.Scores = append(out.Scores, got...)
		}
		return out, nil
	case KeyJOpen:
		out := JOpenResult{Kind: q.Kind, Items: []JOpenItem{}}
		for _, s := range q.Subjects {
			key := e.key("jopen:" + s)
			n, ref := e.hlen(key)
			if ref != nil {
				return nil, ref
			}
			it := JOpenItem{ID: s, Count: n, Fields: map[string]*string{}}
			if len(q.Names) > 0 {
				f, ref := e.fieldsOf(key, q.Names, hashFieldBytes)
				if ref != nil {
					return nil, ref
				}
				it.Fields = f
			}
			out.Items = append(out.Items, it)
		}
		return out, nil
	case KeyDueCount:
		_, r, ref := e.clockAt()
		if ref != nil {
			return nil, ref
		}
		n, ref := e.zcount(e.key("due"), "-inf", string(r))
		if ref != nil {
			return nil, ref
		}
		return DueCountResult{Kind: q.Kind, R: string(r), Due: n}, nil
	}
	return nil, requestRefusal()
}
