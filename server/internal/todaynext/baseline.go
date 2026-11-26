package todaynext

import (
	"errors"
	"fmt"
	"strings"
)

const (
	// StatusNotMeasured explicitly marks an unmeasured friction metric.
	StatusNotMeasured = "not_measured"
	// StatusNotClaimed explicitly marks an unproven friction reduction claim.
	StatusNotClaimed = "not_claimed"
	// MinMeasuredRounds is the minimum before/after same-input round count required
	// before baseline_steps, active_time, or reduction may be claimed.
	MinMeasuredRounds = 5
)

var (
	ErrInsufficientRounds   = errors.New("at least 5 before/after rounds are required before claiming baseline metrics")
	ErrSameInputUnverified  = errors.New("before/after measurements must verify identical inputs across rounds")
	ErrWaitTimeNotSeparated = errors.New("human active time must be separated from network/model wait time")
	ErrInvalidSample        = errors.New("friction round sample has invalid steps, timing, or duplicate round")
	ErrUnbackedClaim        = errors.New("unmeasured baseline cannot claim steps, active_time, or reduction")
)

// FrictionRoundSample is one paired before/after timing & step observation.
// HumanActiveMS MUST be measured separately from NetworkWaitMS and ModelWaitMS.
type FrictionRoundSample struct {
	Round               int    `json:"round"`
	InputSignature      string `json:"input_signature"`
	BeforeSteps         int    `json:"before_steps"`
	AfterSteps          int    `json:"after_steps"`
	BeforeHumanActiveMS int64  `json:"before_human_active_ms"`
	BeforeNetworkWaitMS int64  `json:"before_network_wait_ms"`
	BeforeModelWaitMS   int64  `json:"before_model_wait_ms"`
	AfterHumanActiveMS  int64  `json:"after_human_active_ms"`
	AfterNetworkWaitMS  int64  `json:"after_network_wait_ms"`
	AfterModelWaitMS    int64  `json:"after_model_wait_ms"`
	SeparatedWaitTime   bool   `json:"separated_wait_time"`
}

// FrictionBaselineRecord is the honest measurement contract for HUI-2598.
// Unless backed by >=5 verified same-input before/after rounds with separated
// human active time vs. network/model wait time, baseline_steps and active_time
// stay "not_measured" and reduction stays "not_claimed".
type FrictionBaselineRecord struct {
	Task               string                `json:"task"`
	Measured           bool                  `json:"measured"`
	SameInputVerified  bool                  `json:"same_input_verified"`
	WaitTimeSeparated  bool                  `json:"wait_time_separated"`
	RoundsCount        int                   `json:"rounds_count"`
	BaselineSteps      string                `json:"baseline_steps"`
	ActiveTime         string                `json:"active_time"`
	HumanActiveTimeMS  string                `json:"human_active_time_ms"`
	NetworkModelWaitMS string                `json:"network_model_wait_ms"`
	Reduction          string                `json:"reduction"`
	Samples            []FrictionRoundSample `json:"samples,omitempty"`
}

// NewUnmeasuredFrictionBaseline returns the default honest record before real
// 5-round browser timing has been collected.
func NewUnmeasuredFrictionBaseline(task string) FrictionBaselineRecord {
	task = strings.TrimSpace(task)
	if task == "" {
		task = "HUI-2598"
	}
	return FrictionBaselineRecord{
		Task:               task,
		Measured:           false,
		SameInputVerified:  false,
		WaitTimeSeparated:  false,
		RoundsCount:        0,
		BaselineSteps:      StatusNotMeasured,
		ActiveTime:         StatusNotMeasured,
		HumanActiveTimeMS:  StatusNotMeasured,
		NetworkModelWaitMS: StatusNotMeasured,
		Reduction:          StatusNotClaimed,
	}
}

// BuildFrictionBaseline builds a FrictionBaselineRecord from observed rounds.
// When samples is empty, it returns an honest unmeasured record with nil error.
// When samples are supplied but fail the 5-round, same-input, or separated
// active-vs-wait-time contract, it returns an unmeasured record plus an error.
func BuildFrictionBaseline(task string, samples []FrictionRoundSample, sameInputVerified bool) (FrictionBaselineRecord, error) {
	base := NewUnmeasuredFrictionBaseline(task)
	if len(samples) == 0 {
		return base, nil
	}
	if err := validateSamples(samples, sameInputVerified); err != nil {
		return base, err
	}

	var totalBeforeSteps, totalAfterSteps int64
	var totalBeforeHuman, totalAfterHuman int64
	var totalBeforeWait, totalAfterWait int64
	copied := make([]FrictionRoundSample, len(samples))
	for i, s := range samples {
		copied[i] = s
		totalBeforeSteps += int64(s.BeforeSteps)
		totalAfterSteps += int64(s.AfterSteps)
		totalBeforeHuman += s.BeforeHumanActiveMS
		totalAfterHuman += s.AfterHumanActiveMS
		totalBeforeWait += s.BeforeNetworkWaitMS + s.BeforeModelWaitMS
		totalAfterWait += s.AfterNetworkWaitMS + s.AfterModelWaitMS
	}

	n := float64(len(samples))
	avgBeforeSteps := float64(totalBeforeSteps) / n
	avgAfterSteps := float64(totalAfterSteps) / n
	avgBeforeHuman := float64(totalBeforeHuman) / n
	avgAfterHuman := float64(totalAfterHuman) / n
	avgBeforeWait := float64(totalBeforeWait) / n
	avgAfterWait := float64(totalAfterWait) / n

	stepReductionPct := ((avgBeforeSteps - avgAfterSteps) / avgBeforeSteps) * 100.0
	humanReductionPct := ((avgBeforeHuman - avgAfterHuman) / avgBeforeHuman) * 100.0

	rec := FrictionBaselineRecord{
		Task:               base.Task,
		Measured:           true,
		SameInputVerified:  true,
		WaitTimeSeparated:  true,
		RoundsCount:        len(samples),
		BaselineSteps:      fmt.Sprintf("before_avg=%.1f,after_avg=%.1f", avgBeforeSteps, avgAfterSteps),
		ActiveTime:         fmt.Sprintf("before_human_ms=%.1f,after_human_ms=%.1f", avgBeforeHuman, avgAfterHuman),
		HumanActiveTimeMS:  fmt.Sprintf("before_avg=%.1f,after_avg=%.1f", avgBeforeHuman, avgAfterHuman),
		NetworkModelWaitMS: fmt.Sprintf("before_avg=%.1f,after_avg=%.1f", avgBeforeWait, avgAfterWait),
		Reduction:          fmt.Sprintf("steps=%.1f%%,human_active=%.1f%%", stepReductionPct, humanReductionPct),
		Samples:            copied,
	}
	return rec, nil
}

// Validate rejects any unbacked claim (e.g., claiming reduction/steps/active_time
// without 5 verified same-input rounds separating human active time from
// network/model wait time).
func (r FrictionBaselineRecord) Validate() error {
	if !r.Measured {
		if strings.TrimSpace(r.BaselineSteps) != StatusNotMeasured ||
			strings.TrimSpace(r.ActiveTime) != StatusNotMeasured ||
			strings.TrimSpace(r.HumanActiveTimeMS) != StatusNotMeasured ||
			strings.TrimSpace(r.Reduction) != StatusNotClaimed {
			return ErrUnbackedClaim
		}
		if r.NetworkModelWaitMS != "" && strings.TrimSpace(r.NetworkModelWaitMS) != StatusNotMeasured {
			return ErrUnbackedClaim
		}
		return nil
	}
	if !r.SameInputVerified {
		return ErrSameInputUnverified
	}
	if !r.WaitTimeSeparated {
		return ErrWaitTimeNotSeparated
	}
	if r.RoundsCount != len(r.Samples) {
		return ErrInvalidSample
	}
	if err := validateSamples(r.Samples, r.SameInputVerified); err != nil {
		return err
	}
	for _, field := range []string{r.BaselineSteps, r.ActiveTime, r.HumanActiveTimeMS, r.Reduction} {
		v := strings.TrimSpace(strings.ToLower(field))
		if v == "" || v == StatusNotMeasured || v == StatusNotClaimed || v == "pass" || v == "ok" || v == "success" {
			return ErrUnbackedClaim
		}
	}
	return nil
}

func validateSamples(samples []FrictionRoundSample, sameInputVerified bool) error {
	if !sameInputVerified {
		return ErrSameInputUnverified
	}
	if len(samples) < MinMeasuredRounds {
		return ErrInsufficientRounds
	}
	seenRounds := make(map[int]bool, len(samples))
	expectedSig := strings.TrimSpace(samples[0].InputSignature)
	if expectedSig == "" {
		return ErrSameInputUnverified
	}
	for _, s := range samples {
		if s.Round < 1 || seenRounds[s.Round] {
			return ErrInvalidSample
		}
		seenRounds[s.Round] = true
		if strings.TrimSpace(s.InputSignature) != expectedSig {
			return ErrSameInputUnverified
		}
		if !s.SeparatedWaitTime {
			return ErrWaitTimeNotSeparated
		}
		if s.BeforeSteps <= 0 || s.AfterSteps <= 0 {
			return ErrInvalidSample
		}
		if s.BeforeHumanActiveMS <= 0 || s.AfterHumanActiveMS <= 0 {
			return ErrInvalidSample
		}
		if s.BeforeNetworkWaitMS < 0 || s.BeforeModelWaitMS < 0 || s.AfterNetworkWaitMS < 0 || s.AfterModelWaitMS < 0 {
			return ErrInvalidSample
		}
	}
	return nil
}
