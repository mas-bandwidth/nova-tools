package roadmap

import (
	"fmt"
	"strings"
)

// ListCriteria returns a slice of all criteria across all epics and features.
func (r *Roadmap) ListCriteria() []*Criterion {
	var result []*Criterion
	for _, epic := range r.Epics {
		for _, feat := range epic.Features {
			result = append(result, feat.Criteria...)
		}
	}
	return result
}

// ListCriteriaByStatus returns all criteria with the specified normalized status.
func (r *Roadmap) ListCriteriaByStatus(status Status) []*Criterion {
	var result []*Criterion
	for _, c := range r.ListCriteria() {
		if c.Status == status {
			result = append(result, c)
		}
	}
	return result
}

// VerifiedCriteria returns all criteria whose status is verified.
func (r *Roadmap) VerifiedCriteria() []*Criterion {
	return r.ListCriteriaByStatus(StatusVerified)
}

// UnverifiedCriteria returns all criteria whose status is unverified.
func (r *Roadmap) UnverifiedCriteria() []*Criterion {
	return r.ListCriteriaByStatus(StatusUnverified)
}

// InProgressCriteria returns all criteria whose status is in-progress.
func (r *Roadmap) InProgressCriteria() []*Criterion {
	return r.ListCriteriaByStatus(StatusInProgress)
}

// ListCriteriaByEpic returns all criteria belonging to the specified epic UID.
func (r *Roadmap) ListCriteriaByEpic(epicID string) []*Criterion {
	epic, ok := r.GetEpic(epicID)
	if !ok {
		return nil
	}
	var result []*Criterion
	for _, feat := range epic.Features {
		result = append(result, feat.Criteria...)
	}
	return result
}

// ListCriteriaByFeature returns all criteria belonging to the specified feature UID.
func (r *Roadmap) ListCriteriaByFeature(featureID string) []*Criterion {
	feat, ok := r.GetFeature(featureID)
	if !ok {
		return nil
	}
	return append([]*Criterion(nil), feat.Criteria...)
}

// ListFeaturesByEpic returns all features belonging to the specified epic UID.
func (r *Roadmap) ListFeaturesByEpic(epicID string) []*Feature {
	epic, ok := r.GetEpic(epicID)
	if !ok {
		return nil
	}
	return append([]*Feature(nil), epic.Features...)
}

// GetEpic retrieves an epic by its UID.
func (r *Roadmap) GetEpic(epicID string) (*Epic, bool) {
	if r.epicMap == nil {
		r.buildIndexes()
	}
	epic, ok := r.epicMap[epicID]
	return epic, ok
}

// GetFeature retrieves a feature by its UID.
func (r *Roadmap) GetFeature(featureID string) (*Feature, bool) {
	if r.featureMap == nil {
		r.buildIndexes()
	}
	feat, ok := r.featureMap[featureID]
	return feat, ok
}

// GetCriterion retrieves a criterion by its UID.
func (r *Roadmap) GetCriterion(criterionID string) (*Criterion, bool) {
	if r.criterionMap == nil {
		r.buildIndexes()
	}
	c, ok := r.criterionMap[criterionID]
	return c, ok
}

// Progress calculates the overall progress of the roadmap criteria.
func (r *Roadmap) Progress() Progress {
	all := r.ListCriteria()
	verified := 0
	unverified := 0
	inProgress := 0

	for _, c := range all {
		switch c.Status {
		case StatusVerified:
			verified++
		case StatusInProgress:
			inProgress++
		default:
			unverified++
		}
	}

	return CalculateProgress(verified, unverified, inProgress)
}

// EpicProgress calculates criteria progress for a specific epic.
func (r *Roadmap) EpicProgress(epicID string) (Progress, error) {
	epic, ok := r.GetEpic(epicID)
	if !ok {
		return Progress{}, fmt.Errorf("epic %q not found", epicID)
	}

	verified := 0
	unverified := 0
	inProgress := 0

	for _, feat := range epic.Features {
		for _, c := range feat.Criteria {
			switch c.Status {
			case StatusVerified:
				verified++
			case StatusInProgress:
				inProgress++
			default:
				unverified++
			}
		}
	}

	return CalculateProgress(verified, unverified, inProgress), nil
}

// FeatureProgress calculates criteria progress for a specific feature.
func (r *Roadmap) FeatureProgress(featureID string) (Progress, error) {
	feat, ok := r.GetFeature(featureID)
	if !ok {
		return Progress{}, fmt.Errorf("feature %q not found", featureID)
	}

	verified := 0
	unverified := 0
	inProgress := 0

	for _, c := range feat.Criteria {
		switch c.Status {
		case StatusVerified:
			verified++
		case StatusInProgress:
			inProgress++
		default:
			unverified++
		}
	}

	return CalculateProgress(verified, unverified, inProgress), nil
}

// CriteriaFilter configures criteria search criteria.
type CriteriaFilter struct {
	EpicID    string
	FeatureID string
	Status    Status
	Keyword   string
}

// QueryCriteria searches criteria across the roadmap matching all provided filter parameters.
func (r *Roadmap) QueryCriteria(filter CriteriaFilter) []*Criterion {
	var candidates []*Criterion

	if filter.FeatureID != "" {
		candidates = r.ListCriteriaByFeature(filter.FeatureID)
	} else if filter.EpicID != "" {
		candidates = r.ListCriteriaByEpic(filter.EpicID)
	} else {
		candidates = r.ListCriteria()
	}

	if filter.Status == "" && filter.Keyword == "" {
		return candidates
	}

	var result []*Criterion
	kw := strings.ToLower(filter.Keyword)

	for _, c := range candidates {
		if filter.Status != "" && c.Status != filter.Status {
			continue
		}
		if kw != "" {
			inID := strings.Contains(strings.ToLower(c.ID), kw)
			inTitle := strings.Contains(strings.ToLower(c.Title), kw)
			inNotes := strings.Contains(strings.ToLower(c.VerificationNotes), kw)
			if !inID && !inTitle && !inNotes {
				continue
			}
		}
		result = append(result, c)
	}

	return result
}
