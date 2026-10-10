package swarm

// Version8 is the row's version: the tool's identity in 8 characters.
func Version8(v string) string {
	if len(v) > 8 {
		return v[:8]
	}
	return v
}
