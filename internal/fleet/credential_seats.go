package fleet

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// CredentialSeat binds either an account's source seat or one profile's target
// seat to a machine. These are public identifiers; no value belongs here.
type CredentialSeat struct {
	AccountID  string `json:"account_id"`
	ProfileID  string `json:"profile_id,omitempty"`
	TargetSeat string `json:"target_seat,omitempty"`
	SourceSeat string `json:"source_seat,omitempty"`
}

func (s CredentialSeat) SeatName() string {
	if s.SourceSeat != "" {
		return s.SourceSeat
	}
	return s.TargetSeat
}

// ParseCredentialSeats reads the optional public machine field. Empty means
// that only the machine's legacy primary seat is declared.
func ParseCredentialSeats(raw string) ([]CredentialSeat, error) {
	if raw == "" || raw == "-" {
		return nil, nil
	}
	if len(raw) > 8192 {
		return nil, fmt.Errorf("credential_seats exceeds 8192 bytes")
	}
	dec := json.NewDecoder(strings.NewReader(raw))
	first, err := dec.Token()
	if err != nil || first != json.Delim('[') {
		return nil, fmt.Errorf("credential_seats wants a JSON array of account_id/source_seat or account_id/profile_id/target_seat objects")
	}
	var seats []CredentialSeat
	for dec.More() {
		if len(seats) == 64 {
			return nil, fmt.Errorf("credential_seats has more than 64 entries")
		}
		open, err := dec.Token()
		if err != nil || open != json.Delim('{') {
			return nil, fmt.Errorf("credential_seats entry %d wants an object", len(seats)+1)
		}
		var seat CredentialSeat
		seen := map[string]bool{}
		for dec.More() {
			token, err := dec.Token()
			if err != nil {
				return nil, fmt.Errorf("credential_seats entry %d has an unreadable key", len(seats)+1)
			}
			key, ok := token.(string)
			if !ok {
				return nil, fmt.Errorf("credential_seats entry %d has a non-text key", len(seats)+1)
			}
			folded := strings.ToLower(key)
			if seen[folded] {
				return nil, fmt.Errorf("credential_seats entry %d repeats a field name", len(seats)+1)
			}
			seen[folded] = true
			var value string
			if err := dec.Decode(&value); err != nil {
				return nil, fmt.Errorf("credential_seats entry %d field wants a text value", len(seats)+1)
			}
			switch key {
			case "account_id":
				seat.AccountID = value
			case "profile_id":
				seat.ProfileID = value
			case "target_seat":
				seat.TargetSeat = value
			case "source_seat":
				seat.SourceSeat = value
			default:
				return nil, fmt.Errorf("credential_seats entry %d has an unknown field name", len(seats)+1)
			}
		}
		close, err := dec.Token()
		if err != nil || close != json.Delim('}') {
			return nil, fmt.Errorf("credential_seats entry %d is not closed", len(seats)+1)
		}
		if !seen["account_id"] || (seen["source_seat"] && (seen["profile_id"] || seen["target_seat"])) ||
			(!seen["source_seat"] && (!seen["profile_id"] || !seen["target_seat"])) {
			return nil, fmt.Errorf("credential_seats entry %d needs exactly a source or target binding", len(seats)+1)
		}
		seats = append(seats, seat)
	}
	close, err := dec.Token()
	if err != nil || close != json.Delim(']') {
		return nil, fmt.Errorf("credential_seats array is not closed")
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return nil, fmt.Errorf("credential_seats has trailing data")
	}
	profiles, names := map[string]bool{}, map[string]bool{}
	for i, seat := range seats {
		isSource := seat.SourceSeat != ""
		if !credentialID(seat.AccountID) || (isSource && (seat.ProfileID != "" || seat.TargetSeat != "" || !credentialID(seat.SourceSeat))) || (!isSource && (!credentialID(seat.ProfileID) || !credentialID(seat.TargetSeat))) {
			return nil, fmt.Errorf("credential_seats entry %d needs either account_id/source_seat or account_id/profile_id/target_seat using letters, digits, - or _", i+1)
		}
		if (!isSource && profiles[seat.ProfileID]) || names[seat.SeatName()] {
			return nil, fmt.Errorf("credential_seats entry %d reuses a profile or seat", i+1)
		}
		if !isSource {
			profiles[seat.ProfileID] = true
		}
		names[seat.SeatName()] = true
	}
	return seats, nil
}

func credentialID(s string) bool {
	if s == "" || len(s) > 64 {
		return false
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z') && !(r >= 'A' && r <= 'Z') && !(r >= '0' && r <= '9') && r != '-' && r != '_' {
			return false
		}
	}
	return true
}
