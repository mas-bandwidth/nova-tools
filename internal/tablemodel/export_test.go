package tablemodel

// The record readers' internals, for the dual-read test (package
// tablemodel_test, which imports internal/tablemodel/recordsread to install
// the typed half; an in-package test cannot, recordsread imports this
// package).

import "github.com/mas-bandwidth/nova-tools/pkg/schemaread"

type (
	Spec[V any]   = recordSpec[V]
	Record[V any] = record[V]
)

var (
	StateSpec       = stateSpec
	TraceSpec       = traceSpec
	ActionSpec      = actionSpec
	EventSpec       = eventSpec
	FindingSpec     = findingSpec
	ReplayEventSpec = replayEventSpec
	TraceReader     = traceReader
	StateCanon      = stateCanon
	TraceCanon      = traceCanon
)

const ModelHelp = modelHelp

func SpecName[V any](s Spec[V]) string                        { return s.name }
func SpecCodec[V any](s Spec[V]) *schemaread.Codec[Record[V]] { return s.codec() }
func SpecWire[V any](s Spec[V], v V) ([]byte, error)          { return s.wire(v) }
func SpecEmpty[V any](s Spec[V]) V                            { return s.empty() }
func RecordOf[V any](v V) *Record[V]                          { return &Record[V]{v} }
func RecordValue[V any](r Record[V]) V                        { return r.v }
