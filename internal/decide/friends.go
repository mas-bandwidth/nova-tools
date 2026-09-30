// Package decide handles mind routing decisions across the ladder.
package decide

// FriendRungs returns the minds in the registry that are friend rungs:
// minds asked by bus, excluding the all-friends broadcast and Glenn.
func (r *Registry) FriendRungs() []Mind {
	if r == nil {
		return nil
	}
	var out []Mind
	for _, m := range r.Minds {
		if m.Ask == AskBus && m.Name != "all-friends" && m.Name != "glenn" && m.Lineage != "friends" && m.Lineage != "glenn" {
			out = append(out, m)
		}
	}
	return out
}
