package records

import (
	"regexp"
)

// Mapping plain-data types.
type (
	// MappingFieldRule defines the 8 members of each numeric source field rule.
	MappingFieldRule struct {
		NumberKind      string
		ZeroSemantics   string
		Unit            string
		SpendRole       string
		AbsentPresence  string
		AbsentReason    string
		InvalidPresence string
		InvalidReason   string
	}

	// MappingIdentityRule defines the closed identity rule for reviewed adapters.
	MappingIdentityRule struct {
		SourceKind               string
		Namespace                string
		EventKey                 []string
		ObservationKind          string
		NormalizedSpendSupported bool
		ProducerVersionFrom      string
		SessionID                string
		ReceiptFields            []string
		ReceiptValueType         string
		// Codex-specific:
		ContainingOrForkedSessionInEventKey *bool
		// Grok-specific:
		CollectionTimestampSubstitution *bool
		ContainingSessionSubstitution   *bool
		UnsupportedReason               *string
	}

	// MappingRevisionRule defines the 7 revision rule members.
	MappingRevisionRule struct {
		Basis                             string
		Native                            *string
		Supersedes                        []string
		IdenticalCopy                     string
		ChangedSameKey                    string
		InferredOrderFromExportUpdateTime bool
		NewestWins                        bool
	}

	// MappingMissingTimestamp represents {basis, occurred_at}.
	MappingMissingTimestamp struct {
		Basis      string
		OccurredAt *string
	}

	// MappingTimeRule defines time rule members.
	MappingTimeRule struct {
		Basis            string
		OccurredAt       string
		Offsets          string
		DayAllocation    string
		Start            *string
		End              *string
		MissingTimestamp *MappingMissingTimestamp
		// Grok-specific:
		CrossMidnight *string
	}

	// MappingModelBasisID represents nested {basis, id}.
	MappingModelBasisID struct {
		Basis string
		ID    *string
	}

	// MappingModelRule defines model rule members.
	MappingModelRule struct {
		ForbiddenWireKeys []string
		// Codex-specific:
		Basis                              *string
		ID                                 *string
		Absent                             *MappingModelBasisID
		LaterModelRelabelsEarlierResponses *bool
		ModelUsage                         []string
		// Grok-specific:
		SingleReportedID              *MappingModelBasisID
		AbsentID                      *MappingModelBasisID
		MultipleModelIDs              *MappingModelBasisID
		SplitNormalized               *bool
		AggregateIsCountingCandidate  *bool
		DetailRetainedNotCountedAgain *bool
		ModelUsageFields              []string
		ModelUsageSortedBy            *string
	}

	// MappingOverlapRule defines overlap rule members.
	MappingOverlapRule struct {
		OwedCoverageTasks []string
		// Codex-specific:
		CountingSource                            *string
		ArithmeticMismatch                        *string
		MissingRawTotal                           *string
		ThreadTokenUsage                          *string
		TurnTokenUsage                            *string
		TokenCountSnapshots                       *string
		SummedOrMixedWithPreferred                *bool
		ReportMustNameUnhandledTokenCountCoverage *bool
		// Grok-specific:
		CountingGrain                                     *string
		ArithmeticViolation                               *string
		EvidencedOverlappingGrain                         *string
		RequestGrainMapping                               *string
		SessionTotals                                     *string
		ForbiddenUnions                                   []string
		ChecksumRequiresEvidencedCompleteMatchingCoverage *bool
		SessionTotalsFillMissingTurnSpend                 *bool
	}

	// Mapping is a validated nova.tokens.mapping/2 body.
	Mapping struct {
		Schema           string
		Name             string
		Revision         string
		SourceShapes     []string
		FieldRules       map[string]MappingFieldRule
		IdentityRule     MappingIdentityRule
		RevisionRule     MappingRevisionRule
		TimeRule         MappingTimeRule
		ModelRule        MappingModelRule
		OverlapRule      MappingOverlapRule
		FixtureDigests   map[string]string
		ImplementationID *string
	}
)

var (
	mappingMembers = []string{
		"schema", "name", "revision", "source_shapes", "field_rules",
		"identity_rule", "revision_rule", "time_rule", "model_rule",
		"overlap_rule", "fixture_digests", "implementation_id",
	}

	fieldRuleMembers = []string{
		"number_kind", "zero_semantics", "unit", "spend_role",
		"absent_presence", "absent_reason", "invalid_presence", "invalid_reason",
	}

	revisionLexeme = regexp.MustCompile(`^[a-z0-9._-]{1,64}$`)
	fieldKeyLexeme = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)
	implIDLexeme   = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}@[a-z0-9._-]{1,64} build=[A-Za-z0-9._-]{1,64}$`)

	zeroSemanticsSet = set([]string{"measured", "default_may_mask_absence", "unknown"})
	spendRolesSet    = set([]string{"base_counter", "subset_detail", "non_spend"})
)

// MappingMembers returns a copy of the 12 members of nova.tokens.mapping/2.
func MappingMembers() []string {
	return append([]string(nil), mappingMembers...)
}

func (v *Validator) validateMapping(body *Object) (*Mapping, error) {
	if err := v.exactKeys(body, "body", mappingMembers...); err != nil {
		return nil, err
	}

	m := &Mapping{Schema: SchemaMapping}

	// 2. name: label
	name, err := v.stringField(body, "body", "name")
	if err != nil {
		return nil, err
	}
	if !labelLexeme.MatchString(name) {
		if err := v.refused(RuleLabelSyntax, "body.name", "a name is a label: [a-z0-9][a-z0-9-]{0,31}"); err != nil {
			return nil, err
		}
	}
	m.Name = name

	// 3. revision: str, [a-z0-9._-]{1,64}
	rev, err := v.stringField(body, "body", "revision")
	if err != nil {
		return nil, err
	}
	if rev == "" {
		if err := v.refused(RuleEmptyString, "body.revision", "revision is non-empty"); err != nil {
			return nil, err
		}
	}
	if !revisionLexeme.MatchString(rev) {
		if err := v.refused(RuleNamespaceSyntax, "body.revision", "revision matches [a-z0-9._-]{1,64}"); err != nil {
			return nil, err
		}
	}
	m.Revision = rev

	// 4. source_shapes: [str] sorted unique; each element a dotted shape id (ns)
	ssArr, err := v.array(body, "body", "source_shapes")
	if err != nil {
		return nil, err
	}
	m.SourceShapes = make([]string, len(ssArr))
	for i, elem := range ssArr {
		s, ok := elem.(string)
		if !ok {
			return nil, refuse(RuleWrongType, indexPath("body.source_shapes", i), "source shape is a string")
		}
		if s == "" {
			if err := v.refused(RuleEmptyString, indexPath("body.source_shapes", i), "source shape is non-empty"); err != nil {
				return nil, err
			}
		}
		if !namespaceLexeme.MatchString(s) {
			if err := v.refused(RuleNamespaceSyntax, indexPath("body.source_shapes", i), "source shape matches ns grammar"); err != nil {
				return nil, err
			}
		}
		m.SourceShapes[i] = s
	}
	if err := v.sortedUnique(m.SourceShapes, "body.source_shapes"); err != nil {
		return nil, err
	}

	// 5. field_rules: map of field rules
	frObj, err := v.object(body, "body", "field_rules")
	if err != nil {
		return nil, err
	}
	m.FieldRules = make(map[string]MappingFieldRule, len(frObj.Keys()))
	for _, k := range frObj.Keys() {
		if k == "" {
			if err := v.refused(RuleEmptyString, "body.field_rules", "field rule key is non-empty"); err != nil {
				return nil, err
			}
		}
		ruleVal, _ := frObj.Get(k)
		rObj, ok := ruleVal.(*Object)
		if !ok {
			return nil, refuse(RuleWrongType, "body.field_rules."+k, "a field rule is an object")
		}
		fr, err := v.validateFieldRule(rObj, "body.field_rules."+k)
		if err != nil {
			return nil, err
		}
		m.FieldRules[k] = fr
	}

	// 6. identity_rule: adapter-specific
	idObj, err := v.object(body, "body", "identity_rule")
	if err != nil {
		return nil, err
	}
	m.IdentityRule, err = v.validateIdentityRule(idObj)
	if err != nil {
		return nil, err
	}

	// 7. revision_rule
	revObj, err := v.object(body, "body", "revision_rule")
	if err != nil {
		return nil, err
	}
	m.RevisionRule, err = v.validateRevisionRule(revObj)
	if err != nil {
		return nil, err
	}

	// 8. time_rule
	timeObj, err := v.object(body, "body", "time_rule")
	if err != nil {
		return nil, err
	}
	m.TimeRule, err = v.validateTimeRule(timeObj, m.IdentityRule.SourceKind)
	if err != nil {
		return nil, err
	}

	// 9. model_rule
	modelObj, err := v.object(body, "body", "model_rule")
	if err != nil {
		return nil, err
	}
	m.ModelRule, err = v.validateModelRule(modelObj, m.IdentityRule.SourceKind)
	if err != nil {
		return nil, err
	}

	// 10. overlap_rule
	overlapObj, err := v.object(body, "body", "overlap_rule")
	if err != nil {
		return nil, err
	}
	m.OverlapRule, err = v.validateOverlapRule(overlapObj, m.IdentityRule.SourceKind)
	if err != nil {
		return nil, err
	}

	// 11. fixture_digests: map str -> cid
	fdObj, err := v.object(body, "body", "fixture_digests")
	if err != nil {
		return nil, err
	}
	m.FixtureDigests = make(map[string]string, len(fdObj.Keys()))
	for _, k := range fdObj.Keys() {
		if k == "" {
			if err := v.refused(RuleEmptyString, "body.fixture_digests", "fixture filename is non-empty"); err != nil {
				return nil, err
			}
		}
		cid, err := v.contentIDField(fdObj, "body.fixture_digests", k)
		if err != nil {
			return nil, err
		}
		m.FixtureDigests[k] = cid
	}

	// 12. implementation_id: str? (null legal for proposal)
	impl, err := v.nullableString(body, "body", "implementation_id")
	if err != nil {
		return nil, err
	}
	if impl != nil {
		if *impl == "" {
			if err := v.refused(RuleEmptyString, "body.implementation_id", "implementation_id is non-empty"); err != nil {
				return nil, err
			}
		}
		if !implIDLexeme.MatchString(*impl) {
			if err := v.refused(RuleWrongType, "body.implementation_id", "an implementation ID is <adapter>@<version> build=<build-id>"); err != nil {
				return nil, err
			}
		}
	}
	m.ImplementationID = impl

	return m, nil
}

func (v *Validator) validateFieldRule(o *Object, path string) (MappingFieldRule, error) {
	var fr MappingFieldRule

	// Required zero_semantics check: absent => RuleMappingMissingZeroSemantics
	if _, ok := o.Get("zero_semantics"); !ok {
		if err := v.refused(RuleMappingMissingZeroSemantics, path+".zero_semantics", "a field rule declares zero_semantics"); err != nil {
			return fr, err
		}
	}

	// Check exact members (ignoring zero_semantics if it was skipped above)
	wants := fieldRuleMembers
	if _, ok := o.Get("zero_semantics"); !ok && v.skip[RuleMappingMissingZeroSemantics] {
		wants = []string{
			"number_kind", "unit", "spend_role", "absent_presence",
			"absent_reason", "invalid_presence", "invalid_reason",
		}
	}
	if err := v.exactKeys(o, path, wants...); err != nil {
		return fr, err
	}

	nk, err := v.enumField(o, path, "number_kind", numberKinds)
	if err != nil {
		return fr, err
	}
	fr.NumberKind = nk

	if _, ok := o.Get("zero_semantics"); ok {
		zs, err := v.enumField(o, path, "zero_semantics", zeroSemanticsSet)
		if err != nil {
			return fr, err
		}
		fr.ZeroSemantics = zs
	}

	unit, err := v.stringField(o, path, "unit")
	if err != nil {
		return fr, err
	}
	if unit == "" {
		if err := v.refused(RuleEmptyString, path+".unit", "unit is non-empty"); err != nil {
			return fr, err
		}
	}
	if !unitLexeme.MatchString(unit) {
		if err := v.refused(RuleUnitSyntax, path+".unit", "a unit is [a-z0-9][a-z0-9_-]{0,31}"); err != nil {
			return fr, err
		}
	}
	fr.Unit = unit

	sr, err := v.enumField(o, path, "spend_role", spendRolesSet)
	if err != nil {
		return fr, err
	}
	fr.SpendRole = sr

	ap, err := v.enumField(o, path, "absent_presence", presences)
	if err != nil {
		return fr, err
	}
	fr.AbsentPresence = ap

	ar, err := v.enumField(o, path, "absent_reason", reasonCodes)
	if err != nil {
		return fr, err
	}
	fr.AbsentReason = ar

	ip, err := v.enumField(o, path, "invalid_presence", presences)
	if err != nil {
		return fr, err
	}
	fr.InvalidPresence = ip

	ir, err := v.enumField(o, path, "invalid_reason", reasonCodes)
	if err != nil {
		return fr, err
	}
	fr.InvalidReason = ir

	return fr, nil
}

func (v *Validator) mappingNonEmptyString(o *Object, path, name string) (string, error) {
	val, err := v.member(o, path, name)
	if err != nil {
		return "", err
	}
	s, ok := val.(string)
	if !ok {
		return "", v.refused(RuleMappingRuleShape, path+"."+name, "expected string")
	}
	if s == "" {
		if err := v.refused(RuleEmptyString, path+"."+name, name+" is non-empty"); err != nil {
			return "", err
		}
	}
	return s, nil
}

func (v *Validator) mappingNullableNonEmptyString(o *Object, path, name string) (*string, error) {
	val, err := v.member(o, path, name)
	if err != nil {
		return nil, err
	}
	if val == nil {
		return nil, nil
	}
	s, ok := val.(string)
	if !ok {
		return nil, v.refused(RuleMappingRuleShape, path+"."+name, "expected string or null")
	}
	if s == "" {
		if err := v.refused(RuleEmptyString, path+"."+name, name+" is non-empty"); err != nil {
			return nil, err
		}
	}
	return &s, nil
}

func (v *Validator) mappingStringArray(o *Object, path, name string) ([]string, error) {
	arr, err := v.array(o, path, name)
	if err != nil {
		return nil, v.refused(RuleMappingRuleShape, path+"."+name, "expected array")
	}
	res := make([]string, len(arr))
	for i, elem := range arr {
		s, ok := elem.(string)
		if !ok {
			return nil, v.refused(RuleMappingRuleShape, indexPath(path+"."+name, i), "expected string")
		}
		if s == "" {
			if err := v.refused(RuleEmptyString, indexPath(path+"."+name, i), "array element is non-empty"); err != nil {
				return nil, err
			}
		}
		res[i] = s
	}
	return res, nil
}

func (v *Validator) exactRuleKeys(o *Object, path string, want ...string) error {
	wants := set(want)
	for _, k := range o.keys {
		if !wants[k] {
			if err := v.refused(RuleMappingRuleShape, path+"."+k, "key outside adapter rule shape"); err != nil {
				return err
			}
		}
	}
	present := map[string]bool{}
	for _, k := range o.keys {
		present[k] = true
	}
	for _, w := range want {
		if !present[w] {
			if err := v.refused(RuleMappingRuleShape, path+"."+w, "missing key in adapter rule shape"); err != nil {
				return err
			}
		}
	}
	return nil
}

func (v *Validator) validateIdentityRule(o *Object) (MappingIdentityRule, error) {
	var id MappingIdentityRule

	skVal, ok := o.Get("source_kind")
	if !ok {
		return id, v.refused(RuleMappingRuleShape, "body.identity_rule.source_kind", "source_kind is required")
	}
	sk, ok := skVal.(string)
	if !ok {
		return id, v.refused(RuleMappingRuleShape, "body.identity_rule.source_kind", "source_kind is a string")
	}
	if sk != "codex_desktop" && sk != "grok" {
		if err := v.refused(RuleMappingRuleShape, "body.identity_rule.source_kind", "mapping rule shape is defined for codex_desktop and grok"); err != nil {
			return id, err
		}
	}
	id.SourceKind = sk

	var wantKeys []string
	if sk == "codex_desktop" {
		wantKeys = []string{
			"source_kind", "namespace", "event_key", "observation_kind",
			"normalized_spend_supported", "producer_version_from", "session_id",
			"receipt_fields", "receipt_value_type",
			"containing_or_forked_session_in_event_key",
		}
	} else {
		wantKeys = []string{
			"source_kind", "namespace", "event_key", "observation_kind",
			"normalized_spend_supported", "producer_version_from", "session_id",
			"receipt_fields", "receipt_value_type",
			"collection_timestamp_substitution", "containing_session_substitution",
			"unsupported_reason",
		}
	}
	if err := v.exactRuleKeys(o, "body.identity_rule", wantKeys...); err != nil {
		return id, err
	}

	ns, err := v.mappingNonEmptyString(o, "body.identity_rule", "namespace")
	if err != nil {
		return id, err
	}
	if !namespaceLexeme.MatchString(ns) {
		if err := v.refused(RuleNamespaceSyntax, "body.identity_rule.namespace", "namespace matches ns grammar"); err != nil {
			return id, err
		}
	}
	id.Namespace = ns

	ekArr, err := v.mappingStringArray(o, "body.identity_rule", "event_key")
	if err != nil {
		return id, err
	}
	if len(ekArr) == 0 {
		if err := v.refused(RuleEmptyArray, "body.identity_rule.event_key", "event_key is non-empty"); err != nil {
			return id, err
		}
	}
	id.EventKey = ekArr

	okind, err := v.enumField(o, "body.identity_rule", "observation_kind", observationKinds)
	if err != nil {
		return id, err
	}
	id.ObservationKind = okind

	normSup, ok := o.Get("normalized_spend_supported")
	if b, ok := normSup.(bool); ok {
		id.NormalizedSpendSupported = b
	} else {
		return id, v.refused(RuleMappingRuleShape, "body.identity_rule.normalized_spend_supported", "expected boolean")
	}

	pvf, err := v.mappingNonEmptyString(o, "body.identity_rule", "producer_version_from")
	if err != nil {
		return id, err
	}
	id.ProducerVersionFrom = pvf

	sessID, err := v.mappingNonEmptyString(o, "body.identity_rule", "session_id")
	if err != nil {
		return id, err
	}
	id.SessionID = sessID

	rfArr, err := v.mappingStringArray(o, "body.identity_rule", "receipt_fields")
	if err != nil {
		return id, err
	}
	for i, s := range rfArr {
		if !fieldKeyLexeme.MatchString(s) {
			if err := v.refused(RuleLabelSyntax, indexPath("body.identity_rule.receipt_fields", i), "expected field_key"); err != nil {
				return id, err
			}
		}
	}
	if err := v.sortedUnique(rfArr, "body.identity_rule.receipt_fields"); err != nil {
		return id, err
	}
	id.ReceiptFields = rfArr

	rvt, err := v.mappingNonEmptyString(o, "body.identity_rule", "receipt_value_type")
	if err != nil || rvt != "string" {
		return id, v.refused(RuleMappingRuleShape, "body.identity_rule.receipt_value_type", "receipt_value_type must be string")
	}
	id.ReceiptValueType = rvt

	if sk == "codex_desktop" {
		cVal, _ := o.Get("containing_or_forked_session_in_event_key")
		if b, ok := cVal.(bool); ok {
			id.ContainingOrForkedSessionInEventKey = &b
		} else {
			return id, v.refused(RuleMappingRuleShape, "body.identity_rule.containing_or_forked_session_in_event_key", "expected boolean")
		}
	} else {
		tsVal, _ := o.Get("collection_timestamp_substitution")
		if b, ok := tsVal.(bool); ok {
			id.CollectionTimestampSubstitution = &b
		} else {
			return id, v.refused(RuleMappingRuleShape, "body.identity_rule.collection_timestamp_substitution", "expected boolean")
		}
		csVal, _ := o.Get("containing_session_substitution")
		if b, ok := csVal.(bool); ok {
			id.ContainingSessionSubstitution = &b
		} else {
			return id, v.refused(RuleMappingRuleShape, "body.identity_rule.containing_session_substitution", "expected boolean")
		}
		urVal, err := v.mappingNonEmptyString(o, "body.identity_rule", "unsupported_reason")
		if err != nil {
			return id, err
		}
		id.UnsupportedReason = &urVal
	}

	return id, nil
}

func (v *Validator) validateRevisionRule(o *Object) (MappingRevisionRule, error) {
	var rev MappingRevisionRule

	wantKeys := []string{
		"basis", "native", "supersedes", "identical_copy", "changed_same_key",
		"inferred_order_from_export_update_time", "newest_wins",
	}
	if err := v.exactRuleKeys(o, "body.revision_rule", wantKeys...); err != nil {
		return rev, err
	}

	basis, err := v.enumField(o, "body.revision_rule", "basis", revisionBases)
	if err != nil {
		return rev, err
	}
	rev.Basis = basis

	nat, err := v.mappingNullableNonEmptyString(o, "body.revision_rule", "native")
	if err != nil {
		return rev, err
	}
	rev.Native = nat

	supArr, err := v.mappingStringArray(o, "body.revision_rule", "supersedes")
	if err != nil {
		return rev, err
	}
	for i, s := range supArr {
		if !contentIDLexeme.MatchString(s) {
			if err := v.refused(RuleContentIDSyntax, indexPath("body.revision_rule.supersedes", i), "content ID syntax"); err != nil {
				return rev, err
			}
		}
	}
	if err := v.sortedUnique(supArr, "body.revision_rule.supersedes"); err != nil {
		return rev, err
	}
	rev.Supersedes = supArr

	ic, err := v.mappingNonEmptyString(o, "body.revision_rule", "identical_copy")
	if err != nil {
		return rev, err
	}
	rev.IdenticalCopy = ic

	csk, err := v.mappingNonEmptyString(o, "body.revision_rule", "changed_same_key")
	if err != nil {
		return rev, err
	}
	rev.ChangedSameKey = csk

	ioVal, _ := o.Get("inferred_order_from_export_update_time")
	if b, ok := ioVal.(bool); ok {
		rev.InferredOrderFromExportUpdateTime = b
	} else {
		return rev, v.refused(RuleMappingRuleShape, "body.revision_rule.inferred_order_from_export_update_time", "expected boolean")
	}

	nwVal, _ := o.Get("newest_wins")
	if b, ok := nwVal.(bool); ok {
		rev.NewestWins = b
	} else {
		return rev, v.refused(RuleMappingRuleShape, "body.revision_rule.newest_wins", "expected boolean")
	}

	return rev, nil
}

func (v *Validator) validateTimeRule(o *Object, sk string) (MappingTimeRule, error) {
	var tr MappingTimeRule

	wantKeys := []string{"basis", "occurred_at", "offsets", "day_allocation", "start", "end", "missing_timestamp"}
	if sk == "grok" {
		wantKeys = append(wantKeys, "cross_midnight")
	}
	if err := v.exactRuleKeys(o, "body.time_rule", wantKeys...); err != nil {
		return tr, err
	}

	basis, err := v.enumField(o, "body.time_rule", "basis", timeBases)
	if err != nil {
		return tr, err
	}
	tr.Basis = basis

	occ, err := v.mappingNonEmptyString(o, "body.time_rule", "occurred_at")
	if err != nil {
		return tr, err
	}
	tr.OccurredAt = occ

	off, err := v.mappingNonEmptyString(o, "body.time_rule", "offsets")
	if err != nil {
		return tr, err
	}
	tr.Offsets = off

	da, err := v.mappingNonEmptyString(o, "body.time_rule", "day_allocation")
	if err != nil {
		return tr, err
	}
	tr.DayAllocation = da

	st, err := v.mappingNullableNonEmptyString(o, "body.time_rule", "start")
	if err != nil {
		return tr, err
	}
	tr.Start = st

	end, err := v.mappingNullableNonEmptyString(o, "body.time_rule", "end")
	if err != nil {
		return tr, err
	}
	tr.End = end

	mtVal, _ := o.Get("missing_timestamp")
	if mtVal != nil {
		mtObj, ok := mtVal.(*Object)
		if !ok {
			return tr, v.refused(RuleMappingRuleShape, "body.time_rule.missing_timestamp", "expected object or null")
		}
		if err := v.exactRuleKeys(mtObj, "body.time_rule.missing_timestamp", "basis", "occurred_at"); err != nil {
			return tr, err
		}
		mtb, err := v.enumField(mtObj, "body.time_rule.missing_timestamp", "basis", timeBases)
		if err != nil {
			return tr, err
		}
		mtocc, err := v.mappingNullableNonEmptyString(mtObj, "body.time_rule.missing_timestamp", "occurred_at")
		if err != nil {
			return tr, err
		}
		tr.MissingTimestamp = &MappingMissingTimestamp{Basis: mtb, OccurredAt: mtocc}
	}

	if sk == "grok" {
		cm, err := v.mappingNonEmptyString(o, "body.time_rule", "cross_midnight")
		if err != nil {
			return tr, err
		}
		tr.CrossMidnight = &cm
	}

	return tr, nil
}

func (v *Validator) validateModelBasisID(o *Object, path string, idRequired bool) (*MappingModelBasisID, error) {
	if err := v.exactRuleKeys(o, path, "basis", "id"); err != nil {
		return nil, err
	}
	b, err := v.enumField(o, path, "basis", modelBases)
	if err != nil {
		return nil, err
	}
	id, err := v.mappingNullableNonEmptyString(o, path, "id")
	if err != nil {
		return nil, err
	}
	if idRequired && id == nil {
		return nil, v.refused(RuleMappingRuleShape, path+".id", "id is required")
	}
	if !idRequired && id != nil {
		return nil, v.refused(RuleMappingRuleShape, path+".id", "id must be null")
	}
	return &MappingModelBasisID{Basis: b, ID: id}, nil
}

func (v *Validator) validateModelRule(o *Object, sk string) (MappingModelRule, error) {
	var mr MappingModelRule

	var wantKeys []string
	if sk == "codex_desktop" {
		wantKeys = []string{
			"basis", "id", "absent", "later_model_relabels_earlier_responses",
			"forbidden_wire_keys", "model_usage",
		}
	} else {
		wantKeys = []string{
			"single_reported_id", "absent_id", "multiple_model_ids", "split_normalized",
			"aggregate_is_counting_candidate", "detail_retained_not_counted_again",
			"model_usage_fields", "model_usage_sorted_by", "forbidden_wire_keys",
		}
	}
	if err := v.exactRuleKeys(o, "body.model_rule", wantKeys...); err != nil {
		return mr, err
	}

	fwkArr, err := v.mappingStringArray(o, "body.model_rule", "forbidden_wire_keys")
	if err != nil {
		return mr, err
	}
	if err := v.sortedUnique(fwkArr, "body.model_rule.forbidden_wire_keys"); err != nil {
		return mr, err
	}
	mr.ForbiddenWireKeys = fwkArr

	if sk == "codex_desktop" {
		b, err := v.enumField(o, "body.model_rule", "basis", modelBases)
		if err != nil {
			return mr, err
		}
		mr.Basis = &b

		id, err := v.mappingNonEmptyString(o, "body.model_rule", "id")
		if err != nil {
			return mr, err
		}
		mr.ID = &id

		absObj, err := v.object(o, "body.model_rule", "absent")
		if err != nil {
			return mr, v.refused(RuleMappingRuleShape, "body.model_rule.absent", "expected object")
		}
		mr.Absent, err = v.validateModelBasisID(absObj, "body.model_rule.absent", false)
		if err != nil {
			return mr, err
		}

		lmVal, _ := o.Get("later_model_relabels_earlier_responses")
		if b, ok := lmVal.(bool); ok {
			mr.LaterModelRelabelsEarlierResponses = &b
		} else {
			return mr, v.refused(RuleMappingRuleShape, "body.model_rule.later_model_relabels_earlier_responses", "expected boolean")
		}

		muArr, err := v.array(o, "body.model_rule", "model_usage")
		if err != nil {
			return mr, v.refused(RuleMappingRuleShape, "body.model_rule.model_usage", "expected array")
		}
		if len(muArr) != 0 {
			return mr, v.refused(RuleMappingRuleShape, "body.model_rule.model_usage", "codex model_usage must be empty array []")
		}
		mr.ModelUsage = []string{}
	} else {
		sObj, err := v.object(o, "body.model_rule", "single_reported_id")
		if err != nil {
			return mr, v.refused(RuleMappingRuleShape, "body.model_rule.single_reported_id", "expected object")
		}
		mr.SingleReportedID, err = v.validateModelBasisID(sObj, "body.model_rule.single_reported_id", true)
		if err != nil {
			return mr, err
		}

		aObj, err := v.object(o, "body.model_rule", "absent_id")
		if err != nil {
			return mr, v.refused(RuleMappingRuleShape, "body.model_rule.absent_id", "expected object")
		}
		mr.AbsentID, err = v.validateModelBasisID(aObj, "body.model_rule.absent_id", false)
		if err != nil {
			return mr, err
		}

		mObj, err := v.object(o, "body.model_rule", "multiple_model_ids")
		if err != nil {
			return mr, v.refused(RuleMappingRuleShape, "body.model_rule.multiple_model_ids", "expected object")
		}
		mr.MultipleModelIDs, err = v.validateModelBasisID(mObj, "body.model_rule.multiple_model_ids", false)
		if err != nil {
			return mr, err
		}

		snVal, _ := o.Get("split_normalized")
		if b, ok := snVal.(bool); ok {
			mr.SplitNormalized = &b
		} else {
			return mr, v.refused(RuleMappingRuleShape, "body.model_rule.split_normalized", "expected boolean")
		}

		agVal, _ := o.Get("aggregate_is_counting_candidate")
		if b, ok := agVal.(bool); ok {
			mr.AggregateIsCountingCandidate = &b
		} else {
			return mr, v.refused(RuleMappingRuleShape, "body.model_rule.aggregate_is_counting_candidate", "expected boolean")
		}

		drVal, _ := o.Get("detail_retained_not_counted_again")
		if b, ok := drVal.(bool); ok {
			mr.DetailRetainedNotCountedAgain = &b
		} else {
			return mr, v.refused(RuleMappingRuleShape, "body.model_rule.detail_retained_not_counted_again", "expected boolean")
		}

		mufArr, err := v.mappingStringArray(o, "body.model_rule", "model_usage_fields")
		if err != nil {
			return mr, err
		}
		mr.ModelUsageFields = mufArr

		mus, err := v.mappingNonEmptyString(o, "body.model_rule", "model_usage_sorted_by")
		if err != nil {
			return mr, err
		}
		mr.ModelUsageSortedBy = &mus
	}

	return mr, nil
}

func (v *Validator) validateOverlapRule(o *Object, sk string) (MappingOverlapRule, error) {
	var or MappingOverlapRule

	var wantKeys []string
	if sk == "codex_desktop" {
		wantKeys = []string{
			"counting_source", "arithmetic_mismatch", "missing_raw_total",
			"thread_token_usage", "turn_token_usage", "token_count_snapshots",
			"summed_or_mixed_with_preferred", "report_must_name_unhandled_token_count_coverage",
			"owed_coverage_tasks",
		}
	} else {
		wantKeys = []string{
			"counting_grain", "arithmetic_violation", "evidenced_overlapping_grain",
			"request_grain_mapping", "session_totals", "forbidden_unions",
			"checksum_requires_evidenced_complete_matching_coverage",
			"session_totals_fill_missing_turn_spend", "owed_coverage_tasks",
		}
	}
	if err := v.exactRuleKeys(o, "body.overlap_rule", wantKeys...); err != nil {
		return or, err
	}

	octArr, err := v.mappingStringArray(o, "body.overlap_rule", "owed_coverage_tasks")
	if err != nil {
		return or, err
	}
	or.OwedCoverageTasks = octArr

	if sk == "codex_desktop" {
		cs, err := v.mappingNonEmptyString(o, "body.overlap_rule", "counting_source")
		if err != nil {
			return or, err
		}
		or.CountingSource = &cs

		am, err := v.mappingNonEmptyString(o, "body.overlap_rule", "arithmetic_mismatch")
		if err != nil {
			return or, err
		}
		or.ArithmeticMismatch = &am

		mrt, err := v.mappingNonEmptyString(o, "body.overlap_rule", "missing_raw_total")
		if err != nil {
			return or, err
		}
		or.MissingRawTotal = &mrt

		ttu, err := v.mappingNonEmptyString(o, "body.overlap_rule", "thread_token_usage")
		if err != nil {
			return or, err
		}
		or.ThreadTokenUsage = &ttu

		turnTU, err := v.mappingNonEmptyString(o, "body.overlap_rule", "turn_token_usage")
		if err != nil {
			return or, err
		}
		or.TurnTokenUsage = &turnTU

		tcs, err := v.mappingNonEmptyString(o, "body.overlap_rule", "token_count_snapshots")
		if err != nil {
			return or, err
		}
		or.TokenCountSnapshots = &tcs

		smpVal, _ := o.Get("summed_or_mixed_with_preferred")
		if b, ok := smpVal.(bool); ok {
			or.SummedOrMixedWithPreferred = &b
		} else {
			return or, v.refused(RuleMappingRuleShape, "body.overlap_rule.summed_or_mixed_with_preferred", "expected boolean")
		}

		rmuVal, _ := o.Get("report_must_name_unhandled_token_count_coverage")
		if b, ok := rmuVal.(bool); ok {
			or.ReportMustNameUnhandledTokenCountCoverage = &b
		} else {
			return or, v.refused(RuleMappingRuleShape, "body.overlap_rule.report_must_name_unhandled_token_count_coverage", "expected boolean")
		}
	} else {
		cg, err := v.mappingNonEmptyString(o, "body.overlap_rule", "counting_grain")
		if err != nil {
			return or, err
		}
		or.CountingGrain = &cg

		av, err := v.mappingNonEmptyString(o, "body.overlap_rule", "arithmetic_violation")
		if err != nil {
			return or, err
		}
		or.ArithmeticViolation = &av

		eog, err := v.mappingNonEmptyString(o, "body.overlap_rule", "evidenced_overlapping_grain")
		if err != nil {
			return or, err
		}
		or.EvidencedOverlappingGrain = &eog

		rgm, err := v.mappingNonEmptyString(o, "body.overlap_rule", "request_grain_mapping")
		if err != nil {
			return or, err
		}
		or.RequestGrainMapping = &rgm

		st, err := v.mappingNonEmptyString(o, "body.overlap_rule", "session_totals")
		if err != nil {
			return or, err
		}
		or.SessionTotals = &st

		fuArr, err := v.mappingStringArray(o, "body.overlap_rule", "forbidden_unions")
		if err != nil {
			return or, err
		}
		or.ForbiddenUnions = fuArr

		cVal, _ := o.Get("checksum_requires_evidenced_complete_matching_coverage")
		if b, ok := cVal.(bool); ok {
			or.ChecksumRequiresEvidencedCompleteMatchingCoverage = &b
		} else {
			return or, v.refused(RuleMappingRuleShape, "body.overlap_rule.checksum_requires_evidenced_complete_matching_coverage", "expected boolean")
		}

		sVal, _ := o.Get("session_totals_fill_missing_turn_spend")
		if b, ok := sVal.(bool); ok {
			or.SessionTotalsFillMissingTurnSpend = &b
		} else {
			return or, v.refused(RuleMappingRuleShape, "body.overlap_rule.session_totals_fill_missing_turn_spend", "expected boolean")
		}
	}

	return or, nil
}

// Body encodes the mapping to canonical JSON value representation.
func (m Mapping) Body() (Value, error) {
	b := newBuilder()
	b.set("schema", m.Schema)
	b.set("name", m.Name)
	b.set("revision", m.Revision)
	b.set("source_shapes", Strings(m.SourceShapes))

	b.setObject("field_rules", func(s *builder) {
		for _, k := range sortedKeys(m.FieldRules) {
			fr := m.FieldRules[k]
			s.setObject(k, func(fs *builder) {
				fs.set("absent_presence", fr.AbsentPresence)
				fs.set("absent_reason", fr.AbsentReason)
				fs.set("invalid_presence", fr.InvalidPresence)
				fs.set("invalid_reason", fr.InvalidReason)
				fs.set("number_kind", fr.NumberKind)
				fs.set("spend_role", fr.SpendRole)
				fs.set("unit", fr.Unit)
				if fr.ZeroSemantics != "" {
					fs.set("zero_semantics", fr.ZeroSemantics)
				}
			})
		}
	})

	b.setObject("identity_rule", func(s *builder) {
		s.set("source_kind", m.IdentityRule.SourceKind)
		s.set("namespace", m.IdentityRule.Namespace)
		s.set("event_key", Strings(m.IdentityRule.EventKey))
		s.set("observation_kind", m.IdentityRule.ObservationKind)
		s.set("normalized_spend_supported", m.IdentityRule.NormalizedSpendSupported)
		s.set("producer_version_from", m.IdentityRule.ProducerVersionFrom)
		s.set("session_id", m.IdentityRule.SessionID)
		s.set("receipt_fields", Strings(m.IdentityRule.ReceiptFields))
		s.set("receipt_value_type", m.IdentityRule.ReceiptValueType)
		if m.IdentityRule.SourceKind == "codex_desktop" {
			if m.IdentityRule.ContainingOrForkedSessionInEventKey != nil {
				s.set("containing_or_forked_session_in_event_key", *m.IdentityRule.ContainingOrForkedSessionInEventKey)
			}
		} else if m.IdentityRule.SourceKind == "grok" {
			if m.IdentityRule.CollectionTimestampSubstitution != nil {
				s.set("collection_timestamp_substitution", *m.IdentityRule.CollectionTimestampSubstitution)
			}
			if m.IdentityRule.ContainingSessionSubstitution != nil {
				s.set("containing_session_substitution", *m.IdentityRule.ContainingSessionSubstitution)
			}
			if m.IdentityRule.UnsupportedReason != nil {
				s.set("unsupported_reason", *m.IdentityRule.UnsupportedReason)
			}
		}
	})

	b.setObject("revision_rule", func(s *builder) {
		s.set("basis", m.RevisionRule.Basis)
		s.set("native", nullable(m.RevisionRule.Native))
		s.set("supersedes", Strings(m.RevisionRule.Supersedes))
		s.set("identical_copy", m.RevisionRule.IdenticalCopy)
		s.set("changed_same_key", m.RevisionRule.ChangedSameKey)
		s.set("inferred_order_from_export_update_time", m.RevisionRule.InferredOrderFromExportUpdateTime)
		s.set("newest_wins", m.RevisionRule.NewestWins)
	})

	b.setObject("time_rule", func(s *builder) {
		s.set("basis", m.TimeRule.Basis)
		s.set("occurred_at", m.TimeRule.OccurredAt)
		s.set("offsets", m.TimeRule.Offsets)
		s.set("day_allocation", m.TimeRule.DayAllocation)
		s.set("start", nullable(m.TimeRule.Start))
		s.set("end", nullable(m.TimeRule.End))
		if m.TimeRule.MissingTimestamp != nil {
			s.setObject("missing_timestamp", func(ms *builder) {
				ms.set("basis", m.TimeRule.MissingTimestamp.Basis)
				ms.set("occurred_at", nullable(m.TimeRule.MissingTimestamp.OccurredAt))
			})
		} else {
			s.set("missing_timestamp", nil)
		}
		if m.IdentityRule.SourceKind == "grok" && m.TimeRule.CrossMidnight != nil {
			s.set("cross_midnight", *m.TimeRule.CrossMidnight)
		}
	})

	b.setObject("model_rule", func(s *builder) {
		s.set("forbidden_wire_keys", Strings(m.ModelRule.ForbiddenWireKeys))
		if m.IdentityRule.SourceKind == "codex_desktop" {
			if m.ModelRule.Basis != nil {
				s.set("basis", *m.ModelRule.Basis)
			}
			if m.ModelRule.ID != nil {
				s.set("id", *m.ModelRule.ID)
			}
			if m.ModelRule.Absent != nil {
				s.setObject("absent", func(as *builder) {
					as.set("basis", m.ModelRule.Absent.Basis)
					as.set("id", nullable(m.ModelRule.Absent.ID))
				})
			}
			if m.ModelRule.LaterModelRelabelsEarlierResponses != nil {
				s.set("later_model_relabels_earlier_responses", *m.ModelRule.LaterModelRelabelsEarlierResponses)
			}
			s.set("model_usage", Strings(m.ModelRule.ModelUsage))
		} else if m.IdentityRule.SourceKind == "grok" {
			if m.ModelRule.SingleReportedID != nil {
				s.setObject("single_reported_id", func(ss *builder) {
					ss.set("basis", m.ModelRule.SingleReportedID.Basis)
					ss.set("id", nullable(m.ModelRule.SingleReportedID.ID))
				})
			}
			if m.ModelRule.AbsentID != nil {
				s.setObject("absent_id", func(as *builder) {
					as.set("basis", m.ModelRule.AbsentID.Basis)
					as.set("id", nullable(m.ModelRule.AbsentID.ID))
				})
			}
			if m.ModelRule.MultipleModelIDs != nil {
				s.setObject("multiple_model_ids", func(ms *builder) {
					ms.set("basis", m.ModelRule.MultipleModelIDs.Basis)
					ms.set("id", nullable(m.ModelRule.MultipleModelIDs.ID))
				})
			}
			if m.ModelRule.SplitNormalized != nil {
				s.set("split_normalized", *m.ModelRule.SplitNormalized)
			}
			if m.ModelRule.AggregateIsCountingCandidate != nil {
				s.set("aggregate_is_counting_candidate", *m.ModelRule.AggregateIsCountingCandidate)
			}
			if m.ModelRule.DetailRetainedNotCountedAgain != nil {
				s.set("detail_retained_not_counted_again", *m.ModelRule.DetailRetainedNotCountedAgain)
			}
			s.set("model_usage_fields", Strings(m.ModelRule.ModelUsageFields))
			if m.ModelRule.ModelUsageSortedBy != nil {
				s.set("model_usage_sorted_by", *m.ModelRule.ModelUsageSortedBy)
			}
		}
	})

	b.setObject("overlap_rule", func(s *builder) {
		s.set("owed_coverage_tasks", Strings(m.OverlapRule.OwedCoverageTasks))
		if m.IdentityRule.SourceKind == "codex_desktop" {
			if m.OverlapRule.CountingSource != nil {
				s.set("counting_source", *m.OverlapRule.CountingSource)
			}
			if m.OverlapRule.ArithmeticMismatch != nil {
				s.set("arithmetic_mismatch", *m.OverlapRule.ArithmeticMismatch)
			}
			if m.OverlapRule.MissingRawTotal != nil {
				s.set("missing_raw_total", *m.OverlapRule.MissingRawTotal)
			}
			if m.OverlapRule.ThreadTokenUsage != nil {
				s.set("thread_token_usage", *m.OverlapRule.ThreadTokenUsage)
			}
			if m.OverlapRule.TurnTokenUsage != nil {
				s.set("turn_token_usage", *m.OverlapRule.TurnTokenUsage)
			}
			if m.OverlapRule.TokenCountSnapshots != nil {
				s.set("token_count_snapshots", *m.OverlapRule.TokenCountSnapshots)
			}
			if m.OverlapRule.SummedOrMixedWithPreferred != nil {
				s.set("summed_or_mixed_with_preferred", *m.OverlapRule.SummedOrMixedWithPreferred)
			}
			if m.OverlapRule.ReportMustNameUnhandledTokenCountCoverage != nil {
				s.set("report_must_name_unhandled_token_count_coverage", *m.OverlapRule.ReportMustNameUnhandledTokenCountCoverage)
			}
		} else if m.IdentityRule.SourceKind == "grok" {
			if m.OverlapRule.CountingGrain != nil {
				s.set("counting_grain", *m.OverlapRule.CountingGrain)
			}
			if m.OverlapRule.ArithmeticViolation != nil {
				s.set("arithmetic_violation", *m.OverlapRule.ArithmeticViolation)
			}
			if m.OverlapRule.EvidencedOverlappingGrain != nil {
				s.set("evidenced_overlapping_grain", *m.OverlapRule.EvidencedOverlappingGrain)
			}
			if m.OverlapRule.RequestGrainMapping != nil {
				s.set("request_grain_mapping", *m.OverlapRule.RequestGrainMapping)
			}
			if m.OverlapRule.SessionTotals != nil {
				s.set("session_totals", *m.OverlapRule.SessionTotals)
			}
			s.set("forbidden_unions", Strings(m.OverlapRule.ForbiddenUnions))
			if m.OverlapRule.ChecksumRequiresEvidencedCompleteMatchingCoverage != nil {
				s.set("checksum_requires_evidenced_complete_matching_coverage", *m.OverlapRule.ChecksumRequiresEvidencedCompleteMatchingCoverage)
			}
			if m.OverlapRule.SessionTotalsFillMissingTurnSpend != nil {
				s.set("session_totals_fill_missing_turn_spend", *m.OverlapRule.SessionTotalsFillMissingTurnSpend)
			}
		}
	})

	b.setObject("fixture_digests", func(s *builder) {
		for _, k := range sortedKeys(m.FixtureDigests) {
			s.set(k, m.FixtureDigests[k])
		}
	})

	b.set("implementation_id", nullable(m.ImplementationID))

	return b.done()
}

// SealMapping seals a Mapping and strictly validates its own bytes.
func SealMapping(m Mapping) ([]byte, string, error) {
	body, err := m.Body()
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
