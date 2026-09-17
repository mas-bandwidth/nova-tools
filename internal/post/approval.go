package post

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bus"
)

// approvalPrefix is the one body line a release receipt carries.
const approvalPrefix = "APPROVE nova-post sha256="

// Approval is the reading of one release receipt.
type Approval struct {
	ID     string
	Sender string
	Hash   string
	Stamp  time.Time
}

// ReadApproval finds the bus note named by receiptID and checks all four release rules,
// returning a refusal naming the one that failed. The clock is an argument so a test can
// stand at the edge of the 24-hour window without a --now flag: a caller who can name the
// time can forge freshness, so only code in a test ever chooses it.
func ReadApproval(busDir, receiptID, wantHash string, now time.Time) (Approval, error) {
	if strings.TrimSpace(receiptID) == "" {
		return Approval{}, Refuse("no-approval",
			"--approval is required; have Glenn send APPROVE nova-post sha256="+onelineField(wantHash)+" to the bus, then pass that receipt id", 1)
	}
	note, ok := findNote(busDir, receiptID)
	if !ok {
		return Approval{}, Refuse("no-approval",
			"the bus holds no receipt "+onelineField(receiptID)+"; have Glenn send APPROVE nova-post sha256="+onelineField(wantHash)+" and pass its receipt id", 1)
	}
	app := Approval{ID: note.Header.ID, Sender: strings.TrimSpace(note.Header.From), Stamp: note.When()}
	body := strings.TrimSpace(bus.NormalizeBody(note.Body))
	line := firstLine(body)
	if !strings.HasPrefix(line, approvalPrefix) {
		return app, Refuse("not-an-approval",
			"receipt "+onelineField(receiptID)+" does not carry the line APPROVE nova-post sha256=<hash>", 1)
	}
	app.Hash = strings.TrimSpace(strings.TrimPrefix(line, approvalPrefix))
	if app.Sender != "Glenn" {
		return app, Refuse("approval-sender",
			"receipt "+onelineField(receiptID)+" is from "+onelineField(app.Sender)+", not Glenn; only Glenn releases a draft", 1)
	}
	if app.Hash != wantHash {
		return app, Refuse("approval-hash",
			"receipt "+onelineField(receiptID)+" names another hash; have Glenn approve "+onelineField(wantHash), 1)
	}
	if !app.Stamp.IsZero() && !now.Before(app.Stamp.Add(24*time.Hour)) {
		return app, Refuse("approval-stale",
			"receipt "+onelineField(receiptID)+" was received "+app.Stamp.UTC().Format(time.RFC3339)+", 24 hours or more ago; have Glenn approve it again", 1)
	}
	return app, nil
}

// findNote walks the from-* lanes for the note carrying id. It reads only notes, and a
// file that will not parse is stepped over: a release is not the place to fail a bus.
func findNote(busDir, id string) (bus.Note, bool) {
	entries, err := os.ReadDir(busDir)
	if err != nil {
		return bus.Note{}, false
	}
	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), "from-") {
			continue
		}
		files, err := os.ReadDir(filepath.Join(busDir, e.Name()))
		if err != nil {
			continue
		}
		for _, f := range files {
			if f.IsDir() || !strings.HasSuffix(f.Name(), ".md") {
				continue
			}
			raw, err := os.ReadFile(filepath.Join(busDir, e.Name(), f.Name()))
			if err != nil {
				continue
			}
			n, perr := bus.ParseNote(e.Name()+"/"+f.Name(), string(raw))
			if perr != nil {
				continue
			}
			if n.Header.ID == id {
				return n, true
			}
		}
	}
	return bus.Note{}, false
}

func onelineField(s string) string {
	return fmt.Sprintf("%q", s)
}
