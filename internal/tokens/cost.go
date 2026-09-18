package tokens

import "fmt"

// CostReceiptRow is one session folded by the `receipt` verb, joined to one node and one
// of the six stages under a drawn 32-hex id. It is the unit `cost --node` folds: every
// stage of one node summed, so the cost of an attempt is the cost of all of its stages,
// review and repair counted, never merely the builder's tokens.
//
// It is the priced projection of a receipt, not the receipt file's row: ReceiptRow in
// receipt.go is the seventeen columns as written, this is the node, the stage and what a
// rate table made of them. The two were both called ReceiptRow when they were cut in the
// same batch; this one carries the name of what it folds.
//
// PricedTokens are the tokens a caller's dated rate table covered; UnpricedTokens are the
// tokens no rate covered. A missing rate is unpriced, never zero (NEXT-TOOLS): the two are
// kept apart so an unpriced slice stays visible rather than becoming free spend. Usd is
// micro-dollars over the priced tokens only, and 0 where none were priced.
type CostReceiptRow struct {
	NodeID         string
	Stage          string
	PricedTokens   int64
	UnpricedTokens int64
	Usd            int64 // micro-dollars; 0 where the receipt carried no rate
}

// NodeCostSummary is one node folded across all of its stages: total tokens, total cost,
// the blended dollars-per-million-tokens, and the priced/unpriced split of the tokens.
type NodeCostSummary struct {
	NodeID         string
	Tokens         int64   // priced + unpriced, over every stage that named the node
	Usd            int64   // micro-dollars, summed over the priced stages
	PerMtok        float64 // dollars per million tokens; 0 where Tokens is 0
	PricedTokens   int64
	UnpricedTokens int64
}

// AggregateNodeCost sums every receipt that names nodeID across all stages. A receipt for
// another node is skipped; an empty slice, or a node no receipt named, returns the zero
// summary without error, because a node with no receipts is a node whose cost is nothing,
// not a failure.
func AggregateNodeCost(receipts []CostReceiptRow, nodeID string) NodeCostSummary {
	s := NodeCostSummary{NodeID: nodeID}
	for _, r := range receipts {
		if r.NodeID != nodeID {
			continue
		}
		s.PricedTokens += r.PricedTokens
		s.UnpricedTokens += r.UnpricedTokens
		s.Usd += r.Usd
	}
	s.Tokens = s.PricedTokens + s.UnpricedTokens
	if s.Tokens > 0 {
		s.PerMtok = float64(s.Usd) / float64(s.Tokens)
	}
	return s
}

// FormatNodeCost renders one node's summary as the single line `cost --node` prints. usd
// and per_mtok are dollars to four decimals; per_mtok is 0.0000 over no tokens, never a
// division.
func FormatNodeCost(s NodeCostSummary) string {
	return fmt.Sprintf("tokens=%d usd=%.4f per_mtok=%.4f priced_tokens=%d unpriced_tokens=%d",
		s.Tokens, float64(s.Usd)/1_000_000, s.PerMtok, s.PricedTokens, s.UnpricedTokens)
}
