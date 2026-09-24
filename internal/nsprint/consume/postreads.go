package consume

import (
	"context"
	"fmt"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/typedrec"
)

// PostResultRefusal is returned when a card result cannot be posted as read evidence.
type PostResultRefusal struct {
	Field  string
	Defect string
}

func (r *PostResultRefusal) Error() string {
	return fmt.Sprintf("field=%s defect=%s", r.Field, r.Defect)
}

// PostReads posts swarm read evidence into Redis for kind=read cards (#2506).
func PostReads(ctx context.Context, st *store.Store, sprint, label string, res typedrec.Result) error {
	if !res.Valid {
		return &PostResultRefusal{Field: res.Field, Defect: res.Defect}
	}
	head := res.Claims["HEAD"]
	if head == "" {
		return &PostResultRefusal{Field: "HEAD", Defect: "missing"}
	}
	repo := res.Claims["REPO"]
	pr := res.Claims["PR"]
	suggest := res.Claims["SUGGEST"]
	findings := res.Claims["FINDINGS"]
	floor := res.Claims["FLOOR"]
	if repo == "" || pr == "" {
		return fmt.Errorf("missing repo or pr")
	}

	reply, err := st.Client().FCall(ctx, "ns_read_evidence", nil,
		sprint, repo, pr, label, head, suggest, findings, floor).Text()
	if err != nil {
		return err
	}
	if reply != "OK" {
		return fmt.Errorf("ns_read_evidence: %s", reply)
	}
	return nil
}
