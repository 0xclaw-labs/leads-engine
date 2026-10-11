# HUI-2598 度量原始汇总（R5 协议，2026-10-11 续跑）

协议：旧基线 b58ea17（独立 worktree /Volumes/MobileHD/Work/派诺生态/_worktrees/20261011-agg/leads-2598-baseline），新=分支 HEAD（35e77b9 的树）。
每格 5 轮清洁计时 + 第 6 轮 503 故障恢复。主动时间=点击/填写动作耗时之和；等待时间=总时长-主动时间（页面加载、渲染、网络、脚本定位）。
理想脚本操作者：误操作（missteps）清洁轮为 0；不声称改善百分比。

| 版本 | 任务 | 清洁轮中位总时长 ms | 中位主动 ms | 中位等待 ms | 步数 | 输入 | 完成 | 故障轮恢复 |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| new | new | 1287 | 115 | 1174 | 2 | 2 | True | entry_available_input_kept;retried_ok |
| new | due | 1263 | 94 | 1167 | 2 | 2 | True | entry_available_input_kept;retried_ok |
| new | waiting | 1272 | 101 | 1172 | 2 | 2 | True | entry_available_input_kept;retried_ok |
| old | new | 1060 | 92 | 975 | 2 | 2 | True | entry_available_input_kept;retried_ok |
| old | due | 1069 | 79 | 979 | 2 | 2 | True | entry_available_input_kept;retried_ok |
| old | waiting | 1091 | 90 | 993 | 2 | 2 | True | entry_available_input_kept;retried_ok |

每轮原始 JSON：本目录 m-<版本>-<任务>-<轮>.json。m-new-new-1/6 的 .first-attempt 为脚本记账错误的首次运行（lead 被冒烟消费），保留备查。

任务映射（旧系统无 waiting_customer 处置与分配 UI）：
- 新咨询首次处理：旧=工作台 unprocessed 桶→查看新线索→记录+安排下一次；新=Today 新咨询组→详情→记跟进。旧版完成后线索仍停留 unprocessed（status 停留 new），新版完成即离开 Today（scheduled_next）。
- 到期跟进→安排下一步：旧=due_today 桶；新=Today overdue/due 组。
- 等待客户→恢复处理：旧映射为 needs_schedule 桶（in_progress 无未来下一步，种子需显式 PATCH status）；新=Today 等待客户组（disposition=waiting_customer）。
- 质性差异：未分配 Touch 线索在旧 UI 无任何分配入口（任务经 UI 不可完成），新 UI 详情页提供分配动作（见 walk1440/walk390 证据）。

理想操作者脚本下两版步数/输入相同（2/2）；新版中位总时长高约 0.2s（Today 卡片与草稿渲染更重）。2598 的 reduction 维持 not_claimed。
