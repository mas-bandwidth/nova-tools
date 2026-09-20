package pulse

import (
	"github.com/mas-bandwidth/nova-tools/internal/fleet"
)

// Re-export core bench schema types from internal/fleet for pulse consumers.
type (
	AdmissionState      = fleet.AdmissionState
	RequestedSettings   = fleet.RequestedSettings
	ObservedWatermark   = fleet.ObservedWatermark
	EnforcedAllowance   = fleet.EnforcedAllowance
	BenchRecordSnapshot = fleet.BenchRecordSnapshot
	BenchRecord         = fleet.BenchRecord
)

// AdmissionState constants re-exported from internal/fleet.
const (
	AdmissionAdmitting = fleet.AdmissionAdmitting
	AdmissionRefused   = fleet.AdmissionRefused
	AdmissionDrained   = fleet.AdmissionDrained
	AdmissionParked    = fleet.AdmissionParked
	AdmissionInvalid   = fleet.AdmissionInvalid
)

// Error values re-exported from internal/fleet.
var (
	ErrStaleRevision = fleet.ErrStaleRevision
	ErrInvalidUpdate = fleet.ErrInvalidUpdate
	ErrBenchMismatch = fleet.ErrBenchMismatch
)

// Functions re-exported from internal/fleet.
var (
	NewBenchRecord   = fleet.NewBenchRecord
	ReadBenchRecord  = fleet.ReadBenchRecord
	ParseBenchRecord = fleet.ParseBenchRecord
)
