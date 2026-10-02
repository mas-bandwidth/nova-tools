package main

import (
	"fmt"

	"github.com/mas-bandwidth/nova-tools/internal/memindex"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/tool"
)

// retrievalResult holds the evidence once for both renderings (SPEC §2-json).
type retrievalResult struct {
	Verb, Query, Source, Channels string
	K, Files, Chunks              int
	Calibration                   memindex.FileHit
	Candidates                    []retrievalCandidate
	Notes                         []string
}

type retrievalCandidate struct {
	Text string
	Hits []memindex.FileHit
}

// calibrationHits preserves absence of a probe hit as an empty native channel.
func calibrationHits(c *memindex.Corpus, chans []memindex.Channel) memindex.FileHit {
	hits := memindex.Retrieve(c, chans, calibrationProbe, 1)
	if len(hits) > 0 {
		return hits[0]
	}
	return memindex.FileHit{}
}

// out is the result: under --json the value the skeleton renders, else its lines,
// printed here, with the exit they end with.
func (r retrievalResult) out(c *tool.Call) *tool.Out {
	token := "SEARCH"
	if r.Verb == "check" {
		token = "MEMORY"
	}
	if c.Bool("json") {
		out := tool.Done()
		out.Fact("k", r.K).Fact("channels", r.Channels).Fact("files", r.Files).Fact("chunks", r.Chunks)
		if r.Verb == "search" {
			out.Fact("query", r.Query).Fact("hits", len(r.Candidates[0].Hits))
		} else {
			out.Fact("source", r.Source).Fact("candidates", len(r.Candidates))
		}
		var calibrationScore, calibrationChannel any
		if r.Calibration.NativeChan != "" {
			calibrationScore = r.Calibration.Native
			calibrationChannel = r.Calibration.NativeChan
		}
		out.Item("cal", "score", calibrationScore, "score-channel", calibrationChannel, "probe", "unrelated-control")
		for i, cand := range r.Candidates {
			if r.Verb == "check" {
				out.Item("candidate", "n", i+1, "text", cand.Text)
			}
			if len(cand.Hits) == 0 {
				out.Item("miss", "candidate", i+1, "reason", "every query term is out of vocabulary for this corpus")
			}
			for j, h := range cand.Hits {
				out.Item("hit", "candidate", i+1, "rank", j+1, "score", h.Native, "score-channel", h.NativeChan, "fused", h.Fused, "class", h.Class, "name", h.FMName, "type", h.FMType, "root", h.Root, "file", h.File, "line", h.Line, "paragraph", h.Para, "snippet", h.Snippet)
			}
		}
		out.Notes = r.Notes
		return out
	}
	w := c.Stdout
	if r.Verb == "search" {
		fmt.Fprintf(w, "SEARCH OK hits=%d k=%d channels=%s files=%d chunks=%d: query=%s\n", len(r.Candidates[0].Hits), r.K, oneline.Field(r.Channels), r.Files, r.Chunks, oneline.Quote(r.Query))
	} else {
		fmt.Fprintf(w, "MEMORY OK candidates=%d source=%s k=%d channels=%s files=%d chunks=%d\n", len(r.Candidates), oneline.Field(r.Source), r.K, oneline.Field(r.Channels), r.Files, r.Chunks)
	}
	fmt.Fprintf(w, "%s CAL %s probe=unrelated-control\n", oneline.Field(token), scoreFields(r.Calibration.Native, r.Calibration.NativeChan))
	for i, cand := range r.Candidates {
		prefix := ""
		if r.Verb == "check" {
			fmt.Fprintf(w, "MEMORY CAND n=%d: %s\n", i+1, oneline.Quote(cand.Text))
			prefix = fmt.Sprintf("cand=%d ", i+1)
		}
		if len(cand.Hits) == 0 {
			fmt.Fprintf(w, "%s MISS %severy query term is out of vocabulary for this corpus\n", oneline.Field(token), oneline.Escape(prefix))
		}
		for j, h := range cand.Hits {
			fmt.Fprint(w, hitLine(token, prefix, j+1, h))
		}
	}
	for _, note := range r.Notes {
		fmt.Fprintf(w, "%s NOTE %s\n", oneline.Field(token), oneline.Escape(note))
	}
	return tool.Exit(0)
}
