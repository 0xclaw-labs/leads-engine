# 未选择工作范围时的当前成员发现

`GET /api/v1/session/memberships`，浏览器沿原 catch-all BFF 请求 `/api/session/memberships`。不需要 `X-Tenant-ID`，不接客户端 principal。仍要求原 BFF internal credential、原配置门、原 Identity app-scoped 会话解析；与业务 `requireSession` 共用同一身份链。没有新账号、默认租户或 owner。

成功响应只含当前真实主体可用的租户 id/name：

```json
{"items":[{"tenant_id":"tnt_actual_a","display_name":"原组织甲"},{"tenant_id":"tnt_actual_b","display_name":"原组织乙"}]}
```

列表从原 `members` 与 `tenants` 读取，按原 `authz.Authorize(ActionReadList)` 过滤停用/非法成员；agent 必须有该租户当前 `agent_grants`，撤销后即时消失。当前 tenant 模型只有 id/name/created_at，没有 enabled/lifecycle 字段；本接口没有发明额外租户状态权威。渠道 operator 身份不是 CRM 成员，不能由它生成 CRM 范围。

- 已认证但没有当前可用成员：200 `{"items":[]}`，不泄露其他主体租户。
- 缺失、失效、错误 audience 会话：401。
- Identity 不可达或原配置不能核验：503，保持原错误类型。
- 成员或代理授权权威读取失败：503 `memberships_unavailable`，没有 `items`，不把故障改为空列表。
- 客户端伪造 `X-Tenant-ID`、principal header/query 不改变发现主体。

成功响应 `Cache-Control: no-store`。排序仅稳定呈现，不代表默认选择。零范围提示无可用范围；多个范围必须用户明确选择；单一范围可以进入原 whoami 确认。无论如何，选后仍向原 whoami/业务 API 传选定 tenant，原成员、代理授权、assignee/记录权限继续逐次裁决。接口没有“selected/default”字段，也不写选择或授权。

Go 定向回归 `TestSessionMemberships*` 覆盖无会话、无权/停用、多范围、伪造输入、代理授权撤销与权威故障。`web/tests/session-memberships-bff.test.ts` 使用实际 loopback HTTP server 验证原 BFF 在无 tenant 时原样转发 canonical cookie/专用 internal token、不给请求自报 principal 透传权威，并保持200/401/503状态。原 BFF源代码无需特例或新代理分支。
