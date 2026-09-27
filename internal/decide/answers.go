package decide

import "github.com/mas-bandwidth/nova-tools/internal/jevclient"

// ValidateAnswers checks typed provider answers against the offered questions.
func ValidateAnswers(qs map[string]Question, answers map[string]Answer) error {
	return jevclient.ValidateAnswers(qs, answers)
}
