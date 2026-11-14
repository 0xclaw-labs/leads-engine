package store

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
)

var ErrConsentScopeConflict = errors.New("notify consent scope needs provenance review")

// TouchConsentRef preserves the existing opaque consent-key/merge model while
// binding it to the complete source identity. '/' cannot occur in an accepted
// Notify raw source_ref, so a legacy sender cannot name this reserved key.
func TouchConsentRef(sourceTenant, rawRef string) string {
	raw, _ := json.Marshal([]string{"touch-engine", "notify", sourceTenant, rawRef})
	digest := sha256.Sum256(raw)
	return "touch-notify/v1/" + hex.EncodeToString(digest[:])
}

// ResolveTouchConsentRefTx claims a legacy bare ref only with one previously
// committed accepted source identity. A shared/unknown legacy row is never
// attributed by guessing. Renaming keeps consent IDs and revoked markers intact.
// allowNew permits a new grant to leave another source's legacy rows alone.
func ResolveTouchConsentRefTx(tx *sql.Tx, tenant, contact, sourceTenant, rawRef string, allowNew bool) (string, error) {
	scoped := TouchConsentRef(sourceTenant, rawRef)
	var scopedCount, legacyCount int
	if err := tx.QueryRow(`SELECT count(*) FROM contact_consents WHERE tenant_id=? AND contact_id=? AND source_submission_ref=?`, tenant, contact, scoped).Scan(&scopedCount); err != nil {
		return "", err
	}
	if scopedCount > 0 {
		return scoped, nil
	}
	if err := tx.QueryRow(`SELECT count(*) FROM contact_consents WHERE tenant_id=? AND contact_id=? AND source_submission_ref=? AND source_channel IN ('form_authorization','channel_reply','marketing_phone','marketing_sms')`, tenant, contact, rawRef).Scan(&legacyCount); err != nil {
		return "", err
	}
	if legacyCount == 0 {
		return scoped, nil
	}
	rows, err := tx.Query(`SELECT source_app,source_ns,source_tenant_id,receipt_json FROM notify_inbox
 WHERE tenant_id=? AND source_ref=? AND event_type='lead.authorized_submitted'
 AND (contact_id=? OR lead_id IN (SELECT id FROM leads WHERE tenant_id=? AND contact_id=?))`, tenant, rawRef, contact, tenant, contact)
	if err != nil {
		return "", err
	}
	owners := map[string]bool{}
	wanted := false
	unknownTouch := false
	for rows.Next() {
		var app, ns, origin, receipt string
		if err := rows.Scan(&app, &ns, &origin, &receipt); err != nil {
			rows.Close()
			return "", err
		}
		var accepted struct {
			Ingestion string `json:"ingestion"`
		}
		if err := json.Unmarshal([]byte(receipt), &accepted); err != nil {
			rows.Close()
			return "", err
		}
		if accepted.Ingestion != "accepted" {
			continue
		}
		encoded, _ := json.Marshal([]string{app, ns, origin})
		owners[string(encoded)] = true
		if app == "touch-engine" && (ns == "" || origin == "") {
			unknownTouch = true
		}
		if app == "touch-engine" && ns == "notify" && origin == sourceTenant {
			wanted = true
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return "", err
	}
	rows.Close()
	if allowNew && !wanted && !unknownTouch {
		return scoped, nil
	}
	if !wanted || len(owners) != 1 {
		return "", fmt.Errorf("%w: scope=%s contact=%s", ErrConsentScopeConflict, scoped, contact)
	}
	if _, err := tx.Exec(`UPDATE contact_consents SET source_submission_ref=?
 WHERE tenant_id=? AND contact_id=? AND source_submission_ref=?
 AND source_channel IN ('form_authorization','channel_reply','marketing_phone','marketing_sms')`, scoped, tenant, contact, rawRef); err != nil {
		return "", err
	}
	return scoped, nil
}

// RevokeTouchSubmissionTx is deliberately narrower than the existing explicit
// contact-wide marketing opt-out and the legacy sender protocol.
func RevokeTouchSubmissionTx(tx *sql.Tx, tenant, contact, sourceTenant, rawRef, reason string) error {
	scoped, err := ResolveTouchConsentRefTx(tx, tenant, contact, sourceTenant, rawRef, false)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`UPDATE contact_consents SET revoked_at=COALESCE(revoked_at,?),
 revoked_reason=CASE WHEN revoked_at IS NULL THEN ? ELSE revoked_reason END
 WHERE tenant_id=? AND contact_id=? AND source_submission_ref=?
 AND source_channel IN ('form_authorization','channel_reply','marketing_phone','marketing_sms')`, now(), reason, tenant, contact, scoped)
	return err
}

var ErrNotifySourceScopeConflict = errors.New("notify source ref scope needs provenance review")
var ErrNotifySourceOwnerMismatch = errors.New("stored source ref belongs to another source identity")

// Existing source_refs use target/app/rawRef lookup. Touch must also verify the
// stored authority snapshot before using that row or its contact/lead.
func checkTouchSourceScopeTx(tx *sql.Tx, tenant, sourceTenant, rawRef string) error {
	rows, err := tx.Query(`SELECT id,auth_scope_snapshot FROM source_refs WHERE tenant_id=? AND source_app='touch-engine' AND source_ref=?`, tenant, rawRef)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id, snapshot string
		if err := rows.Scan(&id, &snapshot); err != nil {
			return err
		}
		var scope struct {
			App    string `json:"source_app"`
			NS     string `json:"source_ns"`
			Tenant string `json:"source_tenant_id"`
		}
		if json.Unmarshal([]byte(snapshot), &scope) != nil {
			return fmt.Errorf("%w: scope=%s source_row=%s", ErrNotifySourceScopeConflict, TouchConsentRef(sourceTenant, rawRef), id)
		}
		if (scope.App != "" && scope.App != "touch-engine") || (scope.NS != "" && scope.NS != "notify") || (scope.Tenant != "" && scope.Tenant != sourceTenant) {
			return fmt.Errorf("%w: %w: scope=%s source_row=%s", ErrNotifySourceScopeConflict, ErrNotifySourceOwnerMismatch, TouchConsentRef(sourceTenant, rawRef), id)
		}
		if scope.App == "" || scope.NS == "" || scope.Tenant == "" {
			return fmt.Errorf("%w: scope=%s source_row=%s", ErrNotifySourceScopeConflict, TouchConsentRef(sourceTenant, rawRef), id)
		}
	}
	return rows.Err()
}
func FindTouchLeadBySourceTx(tx *sql.Tx, tenant, sourceTenant, rawRef string) (string, string, bool, error) {
	if err := checkTouchSourceScopeTx(tx, tenant, sourceTenant, rawRef); err != nil {
		return "", "", false, err
	}
	return FindLeadBySourceTx(tx, tenant, "touch-engine", rawRef)
}
func EnsureTouchSourceRefTx(tx *sql.Tx, tenant, sourceTenant, rawRef, snapshot string) (string, error) {
	if err := checkTouchSourceScopeTx(tx, tenant, sourceTenant, rawRef); err != nil {
		return "", err
	}
	return EnsureSourceRefTx(tx, tenant, "touch-engine", rawRef, snapshot)
}

// The legacy table has a target/app/rawRef key. Never advance or relabel a
// different source identity's watermark through its ON CONFLICT update.
func UpsertTouchRevocationTx(tx *sql.Tx, rev NotifyRevocation) error {
	var ns, origin string
	err := tx.QueryRow(`SELECT source_ns,source_tenant_id FROM notify_revocations WHERE tenant_id=? AND source_app='touch-engine' AND source_ref=?`, rev.TenantID, rev.SourceRef).Scan(&ns, &origin)
	if err == nil && (ns != "notify" || origin != rev.SourceTenantID) {
		return fmt.Errorf("%w: %w: scope=%s", ErrNotifySourceScopeConflict, ErrNotifySourceOwnerMismatch, TouchConsentRef(rev.SourceTenantID, rev.SourceRef))
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	return UpsertNotifyRevocationTx(tx, rev)
}
