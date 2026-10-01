package yield

func behind(name string) string { return behindScope(name, realScopeDeps()) }

func behindNote() string { return behindScopeNote(realScopeDeps()) }
