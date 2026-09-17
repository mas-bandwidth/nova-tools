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
		return Approval{}, &RefusalError{Code: 1, Reason: "no-approval",
			Detail: "--approval is required; have Glenn send APPROVE nova-post sha256=" + onelineField(wantHash) + " to the bus, then pass that receipt id"}
	}
	note, ok := findNote(busDir, receiptID)
	if !ok {
		return Approval{}, &RefusalError{Code: 1, Reason: "no-approval",
			Detail: "the bus holds no receipt " + onelineField(receiptID) + "; have Glenn send APPROVE nova-post sha256=" + onelineField(wantHash) + " and pass its receipt id"}
	}
	app := Approval{ID: note.Header.ID, Sender: strings.TrimSpace(note.Header.From), Stamp: note.When()}
	body := strings.TrimSpace(bus.NormalizeBody(note.Body))
	line := firstLine(body)
	if !strings.HasPrefix(line, approvalPrefix) {
		return app, &RefusalError{Code: 1, Reason: "not-an-approval",
			Detail: "receipt " + onelineField(receiptID) + " does not carry the line APPROVE nova-post sha256=<hash>"}
	}
	app.Hash = strings.TrimSpace(strings.TrimPrefix(line, approvalPrefix))
	if app.Sender != "Glenn" {
		return app, &RefusalError{Code: 1, Reason: "approval-sender",
			Detail: "receipt " + onelineField(receiptID) + " is from " + onelineField(app.Sender) + ", not Glenn; only Glenn releases a draft"}
	}
	if app.Hash != wantHash {
		return app, &RefusalError{Code: 1, Reason: "approval-hash",
			Detail: "receipt " + onelineField(receiptID) + " names another hash; have Glenn approve " + onelineField(wantHash)}
	}
	if !app.Stamp.IsZero() && !now.Before(app.Stamp.Add(24*time.Hour)) {
		return app, &RefusalError{Code: 1, Reason: "approval-stale",
			Detail: "receipt " + onelineField(receiptID) + " was received " + app.Stamp.UTC().Format(time.RFC3339) + ", 24 hours or more ago; have Glenn approve it again"}
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
