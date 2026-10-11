# AG06-AI ap-20261011 证据包：HUI-1682 + HUI-1688

- 执行者：AG06-AI（实施）。原票：HUI-1682（FEAT-0183）、HUI-1688（FEAT-0189）。
- 仓：`https://github.com/bianjiefilm/leads-engine`。独立树：`/Volumes/MobileHD/Work/派诺生态/_worktrees/20261011-agg/ag06-ai`。
- 分支：`codex/20261011-agg-ag06-ai`。基线：`origin/main = 0d86be4780c808505545fbb491569933f74522b2`（PR #53 合并点，fetch 后实测一致）。
- Go：go1.26.3 darwin/arm64。构建纪律：他人 `go.work` 位于父目录（`_worktrees/20261011-agg/go.work`），全部命令加 `GOWORK=off`，未改动他人文件。

## 一、测试基线与最终回归（PASS）

基线命令（0d86be4，改动前）：
```
cd /Volumes/MobileHD/Work/派诺生态/_worktrees/20261011-agg/ag06-ai/server && GOWORK=off go test ./...
```
基线结果（逐字）：26 个含测试包全部 `ok`，2 个无测试包（`cmd/leads-server`、`internal/provision`）。代表性耗时：httpapi 22.966s、store 10.990s、lightcopy 7.261s、platformtask 6.913s、reception 6.772s。

最终回归（HEAD 见 git log，`1bc2b64` 前）：同一命令三连跑，均 `26 ok / 0 FAIL`（/tmp/ag06-full-175219.log 为其中一次完整输出）。无包回退。

## 二、逐工作项结果

### 工作项 1：普查（PASS）
允许文件窗口实测：`server/internal/lightcopy/{decide.go,decide_test.go}`、`server/internal/platformtask/{client.go,types.go,client_test.go}`、`server/internal/httpapi/{handlers_light_copy.go,light_copy_test.go,server.go,server_test.go,reception_test.go(未改)}`、`server/internal/config/{config.go,config_test.go}`、`server/internal/store/light_copy.go`、`server/internal/db/migrations/0029_*.sql(新)`、`deploy/leads-server.env.example`、docs。未触碰 web/、crafthandoff、Today/workbench/详情页、他人 worktree。

main 现状（0d86be4）：
- `handlers_light_copy.go:49-56` `handleLightCapability` 硬编码 `model_ready:false`。
- `handlers_light_copy.go:158` `handleLightGenerate` 固定传 `lightcopy.ModelOutput{}` → 恒 409 `model_unavailable`。
- `platformtask/client.go` 仅有 `Phrase`（hold 0.01、SUCCEEDED 记 1 分），`taskSummary` 不解析 `result_json`；`Phraser` 接口不回文本。
- `server.go:121` 三键（PLATFORM_TASK_BASE_URL/TOKEN/ACCOUNT_ID）齐时装配 `ReceptionPhraser`。

### 工作项 2：R15a①② TDD（PASS，commits 0f4560f / 955d149 / 8707ecf）
先红后绿，红态输出摘录：
- `internal/platformtask/client_test.go:144: c.Generate undefined` → 实现后 `ok github.com/.../internal/platformtask`。
- `internal/lightcopy/decide_test.go: undefined: OriginModel / undefined: Prompt` → 实现后 `ok`。
- `internal/httpapi` 新增 6 个用例红（capability model_ready=false、generate 409 model_unavailable）→ 实现后全绿。

落地内容：
1. `platformtask.Generate`：沿 Phrase 计费语义（hold 0.01 CNY、提交、15ms 轮询、SUCCEEDED 才记 1 分）；`result_json` 防御性解析 `{"text":...}`；FAILED/CANCELED/超时/5xx → `not_completed`（保任务号）；SUCCEEDED 但文本空/不可解析 → 如实 `recorded` 1 分 + 空 Text（真实扣费不洗白）。capability 默认 `text.generate`，env `PLATFORM_TASK_TEXT_CAPABILITY` 覆盖；`PLATFORM_TASK_TEXT_MODEL` 可选。
2. `lightcopy`：`Generate` 真实模型产出时 `Origin=model、Generated=true、LiveCharge=CostCents`、版本+1、清确认、不发送；`Confirm` 保留来源与账务（不再抹零）；人工 `SaveManual` 重置为 manual/0；新增 `Prompt(kind,language,packet)` 只含允许清单事实、排序确定、空 packet 不造数。
3. `httpapi`：`Server.LightCopyPhraser`（platformtask.TextGenerator），`Open()` 与 ReceptionPhraser 同源装配；`handleLightCapability` `model_ready = LightCopyPhraser != nil`（配置在→true，并带 `provider:"platformtask:<capability>"`）；`handleLightGenerate` 全链：seam 缺席→409 model_unavailable（原语义）；事实闸门在计量前（越权/空选择 400 且零调用）；`not_completed`→409 `generation_not_completed`；recorded 空文本→409 `model_output_unusable`（回执如实 recorded）；占位→409 `placeholder_refused`（已记账如实保留）；成功→200 `{draft, model}`。
4. `store`+migration 0029：`light_copy_drafts` 表重建，`origin IN ('manual','model')`、`generated IN (0,1)`、`live_charge>=0`；`sent/published/marketing_permitted` 仍锁死 CHECK=0；旧行原样迁移。DB 不变量测试：`sent/published/marketing_permitted` 恒 0；`generated=1` 必须伴随 `origin='model' AND live_charge>0`。

### 工作项 3：20 问 journey + 负例（PASS，commit 7aa83df）
新文件 `internal/httpapi/reception_journey_test.go`。stand-in 平台任务 = httptest（`standInTask`），按幂等键脚本化 正常/慢速(150ms sleep vs 60ms client timeout)/5xx/卡死 RUNNING 四态；同一 stand-in 供 `reception.phrase` 与 `text.generate`。

`TestReceptionTwentyQuestionJourney`：20 个访客问题贯穿：
- q01 FAQ 带来源发送（citation source_id+version、epoch=1、model recorded 1 分、usage 1/1/0）；q02 未知澄清；q03 退款写入拒绝（unapproved_tool）；q04 有效价格事实发送；q05 过期价格拒绝（fact_expired 不出现数字）；q06 缺失事实（fact_missing）；q07 超时事实（fact_timeout）；q08 注入仍按 FAQ 数据回答 + untrusted_input；q09 撤回知识不再命中；q10 人设不进正文；
- assist（epoch2）q11-q13 草稿访客不可见、批准→显式发送→幂等重发 duplicate；q12 有依据草稿计量 recorded；
- takeover（epoch3）旧 epoch 二次接管 409 takeover_lost；q14 人工模式消息零模型调用 + interruption 事件；人工稿幂等/冲突保护（reply_conflict）；双租户迟到负例（B 租户读/批/发全 404）；
- release（epoch4）旧 epoch 人工已批未发送 409 send_blocked；q16 FAQ 再发送（epoch=4）；q17 未知；q18 有效事实；
- 授权线索（purpose/allow_contact/姓名/电话/告知版本）→ lead_id，请求 `marketing_allowed:true` 落库仍 0（contact_consents 实查）；follow-up 201；
- close 后 q19/q20 消息 409 session_closed、lead 409、assist 409；
- 展示面：批准未发送当前 epoch 草稿 text/audio 可交付；接管后旧 epoch 草稿消失且发送 409；已发送历史不再出现在可交付面；事件含 takeover@3/release@4；全程无租户 B 正文泄漏。
- 账务收口：journey 全程 stand-in（测试环境行为）；recorded 必须 task_id 非空且 cost=1，非 recorded 必须 cost=0；无 PASS 字样落库；接待 journey 未消耗任何 text.generate。

`TestReceptionStandInProviderFailureModes`：慢速/5xx/卡死三态计量全部 `not_completed` 0 分、FAQ 照常发出；卡死 text.generate → 409 `generation_not_completed`、不写草稿。
`TestReceptionPhraseWindowTakeoverInvalidatesReply`：计量窗口内接管（stand-in GET 挂起 + 真实 HTTP）→ 迟到回复 blocked、访客不可见、展示面无 text/audio、model_calls 恰 1 行。

### 工作项 4：R15b 真实能力普查（全部真实模型测项 NOT_RUN）

普查命令与结果（"凡声称不可达必附确切检索命令"）：

1. public-ai 源 provider 清单（树 `_worktrees/20261011-agg/public-ai-matrixdraft`，只读）：
   - `ls internal/task/ | grep -E "^source_(modelxing|shuhai|http|execution)"` → 仅 modelxing/shuhai/http/execution（视频/数字人/执行面）。
   - `grep -rhoE 'SourceProviderName = "[a-z0-9-]+"' internal/task/*.go | sort -u` → 唯一 `modelxing-video-v1`。
   - `grep -rn "reception.phrase\|text.generate\|text\.source\|textsource" internal --include='*.go' | grep -v _test` → 无 text source provider；`text.generate` 仅出现在 public-ai 仓 `public-ai/internal/task/provider_xingmo.go` 的 task-provider capability 路径表（task provider，非 source provider 契约，需部署侧 `XINGMO_MODE=maas` + maas 密钥才产文本）。
   - 结论与 HUI-2295 preflight 一致：public-ai 无统一 text source provider；`reception.phrase` 无文本产出上游（#53 本地实证：预占 0.01 后 FAILED、`billing_verdict=not_completed`）。
2. 工作区凭证普查（只看键名，未打印任何值）：
   - `grep -oE '^[A-Za-z_]...' modelxing.env` → MODELXING_BASE/MODELXING_API_KEY（ModelXing 视频/他道，非 platform-task 文本凭证）。
   - `credentials-modelxing-glm.env` → 同上两键（perf-evaluator 车道产物）。
   - `GoBoost/GoBoost/.env` → ARK_*/MX_*/MODEL_LLM_* 等（GoBoost 自有车道，不跨用）。
   - `HUI-2295 private/env/product_server.env` → 有 PLATFORM_TASK_BASE_URL、PLATFORM_TASK_TOKEN，**无 PLATFORM_TASK_ACCOUNT_ID**。
   - `grep -rln "PLATFORM_TASK_ACCOUNT_ID" /Volumes/MobileHD/Work/派诺生态 --include="*.env"` → **零文件命中**。
   - `grep -rln "XINGMO_BASE_URL" <GoBoost|_worktrees/orchestrator|HUI-2295 caches|AICut> --include="*.env"` → **零文件命中**（全卷扫描因外置卷过大超时后按目录限定重跑，结果一致）。
   - `platformtask.New` 三键缺一即 nil：工作区不存在可配置齐的 leads→platform-task 凭据组合；亦无任何指向 maas 文本上游的 XINGMO 部署凭证。
3. 判定：**真实模型 E2E NOT_RUN**。解除条件：public-ai 车道交付统一 text source provider（契约见下）并部署后，配置 `PLATFORM_TASK_BASE_URL/TOKEN/ACCOUNT_ID`（+可选 `PLATFORM_TASK_TEXT_CAPABILITY/PLATFORM_TASK_TEXT_MODEL`）即可用本轮已接好的消费端直接跑真实 20 问答。
4. 本轮无任何真实模型调用、无真实费用：全链 stand-in；账务行均为测试环境行为（测试内断言 + 注释标明）。

### text source provider 契约需求（交 Root 安排 public-ai 车道；本仓不改 public-ai）

仿 `modelxing-video-v1`（public-ai 仓 `public-ai/internal/task/source_modelxing_video.go`：Name/Validate/Start/Query、AsyncReceipt{ReceiptVersion,Executor,JobRef,RequestFingerprint}、观察接纳与指纹校验、无上游幂等键时 async-face only）：

1. Provider 名与能力：如 `leads-text-v1`，capability `text.generate`（与 leads 默认一致，或由 `PLATFORM_TASK_TEXT_CAPABILITY` 指定）。
2. 提交参数（`params` 白名单）：`language`、`prompt`（或 `messages`）、可选 `model`、`temperature`、`max_tokens`；未知字段必须丢弃不透传。
3. 结果语义：SUCCEEDED 时 `result_json` 必须是 JSON 字符串 `{"text": "<正文>"}`；空文本/占位文本视为失败（不得以 SUCCEEDED+空文本计费）。
4. 计费语义：沿既有 hold/settle；hold 金额与结算金额分开回报；FAILED/CANCELED 必须释放 hold；部分产出不得按成功结算。
5. 幂等：接受 `idempotency_key`（leads 传 `lightcopy:<subject_kind>:<subject_id>:<kind>:v<version>:<selected>`），重放返回原任务。
6. 越权防护：params 不得携带访客全文/联系人/画像；provider 侧拒绝包含凭据样式的输入。
7. 观测：GET 任务回执含 `result_json`（leads 已按此消费）+ 明确 `error_code`（如 `no_model_upstream`），供 not_completed 判定与 reconcile。

## 三、测项状态总表

| 测项 | 状态 | 证据 |
|---|---|---|
| model_ready 配置驱动（无三键=false） | PASS | TestLightCapabilityReportsConfiguredSeam / OpenWiredStandInTask |
| model_ready（三键配置=true + provider 标识） | PASS | 同上 |
| Generate 真实调用路径（提交/hold/轮询/结算） | PASS（stand-in） | platformtask client_test + TestLightGenerateThroughOpenWiredStandInTask |
| not_completed/错误/超时不记账不写文 | PASS | TestLightGenerateHonestFailures + FailureModes |
| recorded 空文本如实记 1 分不写文 | PASS | TestLightGenerateHonestFailures + platformtask TestGenerateSucceededWithoutUsableTextKeepsHonestCharge |
| 占位正文拒绝且保留真实账务 | PASS | 同上 |
| 事实闸门先于计量 | PASS | TestLightGenerateFactGateRunsBeforeTheMeter |
| 生成来源/账务落库、人工改稿重置、确认保留 | PASS | TestLightGenerateRecordsModelProvenanceAndCharge + migration 0029 |
| 发送/发布/营销许可恒 0 | PASS | 旧不变量测试 + 新 DB 断言 |
| 20 问 journey（assist/approve/takeover/release/close） | PASS | TestReceptionTwentyQuestionJourney |
| 双租户/旧 epoch/计量窗口迟到负例 | PASS | 同上 + TestReceptionPhraseWindowTakeoverInvalidatesReply |
| 人工权：模型只拟稿、人工稿不被后台覆盖、关闭后拒发 | PASS | journey + 既有 reception 测试未回退 |
| 真实平台任务 text E2E（真实 20 问答+真实费用） | NOT_RUN | 见上普查与解除条件 |
| 真实账单/供应商结算对账 | NOT_RUN | 依赖上一项 |
| crafthandoff「真实模型未完成」覆盖移除 | NOT_RUN（按 R15e 保留原样） | 未触碰 crafthandoff |
| web 侧轻文案接线 | UNSUPPORTED（本轮窗口：capability 响应向后兼容加字段，web 不动） | 文件窗口清单 |

## 四、账务行（全部测试环境）

- 本轮零真实供应商调用、零真实费用。所有 `reception_model_calls`/`light_copy_drafts.live_charge` 写入均发生在 go test 的临时 SQLite（t.TempDir）中，属测试行为；journey/失败态测试内含"recorded⇒task_id 非空且 1 分；not_completed⇒0 分；PASS 字样禁止入库"断言。
- UNKNOWN/not_completed 未洗白：`generation_not_completed`、`model_output_unusable`、`blocked` 状态全部如实返回与落库。

## 五、commits 与 HEAD

- 0294e85 docs(ag06-ai): HUI-1682/1688 spec+plan with Root R15a-R15e rulings
- 0f4560f feat(platformtask): HUI-1682 text generation call path ...
- 955d149 feat(lightcopy): model provenance on Generate, Confirm preserves charge, allow-listed Prompt
- 8707ecf feat(httpapi): HUI-1682 model_ready from configured task seam ... migration 0029 ...
- 7aa83df test(httpapi): HUI-1688 twenty-question reception journey ...
- （后续 commit 见 git log；最终 HEAD 以 PR 描述为准）
