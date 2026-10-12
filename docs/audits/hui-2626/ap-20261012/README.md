# HUI-2626 独立盲评判决项修复复录（2026-10-12）

- 基线：`origin/main`（含 #53–#57），工作树 `/Volumes/MobileHD/Work/派诺生态/_worktrees/20261011-agg/ag06-2626fix`，分支 `codex/20261012-ag06-2626fix`
- 盲评原判：评审签名 HEAD `4f10efc4`（证据 `../ap-20261011/`）
- 与原走查的差异（Root 裁定）：**截图全部改视口模式**（fullPage 是采集伪影，非产品缺陷）；其余路径、环境注入与判定口径与 ap-20261011 完全一致

## 1. 链与环境（全 synthetic，secret 走 env 文件 chmod600 不入仓）

| 组件 | 状态 |
| --- | --- |
| Go leads-server（本工作树构建，`GOWORK=off`，:29300） | 真实，`config_issues:0`，`light_copy=on`、`campaign_motion=off`（与 ap-20261011 相同） |
| web BFF（`next dev -p 29330`，本工作树） | 真实 |
| identity / record 替身（:29310 / :29320，`harness/`） | 协议兼容替身，token 环境注入 |
| 浏览器 | 系统 Chrome headless，会话 cookie 与 `leads_tenant_id` 注入 |

租户 A `tnt_65e55d7c9b3378fe488ab538c57748ce`（店主甲）、B `tnt_6c6f6d27e5f5978973cea530113122b9`（店主乙），同一 owner `usr_owner_walk`。Touch 源 `touch_t_a`/`touch_t_b` 绑定不变。六条线索全部本会话 `ingest.mjs` 注入（跨任务不同手机号）。

## 2. 盲评判决项 → 修复对照

| 盲评项 | 修复 | 测试/证据 |
| --- | --- | --- |
| **P1** 390 顶部导航逐字竖排（碰/一/碰、接/单）、租户/状态 chips 断行碎片、邮箱截断 | `eco-top-nav.module.css`：`.quiet`/`.badge` 全宽 nowrap；≤430 媒体查询顶栏换行收纳（bar 放开固定高、隐藏 spacer、账号区靠右、apps/context 可换行）；额度徽标补 badge 类。触控 44px 规则保留，1440 布局零改动 | `web/tests/hui2626.evalfix.test.ts`（P1 四断言）；截图 `walk390-01/02`：导航横排、chips 完整、无逐字竖排；`walk1440-*` 布局不变 |
| **P2①** 空态图标 404/空白灰块；walk tally 5 条 console 404 | ① `SurfaceState` 空/错/恢复态内联 SVG 图标（零外链，无 404 路径）；② `web/src/app/icon.svg` 自带应用图标（favicon 不再 404）；③ capability 发现改为始终可达 `200 + enabled:false`（fail-closed 不变：关闭时读写路由仍不挂载），面板据此隐藏；④ 轻文案只在线索带活动出处时挂载，普通线索不再打必 404 探测 | Go：`TestLightCopyFeatureOffIsInvisible`/`TestCampaignMotionOffIsInvisible` 新契约（200+enabled:false，写路径仍 404）；web：evalfix P2① 五断言；**复录 4 条 walk tally 全部 `consoleErrorCount:0`（原 5 条 404 清零）**；空态图标见 `walk390-10-empty-icon-view.png` |
| **P2②** 左下浮动助手头像（N）压叠正文（390 压时间线首行，1440 贴 sidebar 边） | 该头像为 Next dev 指示器（走查链全程 `next dev`）；`next.config.mjs` 显式 `devIndicators: false`（生产构建本就不渲染） | evalfix P2② 断言；复录 1440/390 全部截图左下无浮动头像 |
| **P2③** 时间线裸露工程标识符（cmp_1、sub_walk_b1、form_authorization、channel_reply、marketing_phone/sms、activity_ref）与微秒 ISO 时间戳 | 新增显示层人化 lib `web/src/lib/humanize.ts`：已知授权项/类别词映射、工程引用段退场、ISO → 本地秒级；时间线/来源事实/时间事实全部接线；轻文案事实复选框改中文标签（`factLabel`）。**原始值保留在接口与数据层不删** | evalfix P2③ 断言 17 条；截图 `walk1440-08-timeline-view.png`/`walk390-08`：`来源 · 碰一碰`、`授权 · 表单授权`、`授权 · 渠道内回复授权`、`2026-10-12 08:11:49`，无 cmp_1/sub_walk/form_authorization/微秒串 |
| **P3** 『授权 · 授权 form_authorization』同词重复 | `eventLine` 摘要以类别词开头时去重（见上） | 同上；截图时间线行 |
| **P3** 『这条线索』占位标题 | `leadTitle(source)`：按来源人化（碰一碰线索/表单线索/线索档案） | evalfix 断言；截图 `walk390-02` 标题「碰一碰线索」 |
| **P3** 390 页脚链接从词中间折连排 | `.foot-nav a { inline-block; nowrap }`，链接整体换行 | evalfix 断言；截图 `walk390-09-footer-view.png`：「订阅与用量」「租户隔离与导出」整体换行，不再拆词 |

**诚实披露保留（Root 裁定）：** 「真实模型未完成」「生成会失败并说明原因，不会用模板占位」「生成完成不等于已发送/已发布/已取得营销许可」等全部原样保留（截图 `walk390-02` 建议行、`walk1440-08` 建议列）。真实模型落地后自动消失。

## 3. 复录结果（视口截图）

### 3.1 主路径 walk（owner，今日 → 详情 → 分配 → 记跟进+下一次 → 今日 → 刷新 → 重登详情）

| 视口 | 租户 | 线索 | 结果 | 计数 | console |
| --- | --- | --- | --- | --- | --- |
| 1440×900 | A | `lead_a5cf2349975512fbe39ba667d63519c7`（访客甲一） | PASS：7 步全成；重登后已分配、分配块不再出现 | 7 点击 / 2 填写 / 1 选择 | **0 错误 / 0 404** |
| 390×844 | A | `lead_84fe6c80528bcc6db3bc811d3ea03c7c`（访客甲二） | PASS：同上 | 7 / 2 / 1 | **0 / 0** |
| 1440×900 | B | `lead_55713590a3bc2fffe92ba95d7b207251`（访客乙一） | PASS：同上 | 7 / 2 / 1 | **0 / 0** |
| 390×844 | B | `lead_b2d0469165d3c3fb73d6e60a9cba1c1d`（访客乙二） | PASS：同上 | 7 / 2 / 1 | **0 / 0** |

截图：`walk1440-01..07`、`walk390-01..07`、`walkB1440-01..07`、`walkB390-01..07`（视口模式）+ 步骤 JSON 同目录；`walk1440-08`/`walk390-08-timeline-view` 为时间线卡片特写；`walk390-09-footer-view` 为 390 页脚整体换行特写；`walk390-10-empty-icon-view` 为空态内联图标特写（脚本同时断言 `data-state-mark="empty"` 内存在 `svg`）。

### 3.2 键盘与触控（与 ap-20261011 口径一致，不回退）

| 项 | 结果 |
| --- | --- |
| 键盘（1440，A 线索 `lead_20254dff9712002c6cc69cfa1a6e0505`） | PASS：Tab×14 到分配触发器 → Enter 开下拉 → Esc 关且焦点返回 → 方向键+Enter 选中「店主甲（店主）」→ Tab×1 到「分配负责人」→ Enter 提交 → `role=status` 确认。`kbd1440-k01..k03.png`、`kbd1440-keyboard.json` |
| 触控目标（390，A 线索 `lead_4aaf1f78fe64cecf098f319c92fad439`） | 5/5 ≥44px（48px 口径 2/5，两按钮正好 44），与基线逐值一致。`touch390.png`、`touch390.json` |

### 3.3 回归（修复后）

| 命令 | 结果 |
| --- | --- |
| `cd web && npx vitest run` | PASS：42 文件 / 294 测试（基线 41/273 不回退，新增 evalfix 21 条） |
| `cd web && npx tsc --noEmit` | 3 个既有基线错误（`tests/channelInteraction.test.ts`、`tests/pageMap.test.ts`），本次改动文件零新增 |
| `cd server && GOWORK=off go test ./... -count=1` | PASS：26 个含测试包 `ok`，退出 0 |

## 4. 复跑

1. 生成 env（六值 + 租户 id）：`LEADS_WALK_TOUCH_SECRET/FETCH_TOKEN/INTERNAL_TOKEN/IDENTITY_TOKEN/PEPPER/DEDUP`，文件在 scratchpad，`chmod 600`。
2. `leads-server provision-tenant/-member` 建两租户与 owner（成员显示名店主甲/店主乙），写回 env。
3. `harness/start-go.sh`（:29300）→ `node harness/identity-standin.mjs`（:29310）→ `LEADS_WALK_FETCH_TOKEN=… node harness/recordsrc.mjs`（:29320）→ 本工作树 `web/` 下 `LEADS_SERVER_URL=http://127.0.0.1:29300 LEADS_INTERNAL_TOKEN=… npx next dev -p 29330`。
4. `node harness/ingest.mjs <touch_t_a|touch_t_b> <ref> <name> <phone> <wechat>` 注入线索（手机号 11 位且跨任务不得同号）。
5. `WALK_OUT=<screens> MEMBER_MATCH=店主甲 node harness/walk.mjs <tenant> <w> <h> <lead_id> <tag> <note> <visitor>`；`keyboard.mjs`、`touch.mjs`、`timeline-shot.mjs` 同目录。

脚本依赖 scratchpad 的 playwright-core（未入仓依赖），不属于产品依赖。

## 5. 观察与剩余

- **O1（遗留，未改）**：时间线 owner 视图仍显示明文联系方式（`联系方式 13910001001`）。归属权限设计复核，超出本次盲评判决项。
- 页脚链接与空态图标在首屏之外；特写见 `walk390-09` / `walk390-10`。
- 「授权 · 撤销短信营销授权」类撤销语句为 `eventLine` 撤销重写，数据层原始串不变。
- 复盲评：待 Root 派非作者评审。
