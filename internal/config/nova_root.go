package config

// NovaRootOf is the nova_root a machine row names for the machine host: the
// row whose machine is host, else the one row that sets a root. It is "" when
// no row sets one, and "" when more than one row sets a root and none names
// host: an ambiguous inventory never picks a machine's root at random.
func NovaRootOf(ws []MachineWidth, host string) string {
	found, many := "", false
	for _, w := range ws {
		if w.NovaRoot == "" {
			continue
		}
		if w.Machine == host {
			return w.NovaRoot
		}
		switch {
		case found == "":
			found = w.NovaRoot
		case found != w.NovaRoot:
			many = true
		}
	}
	if many {
		return ""
	}
	return found
}
