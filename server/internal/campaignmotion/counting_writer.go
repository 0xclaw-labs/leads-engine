package campaignmotion

import "errors"

// ErrWriterCalled is returned by CountingWriter. Leads never writes a creative
// project in this slice, so any call is a failure that must be observed.
var ErrWriterCalled = errors.New("campaignmotion: creative project write is not wired")

// CountingWriter is an observed creative-project port. It replaces a nil port
// so that zero writes is a counted fact, not an absent dependency.
// Prepare does not call it. Any call increments Calls and fails closed.
type CountingWriter struct {
	Calls int
}

// WriteCreativeProject counts the attempt and refuses it.
func (w *CountingWriter) WriteCreativeProject(projectID, revisionID string, export []byte) error {
	_, _, _ = projectID, revisionID, export
	w.Calls++
	return ErrWriterCalled
}

// AssertZero reports whether the port observed no write. A non-zero count
// means an invariant broke, so the caller must not save the handoff.
func (w *CountingWriter) AssertZero() error {
	if w.Calls != 0 {
		return ErrWriterCalled
	}
	return nil
}
