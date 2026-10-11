// b58ea17-contract Touch directed-event injector (test values only).
// 旧 envelope 开 DisallowUnknownFields：剥掉新版新增的
// trace_id / return_target / grant_ref / brand_display_name / notification_brand_ref / campaign_version。
import crypto from "node:crypto";
const [tenant, ref, name, phone, wechat, version = "1"] = process.argv.slice(2);
const SECRET = process.env.LEADS_WALK_TOUCH_SECRET;
const PORT = process.env.OLD_INGEST_PORT || "29400";
const sha = crypto.createHash("sha256").update(JSON.stringify({ name, phone, wechat })).digest("hex");
const record = { submission_ref: ref, tenant_id: tenant, name, phone, email: "", wechat, channel_subject_ref: "",
  authorization: "granted", purpose: "lead_submission",
  consents: { form_authorized: true, channel_reply: false, marketing_phone: true, marketing_sms: false, notice_version: "n1" } };
await fetch("http://127.0.0.1:29320/__put", { method: "POST", headers: { "content-type": "application/json" }, body: JSON.stringify({ ref, record }) });
const payload = { campaign_ref: "cmp_1", store_ref: "sto_1", asset_ref: "ast_walk", channel: "wecom",
  consent_ref: "touch://leads/" + ref, consent_version: "v1", consent_at: "2026-10-10T00:00:00Z", marketing_optin: true };
const profile = { profile_version: "directed-event/v1", event_id: ref, event_type: "lead.authorized_submitted", source_app: "touch-engine",
  target_app: "leads", tenant_scope: tenant, source_ref: ref, source_version: Number(version),
  correlation: { ref: "cmp_1" }, payload_ref: { ref: "touch://leads/" + ref, sha256: sha } };
const body = Buffer.from(JSON.stringify({ type: "lead.authorized_submitted", schema_version: 1, app_id: "touch-engine", tenant_id: tenant,
  data: { event_profile: profile, payload } }));
const sig = "sha256=" + crypto.createHmac("sha256", SECRET).update(body).digest("hex");
const res = await fetch(`http://127.0.0.1:${PORT}/internal/v1/notify/ingest`, { method: "POST",
  headers: { "content-type": "application/json", "x-notify-signature": sig, "x-notify-delivery-id": "dlv-" + ref }, body });
console.log(JSON.stringify({ status: res.status, body: (await res.text()).slice(0, 400) }));
