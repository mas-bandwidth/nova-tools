package land

import (
	"strings"
	"testing"
)

func TestStellaMalformedPolicyNamesTheBadField(t *testing.T) {
	t.Parallel()
	for _, field := range []string{"readers", "land_bar"} {
		p, err := ParsePolicy("repo: nova-tools\nbases:\n  dev:\n    " + field + ": not-a-number\n")
		if err == nil || !strings.Contains(err.Error(), field) {
			t.Errorf("invalid %s silently becomes a default: policy=%+v err=%v", field, p.Bases["dev"], err)
		}
	}
}
