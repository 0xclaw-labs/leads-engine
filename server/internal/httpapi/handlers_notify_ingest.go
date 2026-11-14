// HUI-1680 Notify 接收端。
//
// 只接受部署登记过的来源。验签沿用 platform-notify 的
// X-Notify-Signature: sha256=<hex(hmac(secret, body))>。
// 联系方式不在事件里：按 payload_ref 向登记过的源拉取最小 submission，
// 校验 sha256 后与 inbox 行、线索、consent 同一事务落库。
// 落库前的失败一律不返回成功回执。浏览/企微点击不建联系人。
// 不触发外呼、短信、SOP，也不创建派诺订单。
package httpapi

import (
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/bianjiefilm/leads-engine/server/internal/config"
	"github.com/bianjiefilm/leads-engine/server/internal/notifyingest"
	"github.com/bianjiefilm/leads-engine/server/internal/store"
	"github.com/bianjiefilm/leads-engine/server/internal/subscription"
	"github.com/bianjiefilm/leads-engine/server/internal/tenantmap"
)

const notifyIngestMaxBody = 64 << 10

func (s *Server) handleNotifyIngest(w http.ResponseWriter, r *http.Request) {
	if problems := s.Cfg.IngestGate(); len(problems) > 0 {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"error": "config_gate_ingest", "message": "notify ingest is not configured (fail-closed)", "detail": problems,
		})
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, notifyIngestMaxBody))
	if err != nil {
		fail(w, http.StatusBadRequest, "bad_request", "body too large or unreadable (max 64 KiB)")
		return
	}
	src, ok := matchIngestSignature(s.Cfg.IngestSources, r.Header.Get("X-Notify-Signature"), body)
	if !ok {
		fail(w, http.StatusUnauthorized, "bad_signature", "signature does not match a registered source")
		return
	}
	if !subscription.VisitorLeadAllowed(true, "") {
		fail(w, http.StatusForbidden, "visitor_account_required", "an authorized visitor does not need a platform account")
		return
	}
	delivery, err := notifyingest.ParseDelivery(body)
	if err != nil {
		fail(w, http.StatusBadRequest, "bad_event", "directed event rejected")
		return
	}
	if delivery.Profile.SourceApp != src.AppID {
		fail(w, http.StatusUnauthorized, "bad_signature", "signature source does not match event_profile")
		return
	}
	if delivery.Profile.TargetApp != s.Cfg.AppID {
		fail(w, http.StatusForbidden, "wrong_target", "event target_app is not this application")
		return
	}
	now := time.Now().UTC()
	if delivery.Profile.ExpiresAt != nil && now.After(delivery.Profile.ExpiresAt.UTC()) {
		fail(w, http.StatusBadRequest, "event_expired", "directed event is expired")
		return
	}
	if !delivery.OccurredAt.IsZero() && delivery.OccurredAt.After(now.Add(10*time.Minute)) {
		fail(w, http.StatusBadRequest, "event_skew", "occurred_at is too far in the future")
		return
	}
	sourceTenant := strings.TrimSpace(delivery.TenantID)
	sourceApp := delivery.Profile.SourceApp
	sourceNS := tenantmap.NamespaceNotify
	bodySHA := notifyingest.BodySHA256(body)
	complete := func(raw string) { s.completeNotifyReceipt(w, r, src, sourceTenant, raw) }
	replayOwnScope := func(tx *sql.Tx) bool {
		row, err := store.GetNotifyInboxBySourceFactTx(tx, sourceApp, sourceNS, sourceTenant, notifyingest.EventLeadAuthorizedSubmitted, delivery.Profile.SourceRef)
		if err != nil || row.ContactID == "" {
			return false
		}
		// This is an already committed source fact. Roll back the current
		// attempt, then persist its observed tombstone/pending receipt safely.
		_ = tx.Rollback()
		complete(row.ReceiptJSON)
		return true
	}
	// 同一 trace_id + 整数 source_version：事实相同回原回执，不插第二行；事实不同则冲突，不覆盖。
	// 更高的 source_version（撤回）不是这一版的重复。brand_display_name 不参与租户。
	if row, err := s.St.GetNotifyInboxByTrace(sourceApp, sourceNS, sourceTenant, delivery.Payload.TraceID, delivery.Profile.SourceVersion); respondTrace(w, row, err, delivery, complete) {
		return
	}
	// Replay is keyed by the source identity stored on the first accept.
	// Current bindings are not consulted, so a later local tenant or a
	// changed map cannot move the receipt.
	if existing, err := s.lookupNotifyEvent(sourceApp, sourceNS, sourceTenant, delivery.Profile.EventID); err == nil {
		if existing.BodySHA256 != bodySHA {
			fail(w, http.StatusConflict, "event_content_conflict", "the same event key was delivered with different content")
			return
		}
		complete(existing.ReceiptJSON)
		return
	} else if !errors.Is(err, sql.ErrNoRows) {
		writeTenantMapError(w, err)
		return
	}

	if prior, err := s.lookupNotifyFact(sourceApp, sourceNS, sourceTenant, delivery.Profile.EventType, delivery.Profile.SourceRef); err == nil && delivery.Profile.SourceVersion < prior.SourceVersion {
		complete(prior.ReceiptJSON)
		return
	} else if err != nil && !errors.Is(err, sql.ErrNoRows) {
		writeTenantMapError(w, err)
		return
	}

	decision, err := s.admitNotifyTenant(sourceApp, sourceTenant, delivery.Profile.SourceRef)
	if err != nil {
		writeTenantMapError(w, err)
		return
	}
	delivery.TenantID = decision.TargetTenant

	rec, err := fetchIngestRecord(r, src, sourceTenant, delivery.Profile.SourceRef)
	if err != nil {
		s.Log.Printf("notify ingest fetch failed tenant=%s source=%s event=%s", delivery.TenantID, src.AppID, delivery.Profile.EventID)
		fail(w, http.StatusServiceUnavailable, "source_unavailable", "authorized submission could not be fetched; not accepted")
		return
	}
	if src.AppID == "touch-engine" {
		if err := notifyingest.ValidateTouchRecord(rec, delivery.Profile.SourceVersion); err != nil {
			fail(w, http.StatusUnprocessableEntity, "invalid_source_record", "source authorization metadata is invalid")
			return
		}
		if (delivery.Profile.EventType == notifyingest.EventLeadConsentRevoked || delivery.Payload.Revoked) && !rec.Tombstone {
			fail(w, http.StatusUnprocessableEntity, "invalid_source_record", "revocation requires a source tombstone")
			return
		}
	}
	if !(src.AppID == "touch-engine" && rec.Tombstone) && notifyingest.RecordSHA256(rec.Name, rec.Phone, rec.Wechat) != delivery.Profile.PayloadRef.SHA256 {
		fail(w, http.StatusConflict, "payload_hash_mismatch", "fetched submission does not match payload_ref.sha256")
		return
	}
	if notifyingest.NonLeadChannel(delivery.Payload.Channel, rec.Purpose) {
		fail(w, http.StatusUnprocessableEntity, "not_a_submission", "browse or click events do not create contacts")
		return
	}

	revoke := delivery.Profile.EventType == notifyingest.EventLeadConsentRevoked || delivery.Payload.Revoked || rec.Authorization == "revoked"
	if !revoke && rec.Authorization != "granted" {
		fail(w, http.StatusUnprocessableEntity, "authorization_required", "submission is not authorized")
		return
	}

	tx, err := s.St.DB.Begin()
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "ingest begin failed")
		return
	}
	defer tx.Rollback()

	if row, err := store.GetNotifyInboxByTraceTx(tx, sourceApp, sourceNS, sourceTenant, delivery.Payload.TraceID, delivery.Profile.SourceVersion); traceKindOf(row, err, delivery) != traceNone {
		switch traceKindOf(row, err, delivery) {
		case traceLookupErr:
			fail(w, http.StatusInternalServerError, "internal", "trace lookup failed")
			return
		case traceConflict:
			fail(w, http.StatusConflict, "event_content_conflict", "the same trace and source_version were delivered with different content")
			return
		default:
			if err := tx.Commit(); err != nil {
				fail(w, http.StatusInternalServerError, "internal", "ingest commit failed")
				return
			}
			complete(row.ReceiptJSON)
			return
		}
	}
	if existing, err := getInboxEvent(tx, sourceApp, sourceNS, sourceTenant, delivery.Profile.EventID); err == nil {
		if existing.BodySHA256 != bodySHA {
			fail(w, http.StatusConflict, "event_content_conflict", "the same event key was delivered with different content")
			return
		}
		if err := tx.Commit(); err != nil {
			fail(w, http.StatusInternalServerError, "internal", "ingest commit failed")
			return
		}
		complete(existing.ReceiptJSON)
		return
	} else if !errors.Is(err, sql.ErrNoRows) {
		writeTenantMapError(w, err)
		return
	}

	// 事实键上 source_version 单调。更旧的另一个 event_id 只确认、不改数据，
	// 避免 Notify 把 500 重试到死信。同版本异正文仍是 409。
	// 已有事实行的目标租户钉死，不随当前绑定重算。
	prior, factErr := getInboxFact(tx, sourceApp, sourceNS, sourceTenant, delivery.Profile.EventType, delivery.Profile.SourceRef)
	if factErr == nil {
		delivery.TenantID = inboxTargetID(prior)
		decision = decisionFromInbox(prior, sourceTenant)
	} else if !errors.Is(factErr, sql.ErrNoRows) {
		writeTenantMapError(w, factErr)
		return
	}
	advance := false
	if factErr == nil {
		switch {
		case delivery.Profile.SourceVersion < prior.SourceVersion,
			delivery.Profile.SourceVersion == prior.SourceVersion && prior.BodySHA256 == bodySHA:
			if err := tx.Commit(); err != nil {
				fail(w, http.StatusInternalServerError, "internal", "ingest commit failed")
				return
			}
			complete(prior.ReceiptJSON)
			return
		case delivery.Profile.SourceVersion == prior.SourceVersion:
			fail(w, http.StatusConflict, "event_content_conflict", "the same fact was delivered with different content")
			return
		default:
			advance = true
		}
	}

	rev, revErr := lookupRevocationTx(tx, sourceApp, sourceNS, sourceTenant, delivery.Profile.SourceRef)
	if revErr != nil && !errors.Is(revErr, sql.ErrNoRows) {
		writeTenantMapError(w, revErr)
		return
	}
	if revErr == nil {
		revTenant := rev.MapTargetTenantID
		if revTenant == "" {
			revTenant = rev.TenantID
		}
		if factErr == nil && revTenant != delivery.TenantID {
			writeTenantMapError(w, &tenantmap.Error{Kind: tenantmap.KindConflict, Hint: "The stored submission and revocation for this source ref name different targets. Neither row is moved."})
			return
		}
		if factErr != nil {
			delivery.TenantID = revTenant
			decision = decisionFromRevocation(rev, sourceTenant)
		}
	}
	revVersion, revokedBefore := 0, false
	if revErr == nil {
		revVersion = rev.SourceVersion
		revokedBefore = true
	}
	if factErr == nil {
		var pending struct {
			Revision int `json:"source_tombstone_revision"`
		}
		if json.Unmarshal([]byte(prior.ReceiptJSON), &pending) == nil && pending.Revision > 0 {
			revVersion = maxInt(revVersion, pending.Revision)
			revokedBefore = true
		}
	}
	marketingBlocked := revoke || revokedBefore
	serviceBlocked := marketingBlocked && src.AppID == "touch-engine"

	var leadID, contactID, class string
	duplicate := false
	scopePending := false
	scopePendingCode := ""
	if serviceBlocked && !revoke {
		fail(w, http.StatusServiceUnavailable, "source_ack_pending", "source conflicts with persisted revocation; permissions cannot be restored")
		return
	}
	if revoke {
		leadID, contactID, _, err = findLead(tx, delivery)
		if code := notifyScopeErrorCode(err); code != "" {
			if replayOwnScope(tx) {
				return
			}
			// A rejected foreign source ref must not occupy the original
			// owner's legacy inbox/revocation keys. Its source outbox retains
			// the tombstone until provenance is resolved.
			s.Log.Printf("notify source scope rejected: %v", err)
			fail(w, http.StatusServiceUnavailable, code, "source authority is unresolved; no local business record was changed or acknowledged")
			return
		} else if err != nil {
			fail(w, http.StatusInternalServerError, "internal", "lead lookup failed")
			return
		}
		if contactID != "" {
			if src.AppID == "touch-engine" {
				err = store.RevokeTouchSubmissionTx(tx, delivery.TenantID, contactID, sourceTenant, delivery.Profile.SourceRef, "notify_revoke")
				scopePending = errors.Is(err, store.ErrConsentScopeConflict)
				if scopePending {
					scopePendingCode = notifyScopeErrorCode(err)
				}
			} else {
				err = store.RevokeMarketingTx(tx, delivery.TenantID, contactID, "notify_revoke")
				if err == nil {
					err = store.RevokeConsentChannelsTx(tx, delivery.TenantID, contactID, revokedNotifyChannels(src.AppID), "notify_revoke")
				}
			}
			if err != nil && !scopePending {
				fail(w, http.StatusInternalServerError, "internal", "revoke failed")
				return
			}
		}
		waterline := maxInt(delivery.Profile.SourceVersion, revVersion)
		if src.AppID == "touch-engine" {
			waterline = maxInt(waterline, rec.SourceRevision)
		}
		if err := storeUpsertRevocation(tx, delivery, sourceTenant, decision, waterline); err != nil {
			if code := notifyScopeErrorCode(err); code != "" {
				if replayOwnScope(tx) {
					return
				}
				fail(w, http.StatusServiceUnavailable, code, "source revocation authority needs review; no acknowledgement was sent")
				return
			}
			fail(w, http.StatusInternalServerError, "internal", "revocation write failed")
			return
		}
		class = "revocation"
	} else {
		name := strings.TrimSpace(rec.Name)
		if name == "" && strings.TrimSpace(rec.ChannelSubjectRef) != "" && delivery.Profile.SourceApp != "touch-engine" {
			name = "未留姓名"
		}
		if name == "" && !(delivery.Profile.SourceApp == "touch-engine" && store.NormalizePhone(rec.Phone) != "" && store.LeadFilterReason(rec.Phone, "") == "") {
			fail(w, http.StatusUnprocessableEntity, "insufficient_contact", "authorized submission has no usable contact")
			return
		}
		snapshot := provenanceSnapshot(delivery, sourceTenant, decision)
		sourceRefID, err := ensureSource(tx, delivery, snapshot)
		if err != nil {
			if code := notifyScopeErrorCode(err); code != "" {
				s.Log.Printf("notify source scope rejected: %v", err)
				fail(w, http.StatusConflict, code, "stored source authority does not match this source; no lead or permission was changed")
			} else {
				fail(w, http.StatusInternalServerError, "internal", "source ref failed")
			}
			return
		}
		var found bool
		leadID, contactID, found, err = findLead(tx, delivery)
		if err != nil {
			fail(w, http.StatusInternalServerError, "internal", "lead lookup failed")
			return
		}
		if !found {
			res, err := store.IntakeLeadInTx(tx, store.IntakeInput{
				TenantID: delivery.TenantID, SourceApp: delivery.Profile.SourceApp, SourceNS: "notify",
				EventID: delivery.Profile.EventID, Content: body,
				ContactName: name, Phone: rec.Phone, Email: rec.Email,
				BusinessCategory: "merchant_customer", SourceType: sourceTypeFor(delivery.Profile.SourceApp),
				SourceRefID: sourceRefID, Pepper: s.Cfg.DedupPepper,
				FilterEnabled: s.Cfg.FeatureLeadsFilter, AssignEnabled: s.Cfg.FeatureLeadsAssign,
			})
			if errors.Is(err, store.ErrEventContentConflict) {
				fail(w, http.StatusConflict, "event_content_conflict", "the same event key was delivered with different content")
				return
			}
			if err != nil {
				fail(w, http.StatusInternalServerError, "internal", "intake failed")
				return
			}
			leadID, contactID, class, duplicate = res.LeadID, res.ContactID, res.Class, res.Duplicate
		} else {
			class = "existing"
		}
		consentRef := delivery.Profile.SourceRef
		if src.AppID == "touch-engine" {
			consentRef, err = store.ResolveTouchConsentRefTx(tx, delivery.TenantID, contactID, sourceTenant, delivery.Profile.SourceRef, true)
			if err != nil {
				if errors.Is(err, store.ErrConsentScopeConflict) {
					s.Log.Printf("notify consent scope pending: %v", err)
					fail(w, http.StatusServiceUnavailable, "consent_scope_conflict", "permissions need provenance review before source acknowledgement")
				} else {
					fail(w, http.StatusInternalServerError, "internal", "consent scope lookup failed")
				}
				return
			}
		}
		notice := firstNonEmptyStr(rec.Consents.NoticeVersion, delivery.Payload.ConsentVersion)
		phoneAllowed := rec.Consents.MarketingPhone && !marketingBlocked
		smsAllowed := rec.Consents.MarketingSMS && !marketingBlocked
		if err := store.UpsertConsentsTx(tx, delivery.TenantID, contactID, []store.ConsentUpsert{
			{SourceSubmissionRef: consentRef, SourceChannel: "form_authorization", NoticeVersion: notice, Purpose: "form_authorization", MarketingAllowed: rec.Consents.FormAuthorized && !serviceBlocked},
			{SourceSubmissionRef: consentRef, SourceChannel: "channel_reply", NoticeVersion: notice, Purpose: "channel_reply", MarketingAllowed: rec.Consents.ChannelReply && !serviceBlocked},
			{SourceSubmissionRef: consentRef, SourceChannel: "marketing_phone", NoticeVersion: notice, Purpose: "marketing_phone", MarketingAllowed: phoneAllowed},
			{SourceSubmissionRef: consentRef, SourceChannel: "marketing_sms", NoticeVersion: notice, Purpose: "marketing_sms", MarketingAllowed: smsAllowed},
		}); err != nil {
			fail(w, http.StatusInternalServerError, "internal", "consent write failed")
			return
		}
		if marketingBlocked {
			if err := store.RevokeConsentChannelsTx(tx, delivery.TenantID, contactID, revokedNotifyChannels(src.AppID), "prior_revocation"); err != nil {
				fail(w, http.StatusInternalServerError, "internal", "revoke failed")
				return
			}
		}
		if err := setNotes(tx, delivery.TenantID, contactID, rec); err != nil {
			fail(w, http.StatusInternalServerError, "internal", "contact note failed")
			return
		}
	}

	receipt := map[string]any{
		"ingestion":      ingestionLabel(revoke, contactID),
		"event_id":       delivery.Profile.EventID,
		"source_ref":     delivery.Profile.SourceRef,
		"source_version": delivery.Profile.SourceVersion,
		"duplicate":      duplicate,
		"class":          class,
	}
	if leadID != "" {
		receipt["lead_id"] = leadID
		receipt["contact_id"] = contactID
	}
	if scopePending {
		receipt["ingestion"] = "revocation_pending"
		receipt["scope_error"] = scopePendingCode
		receipt["revocation_pending"] = true
	}
	if src.AppID == "touch-engine" && !scopePending {
		receipt["consent_scope"] = store.TouchConsentRef(sourceTenant, delivery.Profile.SourceRef)
		state := "stored"
		if revoke || revokedBefore {
			state = "revoked"
		}
		receipt["source_ack"] = sourceAcknowledgement(src.AppID, sourceTenant, delivery.Profile.EventID, delivery.Profile.SourceRef, rec.SourceRevision, state)
	}
	raw, _ := json.Marshal(receipt)
	occurred := ""
	if !delivery.OccurredAt.IsZero() {
		occurred = delivery.OccurredAt.UTC().Format(time.RFC3339)
	}
	inboxRow := store.NotifyInbox{
		TenantID: delivery.TenantID, SourceApp: delivery.Profile.SourceApp, EventType: delivery.Profile.EventType,
		SourceRef: delivery.Profile.SourceRef, SourceVersion: delivery.Profile.SourceVersion,
		ProfileEventID: delivery.Profile.EventID, NotifyEventID: delivery.NotifyEventID,
		DeliveryID: r.Header.Get("X-Notify-Delivery-Id"), BodySHA256: bodySHA,
		LeadID: leadID, ContactID: contactID, ReceiptJSON: string(raw), OccurredAt: occurred,
		SourceNS: sourceNS, SourceTenantID: sourceTenant,
		MapTargetTenantID: decision.TargetTenant, MapVersion: decision.Version, MapBasis: decision.Basis,
		TraceID: delivery.Payload.TraceID, PayloadJSON: notifyingest.CanonicalFact(delivery),
	}
	var writeErr error
	if advance {
		writeErr = store.UpdateNotifyInboxFactTx(tx, prior.ID, inboxRow)
	} else {
		writeErr = store.InsertNotifyInboxTx(tx, inboxRow)
	}
	if writeErr != nil && uniqueViolation(writeErr) {
		if row, rerr := store.GetNotifyInboxByTraceTx(tx, sourceApp, sourceNS, sourceTenant, delivery.Payload.TraceID, delivery.Profile.SourceVersion); rerr == nil {
			if notifyingest.SameFact(row.PayloadJSON, delivery) {
				if err := tx.Commit(); err != nil {
					fail(w, http.StatusInternalServerError, "internal", "ingest commit failed")
					return
				}
				complete(row.ReceiptJSON)
				return
			}
			fail(w, http.StatusConflict, "event_content_conflict", "the same trace and source_version were delivered with different content")
			return
		}
		if row, rerr := getInboxEvent(tx, sourceApp, sourceNS, sourceTenant, delivery.Profile.EventID); rerr == nil && row.BodySHA256 == bodySHA {
			if err := tx.Commit(); err != nil {
				fail(w, http.StatusInternalServerError, "internal", "ingest commit failed")
				return
			}
			complete(row.ReceiptJSON)
			return
		}
	}
	if writeErr != nil {
		fail(w, http.StatusInternalServerError, "internal", "inbox write failed")
		return
	}
	if err := tx.Commit(); err != nil {
		fail(w, http.StatusInternalServerError, "internal", "ingest commit failed")
		return
	}
	s.Log.Printf("notify ingest tenant=%s source=%s event=%s class=%s lead=%s phone_fpr=%s dup=%t",
		delivery.TenantID, delivery.Profile.SourceApp, delivery.Profile.EventID, class, leadID,
		shortFPR(s.Cfg.DedupPepper, rec.Phone), duplicate)
	complete(string(raw))
}

func (s *Server) handleNotifyReceipt(w http.ResponseWriter, r *http.Request) {
	if problems := s.Cfg.IngestGate(); len(problems) > 0 {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"error": "config_gate_ingest", "message": "notify ingest is not configured (fail-closed)", "detail": problems,
		})
		return
	}
	appID := strings.TrimSpace(r.Header.Get("X-Notify-App-ID"))
	src, ok := sourceByApp(s.Cfg.IngestSources, appID)
	if !ok {
		fail(w, http.StatusUnauthorized, "bad_signature", "source is not registered")
		return
	}
	macBody := "GET\n" + r.URL.Path
	if !notifyingest.VerifySignature(src.Secret, r.Header.Get("X-Notify-Signature"), []byte(macBody)) {
		fail(w, http.StatusUnauthorized, "bad_signature", "signature does not match a registered source")
		return
	}
	eventID := r.PathValue("eventID")
	rows, err := s.St.ListNotifyInboxByEvent(appID, eventID)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "receipt lookup failed")
		return
	}
	if len(rows) == 0 {
		fail(w, http.StatusNotFound, "not_found", "no receipt for this event")
		return
	}
	if len(rows) > 1 {
		fail(w, http.StatusConflict, "ambiguous_receipt", "more than one receipt for this event")
		return
	}
	writeReceipt(w, rows[0].ReceiptJSON)
}

func matchIngestSignature(sources []config.IngestSource, header string, body []byte) (config.IngestSource, bool) {
	var matched config.IngestSource
	n := 0
	for _, src := range sources {
		if notifyingest.VerifySignature(src.Secret, header, body) {
			matched = src
			n++
		}
	}
	return matched, n == 1
}

func sourceByApp(sources []config.IngestSource, appID string) (config.IngestSource, bool) {
	for _, src := range sources {
		if src.AppID == appID {
			return src, true
		}
	}
	return config.IngestSource{}, false
}

func writeReceipt(w http.ResponseWriter, raw string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(raw))
	if !strings.HasSuffix(raw, "\n") {
		_, _ = w.Write([]byte("\n"))
	}
}

func ingestionLabel(revoke bool, contactID string) string {
	if !revoke {
		return "accepted"
	}
	if contactID == "" {
		return "revocation_accepted"
	}
	return "revoked"
}

func sourceTypeFor(app string) string {
	if app == "touch-engine" {
		return "touch_campaign"
	}
	return "form"
}

func provenanceSnapshot(d notifyingest.Delivery, sourceTenant string, decision tenantmap.Decision) string {
	b, _ := json.Marshal(map[string]any{
		"campaign_ref": d.Payload.CampaignRef, "store_ref": d.Payload.StoreRef,
		"channel": d.Payload.Channel, "tag": d.Payload.Tag, "asset_ref": d.Payload.AssetRef,
		"source_version": d.Profile.SourceVersion, "occurred_at": d.OccurredAt.UTC().Format(time.RFC3339),
		"campaign_version":       d.Payload.CampaignVersion,
		"trace_id":               d.Payload.TraceID,
		"return_target":          d.Payload.ReturnTarget,
		"grant_ref":              d.Payload.GrantRef,
		"consent_ref":            d.Payload.ConsentRef,
		"consent_version":        d.Payload.ConsentVersion,
		"consent_at":             d.Payload.ConsentAt,
		"marketing_optin":        d.Payload.MarketingOptin,
		"brand_display_name":     d.Payload.BrandDisplayName,
		"notification_brand_ref": d.Payload.NotificationBrandRef,
		"source_tenant_id":       sourceTenant,
		"map_target_tenant_id":   decision.TargetTenant,
		"map_version":            decision.Version,
		"map_basis":              decision.Basis,
		"source_app":             d.Profile.SourceApp,
		"source_ns":              decision.SourceNS,
	})
	return string(b)
}

type traceKind int

const (
	traceNone traceKind = iota
	traceReplayHit
	traceConflict
	traceLookupErr
)

func traceKindOf(row store.NotifyInbox, err error, d notifyingest.Delivery) traceKind {
	if d.Payload.TraceID == "" || errors.Is(err, sql.ErrNoRows) {
		return traceNone
	}
	if err != nil {
		return traceLookupErr
	}
	if !notifyingest.SameFact(row.PayloadJSON, d) {
		return traceConflict
	}
	return traceReplayHit
}

func respondTrace(w http.ResponseWriter, row store.NotifyInbox, err error, d notifyingest.Delivery, complete func(string)) bool {
	switch traceKindOf(row, err, d) {
	case traceNone:
		return false
	case traceLookupErr:
		fail(w, http.StatusInternalServerError, "internal", "trace lookup failed")
	case traceConflict:
		fail(w, http.StatusConflict, "event_content_conflict", "the same trace and source_version were delivered with different content")
	default:
		complete(row.ReceiptJSON)
	}
	return true
}

func firstNonEmptyStr(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func contactExtraNotes(rec notifyingest.Record) string {
	extra := map[string]string{}
	if v := strings.TrimSpace(rec.Wechat); v != "" {
		extra["wechat"] = v
	}
	if v := strings.TrimSpace(rec.ChannelSubjectRef); v != "" {
		extra["channel_subject_ref"] = v
	}
	if len(extra) == 0 {
		return ""
	}
	b, _ := json.Marshal(extra)
	return string(b)
}

// wrappers keep the handler from reaching into unexported store helpers.
func getInboxEvent(tx *sql.Tx, app, ns, sourceTenant, eventID string) (store.NotifyInbox, error) {
	row, err := store.GetNotifyInboxBySourceEventTx(tx, app, ns, sourceTenant, eventID)
	if errors.Is(err, sql.ErrNoRows) {
		return store.GetLegacyNotifyInboxByEventTx(tx, app, eventID)
	}
	return row, err
}

func getInboxFact(tx *sql.Tx, app, ns, sourceTenant, eventType, sourceRef string) (store.NotifyInbox, error) {
	row, err := store.GetNotifyInboxBySourceFactTx(tx, app, ns, sourceTenant, eventType, sourceRef)
	if errors.Is(err, sql.ErrNoRows) {
		return store.GetLegacyNotifyInboxByFactTx(tx, app, eventType, sourceRef)
	}
	return row, err
}

func lookupRevocationTx(tx *sql.Tx, app, ns, sourceTenant, sourceRef string) (store.NotifyRevocation, error) {
	rev, err := store.NotifyRevocationBySourceTx(tx, app, ns, sourceTenant, sourceRef)
	if errors.Is(err, sql.ErrNoRows) {
		return store.LegacyNotifyRevocationTx(tx, app, sourceRef)
	}
	return rev, err
}

func inboxTargetID(n store.NotifyInbox) string {
	if n.MapTargetTenantID != "" {
		return n.MapTargetTenantID
	}
	return n.TenantID
}

func uniqueViolation(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}

func findLead(tx *sql.Tx, d notifyingest.Delivery) (string, string, bool, error) {
	if d.Profile.SourceApp == "touch-engine" {
		return store.FindTouchLeadBySourceTx(tx, d.TenantID, d.Profile.TenantScope, d.Profile.SourceRef)
	}
	return store.FindLeadBySourceTx(tx, d.TenantID, d.Profile.SourceApp, d.Profile.SourceRef)
}

func ensureSource(tx *sql.Tx, d notifyingest.Delivery, snapshot string) (string, error) {
	if d.Profile.SourceApp == "touch-engine" {
		return store.EnsureTouchSourceRefTx(tx, d.TenantID, d.Profile.TenantScope, d.Profile.SourceRef, snapshot)
	}
	return store.EnsureSourceRefTx(tx, d.TenantID, d.Profile.SourceApp, d.Profile.SourceRef, snapshot)
}

func storeUpsertRevocation(tx *sql.Tx, d notifyingest.Delivery, sourceTenant string, decision tenantmap.Decision, version int) error {
	rev := store.NotifyRevocation{TenantID: d.TenantID, SourceApp: d.Profile.SourceApp, SourceNS: decision.SourceNS, SourceRef: d.Profile.SourceRef, SourceTenantID: sourceTenant, SourceVersion: version, MapTargetTenantID: decision.TargetTenant, MapVersion: decision.Version, MapBasis: decision.Basis}
	if d.Profile.SourceApp == "touch-engine" {
		return store.UpsertTouchRevocationTx(tx, rev)
	}
	return store.UpsertNotifyRevocationTx(tx, rev)
}

func setNotes(tx *sql.Tx, tenantID, contactID string, rec notifyingest.Record) error {
	return store.SetContactNotesIfEmptyTx(tx, tenantID, contactID, contactExtraNotes(rec))
}
