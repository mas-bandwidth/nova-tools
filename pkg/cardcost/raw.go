package cardcost

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
)

// ReceiptVersion is the typed receipt contract, gated by --receipt-contract 2.
const ReceiptVersion = 2

// RawVersion is the original-counter envelope schema.
const RawVersion = 1

// MaxRawBytes bounds a receipt below the native line scanner's limit.
const MaxRawBytes = 32768

// RawRecord preserves one launch's original counters; absence and zero differ.
// Tokens are never summed or interpreted as disjoint billing classes.
type RawRecord struct {
	Harness         string           `json:"harness"`
	Execution       string           `json:"execution"`
	SourceSchema    string           `json:"source_schema"`
	ProducerVersion string           `json:"producer_version"`
	Billing         string           `json:"billing"`
	Tokens          map[string]int64 `json:"tokens"`
}

// RawUsage retains launch order and provenance across aggregation and restart.
type RawUsage struct {
	Version int         `json:"version"`
	Records []RawRecord `json:"records"`
}

var museRawKeys = []string{"input_tokens", "output_tokens", "cached_tokens", "cache_write_tokens", "cache_read_tokens", "reasoning_tokens"}

// Validate checks the exact envelope schema without normalizing counters.
func (r *RawUsage) Validate() error {
	if r == nil {
		return nil
	}
	if r.Version != RawVersion || len(r.Records) == 0 {
		return fmt.Errorf("unsupported or empty raw usage envelope")
	}
	for _, record := range r.Records {
		if record.Harness != "muse" || record.Execution != "fleet" || record.SourceSchema != "muse-jsonl-v1" || record.ProducerVersion == "" || !slices.Contains(Billings, record.Billing) || record.Tokens == nil {
			return fmt.Errorf("invalid raw usage provenance")
		}
		for key, n := range record.Tokens {
			if !slices.Contains(museRawKeys, key) || n < 0 {
				return fmt.Errorf("invalid raw counter %q", key)
			}
		}
	}
	return nil
}

// EncodeRaw writes bounded base64url JSON, suitable for one usage word.
func EncodeRaw(r *RawUsage) (string, error) {
	if err := r.Validate(); err != nil {
		return "", err
	}
	b, err := json.Marshal(r)
	if err != nil {
		return "", err
	}
	if len(b) > MaxRawBytes {
		return "", fmt.Errorf("raw usage envelope exceeds %d bytes", MaxRawBytes)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// DecodeRaw refuses malformed, unknown and unsupported data instead of dropping it.
func DecodeRaw(s string) (*RawUsage, error) {
	if len(s) > base64.RawURLEncoding.EncodedLen(MaxRawBytes) {
		return nil, fmt.Errorf("raw usage envelope too large")
	}
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("invalid raw usage encoding: %w", err)
	}
	if err := validateRawJSON(b); err != nil {
		return nil, err
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	var r RawUsage
	if err = d.Decode(&r); err != nil {
		return nil, fmt.Errorf("invalid raw usage JSON: %w", err)
	}
	var extra any
	if err = d.Decode(&extra); err != io.EOF {
		return nil, fmt.Errorf("trailing raw usage JSON")
	}
	if err = r.Validate(); err != nil {
		return nil, err
	}
	return &r, nil
}

// MarginalUSD separates the retained harness amount from the card's charge.
func (u Usage) MarginalUSD() string {
	if u.Billing == BillingSubscription {
		return "0"
	}
	if _, err := amount(u.Actual); err == nil {
		return u.Actual
	}
	return u.Predicted
}

// ParseUsageChecked validates typed receipts before a consumer writes them.
// Legacy lines retain their existing permissive parsing behavior.
func ParseUsageChecked(line string) (Usage, error) {
	u := parseUsageLegacy(line)
	values := map[string]string{}
	for _, word := range strings.Fields(line) {
		k, v, ok := strings.Cut(word, "=")
		if !ok {
			continue
		}
		if k == "receipt_version" || k == "billing" || k == "raw_usage" {
			if _, exists := values[k]; exists {
				return u, fmt.Errorf("duplicate receipt field %s", k)
			}
			values[k] = v
		}
	}
	if v, ok := values["receipt_version"]; ok {
		n, err := strconv.Atoi(v)
		u.ReceiptVersion = n
		if err != nil || n != ReceiptVersion {
			return u, fmt.Errorf("unsupported receipt version %q", v)
		}
	}
	if raw, ok := values["raw_usage"]; ok {
		r, err := DecodeRaw(raw)
		if err != nil {
			return u, err
		}
		u.Raw = r
	}
	// Remove fields owned by this codec from the legacy extras.
	u.Extra = slices.DeleteFunc(u.Extra, func(s string) bool {
		return strings.HasPrefix(s, "receipt_version=") || strings.HasPrefix(s, "raw_usage=")
	})
	if u.ReceiptVersion == 0 {
		if u.Raw != nil {
			return u, fmt.Errorf("raw usage requires receipt version 2")
		}
		return u, nil
	}
	if !slices.Contains(Billings, u.Billing) {
		return u, fmt.Errorf("typed receipt requires valid billing")
	}
	for _, word := range strings.Fields(line) {
		k, v, ok := strings.Cut(word, "=")
		if !ok {
			continue
		}
		switch k {
		case "actual_usd":
			if _, err := amount(v); err != nil {
				return u, fmt.Errorf("invalid receipt amount")
			}
		case "input", "cache_read", "cache_write", "output", "reasoning", "requests", "max_prompt":
			n, err := strconv.ParseInt(v, 10, 64)
			if err != nil || n < 0 {
				return u, fmt.Errorf("invalid receipt counter %s", k)
			}
		}
	}
	if u.Raw != nil {
		for _, r := range u.Raw.Records {
			if r.Billing != u.Billing {
				return u, fmt.Errorf("raw usage billing disagrees with receipt")
			}
		}
	}
	return u, nil
}

// ValidateUsage is the write boundary's validation without parsing twice at callers.
func ValidateUsage(line string) error { _, err := ParseUsageChecked(line); return err }

// ParseSpendChecked carries typed metadata through the native spend transport.
func ParseSpendChecked(s string) (Usage, error) {
	legacy := parseSpendLegacy(s)
	var words []string
	for _, part := range strings.Split(s, ",") {
		k, v, ok := strings.Cut(part, ":")
		if ok {
			if k == "cost" {
				k = "actual_usd"
			}
			words = append(words, k+"="+v)
		}
	}
	u, err := ParseUsageChecked(strings.Join(words, " "))
	u.Actual, u.ActualBy, u.Model = legacy.Actual, legacy.ActualBy, legacy.Model
	return u, err
}

// EncodeSpend retains the legacy token projection plus the complete typed receipt.
func EncodeSpend(u Usage) (string, error) {
	if u.Raw != nil {
		if _, err := EncodeRaw(u.Raw); err != nil {
			return "", err
		}
	}
	if err := ValidateUsage(u.String()); err != nil {
		return "", err
	}
	s := SpendWord(u.Tokens, u.Actual, u.Model)
	var parts []string
	if s != "" {
		parts = append(parts, s)
	}
	if u.ReceiptVersion != 0 {
		parts = append(parts, "receipt_version:"+strconv.Itoa(u.ReceiptVersion))
	}
	if u.Billing != "" {
		parts = append(parts, "billing:"+u.Billing)
	}
	if u.Raw != nil {
		raw, err := EncodeRaw(u.Raw)
		if err != nil {
			return "", err
		}
		parts = append(parts, "raw_usage:"+raw)
	}
	return strings.Join(parts, ","), nil
}

// MergeRaw concatenates original records; it never sums their counters.
func MergeRaw(us []Usage) (*RawUsage, error) {
	r := &RawUsage{Version: RawVersion}
	for _, u := range us {
		if u.Raw != nil {
			if err := u.Raw.Validate(); err != nil {
				return nil, err
			}
			r.Records = append(r.Records, u.Raw.Records...)
		}
	}
	if len(r.Records) == 0 {
		return nil, nil
	}
	if _, err := EncodeRaw(r); err != nil {
		return nil, err
	}
	return r, nil
}

// validateRawJSON refuses duplicate members and null token counts. Ordinary JSON
// decoding otherwise silently turns null into zero or retains the last duplicate.
func validateRawJSON(b []byte) error {
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	var walk func(string) error
	walk = func(parent string) error {
		token, err := d.Token()
		if err != nil {
			return err
		}
		delim, container := token.(json.Delim)
		if !container {
			if parent == "tokens" {
				number, ok := token.(json.Number)
				if !ok {
					return fmt.Errorf("raw count must be an integer")
				}
				n, err := number.Int64()
				if err != nil || n < 0 {
					return fmt.Errorf("raw count must be a nonnegative int64")
				}
			}
			return nil
		}
		switch delim {
		case '{':
			seen := map[string]bool{}
			for d.More() {
				keyToken, err := d.Token()
				if err != nil {
					return err
				}
				key, ok := keyToken.(string)
				if !ok || seen[key] {
					return fmt.Errorf("duplicate raw JSON member")
				}
				seen[key] = true
				next := key
				if parent == "tokens" {
					next = "tokens"
				}
				if err := walk(next); err != nil {
					return err
				}
			}
		case '[':
			for d.More() {
				if err := walk(parent); err != nil {
					return err
				}
			}
		default:
			return fmt.Errorf("invalid raw JSON container")
		}
		_, err = d.Token()
		return err
	}
	if err := walk(""); err != nil {
		return err
	}
	if _, err := d.Token(); err != io.EOF {
		return fmt.Errorf("trailing raw JSON")
	}
	return nil
}
