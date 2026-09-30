package main

// Standard JSON output envelope types (SPEC.md: {result, facts, items, notes}).

type jsonResult struct {
	Verb   string   `json:"verb"`
	Status string   `json:"status"` // "ok", "failed", "refused"
	Exit   int      `json:"exit"`
	Remedy string   `json:"remedy,omitempty"`
	Why    []string `json:"why,omitempty"`
}

type jsonMore struct {
	Kind   string `json:"kind"`
	Shown  int    `json:"shown"`
	Total  int    `json:"total"`
	Remedy string `json:"remedy"`
}

type importEnvelope struct {
	Result jsonResult     `json:"result"`
	Facts  map[string]any `json:"facts"`
	Notes  []string       `json:"notes,omitempty"`
}

type verifyEnvelope struct {
	Result jsonResult     `json:"result"`
	Facts  map[string]any `json:"facts"`
	Items  []verifyItem   `json:"items"`
	More   []jsonMore     `json:"more,omitempty"`
	Notes  []string       `json:"notes,omitempty"`
}

type verifyItem struct {
	Kind  string `json:"kind"`
	Path  string `json:"path"`
	Field string `json:"field"`
	Want  string `json:"want,omitempty"`
	Got   string `json:"got,omitempty"`
}
