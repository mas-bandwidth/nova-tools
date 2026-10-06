package friend

import "path/filepath"

// SpendFile is the friend's spend, in the state directory: what her harness's
// runs cost and the limits they read, as the daemon's beat last said it. The
// daemon is its one writer; no file is no spend measured.
const SpendFile = "spend.json"

// WriteSpend writes the snapshot whole to stateDir.
func WriteSpend(stateDir string, s SpendSnapshot) error {
	return write(filepath.Join(stateDir, SpendFile), s)
}

// ReadSpend reads the spend file; found is false when there is none.
func ReadSpend(stateDir string) (s SpendSnapshot, found bool, err error) {
	found, err = read(filepath.Join(stateDir, SpendFile), &s)
	return s, found, err
}

// SpendOf is the spend a deliverer keeps, nil when it keeps none.
func SpendOf(d Deliverer) *Spend {
	switch h := d.(type) {
	case *Claude:
		return h.Spend
	case *OpenCode:
		return h.Spend
	}
	return nil
}
