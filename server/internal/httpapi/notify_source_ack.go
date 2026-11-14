package httpapi

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/bianjiefilm/leads-engine/server/internal/config"
	"github.com/bianjiefilm/leads-engine/server/internal/notifyingest"
	"github.com/bianjiefilm/leads-engine/server/internal/store"
	"github.com/bianjiefilm/leads-engine/server/internal/tenantmap"
)

func fetchIngestRecord(r *http.Request, src config.IngestSource, tenant, ref string) (notifyingest.Record, error) {
	if src.AppID == "touch-engine" {
		return notifyingest.FetchRecord(r.Context(), src.FetchBase, src.FetchToken, tenant, ref, src.FetchServiceID)
	}
	return notifyingest.FetchRecord(r.Context(), src.FetchBase, src.FetchToken, tenant, ref)
}

func sourceAcknowledgement(app, tenant, event, ref string, revision int, state string) notifyingest.Acknowledgement {
	// JSON array encoding avoids ambiguous joins in otherwise opaque source keys.
	key, _ := json.Marshal([]any{app, tenant, event, ref, revision, state})
	digest := sha256.Sum256(key)
	return notifyingest.Acknowledgement{ReceiptID: "leads_" + hex.EncodeToString(digest[:]), SourceEventID: event, SubmissionID: ref, SourceRevision: revision, ReceiverState: state}
}

// Every successful ingest exit comes here after its business transaction commits.
// The inbox receipt is also the durable ACK intent: failures leave the original
// event available for Notify's ordinary retry, including pre-forwarded 409s.
func (s *Server) completeNotifyReceipt(w http.ResponseWriter, r *http.Request, src config.IngestSource, sourceTenant, raw string) {
	if src.AppID != "touch-engine" {
		writeReceipt(w, raw)
		return
	}
	updated, ack, err := s.prepareSourceAcknowledgement(r, src, sourceTenant, raw)
	if err == nil {
		err = notifyingest.AcknowledgeRecord(r.Context(), src.FetchBase, src.FetchToken, src.FetchServiceID, sourceTenant, ack)
	}
	if err != nil {
		if code := notifyScopeErrorCode(err); code != "" {
			s.Log.Printf("notify ingest consent scope pending: %v", err)
			fail(w, http.StatusServiceUnavailable, code, "source acknowledgement is pending; source permissions need provenance review")
			return
		}
		s.Log.Printf("notify ingest source acknowledgement pending source=%s", src.AppID)
		fail(w, http.StatusServiceUnavailable, "source_ack_pending", "local receipt is durable; retry the original event to confirm source acknowledgement")
		return
	}
	writeReceipt(w, updated)
}

func (s *Server) prepareSourceAcknowledgement(r *http.Request, src config.IngestSource, sourceTenant, raw string) (string, notifyingest.Acknowledgement, error) {
	var empty notifyingest.Acknowledgement
	var receipt map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &receipt); err != nil {
		return "", empty, err
	}
	var event string
	if err := json.Unmarshal(receipt["event_id"], &event); err != nil {
		return "", empty, err
	}
	row, err := s.lookupNotifyEvent(src.AppID, tenantmap.NamespaceNotify, sourceTenant, event)
	if err != nil {
		return "", empty, err
	}
	rec, err := fetchIngestRecord(r, src, sourceTenant, row.SourceRef)
	if err != nil {
		return "", empty, err
	}
	if err := notifyingest.ValidateTouchRecord(rec, row.SourceVersion); err != nil {
		return "", empty, err
	}
	tx, err := s.St.DB.Begin()
	if err != nil {
		return "", empty, err
	}
	defer tx.Rollback()
	// Re-read under the write transaction. A concurrently advanced fact must never
	// send an ACK inferred from a replaced older receipt.
	row, err = getInboxEvent(tx, src.AppID, tenantmap.NamespaceNotify, sourceTenant, event)
	if err != nil {
		return "", empty, err
	}
	if err := json.Unmarshal([]byte(row.ReceiptJSON), &receipt); err != nil {
		return "", empty, err
	}
	rev, revErr := lookupRevocationTx(tx, src.AppID, tenantmap.NamespaceNotify, sourceTenant, row.SourceRef)
	if revErr != nil && !errors.Is(revErr, sql.ErrNoRows) {
		return "", empty, revErr
	}
	target := inboxTargetID(row)
	_, contact, found, sourceScopeErr := store.FindTouchLeadBySourceTx(tx, target, sourceTenant, row.SourceRef)
	if sourceScopeErr != nil && notifyScopeErrorCode(sourceScopeErr) == "" {
		return "", empty, sourceScopeErr
	}
	if sourceScopeErr == nil && !found && row.ContactID != "" {
		sourceScopeErr = fmt.Errorf("%w: inbox=%s", store.ErrNotifySourceScopeConflict, row.ID)
	}
	state := "stored"
	if rec.Tombstone {
		state = "revoked"
		if revErr == nil && rec.SourceRevision < rev.SourceVersion {
			return "", empty, fmt.Errorf("source below revocation waterline")
		}
		// A pre-existing accepted inbox may be the only delivery replayed after
		// source revocation. Converge its original target before acknowledging it.
		if errors.Is(sourceScopeErr, store.ErrNotifySourceOwnerMismatch) {
			var observed int
			_ = json.Unmarshal(receipt["source_tombstone_revision"], &observed)
			receipt["source_tombstone_revision"], _ = json.Marshal(maxInt(observed, rec.SourceRevision))
			return "", empty, persistConsentScopePendingTx(tx, row, receipt, sourceScopeErr)
		}
		decision := decisionFromInbox(row, sourceTenant)
		if err := store.UpsertTouchRevocationTx(tx, store.NotifyRevocation{TenantID: target, SourceApp: src.AppID, SourceNS: tenantmap.NamespaceNotify, SourceTenantID: sourceTenant, SourceRef: row.SourceRef, SourceVersion: rec.SourceRevision, MapTargetTenantID: target, MapVersion: decision.Version, MapBasis: decision.Basis}); err != nil {
			if notifyScopeErrorCode(err) != "" {
				receipt["source_tombstone_revision"], _ = json.Marshal(maxInt(jsonRevision(receipt["source_tombstone_revision"]), rec.SourceRevision))
				return "", empty, persistConsentScopePendingTx(tx, row, receipt, err)
			}
			return "", empty, err
		}
		if sourceScopeErr != nil {
			return "", empty, persistConsentScopePendingTx(tx, row, receipt, sourceScopeErr)
		}
		if contact != "" {
			if err := store.RevokeTouchSubmissionTx(tx, target, contact, sourceTenant, row.SourceRef, "source_revocation"); err != nil {
				if !errors.Is(err, store.ErrConsentScopeConflict) {
					return "", empty, err
				}
				// Keep the source tombstone/waterline durable even though ownership
				// needs review. No source acknowledgement is sent for this scope.
				return "", empty, persistConsentScopePendingTx(tx, row, receipt, err)
			}
		}
	} else if jsonRevision(receipt["source_tombstone_revision"]) > 0 {
		return "", empty, fmt.Errorf("%w: inbox=%s", store.ErrNotifySourceScopeConflict, row.ID)
	} else if revErr == nil {
		// Never publish stored against any persisted revocation, even if a source
		// unexpectedly serves an active record again.
		return "", empty, fmt.Errorf("source conflicts with persisted revocation")
	} else if sourceScopeErr != nil {
		return "", empty, persistConsentScopePendingTx(tx, row, receipt, sourceScopeErr)
	} else if contact != "" {
		if _, err := store.ResolveTouchConsentRefTx(tx, target, contact, sourceTenant, row.SourceRef, false); err != nil {
			if errors.Is(err, store.ErrConsentScopeConflict) {
				return "", empty, persistConsentScopePendingTx(tx, row, receipt, err)
			}
			return "", empty, err
		}
	}
	var ack notifyingest.Acknowledgement
	if saved, ok := receipt["source_ack"]; ok {
		if err := json.Unmarshal(saved, &ack); err != nil {
			return "", empty, err
		}
		if (ack.ReceiverState != "stored" && ack.ReceiverState != "revoked") || ack.SourceEventID != row.ProfileEventID || ack.SubmissionID != row.SourceRef || ack.SourceRevision <= 0 || ack.SourceRevision > rec.SourceRevision || ack.ReceiptID != sourceAcknowledgement(src.AppID, sourceTenant, ack.SourceEventID, ack.SubmissionID, ack.SourceRevision, ack.ReceiverState).ReceiptID {
			return "", empty, fmt.Errorf("invalid persisted source acknowledgement")
		}
		if ack.ReceiverState == "revoked" && state != "revoked" {
			return "", empty, fmt.Errorf("persisted acknowledgement is revoked")
		}
	} else if !rec.Tombstone && rec.SourceRevision != row.SourceVersion {
		// Legacy accepted rows did not record the source revision they applied. A
		// newer live revision needs its original delivery; never claim it was stored.
		return "", empty, fmt.Errorf("legacy receipt lacks current source revision")
	}
	if ack.ReceiptID == "" || (state == "revoked" && (ack.ReceiverState != "revoked" || ack.SourceRevision != rec.SourceRevision)) {
		ack = sourceAcknowledgement(src.AppID, sourceTenant, row.ProfileEventID, row.SourceRef, rec.SourceRevision, state)
	}
	delete(receipt, "scope_error")
	delete(receipt, "revocation_pending")
	delete(receipt, "source_ack_pending")
	receipt["consent_scope"], _ = json.Marshal(store.TouchConsentRef(sourceTenant, row.SourceRef))
	receipt["source_ack"], _ = json.Marshal(ack)
	updated, err := json.Marshal(receipt)
	if err != nil {
		return "", empty, err
	}
	if err := store.UpdateNotifyReceiptTx(tx, row.ID, string(updated)); err != nil {
		return "", empty, err
	}
	if err := tx.Commit(); err != nil {
		return "", empty, err
	}
	return string(updated), ack, nil
}

// The legacy sources retain their existing marketing-only protocol. Touch
// revokes the entire permission set attached to its consultation submission.
func revokedNotifyChannels(app string) []string {
	if app == "touch-engine" {
		return []string{"form_authorization", "channel_reply", "marketing_phone", "marketing_sms"}
	}
	return []string{"marketing_phone", "marketing_sms"}
}

// Commits a pending result rather than losing an observed source tombstone on
// the 503 path. It never changes ambiguous consent rows or invents an ACK.
func persistConsentScopePendingTx(tx *sql.Tx, row store.NotifyInbox, receipt map[string]json.RawMessage, scopeErr error) error {
	receipt["scope_error"], _ = json.Marshal(notifyScopeErrorCode(scopeErr))
	receipt["source_ack_pending"], _ = json.Marshal(true)
	// revoked watermark, if observed, is already in the same transaction.
	updated, err := json.Marshal(receipt)
	if err != nil {
		return err
	}
	if err := store.UpdateNotifyReceiptTx(tx, row.ID, string(updated)); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return scopeErr
}

func notifyScopeErrorCode(err error) string {
	if errors.Is(err, store.ErrNotifySourceScopeConflict) {
		return "source_scope_conflict"
	}
	if errors.Is(err, store.ErrConsentScopeConflict) {
		return "consent_scope_conflict"
	}
	return ""
}

func jsonRevision(raw json.RawMessage) int {
	var revision int
	_ = json.Unmarshal(raw, &revision)
	return revision
}
