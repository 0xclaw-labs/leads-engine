# HUI-2626 / HUI-2598 / HUI-2227·2085（consumer）证据包

日期：2026-10-21
分支：`eco/hui-2626-leads-shell`
工作树：`/Volumes/MacintoshHD/派诺生态/wt/leads-engine/mb-eco-leads`
基线：`main` @ `ef48624`
设计/计划：`docs/superpowers/specs/2026-10-21-hui-2626-leads-shell-design.md`、`docs/superpowers/plans/2026-10-21-hui-2626-leads-shell.md`

> 本文件只写实际跑过的命令与结果。本地走查使用仓库既有的协议兼容 identity 替身与
> synthetic 租户（见 §6），**不是**真实 public-ai 服务、真实租户或部署环境；相应项在
> §8 明确标 NOT_VERIFIED。

---

## 1. 门（最终一次）

| 门 | 命令 | 结果 |
|---|---|---|
| Go 格式 | `cd server && gofmt -l .` | 无输出 |
| Go vet | `cd server && go vet ./...` | 退出 0 |
| Go 测试 | `cd server && go test ./... -count=1` | 27 个含测试包 `ok`，退出 0（日志 `/tmp/leads-hui2626/go-test.log`） |
| Web 单测/组件/路由 | `cd web && npx vitest run` | **55 文件 / 423 测试全过**，退出 0 |
| 对比度门 | `cd web && npx vitest run tests/tokenContrast.test.ts --reporter=verbose` | **7/7 通过**；独立报告 `contrast-report.json`（99 对，0 失败） |
| 构建 | `cd web && npm run build` | 退出 0；新增 `/api/eco-nav`、`/api/eco-nav/launch` 为动态路由 |
| 类型 | `cd web && npx tsc --noEmit` | 仅 3 条**基线已有**错误（`tests/channelInteraction.test.ts:33`、`tests/pageMap.test.ts:79/170`），本轮文件零新增 |

原始输出：`/tmp/leads-hui2626/{go-test.log,vitest-final.log,contrast.log,tsc-final.log}`（本机临时）。
`contrast.log` 的 7 条：

```
✓ finds the full leads-web pair set in the generated contract
✓ independently recomputes every declared pair and finds no violation
✓ would fail on a real violation (the math is not a PASS constant)
✓ covers the semantic pairs the today/nav CSS actually uses
✓ only references --pn-* roles that exist in work.light/light
✓ keeps raw hex confined to the :root public-form fallback
✓ keeps the today/nav surfaces on semantic roles
```

---

## 2. 今日工作台 before/after

### Before（`main` @ ef48624）

```
main
├─ header h1
├─ card：3 段 tone 说明（人工/AI/自动化）
├─ card：金额分开看（3 行 KPI 式）
├─ error / loading 状态
└─ 7 个 TODAY_GROUPS section
   └─ RecordFrame 行
      ├─ facts: 来源/负责人/状态/下一步/最近互动/...
      ├─ 一个行内跳转（线索或接待，按 kind 优先级）
      ├─ 跟进表单（记录/处理/下一次）
      └─ ai_draft: 修改/忽略
```

问题：首屏先读说明与金额；会话/草稿必须跳走；草稿只在本机内存；草稿行跳到线索详情；同一会话可在 `human_takeover` 与 `ai_draft` 重复出现。

### After（本轮）

```
main[data-pattern="workspace"]
├─ header[data-region=header] h1 + 当前视图
├─ section[data-region=status] 注意力条
│  ├─ 7 组计数 chip（锚点，不是 KPI 卡）
│  └─ <details> 排序与金额口径（金额/自动化/AI 边界/联合链）
├─ error[data-region=error] / loading[data-region=loading] / empty[data-region=empty]
└─ section[data-today-group][data-region=body]（仅有内容的组）
   └─ RecordFrame
      ├─ title: 客户名 / 待接管的会话 / 待确认的回复草稿 / 来源线索
      ├─ facts: 为什么在今天 / 接待轮次 / 来源 / 负责人 / 最近互动（人话+秒级）/ 下一步 / 草稿
      ├─ quick actions: 展开会话与回复、在接待台打开/查看线索、服务草稿
      ├─ ConversationPanel（同一会话 API + 草稿 API）
      │  ├─ 访客/草稿消息（秒级时间、状态）
      │  ├─ 回复内容（服务端草稿；保存≠发送）
      │  ├─ 保存草稿 / 批准草稿 / 批准并发送 / 发送回复 / 转人工接管 / 交回 AI
      │  └─ 错误 role=alert + 重试，输入保留
      └─ 跟进表单（记录 / 处理 / 下一次 / 提交）
```

服务端同时把“同一会话的重叠行”收口：`awaiting_approval` 会话只占 `human_takeover` 一行，最新草稿正文随会话行展示；草稿不再在 `ai_draft` 里重复。见 `server/internal/workbench/workbench.go` 的 `withLatestDraft` 与 `placedSession`。

---

## 3. 缺陷 → 根因修复

| # | 正常路径缺陷 | 根因 | 修复 | 证据 |
|---|---|---|---|---|
| D1 | `/leads` 负责人显示 `mem_*`；联系人请求失败时全部姓名静默变“未命名” | 列表只返回 lead 行，页面二次拉 contacts；失败分支被忽略 | 服务端 `ListLeadRows` 一次 JOIN contacts/members/source_refs，返回 `contact_name/source_type/owner_label/assignment_reason/source`；页面删掉第二次请求与假名兜底 | `lead_list_view_test.go`、`finishR1.leadsPage.test.tsx`、截图 `desktop-1440-05-leads.png` |
| D2 | AI 草稿行打开线索详情，找不到“发送” | 行目标按 `kind==="reception"` 判断，草稿行落到 `lead_id` | `todayDestination()`：有会话且 kind ∈ {reception, ai_draft, human_takeover} → 会话；否则线索/商机 | `todayWorkspace.test.ts`、`todayWorkspace.page.test.tsx` |
| D3 | 人工接管下保存的草稿既不能批准也不能发送 | `ApproveReceptionReply` 只认 `mode='assist'` | 子查询改为 `mode IN ('assist','human')`；AI 会话仍拒绝（需先接管） | `human_draft_test.go`（human 可批准/发送一次；AI 仍 409） |
| D4 | 今日页草稿只在本机；刷新/接管后面板卸载，输入与提示丢失 | 页面 reload 把 `groups` 置空，面板卸载；草稿无本机恢复 | 同租户 reload 保留旧数据原地替换；未保存回复按 `(tenant, session)` 存 sessionStorage；通知在 `load()` 完成后发出 | `replyCompose.test.ts`、`todayWorkspace.page.test.tsx`（reload 守卫）、走查 `takeover_ok`/`sent_verified` |
| D5 | 可把线索派给停用成员，记录从所有销售视图消失 | `resolveAssignee` 只校验同租户 | 拒绝 `!enabled`，返回 `bad_assignee`，不改原负责人 | `TestAssignRejectsDisabledMember` |
| D6 | 列表只有来源枚举，活动归因看不到 | 列表无 source 投影 | 返回 `source{channel,activity,at,source_app}`；`attributionText` 渠道在前、活动编号如实标注、工程提交号退场 | `todayWorkspace.test.ts`、截图 |
| D7 | 同一会话在 `human_takeover` + `ai_draft` 重复出现（一次会话 3 行） | `projectToday` 独立遍历 desk 与 drafts | `placedSession` 去重；最新草稿正文挂到会话行 | `TestTodayDoesNotRepeatOneSessionAsDraftAndHandoff`、`TestTodayDraftCanBeEditedOrIgnoredWithoutSending`（改口径） |
| D8 | 最近互动显示 `活动 camp-walk · 提交 touch-ref-walk-1 · ISO 微秒` | 显示层只对 source 人化 | `humanSummary` + `interactionText`：提交工程号退场、秒级本地时间 | `todayWorkspace.test.ts`、截图 |
| D9 | 390 卡内 facts 把“为什么在今天”“草稿”拆字 | CSS 选择器写成 `[data-columns="1"][data-facts="chips"]`（两个属性不同元素，永不命中） | 改为 `[data-columns="1"] [data-facts="chips"]`，1/2 列 label 与 value 上下排 | `shell.test.tsx` 选择器守卫、`mobile-390-01/02` |
| D10 | 导航失败文案暴露 `PUBLIC_AI_ECO_NAV_URL` | blocked summary 直接写配置键 | 产品话术 + 单独“重试导航”；配置细节只进文档/日志 | 截图、`ecoNavLoad.test.ts` |
| D11 | 接待台/会话面板把 `mem_*` 当负责人显示；人工回复被标成 “AI” | 接待 desk/session 只返回 `owner_member_id`；线程按 `kind!=="human"` 归为 AI | desk/session 响应补 `owner_label`（成员显示名）；无名字写“已接管（负责人待同步）”；线程 `draft` 标“草稿” | `TestHumanDraftCanBeApprovedAndSentOnce`（owner_label）、截图 `desktop-1440-04-sent.png` |

---

## 4. Semantic token 采用 + 对比度门

- 消费 profile：`tokens.json → consumers["leads-web"]`，`work.light/light`，生成物锁 `01e6765`；本轮**未改**生成物/锁。
- `globals.css`：`var(--pn-*)` 29 个不同角色；`:root` 之外裸 hex **0**。
- `eco-top-nav.module.css`：`var(--pn-*)` 11 个不同角色；裸 hex **0**；`<header>` 自带 `data-pn-surface="work.light" data-pn-theme="light"`，壳外也能解析 token。
- 新增样式全部用语义角色：`--pn-bg-surface/page/sunken/raised`、`--pn-border-default/focus`、`--pn-text-primary/secondary/muted`、`--pn-brand-text/accent/ink/weak`、`--pn-status-{warning,danger,info}-{fg,weak}`、`--pn-radius-*`、`--pn-spacing-*`、`--pn-type-caption-size`、`--pn-focus-*`。
- 对比度门（`web/tests/tokenContrast.test.ts`）：
  - 解析 `tokens.css` 的 `[data-pn-surface="work.light"][data-pn-theme="light"]` 块；
  - 取 `resolved.json` 中 `leads-web/work.light/light` 的 **99 对**；
  - **独立**重算 WCAG 比值（不读 `verdict`），逐对与 `minRatio` 比较；声明非 pass 也算失败；
  - 页面实际使用的 11 对必须在这 99 对里；
  - `--pn-*` 必须存在于 work.light 块；`:root` 之外禁止裸 hex；
  - 自带负例：`#777777` on white 4.48 < 4.5，证明门不是 PASS 常量。
- 独立报告：`contrast-report.json` — 99/99 检查，0 失败；最小 `border.strong.on.bg.sunken` 3.609（门槛 3），最大 `text.primary.on.bg.raised` 15.962（门槛 4.5）。

---

## 5. EcoTopNav / launch / 返回 / 账户

### 数据流

```
GET / (RSC shell) → CrmShell (client)
  → GET /api/whoami（cookie+x-tenant-id，范围对账）
  → GET /api/eco-nav（cookie+x-tenant-id）
      → fetchEcoNavModel → PUBLIC_AI_ECO_NAV_URL（必须 loopback）
      → parseEcoNavDocument → toViewModel（服务端组合）→ {model}
  → renderable ? <EcoTopNav> : unavailable ? “生态导航暂不可用 / 重试导航” : 不渲染
```

- 无 endpoint / 无 cookie / 401 / 403 / 5xx / 契约不符 → 显式 blocked（`unavailable`/`denied`），**不再回退预览 fixture**；fixture 只用于组件测试。
- 应用切换：`POST /api/eco-nav/launch`（只带 `app_id` + `launch_target_id`）→ `GET /api/v1/whoami` 取会话 principal/tenant → `POST /internal/v1/identity/shell/launch` → 校验回执 `ok/app_id/target_id` 与 `launch_url`（https 或显式 loopback http；host 必须在 `LEADS_ECO_LAUNCH_HOSTS`；禁 userinfo/fragment）→ 303（表单）或 200 JSON（导航 fetch 后 `window.location.assign`）。
- 缺任一配置（identity base/token、leads server、app id、host allowlist）→ 503 `config_gate`，零网络调用。
- 403/429/503/400 分别映射 denied/rate_limited/unavailable/invalid_request；不重试、不猜目标。
- 返回 chip：`return_context` 有值才渲染；点击同样走 launch（source app + return target）。`source_ref` 为空时 tooltip 只说“具体条目由来源应用恢复”，不声称精确返回。
- 账户/用量：导航内 `<Link href="/subscription">`（既有 public-ai 用量接线），没有第二钱包/套餐 UI。
- 公开页 `/f/[id]`、`/r/[id]` 不经过 CRM 壳；边界测试继续锁 `EcoTopNav` 不出现。

### 已验证（单测/SSR）

`ecoNavLoad.test.ts` 11 条、`ecoNavLaunch.test.ts` 13 条、`ecoNavRoutes.test.ts` 7 条、`ecoNavShell.test.tsx` 8 条：
- 无配置/未登录/服务失败/契约失败 → blocked；
- cookie + x-tenant-id 透传；
- 配置门零网络；非法 app/target 零网络；
- whoami 401/503 不发 launch；
- identity 403/429/503/400 映射；
- `launch_url` 非 https/非 allowlist/echo 不一致 → 拒绝；
- 当前 app 不允许自切；
- SSR：launchable 是 button、blocked 有原因、return chip、账户/用量链、blocked 模型渲染 null。

### 未验证

真实 public-ai identity shell launch 往返、真实 App Registry/brand/tenant 文档、真实 launch_url 主机、跨应用会话建立。见 §8。

---

## 6. 本地走查（synthetic，协议兼容替身）

链：
- Go leads-server（本工作树构建，`127.0.0.1:29300`；`FEATURE_RECEPTION=on`、`FEATURE_FOLLOWUPS=true`，`LEADS_DEDUP_PEPPER`/`RECEPTION_VISITOR_PEPPER` 环境注入）。
- Next dev（本工作树，`127.0.0.1:29330`，`LEADS_SERVER_URL`/`LEADS_INTERNAL_TOKEN` 注入；未配置 `PUBLIC_AI_ECO_NAV_URL`）。
- identity 替身（`harness/identity-standin.mjs`，`127.0.0.1:29310`，只实现 `session/resolve`，测试 token 不入仓）。
- 租户：`tnt_e5b8143145d5d40ef5173672cd9949ae`（synthetic）；owner `店主甲` / sales `销售甲`。
- 数据：1 个接待会话（assist → 人工接管 → 回复发送）+ 2 条 Touch 来源线索（1 条分配销售甲、1 条待分配）。

路径（`harness/walk.mjs`，Playwright + 系统 Chrome headless）：
1. 今日页：注意力条、为什么在今天、展开会话与回复、草稿正文；
2. **键盘**：focus “展开会话与回复” → Enter → `aria-expanded=true`（focus ring 见截图）；
3. 展开会话：访客消息 + 两条草稿（秒级时间）；
4. 转人工接管：服务端 epoch+1，通知出现；
5. 填写回复 → 批准并发送 → 到接待台核对 `已发送` 与正文；
6. 线索列表：负责人 `销售甲`、待分配文案、来源 `碰一碰 · 活动 camp-walk`、无 `mem_*`；
7. 返回今日；导航不可用时显示“生态导航暂不可用 / 重试导航”，不渲染任何品牌/应用/租户。

| 视口 | 结果 | 计数 | console |
|---|---|---|---|
| 1440×900 | PASS：6 步全成；`sent_verified.hasReply=true`、`hasSentStatus=true`；`hasRawMemberId=false`；`hasNavFault=true`、`hasEcoNav=false` | 3 点击 / 1 填写 | **0 error / 0 404** |
| 390×844 | PASS：同上 | 3 / 1 | **0 / 0** |

截图（`screens/`）：
- `desktop-1440-01-today.png`、`-02-conversation.png`、`-03-takeover.png`、`-04-sent.png`（接待台核对）、`-05-leads.png`、`-06-today-final.png`
- `mobile-390-01-today.png`、`-02-conversation.png`、`-03-takeover.png`、`-04-sent.png`、`-05-leads.png`、`-06-today-final.png`
- 步骤 JSON：`desktop-1440-steps.json`、`mobile-390-steps.json`

### 复跑

```bash
# 1) env（测试值，不入仓）
cd docs/superpowers/evidence/2026-10-21-hui-2626-leads-shell/harness
node identity-standin.mjs &
# 2) 构建并启动 Go（见 /tmp/leads-hui2626 启动命令）
# 3) 创建租户/成员、接待 widget/knowledge、会话/消息/草稿、两条 intake 线索
# 4) 启动 Next dev：
cd web && LEADS_SERVER_URL=http://127.0.0.1:29300 LEADS_INTERNAL_TOKEN=... npx next dev -p 29330
# 5) 走查：
TENANT=... PLAYWRIGHT_CORE=<playwright-core> WALK_TENANT=$TENANT \
  WALK_OUT=.../screens WALK_WIDTH=1440 WALK_HEIGHT=900 WALK_TAG=desktop-1440 \
  node walk.mjs
```

> 注意：同一 `.next` 目录同时跑 `next build` 与 `next dev` 会导致 dev RSC payload 报
> “development vs production” 500。走查前清掉 `.next` 并只保留 dev；这是采集环境问题，
> 不是产品缺陷。

---

## 7. 双轴 code review

### Standards 轴（仓库记录的标准）

| 检查 | 结论 |
|---|---|
| 页面只打 `/api/*` | 通过；新增 `/api/eco-nav`、`/api/eco-nav/launch` 均为本站 BFF |
| BFF 注入内部 token、不转发调用方 token | 通过；launch 只读 cookie + `x-tenant-id`，identity token 仅服务端 env |
| 租户/权限服务端真源 | 通过；列表/时间线/会话/草稿仍走 Go；BFF launch 用 whoami 的 tenant 并比对请求头 |
| 无手填租户/内部 id、无 JSON.stringify/裸 HTTP 错误 | 通过；`fix2.pageHygiene` 16 条全过 |
| 无裸 table/button/input（正式页） | 通过；`finishR1.redline` 11 条、hygiene 16 条 |
| 错误文案产品化、失败保留输入 | 通过；`failureText`、ConversationPanel 重试/保留 |
| 无第二套线索/会话存储 | 通过；无新增表/迁移；草稿仍是 `reception_replies` |
| 不硬编码生态域名 | 通过；`eco-nav/boundaries` 4 条（含 launch 只经 BFF） |
| token 使用 | 通过；99 对独立对比度、裸 hex 0（`:root` 除外） |
| 测试风格 | 纯函数优先；API 路由直接调用 handler；SSR 用 node render；无 jsdom 依赖 |

审查中发现并已修：
- `[data-columns][data-facts]` 选择器永不命中（D9）；
- blocked 文案泄漏 env 键（D10）；
- 同一会话重复占行（D7）；
- reload 卸载面板丢输入（D4）；
- 面板时间戳未人化（D8）；
- 接待台把 `mem_*` 当负责人、把人工草稿标成 AI（D11）；
- 导航并发请求可能用旧租户响应覆盖新租户（nav ticket）；
- launch BFF 先读完整 body 再判上限（改为先看 content-length）；
- launch 业务载荷隔离测试（lead_id/payer 不进 identity body）。

### Spec 轴（对照 brief）

| 要求 | 结论 |
|---|---|
| today-work workspace 取代 KPI 表 | 通过：注意力条 + 口径 disclosure；金额不再是首屏 KPI 卡 |
| 今日 incoming/unanswered/approval/next step/source | 通过：7 组 + 草稿随会话 + 来源/下一步/最近互动 |
| 同屏 source→conversation→reply→next step | 通过：行内 ConversationPanel + 跟进表单 |
| quick actions | 通过：展开会话、保存/批准/发送、转人工/交回、跟进/下一次、查看线索 |
| status-first/empty/error | 通过：loading/empty/error/recovery 文案与动作 |
| 正常路径缺陷根因修复 | 通过：D1–D10，均在服务端/投影/目标选择根因处 |
| token + page-pattern | 通过：work.light 角色、`data-pattern`/`data-region`、对比度门 |
| EcoTopNav 服务端模型/current app/launchable/降级 | 通过：BFF 模型、pinLeadsApp、launchable button、blocked 不渲染 |
| launch BFF `(app_id, launch_target_id)` → shell launch → 校验 redirect | 通过：单测/路由测试；真实服务未验证 |
| 账户/用量复用既有 public wiring | 通过：`/subscription` 链接，无第二钱包 |
| 返回/handoff 不携带 lead/payer | 通过：形状只 app/target；return chip 无载荷；不伪造对象版本 |
| 不新增 fallback/fake | 通过：删除 fixture 运行时回退；失败显式 blocked |

---

## 8. NOT_VERIFIED（明确未验证）

- 真实 public-ai platform-identity 的 `/internal/v1/identity/shell/launch` 往返与一次性 code 兑换；本地 identity 是只实现 `session/resolve` 的协议兼容替身。
- 真实 eco-nav 目录服务（brand/tenant/role/apps/launch_url）与真实 App Registry 主机；本地未配置 `PUBLIC_AI_ECO_NAV_URL`，走查只验证了 fail-closed 的 `unavailable` 状态。
- 真实白标品牌、真实租户数据、真实 billing/额度。
- 部署环境（nginx/域名/TLS/进程守护）与真实浏览器跨应用跳转。
- 入站 handoff（矩阵/碰一碰/GoBoost）的对象/版本解析：本仓无入站 handoff 端点；本轮只保证导航 return chip 不伪造精确返回。该解析归来源应用/task-view `resume_target`。
- 真实短信/外呼/模型；本轮不触发。
- 无障碍自动化（axe）与完整键盘遍历：本轮做了 focus→Enter 展开的键盘走查，未跑 axe 全页扫描。

---

## 9. 文件与提交

`git log --oneline main..HEAD`：

```
f3a5600 fix(hui-2227): 导航请求票号与 launch 业务载荷隔离（双轴审查）
（本轮后续提交：接待台 owner_label、证据包）
5dbc73d fix(hui-2598): 同租户刷新不卸载会话面板，负责人文案去重
c318b62 fix(hui-2626): 今日行标题/最近互动人化、回复草稿保留与 390 chips 布局
d6ecdf8 fix(hui-2598): 同一会话在今日只占一行，草稿随会话展示
e568a9f style(hui-2626): 今日/会话/导航改用语义 token 并加对比度门
c86897d feat(hui-2227): 应用切换 BFF（shell launch）与返回/用量入口
2ed0af4 feat(hui-2227): eco-nav 服务端会话模型与 fail-closed 加载
5d628b4 fix(hui-2598): 线索列表用服务端联系人/负责人投影，去掉静默假名
2cfc367 feat(hui-2626): 今日工作台一行内完成会话/回复/下一步
50a48f9 feat(hui-2626): 今日工作台 IA 纯函数（会话优先/注意力计数/草稿预填）
212396c fix(hui-2598): 人工接管下可批准已保存草稿再发送
993c09a fix(hui-2598): 线索列表投影联系人与负责人，拒绝停用成员分配
dfced52 docs(hui-2626): 获客今日工作台与生态壳设计/计划
```

主要新增文件：
- `web/src/lib/todayWorkspace.ts`（IA 纯函数）
- `web/src/components/workbench/conversationPanel.tsx`（行内会话/回复）
- `web/src/lib/eco-nav/launch.ts`、`web/src/app/api/eco-nav/{route.ts,launch/route.ts}`
- `server/internal/store/lead_list.go`
- `web/tests/{todayWorkspace,todayWorkspace.page,tokenContrast,ecoNavLoad,ecoNavLaunch,ecoNavRoutes,ecoNavShell,replyCompose}.*`
- `server/internal/httpapi/{lead_list_view,human_draft}_test.go`
- 证据：`docs/superpowers/evidence/2026-10-21-hui-2626-leads-shell/`
