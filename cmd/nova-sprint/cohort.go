package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprintcohort"
)

// cohortOptions selects a read-only cohort inspection (SPEC-SPRINT.md,
// Cohort inspection); the ordinary where frame keeps its current behavior.
type cohortOptions struct{ stream, ids, manifest *string }

func cohortFlags(fs *flag.FlagSet) cohortOptions {
	return cohortOptions{
		stream:   fs.String("stream", "", "inspect only this exact stream's primaries; excludes sentinels"),
		ids:      fs.String("ids", "", "inspect these exact primary identities, comma-separated; exclusive with --stream and --manifest"),
		manifest: fs.String("manifest", "", "read exact primary identities from this local file: JSON string array or one identity per line; exclusive with --stream and --ids"),
	}
}
func (o cohortOptions) enabled() bool { return *o.stream != "" || *o.ids != "" || *o.manifest != "" }

// selection reads local identities before an RPC so the server never opens a
// caller's local path. Identity limits bound both parsing and record discovery.
func (o cohortOptions) selection() (sprintcohort.Selection, error) {
	q := sprintcohort.Selection{Stream: *o.stream}
	sources := 0
	for _, v := range []string{*o.stream, *o.ids, *o.manifest} {
		if v != "" {
			sources++
		}
	}
	if sources != 1 {
		return q, fmt.Errorf("give exactly one of --stream, --ids or --manifest")
	}
	if *o.ids != "" {
		q.IDs = strings.Split(*o.ids, ",")
	}
	if *o.manifest != "" {
		f, err := os.Open(*o.manifest)
		if err != nil {
			return q, err
		}
		defer func() {
			_ = f.Close() // ignored: a read-only manifest close cannot change the identities already read
		}()
		data, err := io.ReadAll(io.LimitReader(f, 1024*1024+1))
		if err != nil {
			return q, err
		}
		if len(data) > 1024*1024 {
			return q, fmt.Errorf("--manifest exceeds 1 MiB")
		}
		text := strings.TrimSpace(string(data))
		if strings.HasPrefix(text, "[") {
			if err := json.Unmarshal(data, &q.IDs); err != nil {
				return q, fmt.Errorf("--manifest wants a JSON string array: %w", err)
			}
		} else {
			for _, line := range strings.Split(text, "\n") {
				if strings.TrimSpace(line) != "" {
					q.IDs = append(q.IDs, strings.TrimSpace(line))
				}
			}
		}
	}
	for i := range q.IDs {
		q.IDs[i] = strings.TrimSpace(q.IDs[i])
	}
	return q, q.Validate()
}

// cohortWhere sends one server request, and loads selected historical records
// in batches on the server; it never calls CardOf separately for every primary.
func (a *app) cohortWhere(fs *flag.FlagSet, c common, o cohortOptions, watch bool, epoch int64, args []string, stdout, stderr io.Writer) int {
	if c.max < 0 {
		return refuse(stderr, "where", "--max wants a count at least 0")
	}
	if watch {
		return refuse(stderr, "where", "cohort inspection is one snapshot; omit --watch")
	}
	q, err := o.selection()
	if err != nil {
		return refuse(stderr, "where", err.Error())
	}
	ctx := context.Background()
	if addr := a.server(fs); addr != "" {
		forwarded := without(fs, args, "manifest", "ids", "stream")
		if q.Stream != "" {
			forwarded = append(forwarded, "--stream", q.Stream)
		} else {
			forwarded = append(forwarded, "--ids", strings.Join(q.IDs, ","))
		}
		res, err := a.ask(ctx, addr, []string{"where"}, forwarded)
		if err != nil {
			return a.unanswered("where", addr, err, stderr)
		}
		a.answer(res, stdout, stderr)
		return res.Code
	}
	st, err := a.storeAt(c, epoch)
	if err != nil {
		return refuse(stderr, "where", err.Error())
	}
	snap, err := st.Load(ctx, []string{sprint.Work, sprint.Fleet, sprint.Readers, sprint.Merge}, sprintcohort.Records(q))
	if err != nil {
		return a.readFailed("where", err, stderr)
	}
	if q.Stream != "" && !snap.Work.HasRow(q.Stream) {
		return refuse(stderr, "where", "no exact stream "+q.Stream+" in this epoch")
	}
	status := sprintcohort.Summarize(snap, q)
	if len(status.Missing) > 0 {
		return refuse(stderr, "where", "primary identities have no placed record in this epoch: "+strings.Join(status.Missing, ", "))
	}
	if c.json {
		b, err := json.Marshal(status)
		if err != nil {
			return a.readFailed("where", err, stderr)
		}
		fmt.Fprintln(stdout, string(b))
	} else {
		fmt.Fprint(stdout, cohortText(status, c.max))
	}
	return 0
}

// cohortText renders exactly the counts and evidence in the JSON status value.
func cohortText(v sprintcohort.Status, limit int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "COHORT OK epoch=%d primaries=%d excluded_sentinels=%d\n", v.Epoch, v.Primaries, len(v.Excluded))
	fmt.Fprintf(&b, "TABLE STATES waiting=%d ready=%d working=%d review=%d merging=%d legacy_landed=%d; raw source columns, dev receipt checked separately\n", v.States[sprint.Waiting], v.States[sprint.Ready], v.States[sprint.Working], v.States[sprint.Review], v.States[sprint.Merging], v.States[sprint.Landed])
	fmt.Fprintf(&b, "FIRST WORK ok=%d failed=%d pending=%d; w1 final verdict, provider returns separate\n", v.FirstWork.OK, v.FirstWork.Failed, v.FirstWork.Pending)
	fmt.Fprintf(&b, "WORK attempts=%d retry_attempts=%d retry_cards=%d ok=%d failed=%d pending=%d recorded_takes=%d provider_or_no_result_returns=%d\n", v.WorkAttempts, v.RetryAttempts, v.RetryCards, v.AllWork.OK, v.AllWork.Failed, v.AllWork.Pending, v.RecordedWorkTakes, v.ProviderReturns)
	fmt.Fprintf(&b, "FIRST READS ok=%d broken=%d pending=%d retired_unread=%d; all independent r1 reads\n", v.FirstReads.OK, v.FirstReads.Broken, v.FirstReads.Pending, v.FirstReads.Retired)
	fmt.Fprintf(&b, "READS ok=%d broken=%d pending=%d retired_unread=%d returned_runs=%d\n", v.Reads.OK, v.Reads.Broken, v.Reads.Pending, v.Reads.Retired, v.ReadReturns)
	fmt.Fprintf(&b, "LANDING branch_staged=%d dev_verified=%d dev_unknown=%d; only a verified dev receipt establishes landed\n", v.BranchStaged, v.DevLandedVerified, v.DevLandingUnknown)
	fmt.Fprintf(&b, "COST charged_usd=%s charged_of=%d/%d actual_usd=%s actual_of=%d/%d actual_by=%s predicted_usd=%s predicted_of=%d/%d cut=%d primary_totals_missing=%d; completed consumer records, inflight unknown, not a provider invoice\n", wordOrDash(v.Cost.Charged), v.Cost.ChargedOf, v.Cost.Records, wordOrDash(v.Cost.Actual), v.Cost.ActualOf, v.Cost.Records, wordOrDash(strings.Join(v.Cost.ActualBy, "+")), wordOrDash(v.Cost.Predicted), v.Cost.PredictedOf, v.Cost.Records, v.Cost.Cut, v.Cost.MissingPrimaryTotals)
	shown := len(v.Rows)
	if limit > 0 {
		shown = min(shown, limit)
	}
	for _, r := range v.Rows[:shown] {
		fmt.Fprintf(&b, "CARD id=%s state=%s attempt=%d first_work=%s landing_target=%s dev=%s\n", oneline.Field(r.ID), oneline.Field(r.State), r.Attempt, r.FirstWork, oneline.Field(r.LandingTarget), oneline.Field(r.DevPromotion))
	}
	if shown < len(v.Rows) {
		fmt.Fprintf(&b, "COHORT MORE shown=%d total=%d; run with --max 0 for every primary\n", shown, len(v.Rows))
	}
	return b.String()
}
func wordOrDash(s string) string {
	if s == "" {
		return "-"
	}
	return oneline.Field(s)
}
