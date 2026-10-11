# HUI-2626 / HUI-2598 AG06 集成证据（2026-10-11）

- 基线：`origin/main` `0d86be4780c808505545fbb491569933f74522b2`（PR #53）
- 分支：`codex/20261011-ag06-leads-integrator`（工作树 `/Volumes/MobileHD/Work/派诺生态/_worktrees/20261011-ag06-leads-integrator`）
- 完整 HEAD：见 PR 描述与提交记录（本文件所在提交之后的 HEAD 为准）
- 设计与计划：`docs/superpowers/specs/20261011-ag06-leads-integrator-design.md`、`docs/superpowers/plans/20261011-ag06-leads-integrator.md`

## 1. 栈与依赖范围（真实 vs 替身）

| 组件 | 状态 |
| --- | --- |
| Go leads-server（`server/cmd/leads-server`，本工作树构建，`GOWORK=off`） | 真实，`config_issues:0`，SQLite 真实落库 |
| web BFF（`next dev -p 29330`，`/api/* → Go /api/v1/*`） | 真实，fail-closed 合同在链路上生效 |
| 平台 identity | 协议兼容替身（`harness/identity-standin.mjs`，非仓库产物，只映射会话到成员） |
| Touch 源端 record 拉取 | 替身（`harness/recordsrc.mjs`，按 `payload_ref.sha256` 校验，非真实 Touch） |
| Touch directed-event | 真实 HTTP `POST /internal/v1/notify/ingest`，HMAC 签名（`harness/ingest.mjs`） |
| 浏览器 | 系统 Chrome，Playwright `channel: chrome`，headless；会话 cookie 与 `leads_tenant_id` 注入，服务端逐请求校验 |
| 故障注入 | Playwright route 对 `follow-through` 返回 503（注入点已标注） |

租户：A `tnt_ed161dd306c0d3c05bab0bb3506a1cfb`，B `tnt_b0b0944b0f44dff21e850635c207b026`。同一 owner 成员 `usr_owner_walk` 同时是 A、B 的 owner。
Touch 源租户 `touch_t_a` → A，`touch_t_b` → B（绑定不允许源与目标相同）。
本地测试值（secret、token、pepper）均经环境变量注入，仓库内不含字面量。

## 2. 结果清单（逐条 PASS / FAIL / NOT_RUN）

### 2.1 测试命令

| 命令 | 结果 |
| --- | --- |
| `cd server && GOWORK=off go test ./... -count=1`（基线 0d86be4，本会话未改 Go） | PASS：26 个含测试包 `ok`，退出 0 |
| `cd web && npx vitest run`（改动后） | PASS：41 文件，273 测试 |
| `cd web && npx tsc --noEmit` | 3 个既有错误，全部在 `tests/channelInteraction.test.ts`、`tests/pageMap.test.ts`，本次改动文件无新增错误 |

### 2.2 真实 HTTP：Touch 入站与双租户可见性

| 项 | 结果 | 证据 |
| --- | --- | --- |
| Touch 事件 A、B 各一条，签名有效，`target_app=leads` | PASS：两条均 `200 accepted`，线索与联系人分别落到各自租户 | 本节下方输出 |
| 同一 trace 与版本重放 | PASS：返回相同 `lead_id`、`contact_id`，未新增行。注意：重放响应中 `duplicate` 字段仍为 `false`（见观察 O4） | ingest 输出 |
| A 工作台可见，B 工作台不可见 | PASS：A 的线索在 `unprocessed`、`assignment_exception`、`today.info_or_permission`；B 的工作台不含 A 的任何线索 id | `workbench` 检查 |
| B 读取或改派 A 的线索 | PASS：`GET` 与 `PATCH` 均 `404` | curl 输出 |

### 2.3 缺陷与修复（走查中发现，TDD 修复）

1. **FAIL → FIXED：待分配线索没有分配动作。** 详情页只显示「负责人 待分配」，没有任何分配控件；今日卡上「记跟进」禁用且无原因，线索无法从今日完成首次处理。服务端 `PATCH /api/v1/leads/{id}` 已支持 `assigned_member_id`（仅 owner）。
   - 修复：`web/src/lib/leadAssign.ts`（纯函数）+ 详情页 owner 分配块（`/api/admin/members` 能读取才展示，非 owner 读取失败则隐藏，服务端仍是权限真源）。
   - 测试：`web/tests/leadAssign.test.ts`，先红后绿（6 项）。
2. **FAIL → FIXED：分配成功后确认信息不可见。** 成功后区块因负责人已定而卸载，`assignMsg` 被 effect 清空。
   - 修复：确认信息移到区块外，`role="status"`；effect 仅在线索 id 变化时清空。
   - 测试：同文件的 `role="status"` 断言，先红后绿。
3. **FAIL → FIXED：fix2 页面卫生守卫拦截页面内 `JSON.stringify`。** 
   - 修复：`web/src/lib/fetchJson.ts` 新增 `patchJson`，序列化收敛在 lib 层（与 `postJson` 同契约）。守卫未放宽。

### 2.4 浏览器主路径（owner，单租户 A）

| 视口 | 线索 | 步骤 | 结果 |
| --- | --- | --- | --- |
| 1440×900 | `lead_d1162b2bb98df6a2f5310e9beac88d4d`（来源 `sub_walk_a2`） | 今日「信息缺失或权限异常」→ 查看线索 → 选成员 → 分配负责人 → 已分配负责人 → 填记录与下一次 → 记跟进 → 今日 → 刷新 → 重新登录后详情 | PASS：每步成功；离开信息组；刷新与重登后仍为已分配，区块不再出现。脚本计数：7 次点击、2 次填写、1 次选择 |
| 390×844 | `lead_8ba5f3fa56e28b0831ec36f0cc85323d`（来源 `sub_walk_a5`） | 同上（脚本未重复「刷新后今日」判断，见观察 O3） | PASS：服务端时间线含 `assignment:已分配` 与 `follow_up:真实跟进…`；UI 计数同为 7 / 2 / 1 |
| 1440×900 故障注入 | `lead_0e9ac72461a3de5b3115f2719ebeda3d` | 记跟进请求返回 503 | PASS：输入保留，显示「服务暂未响应」恢复文案。截图 `screens/fault-save-503-1440.png` |

说明：1440 步骤脚本的访客名参数与实际线索不一致（脚本参数误填为其他线索的名字），因此「刷新后今日仍含访客」一项为误报，已以线索 id 与服务端时间线为准重新核对。截图与 JSON 按实际线索 id 记录。

### 2.5 NOT_RUN（本会话未完成，附解除条件）

| 项 | 原因 | 解除条件 |
| --- | --- | --- |
| 租户 B 的浏览器走查（B 的今日 → 详情 → 跟进） | 本会话只完成 B 的 API 隔离与 404 证明 | 在 B 工作范围下重跑 `harness/walk.mjs` 的同一路径 |
| 新分配块的键盘与焦点证据（Tab、Enter、Esc） | 本次只跑了鼠标路径 | 对 `RendererSelect` 做键盘选择与焦点返回录制 |
| 触控 48px 与 390 下分配块的触控目标证据 | 同上 | 以 `touch-targets` 同方法采集 |
| HUI-2598 三个代表任务旧/新各 5 轮的计时（主动时间、等待、步数、错误恢复） | 需要人工计时协议与旧版本并排运行；旧基线选定为 `b58ea17`（Today 实现前的 PR #39 合并） | 独立执行者按设计 spec D4 执行；本会话不写改善百分比 |
| 非作者 Spec 与 Standards 两轴审查 | 本环境没有可派生子代理的工具；作者不自签 | AG00 指定两名非作者审查者（escalation E1） |
| 去 Logo 产品族盲评（非作者） | 需要 2625 方法与评审池 | AG00 安排（escalation E3） |
| Touch 源端 trace 组合版本闭环（AG07，审计时 #48 OPEN） | 归属 AG07 与 AG00 | 见 escalation E2 |
| HUI-1682 真实文本模型、HUI-1688 接待核心 | 归 AG06-AI，本集成人不伪造 | AG06-AI 提供模型标识与费用调用方式 |
| 真实渠道发送 | 无授权，代码路径不发送 | 渠道授权与回执字段 |

## 3. 观察（未修复，需复核）

- **O1 隐私：** 时间线的来源事件中，owner 视图返回并显示明文联系方式（`contact_plaintext`，如 `13900000081`）。`allowed_contacts` 仅为「渠道内回复」。需确认 owner 读取明文是否符合权限设计，超出则应脱敏。
- **O2 今日卡：** 待分配线索的「记跟进」禁用且无原因。待补充项已写「负责人」，但没有行内动作。本次只在详情页补了分配动作，今日卡的提示未改。
- **O3 排期去向：** 设置了明天的下一次后，线索离开今日，今日页没有可见的「已排期」分组。是否需要显示「之后安排」属于产品决定。
- **O4 重放字段：** 同一 trace 与版本的重放返回相同 `lead_id`，但 `duplicate` 为 `false`。需确认字段语义（联系人去重还是事件去重）。
- **O5 控制台 404：** 线索详情加载 `campaigns/.../light-copy` 与 `campaign-motion/capability` 时出现 404，功能开关关闭所致，属既有行为，本次未改。
- **O6 部署键：** Touch 事件的 `target_app` 必须与本机 `LEADS_APP_ID` 一致（本地为 `leads`，测试中曾误用 `leads-engine` 被拒）。

## 4. 复跑（需先在本机启动三段链）

1. `harness/identity-standin.mjs`（端口 29310）与 `harness/recordsrc.mjs`（端口 29320）。先导出 `LEADS_WALK_TOUCH_SECRET`、`LEADS_WALK_FETCH_TOKEN`、`LEADS_WALK_INTERNAL_TOKEN`、`LEADS_WALK_IDENTITY_TOKEN`、`LEADS_WALK_PEPPER`、`LEADS_WALK_DEDUP`。
2. 用 `provision-tenant`、`provision-member` 建两租户与成员（见 `harness/start-go.sh` 所用的环境）。
3. `harness/start-go.sh` 启动 Go（29300）；`next dev -p 29330` 启动 BFF，并设 `LEADS_SERVER_URL`、`LEADS_INTERNAL_TOKEN`。
4. `node harness/ingest.mjs <touch_source_tenant> <ref> <name> <phone> <wechat>` 注入线索。
5. `node harness/walk.mjs <tenant> <w> <h> <lead_id> <tag> <note> <visitor>` 走主路径；`node harness/fault.mjs <tenant> <lead_id> <out.png>` 做故障注入。

脚本依赖 scratchpad 路径与 Playwright（未入仓依赖，安装于本地临时目录），不属于产品依赖。
