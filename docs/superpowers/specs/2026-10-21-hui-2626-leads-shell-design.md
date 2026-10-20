# HUI-2626 / HUI-2598 / HUI-2227·2085（consumer）— 获客今日工作台与生态壳设计

日期：2026-10-21
分支：`eco/hui-2626-leads-shell`
工作树：`/Volumes/MacintoshHD/派诺生态/wt/leads-engine/mb-eco-leads`
基线：`main` @ `ef48624`
范围：获客（leads-engine）消费端切片。Lead 已代答 brainstorm；本文每条拍板写成 `Ruling:`，不从人类伙伴处再来一轮提问。

---

## 1. 问题（repo 证据优先）

### 1.1 票面要求与现状的差距

| 票 | 要求 | 基线实测（2026-10-21，`ef48624`） |
|---|---|---|
| HUI-2626 | 首页是“今天要处理什么”，不是 KPI 表 | `/` 已按 7 个 Today 组排队，但首屏先出现 3 段说明卡 + “金额分开看”卡；每行只有“打开链接”一个导航，没有同屏会话/回复/转人工 |
| HUI-2626 | 同一屏 source → conversation → reply → next step | 来源/负责人/下一步在行内；会话与回复要跳到 `/reception`；离开页面后今天页草稿只在本机 localStorage，会话页看不到 |
| HUI-2598 | 真实 lead→conversation→reply→next-step 正常路径无断点 | 见 §4 缺陷表：线索列表负责人显示 `mem_*` 工程 id；AI 草稿行跳到线索详情而不是会话；人工接管下保存的草稿无法批准/发送；联系人请求失败被静默伪装成“未命名” |
| HUI-2626 | GoBoost 设计语言 + semantic tokens | 壳/导航/卡片仍有 `#fff` 等裸值；无独立对比度门（只有生成物自带的 `resolved.json` 声明） |
| HUI-2227/2085 | EcoTopNav 真实入口、服务端模型、launch BFF、账户/用量 | `loadEcoNavModel` 无会话/租户头，缺配置时直接回预览 fixture；`EcoTopNav` 只渲染 `<span>`，没有 launch；`return_context` 未渲染；布局无账户/用量入口 |
| HUI-2227 | 人工切换不携带 lead/payer | 形状层已锁（`planManualSwitch` 只带 app/target），本轮补真实点击与 BFF 校验，仍不新增载荷字段 |

### 1.2 不能做的事

- 不新增第二套线索/会话存储；会话、回复、下一步继续走 `GET/POST /api/v1/reception/*`、`/api/v1/workbench/*`、`/api/v1/leads/*`。
- 不把 `PUBLIC_AI_ECO_NAV_URL` 缺失时的“预览租户 fixture”当生产数据；不把“未接身份”画成已登录。
- 不手改 fixture 让门变绿；对比度门独立计算，不读 `verdict` 当结论。
- 不在前端拼生态域名；launch 只能经自家 BFF。

---

## 2. Brainstorm（自答，Lead 已预答）

**Q1：谁在什么时刻打开获客？**
A：商家 owner 或销售，早上开工、收到新咨询/新线索提醒时。目标不是“看报表”，是“三分钟内知道先回谁、回什么、下一步什么时候”。⇒ 今日工作台按注意力排序，金额/口径降为可展开说明。

**Q2：这一屏必须回答哪几个问题？**
A：① 这条线索从哪来（平台/活动/碰一碰/表单）；② 谁负责；③ 有没有会话、客户最后说了什么；④ 我要回复什么、能不能直接发/要不要转人工；⑤ 下一步是哪天、记了什么。
Ruling：同一行必须能展开“会话 + 回复”，不要求用户先跳页再回来。

**Q3：草稿到底存哪？**
A：会话回复草稿是服务端事实（`reception_replies`，`status=generated` 未发送），今日页的“跟进备注”是本地未提交输入。两者不混。
Ruling：今日页的“回复草稿”一旦保存就走 `POST /api/v1/workbench/sessions/{id}/reply-draft`；页面本机记忆只用于“尚未保存的输入”，不冒充服务端草稿。

**Q4：转人工是什么？**
A：`takeover` 是服务端状态迁移（mode→human，epoch+1，pending_reason=human_takeover），不是前端标记。
Ruling：只在会话确为 `open` 且 mode≠human 时展示“转人工接管”；成功后重新拉会话；失败保留原文并给恢复动作。

**Q5：导航模型从哪来？**
A：`eco-nav/v1` 文档由服务端产出（品牌/租户/角色/应用可启动性都来自权威），前端只渲染。
Ruling：Next 服务端布局用会话 cookie + 工作范围头拉取 `PUBLIC_AI_ECO_NAV_URL`；缺失/401/5xx/契约不符分别映射为 `unavailable` / `denied` / `unavailable`，**不回退预览 fixture**。fixture 只保留给组件单测。

**Q6：应用切换怎么做？**
A：浏览器 POST 自家 BFF `/api/eco-nav/launch`，只带 `(app_id, launch_target_id)`；BFF 用会话 cookie 向 Go `/api/v1/whoami` 取 principal/tenant，再调 public-ai `POST /internal/v1/identity/shell/launch`；校验 `launch_url`（https + 部署注入 host allowlist）后 303；前端 fetch 走 JSON 模式拿 URL 再跳。
Ruling：BFF 缺任一配置（identity base/token、host allowlist、leads server）即 503 `config_gate`，不拼 URL、不跳转、不猜目标。`launch_url` 主机不在 allowlist、带 userinfo、非 https 一律拒绝。

**Q7：返回来源呢？**
A：`return_context` 是导航 chip，不是业务续接。来源对象/版本归来源应用（task-view `resume_target` / order-handoff `return_target_id`）。
Ruling：`return_context` 有值才渲染；点击同样走 launch（source app + return target）。`source_ref` 为空时文案不得声称“精确回到原条目”，显示“回到来源应用（具体条目由来源应用恢复）”；本轮不伪造对象版本。

**Q8：账户/用量入口？**
A：已有 `/subscription` 页面接 public-ai 用量/订阅 API。
Ruling：导航只加入口链接到 `/subscription`，不新建钱包/套餐卡；导航的 billing 徽标仍只显示权威给的额度/钱包文案，未知就未知。

**Q9：视觉密度与 token？**
A：GoBoost 工作页的密度：一屏能扫完分组，行内事实 2×N，主行动唯一。
Ruling：今日页 = workspace 模式；工具条 56px；行卡 `--pn-radius-card`/`--pn-bg-surface`/`--pn-border-default`；间距只用 `--pn-spacing-*`；状态色只用已登记 pair（warning/ danger/ info / brand + weak/ surface/page）。导航加 `data-pn-surface="work.light" data-pn-theme="light"` 让 token 在壳外也解析。

**Q10：真实走查能做到哪？**
A：本地无生产凭据；public-ai identity/brand/billing 不启动。
Ruling：本地 dev stack 允许使用仓库既有 `identity-standin.mjs`（协议兼容替身，测试值）跑 UI 流程并截图；证据明确标注替身，**真实 public-ai 服务/真实租户/部署环境 = NOT_VERIFIED**。launch 路由用单测 + 注入 fetch 验证；真实 identity launch 无法在本机取证。

---

## 3. 今日工作台信息架构（IA）

```
main[data-pattern=workspace][data-page=today]
├─ header.page-head
│  ├─ h1 今天要完成的客户工作
│  └─ 工作范围 + “口径”展开（说明排序/金额分开/自动化边界）
├─ nav[data-attention-strip]   ← 注意力条：7 组的计数 + 锚点（不是 KPI 卡）
│  └─ a[data-attention-group] 新咨询 3 · 今天应跟进 1 · 等待客户 0 ...
├─ section[data-today-group] × 7（有内容才显示；顺序固定）
│  ├─ h2 组名（计数）
│  ├─ 空态：这组为什么空 + 新工作会出现在哪
│  └─ ul > li > RecordFrame（tone=human/ai/automation）
│     ├─ 标题：客户/线索（不是“这条线索”）
│     ├─ chips：来源（渠道/活动人话）· 分组原因 · 负责人 · 状态 · 下一步 · 最近互动
│     ├─ 主行动（每页仅 1 个）：记跟进 或 继续会话
│     ├─ 快捷动作：展开会话 / 打开完整会话 / 查看线索 / 转人工 / 交回 AI
│     ├─ ConversationPanel（按需加载，同一会话 API）
│     │  ├─ 访客/人工/AI 消息（只读，显示发送状态）
│     │  ├─ 回复输入（服务端草稿，明确“保存不等于发送”）
│     │  ├─ 保存草稿 / 批准并发送 / 发送（按 mode/epoch/status 决定）
│     │  └─ 错误行 + 重试（保留输入）
│     └─ 跟进表单：记录 + 处理（下一步/等待客户）+ 下一次时间 + 提交
└─ footer 链接（次级）
```

**状态语义（不伪造进度）**

- `loading`：骨架 + “正在读取今天的工作”，不显示 0。
- `empty`：解释“新咨询、到期跟进、会话、AI 草稿会出现在这里”，不画成功。
- `error`：真实失败原因（人话）+ 重试；不把 500 说成“没有数据”。
- `read-only`：非 owner 不显示分配/接管等越权动作；服务端仍是真源。
- 状态优先：计数为 0 的组不渲染大卡；计数只是注意力条，不做 KPI 趋势图。

**逐行“为什么在今天”**：`item.reason` 或组名 + 下一步/最近互动；AI 草稿明确“待确认，不会自动发送”；接待明确轮次/版本（`receptionFactLine`）。

---

## 4. 正常路径缺陷与根因修复（HUI-2598）

| # | 正常路径步骤 | 基线缺陷（证据） | 根因 | 修复（根因处） |
|---|---|---|---|---|
| D1 | 线索落地 → owner 打开 `/leads` | 负责人列显示 `mem_...` 原始成员 id；联系人请求失败时所有姓名变“未命名”，页面仍显示成功 | `/api/v1/leads` 只返回 `assigned_member_id`；页面再拉 `/api/contacts` 合并，失败分支被忽略 | 服务端列表视图 LEFT JOIN contacts/members/source_refs，一次返回 `contact_name/source_type/owner_label/assignment_reason`；页面删掉第二次请求和“未命名”兜底 |
| D2 | 回复待确认 → 打开 | AI 草稿行同时有 `lead_id` 和 `session_id`，链接优先级先看 `kind==="reception"`，草稿行落到线索详情 | 行内目标按 kind 而不是按“可执行动作”选 | 目标选择函数：有 `session_id` 且 kind ∈ {reception, ai_draft, human_takeover} → 会话；否则线索/商机。并给“查看线索”次级链接 |
| D3 | 会话里写回复 → 保存草稿 → 发送 | 保存的草稿 `status=generated`；`ApproveReceptionReply` 只允许 `mode='assist'`；人工接管（human）下既不能批准也不能发送 → 死路 | 草稿审批条件只覆盖 assist，没有覆盖 human | 服务端批准允许 `mode IN ('assist','human')`；前端对 human 会话显示“批准并发送”；AI 会话必须先转人工 |
| D4 | 今日页写回复 → 打开会话 | 今日页草稿只在本机 `leads_today_memory`；跳转后会话页输入框为空；已保存的服务端草稿在会话页无提示 | 两个输入面各自为政 | 今日行内 ConversationPanel 直接读/写服务端草稿；会话页在 human 模式预填最近一条 generated draft；离开页面前未保存输入仍按租户留在本机（明确“未保存”） |
| D5 | owner 分配 → 销售看到 | `PATCH /api/v1/leads/{id}` 的 `resolveAssignee` 只校验同租户，**不校验 enabled**；可分配给已停用成员，线索从此没人看见 | 分配校验少了成员可用性 | 拒绝 disabled 成员，返回 `bad_assignee`；前端成员列表已经过滤 disabled（`assignableMembers`） |
| D6 | 平台/活动来源 → 线索 | 列表只有 `source_type`，活动/渠道归因只在详情；列表无法判断“这条从哪来” | 列表缺 source ref 投影 | 列表视图带 `source`（form/activity/channel/at），行 facts 用人话 `sourceText`/`leadTitle` |
| D7 | 切租户/迟到响应 | 已有票号 + epoch 机制，但新增视图/抽屉若另起请求会绕过 | — | 新 ConversationPanel 复用票号/tenant 校验；切换范围立即卸载并丢弃迟到响应 |

**非目标**：不改 `PlaceToday`/`ApplyAIScore`；不自动外呼/发消息/建商机；不新建首页；不重写接待会话语义。

---

## 5. Semantic tokens 与 page-pattern 映射

### 5.1 Consumer profile

- `tokens.json` → `consumers["leads-web"]`：`defaultSurface=work.light`、`defaultTheme=light`、生成根 `web/src/styles/generated/painuo/v1`、importer 仅 `web/src/app/layout.tsx`。
- 生成物锁定在 producer commit `01e6765`（`painuo-tokens.lock.json`），本轮**不重新生成、不改锁**。

### 5.2 角色替换（touched files）

| 用途 | 语义角色 |
|---|---|
| 页面/卡片底 | `--pn-bg-page` / `--pn-bg-surface` / `--pn-bg-sunken` |
| 分隔/描边 | `--pn-border-default` / `--pn-border-strong` |
| 正文/次要/弱化 | `--pn-text-primary` / `--pn-text-secondary` / `--pn-text-muted` |
| 品牌文字/实底 | `--pn-brand-text` / `--pn-brand-accent` / `--pn-brand-ink` / `--pn-brand-weak` |
| 危险/警告/信息/成功 | `--pn-status-{danger,warning,info,success}-{fg,weak}`；实底文字 `-ink` |
| 圆角/间距/控件高/焦点 | `--pn-radius-*`、`--pn-spacing-*`、`--pn-control-height-*`、`--pn-focus-*` |
| 动效 | `--pn-motion-duration-*`（含 `--pn-motion-reduced`） |

Ruling：**业务 CSS 不新增裸 hex**；`:root` 保留的旧值只是公开表单（`/f`、`/r`）在 renderer scope 外的回退，不允许在 `[data-pn-surface="work.light"]` 作用域内新增/使用裸 hex。导航 `<header>` 加 `data-pn-surface="work.light" data-pn-theme="light"`，让壳外也解析 token。

### 5.3 Page-pattern 契约（page-patterns/v1）

| 路由 | 模式 | 采用的区域义务 |
|---|---|---|
| `/` | `workspace` | 一个 h1；section h2；最多一个可执行主行动；status/empty/loading/error 独立投影；移动 touch/桌面 default |
| `/leads` | `list` | header + body + primary_action + status/empty/loading/error；行行动作不冒充主行动 |
| `/leads/[id]` | `detail` | 同上 + 记录事实；分配动作不抢 `data-page-primary` |
| `/reception` | `list` | 会话列表 + 详情面板；关闭/接管是明确动作 |

实现方式：页面根节点带 `data-pattern`、关键区域带 `data-region="header|status|primary_action|empty|loading|error|body"`，测试锁定；不引入外部 page-pattern 运行时包（`pageMap.test` 已禁止）。

### 5.4 对比度门（deterministic）

- 独立计算 WCAG 2.x：`(L1+0.05)/(L2+0.05)`，输入直接解析 `tokens.css` 的 `[data-pn-surface="work.light"][data-pn-theme="light"]` 块，不读 `resolved.json.verdict` 当结论。
- 配对集合：`resolved.json` 中 `profiles` 含 `leads-web` 且 `surface/theme = work.light/light` 的 99 对；逐对重算并与 `minRatio` 比较。
- 页面配对：新增 CSS 使用的角色对（`text.primary/bg.surface`、`text.muted/bg.page`、`brand.ink/brand.accent`、`status.warning.fg/status.warning.weak`、`status.danger.fg/status.danger.weak`、`border.focus/bg.surface`）必须在 99 对中存在，否则失败。
- 裸值门：`globals.css`、`eco-top-nav.module.css`、新增组件 CSS 中除 `:root` 回退块外不得出现 hex/rgb；允许生成的 tokens/aliases 与 `public/offline.html`（离线页用系统色）。
- 失败即红，不写 `PASS` 常量。

---

## 6. EcoTopNav 数据流与 launch

```
(browser)                       (Next RSC)                        (Go)                 (public-ai)
GET /  ──cookie──────▶ layout: loadEcoNavModel(cookie, x-tenant-id)
                              └─ GET PUBLIC_AI_ECO_NAV_URL ─────────────────────────▶ eco-nav model
                              parse eco-nav/v1 strict → renderable? → CrmShell
                                                                    │
click app ─POST /api/eco-nav/launch {app_id, launch_target_id}──────┘
            └─ BFF: config gate → GET /api/v1/whoami (cookie) ─────▶ principal/tenant
                    └─ POST /internal/v1/identity/shell/launch ─────────────────────▶ {launch_url}
                    └─ validate https + host allowlist + echo app/target
                    └─ 303 Location (browser form) / 200 JSON (fetch mode) → window.assign
```

- 模型不可渲染（hidden/denied/unavailable）→ 不渲染导航；`unavailable` 额外显示一行“生态导航暂不可用（可重试）”，不显示品牌/应用/租户数据。
- 当前应用高亮：`current_app.app_id === leads-engine`；只在服务端文档 app_id 为 leads-engine 时采用其显示名/图标，否则用本应用名（`pinLeadsApp`）。
- 可启动项：`state=launchable` 且 `capabilities.can_switch_app`；`entitlement_required`/`temporarily_unavailable` 渲染为不可执行 + 原因；无 target 的降级项不可点。
- 人工切换载荷只有 `(app_id, launch_target_id)`；`creates_handoff=false`、`nav_intent=manual_switch`；不写线索/付款主体。
- 返回 chip：`return_context` 有值才渲染；点击 `POST /api/eco-nav/launch` `(source_app_id, return_target_id)`；`source_ref` 为空时显示“具体条目由来源应用恢复”，不声称精确返回。
- 账户/用量：导航 “账户与用量” → `/subscription`（既有 public-ai 用量接线），不建第二钱包/套餐 UI。

---

## 7. 错误与恢复（诚实失败）

| 场景 | 页面行为 |
|---|---|
| 无工作范围 | recovery 卡：在顶部选择工作范围；不发请求 |
| BFF 未配置 | 503 `bff_config_gate` → 页面显示“服务还没配置好，联系管理员”，不显示空数据 |
| Go 不可达 | 503 `server_unavailable` → “服务暂不可达”，重试 |
| 身份过期 | 401/403 → “登录已过期，重新登录后继续”，保留输入 |
| 会话已关闭/轮次过期 | 409 → 明确“会话已关闭/轮次已变”，刷新会话；不发 |
| 草稿冲突（同 client id 不同内容） | 409 `reply_conflict` → “这条草稿已被另一处修改”，刷新后再改 |
| launch 被拒 | 403/429/503 → 对应人话；显示“目标应用没有打开”，不重试扣费类动作 |
| 返回目标未登记 | BFF 400 `return_target_rejected` → 禁用 chip + 原因 |

---

## 8. 验收与证据边界

- Go：`go test ./...`、`go vet ./...`；新增列表视图/分配/草稿审批测试。
- Web：`npm test`（单测 + SSR 组件 + 路由函数）、`npm run build`；新增对比度门、launch 规划器、今日 IA 纯函数测试。
- 本地 dev：identity 替身 + Go + Next，走 owner 正常路径；截图 1440/390。替身明确标注。
- 明确 NOT_VERIFIED：真实 public-ai identity/brand/billing 服务、真实租户数据、生产部署、真实跨应用 shell launch 往返、真实矩阵/碰一碰入站 handoff 的对象版本解析。

---

## 9. 决策清单（供 Lead 复核）

- R1：今日页不新增首页路由，继续用 `/`。
- R2：会话/回复在今日页内联展开，不复制会话语义；数据仍来自 `/api/v1/reception/*` 与 `/api/v1/workbench/sessions/*`。
- R3：服务端列表返回联系人/负责人投影，页面不再二次拉 contacts。
- R4：human 模式允许批准自己保存的草稿（`mode IN ('assist','human')`），AI 模式仍需先接管/协助。
- R5：导航 loader 不回退 fixture；无配置=不可渲染 + 一行可重试提示。
- R6：launch BFF 要求 host allowlist，缺配置 fail-closed；launch_url 只允许 https。
- R7：返回 chip 不伪造对象版本；来源应用负责精确恢复。
- R8：本轮不改生成 token 产物/锁；只消费并加门。
- R9：本地走查用协议兼容 identity 替身，证据标注替身；不据此宣称真实生态链路已验证。
