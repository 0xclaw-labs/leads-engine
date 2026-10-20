package httpapi

import (
	"strings"
	"testing"
)

// HUI-2598 D3：人工接管（mode=human）下从今日页保存的回复草稿原先既不能
// 批准（store 只认 assist）也不能发送（必须 approved），形成死路。正确语义：
// - human 会话里人类保存的 generated 草稿可以「批准 -> 发送」；
// - AI 会话（未接管）仍不能批准，必须先接管/协助；
// - 草稿未发送前不会出现在访客视图。
func TestHumanDraftCanBeApprovedAndSentOnce(t *testing.T) {
	h := newHarnessOpts(t, harnessOpts{featureReception: true})
	tenantA, _, _ := h.seed()
	widget := h.mustDo("POST", "/api/v1/reception/widgets", sessionOwnerA, tenantA, `{"default_mode":"ai"}`, 201)
	opened := h.mustDo("POST", "/api/v1/public/reception/widgets/"+widget["id"].(string)+"/sessions", "", "", `{"visitor_key":"visitor-key-human-draft"}`, 201)
	sid := opened["session"].(map[string]any)["id"].(string)

	const body = "人工确认后的回复：今天下午可以到店"
	// AI 会话下先存草稿：可以存，但不能批准（必须先接管）。
	savedAI := h.mustDo("POST", "/api/v1/workbench/sessions/"+sid+"/reply-draft", sessionOwnerA, tenantA,
		`{"client_reply_id":"desk-ai-1","body":"`+body+`"}`, 201)
	if savedAI["status"] != "generated" || savedAI["sent"] == true {
		t.Fatalf("ai draft save = %v", savedAI)
	}
	// 接管会开启新轮次并作废旧草稿；这是既有轮次语义，不绕过。
	h.mustDo("POST", "/api/v1/reception/sessions/"+sid+"/takeover", sessionOwnerA, tenantA, `{}`, 200)
	saved := h.mustDo("POST", "/api/v1/workbench/sessions/"+sid+"/reply-draft", sessionOwnerA, tenantA,
		`{"client_reply_id":"desk-human-1","body":"`+body+`"}`, 201)
	if saved["status"] != "generated" || saved["sent"] == true {
		t.Fatalf("human draft save = %v", saved)
	}
	replyID := saved["id"].(string)
	publicBefore := h.mustDo("GET", "/api/v1/public/reception/sessions/"+sid+"?visitor_key=visitor-key-human-draft", "", "", "", 200)
	if strings.Contains(mustJSON(publicBefore), body) {
		t.Fatalf("unsent draft reached the visitor: %v", publicBefore)
	}

	approved := h.mustDo("POST", "/api/v1/reception/sessions/"+sid+"/replies/"+replyID+"/approve", sessionOwnerA, tenantA, `{}`, 200)
	reply, _ := approved["reply"].(map[string]any)
	if reply["status"] != "approved" || reply["sent_at"] != "" {
		t.Fatalf("approve = %v", approved)
	}
	sent := h.mustDo("POST", "/api/v1/reception/sessions/"+sid+"/replies/"+replyID+"/send", sessionOwnerA, tenantA,
		`{"receipt_id":"rcpt-human-1"}`, 200)
	sentReply, _ := sent["reply"].(map[string]any)
	if sentReply["status"] != "sent" || sentReply["sent_at"] == "" {
		t.Fatalf("send = %v", sent)
	}
	again := h.mustDo("POST", "/api/v1/reception/sessions/"+sid+"/replies/"+replyID+"/send", sessionOwnerA, tenantA,
		`{"receipt_id":"rcpt-human-1"}`, 200)
	if again["duplicate"] != true {
		t.Fatalf("resend with same receipt = %v", again)
	}
	publicAfter := h.mustDo("GET", "/api/v1/public/reception/sessions/"+sid+"?visitor_key=visitor-key-human-draft", "", "", "", 200)
	if !strings.Contains(mustJSON(publicAfter), body) {
		t.Fatalf("sent reply missing from visitor view: %v", publicAfter)
	}
}

// AI 会话（未接管）仍不能批准 generated 草稿；轮次/模式限制不放松。
func TestAISessionDraftCannotBeApproved(t *testing.T) {
	h := newHarnessOpts(t, harnessOpts{featureReception: true})
	tenantA, _, _ := h.seed()
	widget := h.mustDo("POST", "/api/v1/reception/widgets", sessionOwnerA, tenantA, `{"default_mode":"ai"}`, 201)
	opened := h.mustDo("POST", "/api/v1/public/reception/widgets/"+widget["id"].(string)+"/sessions", "", "", `{"visitor_key":"visitor-key-ai-approve"}`, 201)
	sid := opened["session"].(map[string]any)["id"].(string)
	saved := h.mustDo("POST", "/api/v1/workbench/sessions/"+sid+"/reply-draft", sessionOwnerA, tenantA,
		`{"client_reply_id":"desk-ai-2","body":"AI 模式下不该被批准"}`, 201)
	replyID := saved["id"].(string)
	status, out, _ := h.do("POST", "/api/v1/reception/sessions/"+sid+"/replies/"+replyID+"/approve", sessionOwnerA, tenantA, `{}`)
	if status != 409 || out["error"] != "send_blocked" {
		t.Fatalf("ai approve = %d %v, want 409 send_blocked", status, out)
	}
}
