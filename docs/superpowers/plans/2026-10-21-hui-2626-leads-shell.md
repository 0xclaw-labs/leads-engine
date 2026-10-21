# HUI-2626 Leads Shell — 实施计划（2026-10-21）

依据：`docs/superpowers/specs/2026-10-21-hui-2626-leads-shell-design.md`
分支：`eco/hui-2626-leads-shell` · 工作树：`/Volumes/MacintoshHD/派诺生态/wt/leads-engine/mb-eco-leads`
基线：`main` @ `ef48624`（Go 全绿、web 47 文件/336 测试全绿、`next build` 绿）

每个任务：先写失败测试 → 实现 → 跑该包测试 → 跑全量相关门 → 一个逻辑提交（HUI id）。
禁止：改 fixture 让门变绿、伪造服务结果、增加第二套线索/会话存储。

---

## Task 0 — 设计/计划入库

- [ ] 提交 spec + plan。
- 验收：`git status` 只含两份文档；`git log` 含 `docs(hui-2626): ...`。

---

## Task 1 — Go：线索列表投影（D1/D6）

**先失败**：`server/internal/httpapi/lead_list_view_test.go`

1. 种子：owner A、sales A1、停用成员、contact+lead（touch 来源）、无负责人 lead。
2. `GET /api/v1/leads`：
   - owner 看到全部；每项有 `contact_name`、`source_type`、`owner_label`、`assignment_reason`、`source`；
   - sales 只看到自己名下；未分配线索 404 掩码不变；
   - 跨租户不可见；
   - 无 N+1 的第二次 contacts 请求（页面层删掉）。
3. 已停用成员不可成为 assignee：`PATCH /api/v1/leads/{id}` → 400 `bad_assignee`；原负责人不变。
4. 列表 handler 不得泄漏 phone/email（现有红线）。

**实现**

- `store.LeadListRow` + `ListLeadRows(tenantID, assigneeFilter)`：
  `leads l LEFT JOIN contacts c ON c.id=l.contact_id LEFT JOIN members m ON m.id=l.assigned_member_id LEFT JOIN source_refs s ON s.id=l.source_ref_id`
  列：lead 字段 + `c.name, c.source_type, m.display_name, m.enabled` + `s.source_app, s.source_ref, s.auth_scope_snapshot`（活动/渠道人话由前端现有函数处理；JSON 只给安全引用）。
- `assignment_reason` 由服务端给固定人话：空负责人=`待分配`；有则 `已分配给 {display_name}`；停用成员=`负责人已停用`。
- handler 输出 `items`（保留旧字段兼容）。
- `resolveAssignee`：`!m.Enabled` → `bad_assignee`。

**门**：`cd server && go test ./internal/store ./internal/httpapi -run 'LeadList|Assign' -count=1`，再全量。

**提交**：`fix(hui-2598): 线索列表投影联系人与负责人，拒绝停用成员分配`

---

## Task 2 — Go：人工接管下草稿可批准（D3）

**先失败**：`server/internal/httpapi/reception_test.go` 增
`TestHumanDraftCanBeApprovedAndSentOnce`

1. assist 会话 → takeover（mode=human, epoch+1）。
2. `POST /workbench/sessions/{id}/reply-draft` 保存 body（kind=draft,status=generated,sent=false）。
3. `POST /reception/sessions/{id}/replies/{rid}/approve` → 200；public session 不含正文。
4. `POST .../send` → 200；再次 send 同 receipt → already=true；public 可见一条。
5. AI 模式（未接管）下 approve 仍 409 `send_blocked`（原语义不松）。
6. 旧轮次草稿 approve 仍 409。

**实现**

- `store.ApproveReceptionReply` 子查询 `mode IN ('assist','human')`。
- 注释写明：human 的 approved 只是可发送，不等于已发送。

**门**：`go test ./internal/store ./internal/httpapi -run 'Reception|Draft' -count=1`。

**提交**：`fix(hui-2598): 人工接管下可批准已保存草稿再发送`

---

## Task 3 — Web：今日 IA 纯函数（D2/D4/D7）

**先失败**：`web/tests/todayWorkspace.test.ts`

- `todayTarget(item)`：会话优先于线索（ai_draft/reception/human_takeover）；否则线索；否则商机；否则 null。
- `attentionSummary(groups)`：固定组序、计数、0 不显示在条上（或显示 0 但无卡）；总数为 0 时 `empty=true`。
- `rowReason(item, group)`：分组人话 + reason/next；不出现工程 id。
- `conversationActions(session)`：`open`、`canTakeover`、`canReply`(human)、`canApprove`(assist|human + generated + same epoch)、`canSend`(approved + same epoch)。
- `draftPrefill(replies, mode)`：human + 最新 generated draft → 文本；否则空；不覆盖用户已输入（由组件 state 保证）。

**实现**：`web/src/lib/todayWorkspace.ts`，纯函数、无 DOM/网络。

**提交**：`feat(hui-2626): 今日工作台 IA 纯函数（会话优先/注意力计数/草稿预填）`

---

## Task 4 — Web：今日工作台重构 + ConversationPanel（HUI-2626 / D2 / D4）

**先失败**：`web/tests/todayWorkspace.page.test.tsx`（SSR + source guards）

- 页面源：`data-pattern="workspace"`、`data-region` 标记、`data-attention-strip`、`data-today-group`。
- 不再出现“金额分开看”作为顶层 card（金额口径在 `<details>` 内）。
- 页面源无裸 `<button>/<input>/<select>/<textarea>/<form>/<table>`，无 `tenant_id` 子串（现有 hygiene 门）。
- SSR：空态说明“新咨询/会话/草稿会出现在这里”；错误态有 action。
- ConversationPanel SSR：loading/error/empty/ready；草稿 textarea 有 label；发送按钮的 disabled 语义由 `conversationActions` 锁定；错误保留输入（组件测试可只测纯逻辑 + 源）。

**实现**

- 新组件 `web/src/components/workbench/conversationPanel.tsx`（client）：
  - props: `tenant`, `sessionId`, `modeHint`, `onChanged`, `onError`.
  - 加载 `GET /api/reception/sessions/{id}`；票号（seq+tenant）丢弃迟到响应。
  - 消息线程（访客/人工/AI，状态文案）；草稿区（服务端 `reply-draft`）。
  - 动作：保存草稿（reply-draft）、批准（approve）、发送（send，`receipt_id` 用 `newStaffToken`）、转人工（takeover）、交回 AI（release）。
  - 失败：`failureText`，保留输入；成功：重新加载 + `onChanged()`。
  - 不自动发送；按钮明确“保存草稿不会发送”。
- 今日页：
  - 首屏 header + 注意力条（`Link` 到组锚点）+ 口径 `<details>`（3 条排序规则 + 金额 lines）。
  - 组 section 带 `id` 与 `data-today-group`；空组一行说明，不渲染大空卡。
  - 行 facts 增加“为什么在今天”和来源人话；行动作按 `todayTarget`；次级“查看线索”。
  - 每个会话/草稿行一个“展开会话/写回复”开关，渲染 ConversationPanel；`data-conversation-session`。
  - 未保存输入继续 `leads_today_memory`，但文案明确“未保存”；保存后清空本地。
  - 主行动仍唯一：今天页只有每行一个 `data-page-primary`（首个有待跟进行），其余行行动作 `false`。
- CSS：`web/src/app/today.css`? globals 已有类；新增类写入 globals（token only）。

**门**：`npm test -- tests/todayWorkspace* tests/fix2.pageHygiene.test.ts tests/hui2626.evalfix.test.ts`。

**提交**：`feat(hui-2626): 今日工作台一行内完成会话/回复/下一步`

---

## Task 5 — Web：线索列表/详情真实负责人（D1/D6）

**先失败**：更新 `web/tests/finishR1.leadsPage.test.tsx`

- 行源直接用服务端 `contact_name/source_type/owner_label/assignment_reason/source`；不再 `fetch /api/contacts`；无“未命名”假名。
- `leadRowFacts` 用 `owner_label`，停用/待分配明确；来源用 `sourceText`。
- 抽屉 loading/error 不变；详情页分配块不变。

**实现**

- `LeadRow` 扩展字段；页面单请求 `/api/leads`。
- `leadRowFacts` 接受 `owner_label/assignment_reason`；保留旧字段回退（测试用）。
- 不新增裸控件。

**提交**：`fix(hui-2598): 线索列表用服务端联系人/负责人投影，去掉静默假名`

---

## Task 6 — Web：EcoTopNav 服务端模型 + launch/return/usage（HUI-2227/2085）

**先失败**：

- `web/tests/ecoNavLaunch.test.ts`（纯函数）：
  - 缺配置 → `config_gate`，fetch 不被调用；
  - 非 loopback identity base → 拒绝；
  - whoami 401 → `unauthenticated`；
  - launch 403 → `denied`（reason 映射）；
  - 503/网络 → `unavailable`；
  - `launch_url` 非 https / host 不在 allowlist / 带 userinfo → `invalid_launch_url`；
  - 成功 → URL + `app_id/target_id` 回显校验；
  - 同一请求体 app/target 非法 slug → 400，不发。
- `web/tests/ecoNavLoad.test.ts`：
  - 无 endpoint → `unavailable`，`renderable=false`，无 fixture；
  - 5xx/网络 → `unavailable`；
  - 401/403 → `denied`；
  - 契约不符 → `unavailable`；
  - 200 valid → `public_ai_context` + 租户/应用/return 映射；
  - 请求带 cookie + `x-tenant-id`。
- `web/tests/ecoNavShell.test.tsx`：blocked 模型不渲染导航；`unavailable` 显示可重试提示；`return_context` chip 文案；launchable 是按钮、non-launchable 是文本；账户/用量链接到 `/subscription`。

**实现**

- `web/src/lib/eco-nav/load.ts`：入参加 `cookie/tenant`，严格映射；删除 fixture 回退（fixture 仅测试导入）。
- `web/src/app/(shell)/layout.tsx`：`cookies()` + `headers()` 取 tenant；调用 loader；无 endpoint → blocked。
- `web/src/lib/eco-nav/launch.ts`：`planLaunch` + 错误码；配置读取集中在 `readLaunchEnv`。
- `web/src/app/api/eco-nav/launch/route.ts`：POST；Content-Type JSON；`accept` 判定 303/JSON；不缓存。
- `EcoTopNav` props：`onLaunchApp`, `onReturn`, `onOpenUsage`, `launchError`；按契约渲染按钮/原因；`data-pn-surface` attributes；返回 chip。
- `CrmShell`：launch fetch → `window.location.assign`；错误行显示 + 可重试（不清空）；usage `router.push("/subscription")`；blocked 提示。
- 不硬编码域名；host allowlist 仅 env。

**提交**：
1. `feat(hui-2227): eco-nav 服务端会话模型与 fail-closed 加载`
2. `feat(hui-2227): 应用切换 BFF（shell launch）与返回/用量入口`

---

## Task 7 — Token 采用 + 对比度门（HUI-2626）

**先失败**：`web/tests/tokenContrast.test.ts`

- 解析 tokens.css `work.light/light` 块；解析 resolved.json 的 leads-web/work.light/light 99 对；独立 WCAG 计算；逐对比 `minRatio`；失败列出 pair/值/比值。
- 页面配对清单必须命中 99 对；缺失即失败。
- 裸值门：`globals.css`/`eco-top-nav.module.css` 在 `:root` 之外无 hex/rgb（tokens/aliases 除外）；`data-pn-surface` 作用域规则必须只用 `--pn-*` 或 `--surface/--danger/--warn` 映射。
- token 解析门：新 CSS 用到的每个 `--pn-*` 必须在 work.light/light 块中存在。

**实现**：替换 touched CSS 的裸值；新增今日/会话/导航样式全部走 token；保持 390/1440 既有布局断言。

**提交**：`style(hui-2626): 今日/会话/导航改用语义 token 并加对比度门`

---

## Task 8 — 全量门 + 真实走查证据

- `cd server && gofmt -l . && go vet ./... && go test ./... -count=1`
- `cd web && npx tsc --noEmit`（记录既有基线错误，若有）
- `cd web && npm test`
- `cd web && npm run build`
- 对比度门输出原文入证据。
- 本地 dev：identity stand-in + provision 租户/owner + Go + Next；跑 owner 路径：
  今日 → 会话展开 → 保存草稿 → 批准/发送 → 下一步 → 线索列表（负责人显示名字）→ 导航（blocked 因无 public-ai）。
  截图 1440/390；记录 console 404/error 数。
- 写 `docs/superpowers/evidence/2026-10-21-hui-2626-leads-shell-evidence.md`：命令原文、before/after、token 统计、对比度输出、缺陷→修复、NOT_VERIFIED。

**提交**：`docs(hui-2626): 证据包（门输出/走查/对比度/未验证项）`

---

## Task 9 — 双轴 code review + 修复

- Standards：仓库标准（无裸控件/裸 hex/租户头/身份纪律/错误文案/BFF 边界/测试风格）。
- Spec：逐条对照 §1/§3/§4/§5/§6。
- 修复后重跑 Task 8 门；记录两个轴的问题表与关闭状态。

**提交**：`fix(hui-2626): 双轴审查修复`（如有）

---

## Task 10 — 集成

- 锁：`/Volumes/MacintoshHD/派诺生态/.locks/leads-engine.integrate.lock`（flock）。
- 在 `repos/leads-engine` 从 main 建 `mb/eco-20261021`（若缺）。
- 合并 `eco/hui-2626-leads-shell`（`--no-ff`），冲突则 abort + 报告。
- 在集成分支重跑 Go + web 测试/build。
- 回 main；保留 worktree；不 push/deploy/改 Linear。

---

## 回滚

- 每个任务一个提交；集成前 `git revert <commit>` 可单独回退。
- token 生成物/锁零修改；对比度门失败时只回退 CSS 采用提交，不动生成物。
- BFF launch 路由新增，不改 catch-all；移除路由即回到基线导航（无 launch）。
