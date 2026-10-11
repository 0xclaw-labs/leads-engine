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

> 2026-10-11 续跑（第二作者接续，见第 5 节）：下表前四项已解除，结果见 2.6；其余项仍 NOT_RUN。

| 项 | 原因 | 解除条件 | 续跑结果 |
| --- | --- | --- | --- |
| 租户 B 的浏览器走查（B 的今日 → 详情 → 跟进） | 本会话只完成 B 的 API 隔离与 404 证明 | 在 B 工作范围下重跑 `harness/walk.mjs` 的同一路径 | 已解除（2.6.1）：1440 与 390 均 PASS |
| 新分配块的键盘与焦点证据（Tab、Enter、Esc） | 本次只跑了鼠标路径 | 对 `RendererSelect` 做键盘选择与焦点返回录制 | 已解除（2.6.2）：全 PASS |
| 触控 48px 与 390 下分配块的触控目标证据 | 同上 | 以 `touch-targets` 同方法采集 | 已解除（2.6.3）：5/5 目标 ≥44px |
| HUI-2598 三个代表任务旧/新各 5 轮的计时（主动时间、等待、步数、错误恢复） | 需要人工计时协议与旧版本并排运行；旧基线选定为 `b58ea17`（Today 实现前的 PR #39 合并） | 独立执行者按设计 spec D4 执行；本会话不写改善百分比 | 已解除（2.6.4 / `metrics/SUMMARY.md`）：36 轮全完成；reduction 维持 not_claimed |
| 非作者 Spec 与 Standards 两轴审查 | 本环境没有可派生子代理的工具；作者不自签 | AG00 指定两名非作者审查者（escalation E1） | 仍 NOT_RUN |
| 去 Logo 产品族盲评（非作者） | 需要 2625 方法与评审池 | AG00 安排（escalation E3） | 仍 NOT_RUN |
| Touch 源端 trace 组合版本闭环（AG07，审计时 #48 OPEN） | 归属 AG07 与 AG00 | 见 escalation E2 | 仍 NOT_RUN |
| HUI-1682 真实文本模型、HUI-1688 接待核心 | 归 AG06-AI，本集成人不伪造 | AG06-AI 提供模型标识与费用调用方式 | 仍 NOT_RUN |
| 真实渠道发送 | 无授权，代码路径不发送 | 渠道授权与回执字段 | 仍 NOT_RUN |

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
4. `node harness/ingest.mjs <touch_source_tenant> <ref> <name> <phone> <wechat>` 注入线索（手机号须 11 位，且跨任务不得同号——旧 ingest 按手机号去重合并联系人）。
5. `node harness/walk.mjs <tenant> <w> <h> <lead_id> <tag> <note> <visitor>` 走主路径（`MEMBER_MATCH` 环境变量选 owner 显示名，租户 B 用 `店主乙`）；`node harness/fault.mjs <tenant> <lead_id> <out.png>` 做故障注入。
6. 续跑新增：`keyboard.mjs`（键盘/焦点）、`touch.mjs`（390 触控目标）、`ingest-old.mjs`（b58ea17 契约注入，剥新版字段）、`seed.mjs`（度量种子）、`metric.mjs`（计时轮）。

脚本依赖 scratchpad 路径与 Playwright（未入仓依赖，安装于本地临时目录），不属于产品依赖。

## 5. 续跑（2026-10-11 下午，第二位 AG06 集成人接续，无产品代码改动）

继承状态：前作者 WIP 已在其会话内提交为 `35e77b9` 并推送（PR #54 草稿）。续跑者对该提交做事实核验（vitest 41 文件 273 测试 PASS、go 26 包 PASS），不重做、不改写。本轮全部为证据补全，产品代码零改动。

### 2.6.1 租户 B 浏览器走查（解除 NOT_RUN #1）

三段链在本会话 scratchpad 重建（identity 替身 :29310、record 替身 :29320、Go :29300 healthz ready `config_issues:0`、BFF :29330）。`walk.mjs` 加 `MEMBER_MATCH` 环境变量（租户 B owner 显示名为「店主乙」），其余路径与 A 租户走查完全一致。

| 视口 | 线索 | 结果 | 计数 |
| --- | --- | --- | --- |
| 1440×900 | `lead_5595c49a79e5ce3a32ca2529c7d0eae6`（B，`sub_walk_b1`，访客乙） | PASS：今日→详情→分配店主乙→记跟进+下一次→今日→刷新→重登，重登后仍已分配、分配块不再出现 | 7 点击 / 2 填写 / 1 选择 |
| 390×844 | `lead_7b5d49e0a823042534c6ee1e6ee3b7be`（B，`sub_walk_b2`，访客B2） | PASS：同上 | 7 / 2 / 1 |

截图 `screens/walkB1440-*.png`、`screens/walkB390-*.png`，步骤 JSON 同目录。控制台 404 为既有 O5（功能开关关闭的 capability 端点）。

### 2.6.2 分配块键盘与焦点（解除 NOT_RUN #2）

`harness/keyboard.mjs`（系统 Chrome headless，1440×900，租户 A 线索 `lead_99fa217ab70cd33f81483ddd6bea835a`）：

| 断言 | 结果 |
| --- | --- |
| Tab 到达分配触发器（第 14 次 Tab，`BUTTON role=combobox`） | PASS |
| Enter 打开下拉（listbox 可见） | PASS |
| Esc 关闭下拉且焦点返回触发器 | PASS |
| 方向键+Enter 键盘选择成员（触发器显示「店主甲（店主）」） | PASS |
| Tab 到「分配负责人」按钮（1 次 Tab）并 Enter 提交 | PASS |
| 确认信息 `role="status"`（`data-assign-result`） | PASS |

证据：`screens/kbd1440-k01..k03.png`、`screens/kbd1440-keyboard.json`。

### 2.6.3 390 触控目标（解除 NOT_RUN #3）

`harness/touch.mjs`（390×844，`hasTouch`+`isMobile`，租户 A 线索 `lead_50e29ba82aa0375336fe9d4146349680`）真实 boundingBox：

| 目标 | 宽×高 px | ≥44px | ≥48px |
| --- | --- | --- | --- |
| 分配触发器（combobox） | 324×48 | 是 | 是 |
| 分配负责人按钮 | 324×44 | 是 | 否（正好 44） |
| 记录 textarea | 324×68 | 是 | 是 |
| 下一次 input | 324×47 | 是 | 否 |
| 记跟进按钮 | 324×44 | 是 | 否（正好 44） |

5/5 满足设计 spec 的 ≥44px；48px 口径下 2/5（两个按钮正好 44px）。如实记录，不做门槛调整。证据：`screens/touch390.png`、`screens/touch390.json`。

### 2.6.4 HUI-2598 度量（解除 NOT_RUN #4，R5 协议）

环境：旧基线独立树 `/Volumes/MobileHD/Work/派诺生态/_worktrees/20261011-agg/leads-2598-baseline`（`b58ea17`，用后保留），旧链 Go :29400 + BFF :29430 + 独立 DB；新链同 2.6.1（HEAD `35e77b9` 的树）。两链同一 identity/record 替身，HMAC 与 fetch token 同值。

每格 5 轮清洁计时 + 第 6 轮 503 故障恢复；理想脚本操作者（missteps 清洁轮为 0）；**不写改善百分比**。

| 版本 | 任务 | 清洁轮中位总时长 ms | 中位主动 ms | 中位等待 ms | 步数 | 输入 | 完成 | 故障轮恢复 |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| 新 | 新咨询首次处理 | 1287 | 115 | 1174 | 2 | 2 | 6/6 | 输入保留+重试成功 |
| 新 | 到期跟进→安排下一步 | 1263 | 94 | 1167 | 2 | 2 | 6/6 | 输入保留+重试成功 |
| 新 | 等待客户→恢复处理 | 1272 | 101 | 1172 | 2 | 2 | 6/6 | 输入保留+重试成功 |
| 旧 | 新咨询首次处理 | 1060 | 92 | 975 | 2 | 2 | 6/6 | 输入保留+重试成功 |
| 旧 | 到期跟进→安排下一步 | 1069 | 79 | 979 | 2 | 2 | 6/6 | 输入保留+重试成功 |
| 旧 | 等待客户→恢复处理 | 1091 | 90 | 993 | 2 | 2 | 6/6 | 输入保留+重试成功 |

如实结论（不构成改善声明，reduction 维持 not_claimed）：

1. 理想操作者脚本下两版步数与输入完全相同（2/2）；新版中位总时长高约 0.2s（Today 多卡片与草稿渲染更重，差在等待侧）。
2. 质性差异（脚本计时未捕捉）：旧版对**未分配** Touch 线索无任何分配入口（经 UI 任务不可完成，步数无意义）；新版提供分配动作（walk 证据 7 步完成）。旧版任务完成后线索仍停留 unprocessed 桶（status 停留 new），新版完成即离开 Today。
3. 任务映射：旧系统无 `waiting_customer` 处置，「等待客户→恢复处理」映射为 needs_schedule 桶（in_progress 无未来下一步）；旧 ingest 按手机号去重合并联系人，种子号码必须跨任务不同（第一轮种子因此作废重种，过程见 harness 注释）。
4. 每轮原始数据：`metrics/m-<版本>-<任务>-<轮>.json`（36 个）+ `metrics/SUMMARY.md`。`m-new-new-1/6` 的 `.first-attempt` 为脚本记账错误（lead 被冒烟消费）的首次运行，保留备查。

### 5.1 续跑中的失败与处置（不抹首轮）

- `recordsrc` 首次复跑 503 `source_unavailable`：scratchpad 里遗留的是前作者旧版替身（token 硬编码），换提交版（读环境变量）后恢复。属环境问题，非产品缺陷。
- 度量种子第一轮全部作废：种子脚本手机号 10 位被服务端判 `invalid_phone` 过滤（服务端行为正确），后又一轮因号码跨任务同号被旧 ingest 合并联系人、`complete:true` 连锁完结开放跟进。两轮种子脚本缺陷均如实记录在 `harness/seed.mjs` 注释，DB 重建后重种。
- `m-new-new-1/6` 首次运行失败（lead 记账错误），JSON 保留为 `.first-attempt` 后用干净 lead 重跑。
