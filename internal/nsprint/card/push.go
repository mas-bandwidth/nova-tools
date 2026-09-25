package card

import (
	"crypto/sha256"
	"fmt"
	"strings"
)

type CardFields struct {
	Kind         string
	Check        string
	Expect       string
	CheckSha256  string
	ExpectSha256 string
}

func ExtractCardFields(raw string) (CardFields, error) {
	findings := LintCard(raw)
	if len(findings) > 0 {
		return CardFields{}, fmt.Errorf("%s", findings[0].Check)
	}

	lines := strings.Split(raw, "\n")
	cf := CardFields{Kind: "model"}
	for _, l := range lines {
		trimmed := strings.TrimSpace(l)
		upper := strings.ToUpper(trimmed)
		if strings.HasPrefix(upper, "KIND:") {
			cf.Kind = strings.TrimSpace(trimmed[5:])
		} else if strings.HasPrefix(upper, "CHECK:") {
			cf.Check = strings.TrimSpace(trimmed[6:])
		} else if strings.HasPrefix(upper, "EXPECT:") {
			cf.Expect = strings.TrimSpace(trimmed[7:])
		}
	}

	if CheckBound(cf.Kind) {
		if cf.Check == "" {
			return CardFields{}, fmt.Errorf("missing CHECK")
		}
		if cf.Expect == "" {
			return CardFields{}, fmt.Errorf("missing EXPECT")
		}
		cf.CheckSha256 = fmt.Sprintf("%x", sha256.Sum256([]byte(cf.Check)))
		cf.ExpectSha256 = fmt.Sprintf("%x", sha256.Sum256([]byte(cf.Expect)))
	}

	return cf, nil
}
