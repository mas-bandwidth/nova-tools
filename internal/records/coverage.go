package records

import (
	"strconv"
	"time"
)

// Coverage plain-data types.
type (
	// CoverageInterval defines the interval start and end timestamps.
	CoverageInterval struct {
		Start string
		End   string
	}

	// CoverageReason defines one reason entry.
	CoverageReason struct {
		Code   string
		Source *string
	}

	// ShardRef defines a referenced shard and its disjoint tagged inventory.
	ShardRef struct {
		ShardID       string
		RecordCount   string
		InlineIDs     []string
		InventoryFile *string
	}

	// CoverageCounts defines the five non-negative integer string counts.
	CoverageCounts struct {
		SourceCandidates string
		RecordsEmitted   string
		Observations     string
		Conflicts        string
		Gaps             string
	}

	// Coverage is a validated nova.tokens.coverage/2 body.
	Coverage struct {
		Schema          string
		ScopeID         string
		SourceIDs       []string
		Interval        CoverageInterval
		Status          string
		Reasons         []CoverageReason
		CollectedAt     string
		CollectorBuild  string
		CollectorFriend *string
		CollectionBench *string
		MappingIDs      []string
		Shards          []ShardRef
		Predecessors    []string
		Counts          CoverageCounts
	}
)

var (
	coverageMembers = []string{
		"schema", "scope_id", "source_ids", "interval", "status",
		"reasons", "collected_at", "collector_build", "collector_friend",
		"collection_bench", "mapping_ids", "shards", "predecessors", "counts",
	}

	coverageStatuses = set([]string{"complete_within_scope", "partial", "unavailable"})

	coverageReasonCodes = set([]string{
		"source_unavailable", "unsupported_rows", "unknown_fields",
		"partial_interval", "conflict",
	})

	coverageCountFields = []string{
		"source_candidates", "records_emitted", "observations", "conflicts", "gaps",
	}
)

// CoverageMembers returns a copy of the 14 members of nova.tokens.coverage/2.
func CoverageMembers() []string {
	return append([]string(nil), coverageMembers...)
}

func (v *Validator) validateCoverage(body *Object) (*Coverage, error) {
	if err := v.exactKeys(body, "body", coverageMembers...); err != nil {
		return nil, err
	}

	c := &Coverage{Schema: SchemaCoverage}

	// 2. scope_id: ns
	scopeID, err := v.stringField(body, "body", "scope_id")
	if err != nil {
		return nil, err
	}
	if !namespaceLexeme.MatchString(scopeID) {
		if err := v.refused(RuleNamespaceSyntax, "body.scope_id", "scope_id matches ns grammar"); err != nil {
			return nil, err
		}
	}
	c.ScopeID = scopeID

	// 3. source_ids: [ns] sorted unique, non-empty
	sidsArr, err := v.array(body, "body", "source_ids")
	if err != nil {
		return nil, err
	}
	if len(sidsArr) == 0 {
		if err := v.refused(RuleEmptyArray, "body.source_ids", "source_ids is non-empty"); err != nil {
			return nil, err
		}
	}
	c.SourceIDs = make([]string, len(sidsArr))
	sourceIDsSet := make(map[string]bool, len(sidsArr))
	for i, elem := range sidsArr {
		s, ok := elem.(string)
		if !ok {
			return nil, refuse(RuleWrongType, indexPath("body.source_ids", i), "source_id is a string")
		}
		if !namespaceLexeme.MatchString(s) {
			if err := v.refused(RuleNamespaceSyntax, indexPath("body.source_ids", i), "source_id matches ns grammar"); err != nil {
				return nil, err
			}
		}
		c.SourceIDs[i] = s
		sourceIDsSet[s] = true
	}
	if err := v.sortedUnique(c.SourceIDs, "body.source_ids"); err != nil {
		return nil, err
	}

	// 4. interval: {start: ts, end: ts} both REQUIRED, start < end
	intObj, err := v.object(body, "body", "interval")
	if err != nil {
		return nil, err
	}
	if err := v.exactKeys(intObj, "body.interval", "start", "end"); err != nil {
		return nil, err
	}
	sVal, _ := intObj.Get("start")
	eVal, _ := intObj.Get("end")
	if sVal == nil || eVal == nil {
		if err := v.refused(RuleIntervalIncomplete, "body.interval", "an interval declares both start and end"); err != nil {
			return nil, err
		}
	}
	startStr, ok := sVal.(string)
	if !ok && sVal != nil {
		return nil, refuse(RuleWrongType, "body.interval.start", "interval start is a string")
	}
	endStr, ok := eVal.(string)
	if !ok && eVal != nil {
		return nil, refuse(RuleWrongType, "body.interval.end", "interval end is a string")
	}
	if err := v.timestamp(startStr, "body.interval.start"); err != nil {
		return nil, err
	}
	if err := v.timestamp(endStr, "body.interval.end"); err != nil {
		return nil, err
	}
	tStart, err := time.Parse(time.RFC3339, startStr)
	if err == nil {
		tEnd, err := time.Parse(time.RFC3339, endStr)
		if err == nil {
			if !tEnd.After(tStart) {
				if err := v.refused(RuleIntervalOrder, "body.interval", "interval end is after its start"); err != nil {
					return nil, err
				}
			}
		}
	}
	c.Interval = CoverageInterval{Start: startStr, End: endStr}

	// 5. status: ∈ {complete_within_scope, partial, unavailable}
	status, err := v.enumField(body, "body", "status", coverageStatuses)
	if err != nil {
		return nil, err
	}
	c.Status = status

	// 6. reasons: [{code, source}] sorted by (code, source)
	reasonsArr, err := v.array(body, "body", "reasons")
	if err != nil {
		return nil, err
	}
	c.Reasons = make([]CoverageReason, len(reasonsArr))
	for i, elem := range reasonsArr {
		rObj, ok := elem.(*Object)
		if !ok {
			return nil, refuse(RuleWrongType, indexPath("body.reasons", i), "a reason is an object")
		}
		if err := v.exactKeys(rObj, indexPath("body.reasons", i), "code", "source"); err != nil {
			return nil, err
		}
		code, err := v.enumField(rObj, indexPath("body.reasons", i), "code", coverageReasonCodes)
		if err != nil {
			return nil, err
		}
		sourceVal, _ := rObj.Get("source")
		var sourcePtr *string
		if sourceVal != nil {
			srcStr, ok := sourceVal.(string)
			if !ok {
				return nil, refuse(RuleWrongType, indexPath("body.reasons", i)+".source", "a reason source is a string or null")
			}
			if !namespaceLexeme.MatchString(srcStr) {
				if err := v.refused(RuleNamespaceSyntax, indexPath("body.reasons", i)+".source", "a reason source matches ns grammar"); err != nil {
					return nil, err
				}
			}
			if !sourceIDsSet[srcStr] {
				if err := v.refused(RuleCoverageReasonSource, indexPath("body.reasons", i)+".source", "a reason source is declared in source_ids"); err != nil {
					return nil, err
				}
			}
			sourcePtr = &srcStr
		}
		c.Reasons[i] = CoverageReason{Code: code, Source: sourcePtr}
	}
	// Sort and uniqueness check for reasons
	for i := 1; i < len(c.Reasons); i++ {
		prev := c.Reasons[i-1]
		curr := c.Reasons[i]
		cmp := compareReasons(prev, curr)
		if cmp == 0 {
			if err := v.refused(RuleDuplicateElement, indexPath("body.reasons", i), "the reason occurs twice"); err != nil {
				return nil, err
			}
		} else if cmp > 0 {
			if err := v.refused(RuleNotSorted, indexPath("body.reasons", i), "reasons are sorted by (code, source)"); err != nil {
				return nil, err
			}
		}
	}

	// 7. collected_at: ts, REQUIRED
	collAt, err := v.stringField(body, "body", "collected_at")
	if err != nil {
		return nil, err
	}
	if err := v.timestamp(collAt, "body.collected_at"); err != nil {
		return nil, err
	}
	c.CollectedAt = collAt

	// 8. collector_build: str
	cb, err := v.stringField(body, "body", "collector_build")
	if err != nil {
		return nil, err
	}
	if cb == "" {
		if err := v.refused(RuleEmptyString, "body.collector_build", "collector_build is non-empty"); err != nil {
			return nil, err
		}
	}
	c.CollectorBuild = cb

	// 9. collector_friend: label?
	cf, err := v.nullableLabel(body, "body", "collector_friend")
	if err != nil {
		return nil, err
	}
	c.CollectorFriend = cf

	// 10. collection_bench: label?
	cbench, err := v.nullableLabel(body, "body", "collection_bench")
	if err != nil {
		return nil, err
	}
	c.CollectionBench = cbench

	// 11. mapping_ids: [cid] sorted unique
	midsArr, err := v.array(body, "body", "mapping_ids")
	if err != nil {
		return nil, err
	}
	c.MappingIDs = make([]string, len(midsArr))
	for i, elem := range midsArr {
		s, ok := elem.(string)
		if !ok || !contentIDLexeme.MatchString(s) {
			if err := v.refused(RuleContentIDSyntax, indexPath("body.mapping_ids", i), "a mapping ID is a sha256 content ID"); err != nil {
				return nil, err
			}
		}
		c.MappingIDs[i] = s
	}
	if err := v.sortedUnique(c.MappingIDs, "body.mapping_ids"); err != nil {
		return nil, err
	}

	// 12. shards: [shard-ref] sorted unique by shard_id
	shardsArr, err := v.array(body, "body", "shards")
	if err != nil {
		return nil, err
	}
	c.Shards = make([]ShardRef, len(shardsArr))
	for i, elem := range shardsArr {
		sObj, ok := elem.(*Object)
		if !ok {
			return nil, refuse(RuleWrongType, indexPath("body.shards", i), "a shard reference is an object")
		}
		sRef, err := v.validateShardRef(sObj, indexPath("body.shards", i))
		if err != nil {
			return nil, err
		}
		c.Shards[i] = sRef
	}
	// Verify shards sorted unique by shard_id
	for i := 1; i < len(c.Shards); i++ {
		prev := c.Shards[i-1].ShardID
		curr := c.Shards[i].ShardID
		if curr == prev {
			if err := v.refused(RuleDuplicateElement, indexPath("body.shards", i), "duplicate shard ID"); err != nil {
				return nil, err
			}
		} else if curr < prev {
			if err := v.refused(RuleNotSorted, indexPath("body.shards", i), "shards are sorted unique by shard_id"); err != nil {
				return nil, err
			}
		}
	}

	// 13. predecessors: [cid] sorted unique
	predsArr, err := v.array(body, "body", "predecessors")
	if err != nil {
		return nil, err
	}
	c.Predecessors = make([]string, len(predsArr))
	for i, elem := range predsArr {
		s, ok := elem.(string)
		if !ok || !contentIDLexeme.MatchString(s) {
			if err := v.refused(RuleContentIDSyntax, indexPath("body.predecessors", i), "a predecessor ID is a sha256 content ID"); err != nil {
				return nil, err
			}
		}
		c.Predecessors[i] = s
	}
	if err := v.sortedUnique(c.Predecessors, "body.predecessors"); err != nil {
		return nil, err
	}

	// 14. counts: object of 5 integer string lexemes
	cntObj, err := v.object(body, "body", "counts")
	if err != nil {
		return nil, err
	}
	if err := v.exactKeys(cntObj, "body.counts", coverageCountFields...); err != nil {
		return nil, err
	}
	counts, err := v.validateCoverageCounts(cntObj, len(c.Reasons))
	if err != nil {
		return nil, err
	}
	c.Counts = counts

	return c, nil
}

func compareReasons(a, b CoverageReason) int {
	if a.Code < b.Code {
		return -1
	}
	if a.Code > b.Code {
		return 1
	}
	if a.Source == nil && b.Source == nil {
		return 0
	}
	if a.Source == nil && b.Source != nil {
		return -1
	}
	if a.Source != nil && b.Source == nil {
		return 1
	}
	if *a.Source < *b.Source {
		return -1
	}
	if *a.Source > *b.Source {
		return 1
	}
	return 0
}

func (v *Validator) validateShardRef(o *Object, path string) (ShardRef, error) {
	var s ShardRef

	_, hasShardID := o.Get("shard_id")
	_, hasRecordCount := o.Get("record_count")
	if !hasShardID || !hasRecordCount {
		if err := v.refused(RuleShardReference, path, "a shard reference requires shard_id and record_count"); err != nil {
			return s, err
		}
	}

	_, hasInline := o.Get("inline_ids")
	_, hasInv := o.Get("inventory_file")
	if (hasInline && hasInv) || (!hasInline && !hasInv) {
		if err := v.refused(RuleShardReference, path, "a shard reference has exactly one of inline_ids or inventory_file"); err != nil {
			return s, err
		}
	}

	var allowedKeys []string
	if hasInline {
		allowedKeys = []string{"shard_id", "record_count", "inline_ids"}
	} else {
		allowedKeys = []string{"shard_id", "record_count", "inventory_file"}
	}
	for _, k := range o.keys {
		if k != "shard_id" && k != "record_count" && k != "inline_ids" && k != "inventory_file" {
			if err := v.refused(RuleShardReference, path+"."+k, "unknown key in shard reference"); err != nil {
				return s, err
			}
		}
	}
	if err := v.exactKeys(o, path, allowedKeys...); err != nil {
		return s, err
	}

	sid, err := v.contentIDField(o, path, "shard_id")
	if err != nil {
		return s, err
	}
	s.ShardID = sid

	rcVal, _ := o.Get("record_count")
	if _, ok := rcVal.(interface{ String() string }); ok {
		return s, refuse(RuleRawJSONNumber, path+".record_count", "usage and record counts are JSON strings, never numbers")
	}
	rcStr, ok := rcVal.(string)
	if !ok {
		return s, refuse(RuleWrongType, path+".record_count", "record_count is a string lexeme")
	}
	if negativeLexeme.MatchString(rcStr) {
		if err := v.refused(RuleNegativeValue, path+".record_count", "record_count is never negative"); err != nil {
			return s, err
		}
	}
	if !integerLexeme.MatchString(rcStr) {
		if err := v.refused(RuleIntegerLexeme, path+".record_count", "record_count is an integer lexeme"); err != nil {
			return s, err
		}
	}
	rcInt, _ := strconv.Atoi(rcStr)
	if rcInt < 1 {
		if err := v.refused(RuleShardReference, path+".record_count", "record_count is at least 1"); err != nil {
			return s, err
		}
	}
	s.RecordCount = rcStr

	if hasInline {
		arrVal, _ := o.Get("inline_ids")
		arr, ok := arrVal.([]Value)
		if !ok {
			return s, refuse(RuleWrongType, path+".inline_ids", "inline_ids is an array")
		}
		s.InlineIDs = make([]string, len(arr))
		for i, elem := range arr {
			idStr, ok := elem.(string)
			if !ok || !contentIDLexeme.MatchString(idStr) {
				if err := v.refused(RuleContentIDSyntax, indexPath(path+".inline_ids", i), "an inline ID is a sha256 content ID"); err != nil {
					return s, err
				}
			}
			s.InlineIDs[i] = idStr
		}
		if err := v.sortedUnique(s.InlineIDs, path+".inline_ids"); err != nil {
			return s, err
		}
		if len(s.InlineIDs) != rcInt {
			if err := v.refused(RuleShardReference, path+".record_count", "record_count equals the number of inline IDs"); err != nil {
				return s, err
			}
		}
	} else {
		inv, err := v.contentIDField(o, path, "inventory_file")
		if err != nil {
			return s, err
		}
		s.InventoryFile = &inv
	}

	return s, nil
}

func (v *Validator) validateCoverageCounts(o *Object, reasonCount int) (CoverageCounts, error) {
	var c CoverageCounts

	checkCount := func(name string) (string, error) {
		field := "body.counts." + name
		val, _ := o.Get(name)
		if _, ok := val.(interface{ String() string }); ok {
			return "", refuse(RuleRawJSONNumber, field, "counts are JSON strings, never numbers")
		}
		s, ok := val.(string)
		if !ok {
			return "", refuse(RuleWrongType, field, "count is a string lexeme")
		}
		if negativeLexeme.MatchString(s) {
			if err := v.refused(RuleNegativeValue, field, "counts are never negative"); err != nil {
				return "", err
			}
		}
		if !integerLexeme.MatchString(s) {
			if err := v.refused(RuleIntegerLexeme, field, "count is an integer lexeme 0|[1-9][0-9]*"); err != nil {
				return "", err
			}
		}
		return s, nil
	}

	var err error
	if c.SourceCandidates, err = checkCount("source_candidates"); err != nil {
		return c, err
	}
	if c.RecordsEmitted, err = checkCount("records_emitted"); err != nil {
		return c, err
	}
	if c.Observations, err = checkCount("observations"); err != nil {
		return c, err
	}
	if c.Conflicts, err = checkCount("conflicts"); err != nil {
		return c, err
	}
	if c.Gaps, err = checkCount("gaps"); err != nil {
		return c, err
	}

	gapsInt, _ := strconv.Atoi(c.Gaps)
	if gapsInt != reasonCount {
		if err := v.refused(RuleIntegerLexeme, "body.counts.gaps", "gaps count equals the number of reasons"); err != nil {
			return c, err
		}
	}

	return c, nil
}

// Body encodes the coverage record to canonical JSON value representation.
func (c Coverage) Body() (Value, error) {
	b := newBuilder()
	b.set("schema", c.Schema)
	b.set("scope_id", c.ScopeID)
	b.set("source_ids", Strings(c.SourceIDs))

	b.setObject("interval", func(s *builder) {
		s.set("start", c.Interval.Start)
		s.set("end", c.Interval.End)
	})

	b.set("status", c.Status)

	reasonsVals := make([]Value, len(c.Reasons))
	for i, r := range c.Reasons {
		ro := NewObject()
		_ = ro.Set("code", r.Code)
		_ = ro.Set("source", nullable(r.Source))
		reasonsVals[i] = ro
	}
	b.set("reasons", reasonsVals)

	b.set("collected_at", c.CollectedAt)
	b.set("collector_build", c.CollectorBuild)
	b.set("collector_friend", nullable(c.CollectorFriend))
	b.set("collection_bench", nullable(c.CollectionBench))
	b.set("mapping_ids", Strings(c.MappingIDs))

	shardsVals := make([]Value, len(c.Shards))
	for i, s := range c.Shards {
		so := NewObject()
		_ = so.Set("shard_id", s.ShardID)
		_ = so.Set("record_count", s.RecordCount)
		if s.InlineIDs != nil {
			_ = so.Set("inline_ids", Strings(s.InlineIDs))
		}
		if s.InventoryFile != nil {
			_ = so.Set("inventory_file", *s.InventoryFile)
		}
		shardsVals[i] = so
	}
	b.set("shards", shardsVals)

	b.set("predecessors", Strings(c.Predecessors))

	b.setObject("counts", func(s *builder) {
		s.set("source_candidates", c.Counts.SourceCandidates)
		s.set("records_emitted", c.Counts.RecordsEmitted)
		s.set("observations", c.Counts.Observations)
		s.set("conflicts", c.Counts.Conflicts)
		s.set("gaps", c.Counts.Gaps)
	})

	return b.done()
}

// SealCoverage seals a Coverage record and strictly validates its own bytes.
func SealCoverage(c Coverage) ([]byte, string, error) {
	body, err := c.Body()
	if err != nil {
		return nil, "", err
	}
	raw, id, err := Seal(body)
	if err != nil {
		return nil, "", err
	}
	if _, err := (&Validator{}).ValidateEnvelope(raw); err != nil {
		return nil, "", err
	}
	return raw, id, nil
}
