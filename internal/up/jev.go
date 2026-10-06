// Package upjev provides Jev setup helpers for nova-up.
package upjev

import (
	"github.com/mas-bandwidth/nova-tools/internal/doctor"
)

// Check runs the Jev availability check. Returns nil on success,
// or a Warning if JEV_API_KEY is not set (optional dependency).
func Check() error {
	return doctor.Run(nil, "jev")
}

// KeyEnv returns the name of the environment variable to set.
func KeyEnv() string {
	return doctor.KeyEnv()
}

// DefaultClientBaseURL returns the default Jev endpoint.
func DefaultClientBaseURL() string {
	return doctor.ClientBaseURL()
}

// Note provides setup guidance for users.
func Note() string {
	return "Set JEV_API_KEY environment variable to enable Jev decision-making. " +
		"This is optional: nova-decide can skip decisions if the key is not set."
}
