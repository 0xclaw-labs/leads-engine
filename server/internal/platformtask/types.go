// Package platformtask submits an optional reception phrase task.
// A missing client is text degradation, never a billing pass.
package platformtask

import "context"

// PhraseInput is the only payload a reception task may carry.
// Visitor text, phone numbers, persona, and other-tenant knowledge stay out.
type PhraseInput struct {
	IdempotencyKey string
	Language       string
	Body           string
	CitationIDs    []string
}

// PhraseResult is the local receipt. BillingVerdict is recorded or not_completed.
type PhraseResult struct {
	TaskID         string
	CostCents      int
	BillingVerdict string
}

// Phraser meters one grounded answer.
type Phraser interface {
	Phrase(ctx context.Context, in PhraseInput) PhraseResult
}

// GenerateInput is the only payload a light-copy generation task may carry.
// The prompt is built by the caller from allow-listed facts only; visitor
// text, contact bodies, and profiles never enter this struct.
type GenerateInput struct {
	IdempotencyKey string
	Language       string
	Prompt         string
}

// GenerateResult is the local receipt. BillingVerdict is recorded or
// not_completed. A recorded verdict with an empty Text means the upstream task
// really succeeded and was charged but produced no usable text: the charge is
// reported, never hidden, and the caller must refuse to write a draft.
type GenerateResult struct {
	TaskID         string
	Text           string
	CostCents      int
	BillingVerdict string
}

// TextGenerator produces one model draft through the platform task meter.
type TextGenerator interface {
	Generate(ctx context.Context, in GenerateInput) GenerateResult
}
