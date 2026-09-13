package secrets

// CreationRule defines one rule in .sops.yaml.
type CreationRule struct {
	PathRegex        string
	UnencryptedRegex string
	Recipients       []string
}

// SopsConfig holds creation rules parsed from .sops.yaml.
type SopsConfig struct {
	CreationRules []CreationRule
}

// CheckFailure records one invariant failure found during 'check'.
type CheckFailure struct {
	Kind   string // invariant kind: "rule-shape", "recipients-drift", "unsealed", "decrypt-failed", "foreign-openable", "store-private-key", "untracked-plaintext", "stale-working-copy"
	File   string // file path or subject
	Reason string // diagnostic detail
}

// CheckSummary aggregates counts and failures across the store.
type CheckSummary struct {
	Recipients int // unique recipient public keys across all rules
	Files      int // count of *.yaml files (excluding .sops.yaml)
	Sealed     int // count of files that are sealed
	Mine       int // count of files whose recipients list this seat
	Foreign    int // count of files whose recipients do not list this seat
	Clear      int // count of keys in the clear under unencrypted_regex
	Failures   []CheckFailure
}
