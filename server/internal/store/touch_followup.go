package store

import (
	"database/sql"
	"encoding/json"
	"errors"
)

var ErrFollowUpRevoked = errors.New("follow-up source permission revoked or unresolved")

// TouchFollowUpLead resolves only the committed complete Notify identity.
// It never substitutes a phone, receiver receipt or bare source_refs LIMIT 1.
// The read transaction does not alter the original revocation watermark.
func (s *Store) TouchFollowUpLead(sourceTenant, ref string, revision int) (Lead, error) {
	tx, err := s.DB.Begin()
	if err != nil {
		return Lead{}, err
	}
	defer tx.Rollback()
	rows, err := tx.Query(`SELECT `+inboxCols+` FROM notify_inbox WHERE source_app='touch-engine' AND source_ns='notify' AND source_tenant_id=? AND source_ref=? AND event_type='lead.authorized_submitted'`, sourceTenant, ref)
	if err != nil {
		return Lead{}, err
	}
	var chosen NotifyInbox
	seen := map[string]bool{}
	for rows.Next() {
		n, e := scanInbox(rows)
		if e != nil {
			rows.Close()
			return Lead{}, e
		}
		seen[n.TenantID+"\x00"+n.LeadID] = true
		if chosen.ID == "" || n.SourceVersion > chosen.SourceVersion {
			chosen = n
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return Lead{}, err
	}
	if len(seen) == 0 {
		return Lead{}, sql.ErrNoRows
	}
	if len(seen) != 1 {
		return Lead{}, ErrAmbiguousNotify
	}
	if chosen.LeadID == "" || chosen.TenantID == "" || chosen.MapTargetTenantID != chosen.TenantID || chosen.SourceVersion < revision {
		return Lead{}, ErrFollowUpRevoked
	}
	var receipt struct {
		Ingestion string `json:"ingestion"`
		Pending   bool   `json:"revocation_pending"`
		Tombstone int    `json:"source_tombstone_revision"`
		ACK       struct {
			State string `json:"receiver_state"`
		} `json:"source_ack"`
	}
	if json.Unmarshal([]byte(chosen.ReceiptJSON), &receipt) != nil || receipt.Ingestion != "accepted" || receipt.Pending || receipt.Tombstone > 0 || receipt.ACK.State != "stored" {
		return Lead{}, ErrFollowUpRevoked
	}
	var count int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM notify_revocations WHERE source_app='touch-engine' AND source_ns='notify' AND source_tenant_id=? AND source_ref=?`, sourceTenant, ref).Scan(&count); err != nil {
		return Lead{}, err
	}
	if count > 0 {
		return Lead{}, ErrFollowUpRevoked
	}
	if err := checkTouchSourceScopeTx(tx, chosen.TenantID, sourceTenant, ref); err != nil {
		return Lead{}, err
	}
	lead, err := scanLead(tx.QueryRow(`SELECT `+leadCols+` FROM leads WHERE id=? AND tenant_id=?`, chosen.LeadID, chosen.TenantID))
	if err != nil {
		return Lead{}, err
	}
	if err := tx.QueryRow(`SELECT COUNT(*) FROM contact_consents WHERE tenant_id=? AND contact_id=? AND source_submission_ref=? AND source_channel='form_authorization' AND revoked_at IS NULL`, lead.TenantID, lead.ContactID, TouchConsentRef(sourceTenant, ref)).Scan(&count); err != nil {
		return Lead{}, err
	}
	if count == 0 {
		return Lead{}, ErrFollowUpRevoked
	}
	if err := tx.Commit(); err != nil {
		return Lead{}, err
	}
	return lead, nil
}
