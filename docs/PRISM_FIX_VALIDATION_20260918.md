# 9999 Prism 修复及在线验证记录

日期：2026-09-18。修复分支：`fix/prism-turn-state`，基于 `914295ea9`。仅本地提交，未推送，主分支与独立 Codex turn-state 功能分支没有合并或改动。

## 故障与修复

原错误为 `prism upstream /api/llm/response_with_tools_status: turn_state is required`。真实 start 已同步返回 `completed/error`，没有 turn_state；旧实现仍用空对象轮询，掩盖了原本的模型拒绝。修复优先处理 start/status 终态，仅对非终态有效对象继续轮询，保留不透明 JSON 状态，错误不暴露上游 debug/认证/sandbox 信息，也不重放已启动任务。

指定账号实测 `gpt-5.5`、旧 HAR 的 `gpt-6-astra` 均报 Unsupported assistant model。按用户明确选择，默认请求改为当前官方前端的 `gpt-5.6-sol` / `medium`。未修改账号模型映射，没有以其他模型冒充原模型。上游完成载荷不带可独立核验的模型身份；报告只证明发送的模型目标与成功结果。

额外修复了 Responses 简写消息规范化、完整纯文本历史随当前提示传送、标准输出空 annotations/logprobs 回灌，以及 SSE 转非流式 JSON 后的 Content-Type。

## 自动认证核对

账号 36 只有通用 OpenAI access token 等 OAuth 凭据，没有手填 Prism 会话令牌。后端把普通 access token 放入 `prism_oai_access_token` Cookie，调用 `/auth/session`，接收服务端 `Set-Cookie: prism_session_token`，后续自动使用并保存到私有状态。

HAR 中也观察到 `/auth/session` 更新此 Cookie，但 HAR 从已登录状态开始，不单靠它推断完整首次 OAuth 登录。此次真实账号成功补充证明无需手动双填。

界面主字段现为“OpenAI 访问令牌（access_token）”，说明与 Web 共用、自动获取 Prism 会话；手动会话令牌放在默认折叠的高级覆盖区。编辑不回显 secret，留空保留已有值，没有变更 OAuth 导入/刷新业务逻辑。

## 本地提交

| 提交 | 内容 |
| --- | --- |
| `951d06aaf` | 同步终态和 turn_state 处理 |
| `53785ada1` | 当前模型及 reasoning 默认值 |
| `f51d02144` | Responses 简写消息规范化 |
| `019054a83` | 自动认证界面及高级覆盖说明 |
| `403496acb` | 完整纯文本历史适配 |
| `b7d4785c4` | SSE 转 JSON 的 Content-Type |

## 部署与验证

- 9999 镜像：`local/sub2api:prism-fix-b7d4785c4`。
- 运行版本：`0.2.4-prism-fix.b7d4785c4`，二进制 Commit：`b7d4785c405e6bb517317db0abee04657c0c086a`。
- 最终容器启动：`2026-09-18T11:21:33.489486223Z`。
- 仅执行 9999 compose 的 `up -d --no-deps --pull never sub2api`。未启停 10000。
- service、handler、routes：`go test -p 1 -tags unit ./internal/service ./internal/handler ./internal/server/routes -run 'Prism|SSEToJSON|NonStreaming' -count=1 -timeout=180s` 通过。
- Create/Edit/i18n：119/119 测试通过；vue-tsc、变更文件 ESLint、生产构建通过。
- Linux embed 构建与 Docker 构建通过；部署二进制版本、前端实际 HTTP 资源字节及自动认证文案已核对。
- 两端 `/health` 均 HTTP 200，Docker health 均 healthy。
- 此次未重跑后端全量测试；以前的主版本全量结果不作为此次全量验证。

使用用户提供的下游 API key，实际请求 `http://66.92.18.39:9999/v1/responses`，`model=gpt-5.6-sol`、`store=false`：

| 轮次 | 请求与期望 | 结果 | 响应类型 | 耗时 |
| --- | --- | --- | --- | --- |
| 1 | 记住 731，仅回答 PRISM_OK | `completed`，精确 `PRISM_OK` | `text/event-stream`，含 response.completed | 13.46 秒 |
| 2 | 回灌首轮原始标准消息，问刚才的数字 | `completed`，精确 `731` | `application/json; charset=utf-8` | 18.76 秒 |

数据库使用记录确认这两轮均为指定账号 36，requested/model/upstream_model 均 `gpt-5.6-sol`；记录时间为 `19:21:48.168578+08:00`、`19:22:06.928457+08:00`。上游未返回 usage，保留 null。

## 仍存在的限制

- 只有 `previous_response_id` 和本轮增量消息的模式未通过内容验证：项目、conversation、sandbox 相同，上游 `codex_session_id` 却改变，回答没有记住上轮数字。此修复不宣称解决了上游游标恢复，当前纯文本客户端应逐轮携带完整历史。
- 文本历史适配不覆盖非文本内容、未知字段、非空注释或其他复杂 Responses 项目，这些保留原始结构，不承诺上下文连续性。
- 验证范围为 API 文本对话，不是完整 Codex CLI 自定义工具工作流。客户端工具声明仍需显式启用可选 prompt bridge；本次没有更改账号桥接开关，也未在线验收工具、上传、渲染或文件回写。
- 未对 10000 执行修改或启停。容器 ID 和镜像未变，最终健康；其 StartedAt 从先前基线变为 `2026-09-18T10:15:47.939948945Z`，早于此次 9999 发布，原因未查明。不能报告成整个调查期间从未重启；从 10:48 观察到最终核对保持相同。

## 输出与回滚

- 配置：`/root/sub2api-deploy-9999/codex-prism.config.toml`，model/review_model 为 `gpt-5.6-sol`，base_url 包含 `/v1`，不含 API key。
- 脱敏 API 结果：`/root/sub2api-deploy-9999/prism-validation-20260918.json`。
- Compose：`/root/sub2api-deploy-9999/docker-compose.yml`。
- 备份：`/root/sub2api-deploy-9999/backups/prism-fix-20260918/`；原 Compose 和数据库 dump 已保存，dump 列表检查通过。
- 本次没有数据库迁移或账号配置变更。回滚时将 9999 compose image 恢复为 `local/sub2api:saigou-main-914295ea9`，在该部署目录执行 `docker compose up -d --no-deps --pull never sub2api` 并检查健康。常规二进制回滚不需要恢复数据库。

临时诊断凭据、Cookie、sandbox 状态和原始上游捕获只在私有临时目录处理，收尾删除；不纳入提交。原始用户 HAR 保持不动。
