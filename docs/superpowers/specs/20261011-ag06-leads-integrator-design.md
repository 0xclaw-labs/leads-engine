# AG06 Leads 集成人设计：HUI-2626 / HUI-2598 剩余验收

- 日期：2026-10-11
- 票：HUI-2626（UI-R5 L Leads Product Finish P0，In Progress，Urgent）、HUI-2598（UX-R4 L1 Today/Next，In Progress，High）
- 仓：`/Volumes/MobileHD/Work/派诺生态/leads-engine`（origin `https://github.com/bianjiefilm/leads-engine.git`）
- 基线：`origin/main` = `0d86be4780c808505545fbb491569933f74522b2`（PR #53 合并）
- 本工作树：`/Volumes/MobileHD/Work/派诺生态/_worktrees/20261011-ag06-leads-integrator`，分支 `codex/20261011-ag06-leads-integrator`，从 origin/main 新建。根 checkout（`main` @ `368eaa0`，脏文件 `docs/agents/`）未触碰。
- 路径分类：architectural（跨 Today/Next、共享详情、接待、Touch trace 契约、度量与独立验收）。
- 协调者自批：本运行没有人类可回答。按任务指令，设计批准由协调者（AG06 集成人，受 AG00 指挥）自批并写明。跨群接口、共享源码、权限或费用语义、新窄修，记入 escalations，不停其余工作。

## 1. 意图与成功标准（协调者写回）

- 意图：Leads 成为日常销售工作台。销售从 Today 打开共享客户详情，记录跟进、安排下一步，回到原筛选与滚动位置；刷新或重登后服务端事实保持；Touch 来源线索可进入同一条路径。
- 成功标准（票面原文）：
  1. 双租户 Touch→Today→详情/跟进/重登可在真实 HTTP 与浏览器中走通，A/B 不串。
  2. 1440 与 390 下操作数、重复输入、恢复记录可量化；键盘焦点与移动触控有证据。
  3. HUI-2598 三个代表任务旧/新各 5 轮的主动时间、等待时间、步数、错误恢复记录完整，不先写改善百分比。
  4. 去 Logo 正式评审由非作者盲评完成，作者自评与独立评审分别记。
- 不属于本轮：HUI-1682 真实文本生成（硬关闭，`model_ready=false`，归 AG06-AI，本集成人不伪造）；HUI-1688 接待核心改造（归 AG06-AI）；HUI-2391 / HUI-2747 送矩阵与 Motion（归 AG06-HANDOFF）；HUI-2294、HUI-2581（归 AG00）；AG02 附件 35 票与 HUI-3136～3142（排除）。

## 2. 当前事实（已核实，不沿用旧说法）

- origin/main 已含：Today 工作面与 `ffd69ee`（PR #40）；`e34fd7a`/PR #47 摩擦证据；PR #48 `687f9d0` SOP/外呼去手填内部号；PR #50/#51 九页壳、控件、错误/离线/焦点与 fix2；PR #41 `831067e` 销售工作台外壳；PR #52 与 PR #53 `0d86be4`（Today 回复草稿、会话上下文、接待深链、Touch trace/source_version/return_target 与按 trace 与版本幂等）。
- 原 AG06 主管状态文件所列的 6 项遗留（浏览器手改与返回继续、真实文本模型、真实发送、非作者审查、Linear 回读、CI）仍有效，除 PR #53 已落地的代码部分。
- 本工作树基线实测（2026-10-11，`0d86be4`，本会话运行）：
  - `cd server && GOWORK=off go test ./... -count=1`：全部 26 个含测试包 `ok`，退出码 0。
  - `cd web && npm ci && npx vitest run`：40 文件、267 测试全部通过。
- 旧 2598 基线 `baseline_steps` / `active_time` 仍 `not_measured`，`reduction` 仍 `not_claimed`。
- 2626 票面要求的非作者盲评、2598 的 5 轮度量、双租户浏览器走查，在源码与历史证据包中均未完成。

## 3. 决定与理由（自答记录）

**D1 单作者窗口。** 本工作树是 Today、接待、客户详情与 `server/internal/workbench`、共享 BFF/API 文件的唯一写入者。AG06-AI（`hui-1688*`、`hui-1682-r3`）与 AG06-HANDOFF（`hui-2391-r3`、`hui-2747*`）各自在独立树工作，只通过接口版本与补丁交付，由本集成人合并。理由：票面要求「共享文件由你排窗口，不能两角色另造 CRM 或 duplicate conversation 数据」。

**D2 不改已合入的 trace 契约。** 当前 main 的 notify 解析（`server/internal/notifyingest/envelope.go`）接受 `trace_id`（必须为 `lead:<source_ref>`）、`source_version`、`return_target`，并已按 trace 与版本幂等。不重命名、不新增字段。理由：契约已在 main 上并有测试；AG00 只需对「新增字段名、所在对象、幂等键」做裁定时才需介入，本轮不改。Touch 源端 trace 新增审计时仍为 OPEN（编号 #48，归属待核实），由 AG07 精确组合版本后签闭环。本集成人不据此签 Touch 全链完成。

**D3 真实 HTTP 为主证据。** 本机三段链：Go `:29300` `/api/v1/*` → SQLite；BFF `:29330`；identity stand-in `:29310`。两租户 A/B 各有成员与数据。fixture、包测试、CI、PR 合并、旧 Done、健康页都不作为当前生产、设备、供应商账单或真人验收证据。

**D4 度量协议。** 旧基线取 `b58ea17`（PR #39 合并，即 Today 实现前的客户列表首页，`03bd4a8` 的父提交）。理由：原 `HUI-1893` 基线不可运行，按票面「最近可复现替代」处理，并在证据中说明替换理由。新版本取 `origin/main` 当前 HEAD。三个代表任务：新咨询首次处理、到期跟进→安排下一步、等待客户→恢复处理。每项各 5 轮，记录主动时间、等待时间（网络与模型等待单列）、步数、输入次数、误操作、完成与恢复结果。本会话若无法完成全部运行，记 `NOT_RUN` 与解除条件，不写改善百分比。

**D5 独立审查无法在本环境派出。** 本环境没有可派生子代理的工具。按协议记 `NOT_RUN` 并写入 escalations，由 AG00 指定两名真正非作者分别做 Spec 与 Standards 审查，读完整原票、diff、最终 HEAD 与证据。作者不签字。

**D6 盲评不由本集成人签字。** 去 Logo 产品族评审需非作者盲走，按 2625 方法。本集成人只提供截图矩阵与证据包，结论记 `NOT_RUN`。

**D7 本轮代码改动范围。** 只修真实走查中发现的缺陷，且修复必须有失败测试先行（TDD）。不新增功能，不改 HUI-1682 真实文本，不发送任何渠道消息，不调用 `.../replies/{replyId}/send`。

**D8 收口。** 完成后固定提交（commit 尾行 `Co-Authored-By: Claude Haiku 5.5 <noreply@anthropic.com>`），`git push -u origin codex/20261011-ag06-leads-integrator`，`gh` 开 draft PR（PR 描述尾行 `🤖 Generated with [Claude Code](https://claude.com/claude-code)`）。不合并、不开 auto-merge、不部署、不 force-push、不删除任何 worktree 或分支。

## 4. 设计：验证与改动面

**改动面（仅当走查证明缺陷时）**：`web/src/app/(shell)/page.tsx`（Today）、`web/src/app/reception/`、`web/src/app/(shell)/leads/[id]` 与 `contacts/[id]` 详情、`web/src/components/workbench/*`、`web/src/lib/*`（BFF 与 fetch 票号）、`server/internal/workbench/*`、`server/internal/httpapi/*` 的 workbench 与 notify 处理函数。所有其他文件不动。

**验证路径**：
1. 启动本机三段链（使用工作树自身的 `server/cmd/leads-server` 与 `web` dev server；环境变量仅用 `deploy/*.env.example` 的键名，值为本机测试值，不读取任何凭证文件）。
2. 用真实 HTTP 注入 Touch 来源事件（`directed-event/v1`，`lead.authorized_submitted`，`trace_id=lead:<source_ref>`，`source_version`，`return_target`），并分别在 A、B 两租户验证线索只出现在对应 Today。
3. 浏览器（系统 Chrome，Playwright 或等效工具，headless）走：Today → 打开共享详情 → 记录跟进 → 安排下一步 → 回原筛选与滚动 → 刷新 → 重登 → 结果一致。
4. 1440 与 390 视口各跑一遍同一路径，记录点击数、输入次数、重复输入、恢复成功与否。
5. 键盘：Tab 到达、Enter 打开、Esc 关闭、焦点返回；移动：触控目标尺寸（≥44px）。
6. 失败保留输入：断网或服务端 500 时，输入保留并提示恢复入口。

**不做**：不写 `TODO` 占位，不把截图当作验收，不把模拟数据当作 Touch 全链证据。

## 5. 风险与未决项（escalations 草稿）

- E1（AG00）：指定两名非作者审查者（Spec、Standards），分别读本 spec、plan、diff 与证据。
- E2（AG00 / AG07）：Touch 源端 trace 组合版本与 #48 闭环；本集成人不签 Touch 全链。
- E3（AG00 / 2625 方法）：去 Logo 盲评由非作者评审池执行。
- E4（AG00）：2598 度量需要的 5 轮真实运行是否由本集成人执行，或由独立执行者按本 spec D4 执行。
- E5（AG06-AI）：HUI-1682 真实文本模型标识与费用调用方式。缺失则保持 `model_ready=false`。
- E6（AG06-HANDOFF）：HUI-2391/2747 的 Matrix 草稿与 Motion 成片引用；缺供给只禁用动作，不挡 Today 主链。
