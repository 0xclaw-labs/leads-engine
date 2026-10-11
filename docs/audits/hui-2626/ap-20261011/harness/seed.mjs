// Metric seeding: ingest + assign (+ task-specific follow-through) per lead. Test only.
// Usage: node seed.mjs <new|old> <new|due|waiting> <count>
// Env: BFF_PORT, TENANT, SEED_MEMBER, LEADS_WALK_TOUCH_SECRET
import { execFileSync } from "node:child_process";
const [version, task, countStr] = process.argv.slice(2);
const count = Number(countStr);
const BFF = `http://127.0.0.1:${process.env.BFF_PORT}`;
const TENANT = process.env.TENANT;
const MEMBER = process.env.SEED_MEMBER;
const HEADERS = { "content-type": "application/json", cookie: "leads_session=tok_owner_walk", "x-tenant-id": TENANT };
const NAME_PREFIX = version === "new" ? { new: "度新", due: "度临", waiting: "度等" }[task] : { new: "旧新", due: "旧临", waiting: "旧等" }[task];
const refFor = (i) => (version === "new" ? `m_${task}_${i}` : `om_${task}_${i}`);
const yesterISO = new Date(Date.now() - 24 * 3600 * 1000).toISOString().replace(/\.\d{3}Z$/, "Z");
const out = [];
for (let i = 1; i <= count; i++) {
  const ref = refFor(i);
  const name = `${NAME_PREFIX}${i}`;
  // 号码含 version+task+i 三个维度（11 位），避免跨任务同号触发联系人去重合并。
  const phone = `${version === "new" ? "1391" : "1392"}${{ new: "0", due: "1", waiting: "2" }[task]}0000${String(i).padStart(2, "0")}`;
  const script = version === "new" ? "ingest.mjs" : "ingest-old.mjs";
  const srcTenant = version === "new" ? "touch_t_a" : "touch_t_old";
  const r = JSON.parse(execFileSync("node", [script, srcTenant, ref, name, phone, `wx-${ref}`], { env: process.env, encoding: "utf8" }));
  if (r.status !== 200) { console.log(JSON.stringify({ ref, error: "ingest_failed", r })); process.exit(1); }
  const lead = r.body.lead_id ?? JSON.parse(r.body).lead_id;
  // assign to owner (pre-assign so both versions start from the same entry state)
  const pa = await fetch(`${BFF}/api/leads/${lead}`, { method: "PATCH", headers: HEADERS, body: JSON.stringify({ assigned_member_id: MEMBER }) });
  if (!pa.ok) { console.log(JSON.stringify({ ref, lead, error: "assign_failed", status: pa.status })); process.exit(1); }
  // task-specific setup follow-through
  if (task === "due") {
    const res = await fetch(`${BFF}/api/leads/${lead}/follow-through`, { method: "POST", headers: HEADERS,
      body: JSON.stringify({ note: `种子：${name} 首次电话已打，约明天回访`, complete: true, next_follow_up_at: yesterISO }) });
    if (!res.ok) { console.log(JSON.stringify({ ref, lead, error: "due_seed_failed", status: res.status, body: (await res.text()).slice(0, 200) })); process.exit(1); }
  } else if (task === "waiting") {
    const body = version === "new"
      ? { note: `种子：${name} 已发方案，等客户回复`, complete: true, disposition: "waiting_customer" }
      : { note: `种子：${name} 已发方案，等客户回复`, complete: true };
    const res = await fetch(`${BFF}/api/leads/${lead}/follow-through`, { method: "POST", headers: HEADERS, body: JSON.stringify(body) });
    if (!res.ok) { console.log(JSON.stringify({ ref, lead, error: "waiting_seed_failed", status: res.status, body: (await res.text()).slice(0, 200) })); process.exit(1); }
    // 旧版 follow-through 不改线索状态；waiting 恢复任务在旧系统映射为
    // needs_schedule（in_progress 且无未来下一步），需显式 PATCH 状态。
    if (version === "old") {
      const ps = await fetch(`${BFF}/api/leads/${lead}`, { method: "PATCH", headers: HEADERS, body: JSON.stringify({ status: "in_progress" }) });
      if (!ps.ok) { console.log(JSON.stringify({ ref, lead, error: "status_patch_failed", status: ps.status })); process.exit(1); }
    }
  }
  out.push({ ref, lead, name });
}
console.log(JSON.stringify(out));
