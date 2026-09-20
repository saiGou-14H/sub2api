# Codex turn-state 采集与注入（实验性）

开发基线：`saiGou-14H/sub2api main@b78dd86a4ecc74a1291d14efb30c6a5a59c4de5e`，2026-09-20 从 origin/main 新建独立分支 `feat/codex-turn-state-main-20260920`。工作区 `/root/project-development/sub2api-codex-turn-state-main-20260920`。本轮不推送，不部署生产。

参考设计：`/root/dsh/reports/SUB2API_CODEX_TICKET_DEVELOPMENT_ARCHITECTURE.zh-CN.md`。复用并逐项验证旧本地分支 `feat/codex-turn-state@0eea17694`，未修改旧工作区。参考项目：[gylive/ccodex-sleep-state](https://github.com/gylive/ccodex-sleep-state/tree/b18fabf9ad8e9d7af7d9d0306b623ba6091a39d6)，没有整包引入其代理核心、配置接管器或代码依赖。

## 开启方式

1. 在现有代理管理中录入采集代理。
2. 在管理员设置的 Codex turn-state 区域选择代理，保存并启用全局配置。
3. 对需要参与的 OpenAI Codex OAuth/setup-token 账号显式开启 turn-state。新建、编辑和批量编辑均有开关；批量操作默认不改动。
4. 核对配置期望版本/已应用版本、代理状态、正在采集及就绪数量、失败分类。保存成功和已生效是两个独立状态。

全局与每账号均默认关闭，必须同时开启。只有 `extra.codex_turn_state_enabled` 的布尔值 `true` 表示账号启用。Web、Prism、API-key、shadow、agent-identity 不适用，不能用相同模型名绕过协议限制。采集代理只控制探测出口，业务请求继续使用账号原有 `proxy_id`；不改动业务认证、计费或代理。

开关启用会产生额外真实模型请求，可能消耗上游额度。采集代理缺失、停用、过期或不支持时，不回退直连或环境代理。

## 效果边界

`X-Codex-Turn-State` 是不透明状态。官方约定主要用于同 turn 回带；按账号/模型跨轮复用是实验策略。292/332 是字符数，不是质量、权限或套餐证明。目标长度和 TTL 是本地筛选与缓存期限，不是上游承诺；不解密、不验证上游签名，不保证改善质量、解除限流或减少过载。

与参考项目的取舍：不接管客户端配置、不导入代理订阅、不自动轮换 IP、不重放正式生成、不从 token 长度判断套餐。以 sub2api 的账号仓库、已录入代理、设置持久化、Redis 和原转发链路为基础。

默认模型为 `gpt-6-astra`、`gpt-5.6-sol`，按最终上游模型精确匹配，可配置；模型是否可调用取决于上游账号权限，不隐式映射成另一个模型。

## 配置与缓存

默认目标长度292、TTL3600秒、提前刷新600秒、缺票 `passthrough`、2个worker、全功能12次/分钟探测预算。模型1-16个、目标长度64-4096、TTL60-3600秒、提前刷新必须小于TTL、worker1-8、预算1-60次/分钟。

- 配置保存到 settings 专用 JSON 行，revision 为十进制字符串，PUT 使用 `expected_revision` 乐观锁。
- 配置与所选代理更新遵循同一事务锁序；代理身份、状态、有效期变更提升配置版本，改名不提升。被选择的代理即使全局关闭也不能直接删除，先取消选择或换选。
- 原始 state 只存在内部内存/独立 Redis TTL 缓存，不进入账号 Extra、CRUD DTO、导出、状态接口或普通日志。
- 票据按配置版本、账号ID、稳定上游身份摘要及最终模型隔离；普通 token 刷新不清除稳定身份，相同账号记录更换身份使旧票不匹配。
- Redis owner 租约15秒、5秒续租；数据库控制快照每2秒刷新、最多新鲜6秒。查库失败不能给旧快照续期，旧任务不能跨版本写回。
- 队列32，账号分页100，固定worker，单任务最多30秒；Redis共享预算不因换模型、配置版本或leader重启而重新计算当前分钟次数。
- 探测必须返回 HTTP 200 且有效 SSE `response.completed`，错误、截断、EOF、单独 `[DONE]` 都不能当成功。帧、正文、响应头均有上限。

功能关闭、配置切换、代理失效、控制快照过期或租约丢失会取消当前采集任务。全局跨实例生效有最多约6秒控制窗口；已经发出的业务请求不被撤回。

每个执行worker最多持有一个账号监视任务（最多8个，队列里的任务不创建监视器）。每2秒重读账号，查询期限2秒；关闭、删除、身份变化、变为不支持/不可调度或数据库检查失败时取消该任务并join。只有普通取消、不含明确上游拒绝时，不记失败退避或共享冷却。正常传播目标约2–4秒加调度余量，30秒单任务期限保留作兜底；短暂关后重开可能落在轮询间隙，不能承诺撤回上游已完成的计算。

采集认证/限流冷却按账号ID与稳定身份共享，不含模型、代理或配置版本；429尊重Retry-After，401/403及SSE明确配额错误至少冷却1小时。并发更新只延长不缩短，晚到成功不清除。实际已观测拒绝会在取消早退之前用独立2秒收尾写入，不因配置代际或账号暂时关闭而被丢弃；票据和普通失败仍受generation围栏与账号重新检查保护。Redis暂时写失败会保留有界本地待写限制，在任何新采集前先尝试全部发布，失败则暂停新采集。有限冷却过期后可重新采集，身份变化使用新隔离范围。它只约束后台采集，不更改正常业务认证和计费。

专用采集transport强制HTTP/1.1、禁止keep-alive和重定向，与业务连接池隔离；支持已录入的http/https/socks5/socks5h代理。每次新连接不保证出口IP变化。

## 转发规则

在现有 turn-state echo guard 之后决策：不适用/关闭/模型不匹配时不干预；有合法缓存时只覆盖本次出站状态头；缺票 `passthrough` 时保留原行为，`reject` 时返回本功能503且不惩罚账号。换号后重新按新账号、身份和最终模型决策。

旧 `/responses/compact` 及普通 Responses body 内带 `input[].type=compaction_trigger` 的V2远程压缩都跳过实验注入和缺票门禁，保留原echo guard；下一轮正常生成继续匹配策略。专用计数端点不参与。普通转发按账号映射后的最终模型决策；passthrough遵守main不重写普通账号映射的规则，按实际保留的出站模型决策。

HTTP Responses（含透传及 Chat/Messages 转换）复用现有 builder。WebSocket 在功能开启时使用既有 HTTP bridge 逐轮执行策略；已经建立的原生 WS 若下一轮启用，则在转发该帧前明确要求重连，关闭码1008，不把已发送请求重放。关闭功能后的已有HTTP桥可继续至连接结束。

## 管理 API

均使用现有管理员鉴权：

- `GET /api/v1/admin/settings/openai-codex-ticket`
- `PUT /api/v1/admin/settings/openai-codex-ticket`
- `GET /api/v1/admin/settings/openai-codex-ticket/status`

GET/PUT 返回 `settings`、脱敏 `selected_proxy`、`runtime`、`runtime_error_reason`。PUT数据库成功但运行状态不可用仍表示“配置保存成功、运行状态未知”；独立status不可用返回503。旧revision PUT返回409 `CODEX_TICKET_REVISION_CONFLICT`。前端不会用旧草稿自动覆盖其他管理员保存的配置。

状态 `counter_scope=local_instance_observed` 表示本实例观察数量，不冒充全局集群健康。有限错误分类与本地化文案不含上游自由错误文本、认证值或票据。

## 开发验证

本轮只进行本地模拟、临时数据库/缓存、构建和回归。任何真实账号采集或生产发布都需要另行明确执行；程序测试通过不能证明实验策略有效。完整测试命令、结果、提交及已知限制记录在 `CODEX_TURN_STATE_VALIDATION.md`。
