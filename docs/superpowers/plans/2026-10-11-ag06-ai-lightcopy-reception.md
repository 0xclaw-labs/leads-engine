# AG06-AI 2026-10-11 实施计划（HUI-1682 + HUI-1688）

Spec：`docs/superpowers/specs/2026-10-11-ag06-ai-lightcopy-reception.md`（含 Root R15a–R15e 裁定）。
每步一 commit；TDD 先红后绿；`GOWORK=off go test`。

## 步骤

1. **platformtask 生成路径（红→绿）**
   - 测试 `internal/platformtask/client_test.go`：stand-in httptest 平台任务——`text.generate` 提交带 hold 0.01、认证头、POST body 无访客敏感文本；QUEUED→SUCCEEDED+`result_json={"text":...}` 回 `{TaskID, Text, CostCents:1, recorded}`；FAILED→`not_completed` 带任务号；卡 RUNNING→ctx 超时 `not_completed`；5xx→`not_completed`；result_json 非文本→recorded 但 Text 空。
   - 实现：`taskSummary.ResultJSON`、`GenerateInput/GenerateResult/TextGenerator`、`Client.Generate`（沿 Phrase 模式）、`Client.TextCapability/TextModel` 默认 `text.generate`/""。

2. **lightcopy 生成决策（红→绿）**
   - 测试 `internal/lightcopy/decide_test.go`：`Generate` 成功置 `Generated=true/Origin=model/LiveCharge` 版本+1 需再确认；`ModelOutput.CostCents` 贯穿；`Prompt(kind, packet)` 只含允许事实、空 packet 不产生捏造数字；占位拒绝路径保持。
   - 实现：`OriginModel`、`ModelOutput.CostCents`、`Generate` 后置条件、`Prompt`。

3. **httpapi 装配与 Generate 端点（红→绿）**
   - 测试 `internal/httpapi/light_copy_test.go`（新增用例，不改旧断言语义）：三键缺席 capability `model_ready=false`；注入 stub generator 后 `model_ready=true`+`provider`；generate 走 stub：成功 200 草稿 `generated=true,origin=model,live_charge=1,content_version+1` + `model` 回执；`not_completed`→409 `generation_not_completed`+回执；recorded 空文本→409 `model_output_unusable`+回执 recorded；占位→409 `placeholder_refused`；facts 越权字段照旧拒绝；DB 不变量 `sent/published/marketing_permitted` 恒 0。
   - 实现：`Server.LightCopyPhraser`、`Open()` 装配、`handleLightCapability/handleLightGenerate` 改造、`config.TaskTextCapability/TaskTextModel`（env `PLATFORM_TASK_TEXT_CAPABILITY/PLATFORM_TASK_TEXT_MODEL`）。

4. **20 问 journey + 负例（单测试文件）**
   - 新文件 `internal/httpapi/reception_journey_test.go`：stand-in 任务服务器四态脚本；20 问覆盖清单见 spec；贯穿断言 reply.epoch/session_version/citations.source_id+version/notice_version；负例：双租户、旧 epoch 迟到、close 后 send、takeover 丢 epoch 409、人工稿不被覆盖、model call PASS 判拒、慢速/错误/卡死账务 `not_completed`。

5. **全量回归**：`GOWORK=off go test ./...` 26 包逐字对比基线。

6. **证据包 + R15b 契约文档**：`docs/audits/hui-1682+hui-1688/ap-20261011/`（README、命令与结果、账务行、text-source-provider 契约需求）。

7. **收口**：push、PR（描述含证据索引）、CI、Linear 两票各一条增量评论。

## 允许文件窗口

- `server/internal/platformtask/{client,types,client_test}.go`
- `server/internal/lightcopy/{decide,decide_test}.go`
- `server/internal/httpapi/{server.go,handlers_light_copy.go,light_copy_test.go,reception_journey_test.go(新)}`
- `server/internal/config/{config.go,config_test.go}`（仅新增两个 task text env）
- `docs/superpowers/`、`docs/audits/`
- 不碰：web/、Today/workbench/详情页、crafthandoff、`_worktrees` 他人树、根 checkout。
