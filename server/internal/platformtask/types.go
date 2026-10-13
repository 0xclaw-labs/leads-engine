// Package platformtask submits text tasks to the platform task API.
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
// Text is the model draft only when it was present in the succeeded task;
// callers must still check it against the grounded answer.
type PhraseResult struct {
	TaskID         string
	CostCents      int
	BillingVerdict string
	Text           string
}

// Phraser meters one grounded answer and may return a model draft.
type Phraser interface {
	Phrase(ctx context.Context, in PhraseInput) PhraseResult
}

// TextInput is one prompt for a generic text capability (HUI-1682 light copy).
// Prompt must carry only selected facts and instructions, never visitor text,
// contact text, sales notes, or profiles.
type TextInput struct {
	IdempotencyKey string
	Capability     string
	Language       string
	Prompt         string
}

// TextResult is the local receipt for one text task.
// Text is empty unless the succeeded task carried a non-blank text field.
type TextResult struct {
	TaskID         string
	Text           string
	CostCents      int
	BillingVerdict string
}

// TextGenerator is the light-copy seam. It returns a receipt, never a draft.
type TextGenerator interface {
	Text(ctx context.Context, in TextInput) TextResult
}
