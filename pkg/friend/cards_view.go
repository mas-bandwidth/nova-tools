package friend

import (
	"sync"

	"github.com/mas-bandwidth/nova-tools/pkg/schemaread"
)

// Holders is the server's `view cards` document as parseHolders reads it: the
// owner of each card id. It is the one record this dual-read card routes
// through pkg/schemaread (S2, C13), so its table image, the padded standard
// base64 of that image and the legacy JSON all decode to one equal value.
type Holders map[string]string

// HoldersHelp is the command a holders-view refusal points at: nova-friend's
// cards verb, which reads the server's view.
const HoldersHelp = "nova-friend cards -h"

// holdersHooks is the generated HoldersView table's loads (pkg/answerwire,
// schemas/answer), installed by RegisterHoldersWire. pkg/friend does not
// import pkg/answerwire, which imports pkg/friend; the binary wires it here.
type holdersHooks struct {
	measure  func([]byte) error
	loadWire func(*Holders, []byte) schemaread.Report
	loadJSON func(*Holders, []byte) schemaread.Report
	saveWire func(*Holders) []byte
	saveJSON func(*Holders) []byte
}

var (
	holdersMu sync.RWMutex
	holdersHk holdersHooks
)

// RegisterHoldersWire installs the generated HoldersView table functions into
// the cards-view reader. Callers in cmd/nova-friend and pkg/friend's tests
// wire pkg/answerwire here; pkg/friend itself never imports pkg/answerwire.
func RegisterHoldersWire(
	measure func([]byte) error,
	loadWire func(*Holders, []byte) schemaread.Report,
	loadJSON func(*Holders, []byte) schemaread.Report,
	saveWire func(*Holders) []byte,
	saveJSON func(*Holders) []byte,
) {
	holdersMu.Lock()
	defer holdersMu.Unlock()
	holdersHk = holdersHooks{measure, loadWire, loadJSON, saveWire, saveJSON}
}

// HoldersReader reads the cards view strictly through pkg/schemaread: binary
// wire, padded standard base64, or the schema JSON text form, each refused by
// name when damaged and counted once per form. The schema JSON text and the
// legacy JSON parseHolders always read are the same text (the generated writer
// spells the cards view as the old writer did), so the JSON loader reads both
// and counts them under the json form. Writers stay legacy; this reader is the
// read side of S4 (readers first).
type HoldersReader struct {
	r *schemaread.Reader[Holders]
}

// NewHoldersReader returns a cards-view reader with its counters at zero.
func NewHoldersReader() *HoldersReader {
	holdersMu.RLock()
	h := holdersHk
	holdersMu.RUnlock()
	return &HoldersReader{r: schemaread.NewReader(&schemaread.Codec[Holders]{
		Name:     "CARD_HOLDERS",
		Help:     HoldersHelp,
		Reset:    func(v *Holders) { *v = nil },
		Measure:  h.measure,
		LoadWire: h.loadWire,
		LoadJSON: h.loadJSON,
		SaveWire: h.saveWire,
		SaveJSON: h.saveJSON,
	})}
}

// Counts returns the reader's per-form counters and refusal tallies.
func (r *HoldersReader) Counts() *schemaread.Counts { return r.r.Counts() }

// Read reads the cards view in whichever form it arrived, returning the owners
// map. A refusal is a *schemaread.Refusal and the map is nil.
func (r *HoldersReader) Read(in []byte) (Holders, schemaread.Form, error) {
	v, res, err := r.r.Read(in)
	if err != nil {
		return nil, res.Form, err
	}
	return v, res.Form, nil
}
