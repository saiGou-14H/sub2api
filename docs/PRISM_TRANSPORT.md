# Prism 接入说明

Prism 是 OpenAI 账号的一种独立上游传输方式。网关将 Responses、Chat Completions 和 Anthropic Messages 请求接入 Prism 异步项目聊天，同时提供项目、文件、同步状态与渲染相关的工作区 API。

本文描述当前代码行为，验证结果见文末。实现依据包括 HAR 请求结构、当前官方前端、官方 Yjs 合成样本、离线模拟及用户授权账号的真实 API 验证。未使用 HAR 中的凭据执行请求，本文不包含凭据或私有状态。

能力范围以 HAR 中已经观察到的请求、响应及协作文档结构为依据。接入这些链路不代表已支持 Prism 未公开的全部工具、任意客户端工具注册或完整浏览器编辑器。

## 账号配置

管理端 OpenAI OAuth / setup-token 账号提供以下协议选项：

| 模式 | `extra.openai_transport` | 用途 |
| --- | --- | --- |
| Codex | `codex` | 既有 Codex 协议；未设置时的默认模式 |
| ChatGPT Web | `web` | ChatGPT Web 会话协议 |
| Prism | `prism` | Prism 项目聊天 start/status 协议 |

这些选项不适用于 OpenAI API Key 类型的上游账号。客户端访问 sub2api 的下游 API key 与这里的上游账号类型是两个概念。新建 Prism 时选择 OpenAI、OAuth 类别及 Prism，填写账号名和凭据；界面创建 `setup-token` 账号，不进入 OAuth refresh 流程。通过 access-token 导入时保留所选协议。

| 界面字段 | 保存位置 | 含义 |
| --- | --- | --- |
| OpenAI 访问令牌（access_token） | `credentials.access_token` | 与 Web 共用的普通 OpenAI access token，以 `prism_oai_access_token` Cookie 引导自动认证 |
| 高级：手动覆盖 Prism 会话 | `credentials.prism_session_token` | 默认折叠、通常留空；仅用于兼容手动覆盖 |
| 文本提示工具桥接 | `extra.prism_prompt_tool_bridge` | 严格布尔值，默认 `false`，仅 Prism 生效 |

访问令牌为创建必填项。已在指定测试账号验证：仅有普通 OpenAI `access_token`、没有手动 Prism 会话令牌，也能由后端请求 `/auth/session` 自动获取 `prism_session_token` 并完成真实对话，无需抓 Cookie。编辑留空会保留已有令牌。后端仍兼容 `credentials.prism_oai_access_token` 和 `credentials.prism_cookie`。

编辑框不回填已有凭据，留空由后端保留原值；旧后端返回的敏感字段不会被编辑界面再次提交。上游 `Set-Cookie` 更新进入 Cookie jar，并随私有续链/项目状态保存以便恢复，不写回账号配置，也不传给客户端。

桥接仅在 Prism 面板显示，新建默认关闭，编辑可明确保存开启或关闭。批量编辑须先勾选更新上游协议并选择 Prism，再单独勾选是否更新桥接；未勾选就保留各账号原值，切回 Codex/Web 时不提交新的桥接选择。后端在 Codex/Web 模式下不启用此开关。

Prism 不使用 Codex 透传、Responses WebSocket、Codex CLI 限制、指纹、compact 或 Codex 图像桥接。代理、分组、并发与模型映射仍沿用网关配置。

## 模型和客户端工具

默认模型为 `gpt-5.6-sol`，未指定 reasoning effort 时使用 `medium`，与 2026-09-18 Prism 当前前端默认值一致。旧 HAR 中的 `gpt-6-astra` 和客户端 `gpt-5.5` 在本次指定账号实测均被上游拒绝；不会自动把它们伪装成受支持模型。未配置映射时，Prism 只声明并接受默认模型，不自动引入 Codex/Web 别名。`credentials.model_mapping` 的公开名称构成账号模型白名单，例如：

```json
{"model_mapping":{"prism-default":"gpt-5.6-sol"}}
```

此时客户端请求 `prism-default`；要同时接受原名称，须另加该名称的映射。映射键和值必须是具体模型名，不含空白、通配符或路径分隔符。映射不证明上游实际开放了目标模型。项目创建也检查分组模型白名单。

Prism 的 endpoint capability 声明包含 Responses 与 Chat Completions；网关另提供 Anthropic Messages 适配。这不代表 embeddings、实时语音、搜索专用端点或 compact 已获支持。

| 工具路径 | 执行位置 | 当前行为 |
| --- | --- | --- |
| Prism 内置工具 | Prism 远端 sandbox | 保留原生协议，由上游选择并执行；桥接不负责注册或执行这些工具 |
| 客户端自定义工具，桥接关闭 | 无 | 携带客户端 `tools` 声明时明确返回 HTTP 400，提示按需启用桥接 |
| 客户端自定义工具，桥接开启 | 客户端 | 工具定义、约束和历史结果转换成文本提示，符合协议的模型输出转换为客户端工具调用 |

`extra.prism_prompt_tool_bridge` 缺省或 `false` 时，原生请求不进入提示桥接解析器；普通消息和指令保持原生语义。此模式没有把任意客户端自定义工具注册到 Prism 的能力。

开启桥接后，工具文本信封须通过请求 nonce、协议名、schema hash、工具名称和参数校验，才能转换成 `function_call` / `custom_tool_call`，再适配下游协议。桥接生成的调用由客户端执行，sub2api 不在本机执行。Prism 内置工具仍由上游处理。提示协议依赖模型遵循，真实遵循率尚待确认。

切换桥接模式会改变续链隔离键，旧 `previous_response_id` 不能跨工具模式继续；客户端应重新发送完整上下文。

## 项目工作区 API

以下 17 个端点以 sub2api API key 认证，仅接受 OpenAI 分组，复用计费资格及用户并发检查。项目账号选择、账号并发、所有权和项目锁由服务层验证。依赖缺失时返回 503，不跳过计费检查。

`POST /v1/prism/projects` 接受 JSON `{ "title": "Paper", "model": "gpt-5.6-sol" }`；字段可省略，服务端补默认标题与模型。成功返回 HTTP 201，公开字段为 `id`、`object`、`title`、`model`、`created_at`、`expires_at`。`id` 是 sub2api 生成的 `prism_proj_…` 句柄，不接受上游原始 project UUID 代替。表中的 `{project_id}` 均指该公开句柄。

| 方法 | 端点 | 参数与用途 |
| --- | --- | --- |
| POST | `/v1/prism/projects` | JSON `title`、`model`；创建项目及 sandbox |
| GET | `/v1/prism/projects/{project_id}` | 验证访问权，返回公开项目对象 |
| GET | `/v1/prism/projects/{project_id}/conversation-history` | 读取当前保存会话的历史，过滤私有字段 |
| GET | `/v1/prism/projects/{project_id}/delta-files` | 读取项目最近保存的文件变化；没有记录时返回空数组 |
| GET | `/v1/prism/projects/{project_id}/sync-status` | 读取最近一次聊天文件回写的总体状态和逐文件结果；不触发任务或重新同步 |
| POST | `/v1/prism/projects/{project_id}/files` | multipart 单文件字段 `file`，最大 32 MiB |
| PATCH | `/v1/prism/projects/{project_id}/thumbnail` | JSON `file_id`，使用上传返回的 `prism_file_…` 句柄 |
| POST | `/v1/prism/projects/{project_id}/render` | JSON `main_document`、`client_state_vector`、`client_delete_set_update` |
| GET | `/v1/prism/projects/{project_id}/render-status` | 查询渲染结果/状态 |
| GET | `/v1/prism/projects/{project_id}/pdf` | 读取渲染结果；上游提供 PDF 时返回 `application/pdf` |
| GET | `/v1/prism/projects/{project_id}/logs` | 获取渲染日志 |
| GET | `/v1/prism/projects/{project_id}/synctex` | 获取 SyncTeX 数据 |
| GET | `/v1/prism/projects/{project_id}/word-count` | 查询参数 `main_document`、`include_bibliography` |
| GET | `/v1/prism/projects/{project_id}/latest-render` | 查询参数 `main_document` |
| GET | `/v1/prism/projects/{project_id}/version-history` | 查询参数 `page`、`page_size`；页大小最大 100 |
| POST | `/v1/prism/projects/{project_id}/heartbeat` | JSON `{}` 或空请求体，触发 sandbox heartbeat |
| POST | `/v1/prism/projects/{project_id}/wait-for-sync` | JSON `wait_ms`，最大 30000；省略使用封装默认值 |

上传还受部署的网关请求体上限约束，并将含 multipart 开销的请求体限制为 33 MiB；临时文件在解析后清理。成功返回文件句柄、文件名、字节数和所属项目句柄。PDF 使用固定安全文件名 `document.pdf`；`/pdf` 不保证渲染尚未完成时也返回 PDF。

上传实现区分两个私有标识：请求生成的文件节点 ID 用于项目协作文件树，上传响应的 blob UUID 用于存储对象和缩略图；两者均映射到返回给客户端的 `prism_file_…` 句柄。blob 上传成功后，通过 Yjs 增量将节点挂接到项目文件树，再读取服务端文档确认节点，并等待 sandbox 文件同步；这些步骤完成后才返回上传成功及 `project_path`。挂接失败会返回错误，不自动重试整个上传，以免重复创建节点。

该链路已通过离线回归。服务端读回和同步状态确认不等于已验证真实账号下所有工具的文件读取或编译效果；这些在线行为仍待确认。

`delta-files` 来自上游任务保存的文件变化，保留已识别的路径、状态、diff、截断/错误标记及可用的二进制内容元数据，并过滤私有字段。它不提供任意文件读取、完整文件系统导出或编辑器协作协议，也不证明未知工具已接入。

文件变化记录与项目文档回写结果分别返回。`sync-status` 的响应含 `status` 和 `files`，每个文件含 `path`、`status` 及可用的 `reason`；这些字段只描述保存的最近一次聊天结果，不扫描整个项目。总体状态为 `synced`、`partial`、`unsynced` 或 `not_required`，逐文件状态为 `synced`、`skipped` 或 `unsynced`。跳过、不支持或无法确认的变化会保留原因，不能将聊天成功或 `delta-files` 非空理解为所有文件已永久写入项目。旧状态存在变化但未保存同步结果时，公开为 `unsynced`；无变化时为 `not_required`。

`synced` 表示所有需要处理的目标已确认同步；`partial` 表示同时存在已同步和未同步目标；`unsynced` 表示需要处理的目标均未确认同步；`not_required` 表示无需同步。项目根路径下名称精确为 `AGENTS.md` 的变化以 `skipped` / `ignored_agent_instructions` 标记并排除在同步目标之外，仅有此类文件时也是 `not_required`。这一跳过规则不泛化到子目录或其他大小写名称。

## 原生工具生成文件的自动回写

原生聊天任务完成后，网关读取终态 `codexDeltaFiles`，自动将支持的文本变化写回同一项目的 Yjs 文档。该步骤不依赖 `prism_prompt_tool_bridge`，三种下游聊天协议使用同一流程；客户端无需再次执行远端工具或调用同步接口。

实现沿用 HAR 中观察到的 `folder` / `text` 节点结构：新增文本文件时创建缺失的父文件夹和文本节点；修改已有文本时先读取当前内容，再按完整 unified diff 精确匹配基线并生成文本增量。支持捕获中的 `render/`、`codex/` diff 标签，保留已有文本的换行风格。已有路径冲突、找不到修改基线、截断或报错的 diff 均不会通过模糊匹配强行应用。

每次写入后，网关重新请求服务端文档，核对目标节点、已接收的更新和文本内容；随后调用 sandbox `wait-for-sync`，确认协作文档已同步到运行环境。只有确认完成的文件才标记 `synced`。这确认的是该次回写结果，不保证后续用户编辑、任务执行或上游存储变化不会再次修改文件。

| 变化类型或条件 | 当前回写行为 |
| --- | --- |
| `added` 文本文件及缺失父目录 | 创建 `folder` / `text` 节点，完成服务端读回和 sandbox 同步确认 |
| `modified` 已有文本文件 | 精确验证当前文本与 diff 基线，写入文本增量后再次确认 |
| 根路径 `AGENTS.md` | 跳过，原因 `ignored_agent_instructions` |
| 二进制内容或 PDF | 尚无完整自动回写支持，标记 `unsynced` / `unsupported_binary`；独立文件上传接口不等同于生成文件回写 |
| 删除或其他未支持的变化状态 | 不执行对应操作，标记 `unsynced` / `unsupported_status` |
| 重复路径、结构歧义、基线不匹配或读回冲突 | 停止无法确认的更新，按文件记录具体原因 |
| 网络、授权、同步确认失败或超出同步限制 | 保留未确认状态及原因，不将其当作同步成功 |

并发修改采用保守处理：无法唯一确定目标或基线时拒绝写入，写后读回不一致时标记 `readback_conflict`。多文件回写不是整体事务；已经确认的更新不会因另一个文件失败而自动回滚，写入后失去确认也可能已在上游生效。因此客户端应检查逐文件状态，不能根据一次聊天成功推断全部文件已写入。

常规回写失败作为同步结果保存，仍返回已完成的模型回复，不重新启动模型任务。显式项目先保存包含文件变化和同步结果的项目状态，再保存续链，最后公开同步状态；存储故障或租约失效会结束请求，不发布成功完成的同步事件，也不会自动重跑远端任务。项目接口可查询已成功保存的结果；请求取消、存储失败或丢失租约时不保证此次结果已持久化。

## 把聊天绑定到项目

创建项目后，在 `/v1/responses`、`/v1/chat/completions` 或 `/v1/messages` 请求中携带：

```http
Authorization: Bearer <SUB2API_API_KEY>
X-Prism-Project-ID: <创建接口返回的项目 id>
Content-Type: application/json
```

绑定在账号调度前发生，请求只能使用该项目固定的上游账号，不能通过普通调度或故障切换访问另一个账号的项目。API key、用户、分组、账号归属和凭据变化都会重新检查。

不传项目头且不传 `previous_response_id` 时，为新聊天创建独立项目和会话。传项目头但不传 `previous_response_id` 时，在指定项目中开始新会话，复用文件，不继承上一会话的响应游标。续接指定项目的聊天时，同时保留项目头和有效 `previous_response_id`；游标项目与项目头不匹配时拒绝请求。

公开句柄不暴露上游项目 ID、会话 ID、sandbox token 或内部快照。项目 API 不接受这些上游标识绕过所有权校验。

## 上游初始化、流式响应与原生进度

项目初始化包含 `/auth/session`、创建/验证项目、动态创建 sandbox、获取资源授权并传给 sandbox、通过 `/api/y` 获取文档授权、将授权交给 sandbox，以及等待文件同步。授权 URL 须符合 Prism 基础地址的同源规则；sandbox URL 路径限定为 `/s/sandboxes/proxy`。

聊天通过 `POST /api/llm/response_with_tools_start` 提交任务。start 可以同步返回 completed/success 或 completed/error，这两种情况直接处理结果，不再轮询。只有非终态且带有效、非空对象 `turn_state` 时才调用 `POST /api/llm/response_with_tools_status`。默认间隔 500 毫秒、最多 240 次；实际耗时包含 HTTP 往返并受上下文超时约束。`turn_state` 和 `codex_listen_snapshot` 等私有状态保留不透明结构；pending 的缺失/null 状态不会覆盖上一轮有效对象，非法非空状态会结束请求。上游错误中的 debug、认证头和 sandbox URL 不公开。

Responses 的简写消息（省略 `type` 或使用字符串 `content`）在发送前转换为 Prism 所需的 message/text 结构，避免用户正文被上游忽略。对于客户端提供的完整纯文本历史，先前 user/assistant 消息以 JSON 引用方式携带在本轮用户提示中，system/developer 仍保留原有角色；支持标准输出中的空 annotations/logprobs。未知输入条目、未知附加字段、非空注释和非文本内容保持原始 JSON，这些组合的上下文连续性未在线验证。

2026-09-18 指定账号实测发现：仅带 `previous_response_id` 和本轮增量消息时，虽然项目、会话及 sandbox 状态正确复用，上游 `codex_session_id` 仍发生重建，模型未保留先前数字。当前不保证该模式的语义连续性；文本对话客户端应随每轮请求带完整历史。HAR 中旧版本的稳定续聊不能替代当前版本的在线结果。

拿到 `request_id` 后的轮询、校验或状态保存失败不会触发换账号重新启动，以免远端工具重复执行。Prism 完成结果的本地转换不启用原生 HTTP Responses 首输出超时或首输出暂存；转换产生的账号重试信号也被转为当前请求的终结错误，不会重放已完成的任务。显式项目先完成租约检查的项目状态保存，再发布续链游标。当前不持久化未完成任务来恢复轮询；重启恢复指已保存项目/续链状态，不包括中途任务恢复。

`stream=true` 的最终文本取自 start 或轮询的成功终态，随后转换为下游内容事件，并非上游逐 token 转发。等待期间默认发送 SSE 心跳注释 `: prism task pending`，由轮询进度驱动、约 15 秒节流；它与项目 `/heartbeat` API 用途不同。

要观察远端原生工具进度，流式请求额外携带 `X-Prism-Progress: true`。状态变化时发送独立事件：

```text
event: prism.tool_progress
data: {"type":"prism.tool_progress","execution":"upstream","progress":{"transcript_cursor":0,"line_count":0,"tool_calls":[],"reasoning_summaries":[]}}
```

上例为合成空进度。实际事件包含上游提供且经过过滤的工具名、调用标识、行号、状态及可识别摘要，不转成要求客户端执行的 `function_call` 或 `tool_use`。客户端收到 `execution: "upstream"` 时只展示进度，不提交对应工具结果。未启用请求头时仍保留心跳，不发送扩展进度；非流式请求也不发送这些事件。

三种聊天协议均公开最终文件同步状态。非流式成功响应使用 `X-Prism-Sync-Status` 响应头，浏览器允许的跨域来源可读取该头。流式请求开启 `X-Prism-Progress: true` 后，在完成内容之前额外发送独立事件，例如：

```text
event: prism.file_sync
data: {"type":"prism.file_sync","sync":{"status":"partial","files":[{"path":"main.tex","status":"synced"},{"path":"figure.png","status":"unsynced","reason":"unsupported_binary"}]}}
```

这是合成示例；实际结果以逐文件状态为准。普通流式请求仅发送 `: prism file sync <status>` 诊断注释，兼容客户端可忽略。此事件不会变成客户端工具调用，不受文本提示桥接开关影响。显式项目可在完成后调用 `/sync-status` 读取完整结果。

远端终态输出仅保留消息、可识别推理摘要和 usage，过滤已经在 sandbox 执行的工具调用与未知内部字段。可选提示桥接随后解析消息文本，并独立生成通过校验的客户端工具调用；这些后生成的调用不经过远端工具过滤器。

SSE 已提交后发生错误时，通过相应流式错误事件结束，客户端须处理流内失败，不能只看 HTTP 状态。usage 只取上游实际提供的统计，不按字符数伪造 token；缺失或零值统计不能证明实际调用成本为零。

## Redis、有效期与隔离

续链键包含**分组 ID、下游 API key ID、上游账号 ID、实际凭据摘要、桥接开关值、response ID**。更换任一边界后，旧游标不能命中其他状态。摘要覆盖实际访问令牌、会话令牌及兼容 Cookie。

| 状态 | Redis 命名空间 | 有效期与恢复 |
| --- | --- | --- |
| 已完成响应的续链 | `prism:session:` | 1 小时；成功续链给新 response ID 写新条目，不默认延长旧游标 |
| 显式项目及所有权 | `prism:project:` | 保存时续期至 7 天，含私有会话/sandbox/文件句柄映射 |
| 项目跨实例租约 | `prism:project-lock:` | 15 分钟；活跃操作续租，按 owner 释放 |

普通续链也采用独立 Prism 名称的分布式租约，避免与 Web 会话锁冲突。项目内聊天、上传和渲染通过项目租约串行化，拿锁后重读状态，防止等待期间覆盖其他实例的新状态；聊天和工作区操作还有各自执行超时。

配置了支持 Prism 存储的 Redis cache 后，以 Redis 为权威状态源。读取、写入或租约异常会拒绝相关操作，不以本地旧缓存绕过检查；过期、丢失或不匹配的记录也不能继续。共享同一 Redis 且记录有效时，另一个实例或重启后的进程可恢复已保存状态，包括必要 Cookie 和快照。Redis 数据丢失或过期后不能恢复。

没有实现这些可选存储接口的环境使用内存状态，不能跨实例或重启恢复。续链内存保存时清理过期项，达到 4096 条时淘汰最早到期项。状态过期不等于删除上游项目。

## 实现与验证状态

主要实现位置：

- [openai_gateway_prism.go](../backend/internal/service/openai_gateway_prism.go)：三协议、项目执行、续链与失败处理。
- [openai_prism_transport.go](../backend/internal/service/openai_prism_transport.go) 和 [openai_prism_native.go](../backend/internal/service/openai_prism_native.go)：认证、sandbox、异步任务、原生进度和文件/渲染。
- [openai_prism_prompt_tools.go](../backend/internal/service/openai_prism_prompt_tools.go)：可选客户端文本工具协议。
- [openai_prism_native_output.go](../backend/internal/service/openai_prism_native_output.go)：远端终态输出与 usage 白名单。
- [openai_prism_attach.go](../backend/internal/service/openai_prism_attach.go) 和 [openai_prism_yjs.go](../backend/internal/service/openai_prism_yjs.go)：文件树增量挂接、服务端确认与 sandbox 同步。
- [openai_prism_delta_sync.go](../backend/internal/service/openai_prism_delta_sync.go) 和 [openai_prism_delta_patch.go](../backend/internal/service/openai_prism_delta_patch.go)：原生任务文件变化的自动回写、精确 diff 应用与逐文件结果。
- [openai_prism_stream_progress.go](../backend/internal/service/openai_prism_stream_progress.go)：心跳、扩展进度和流内错误。
- [openai_prism_session_store.go](../backend/internal/service/openai_prism_session_store.go) 和 [openai_prism_workspace.go](../backend/internal/service/openai_prism_workspace.go)：持久化、所有权与租约。
- [工作区 handler](../backend/internal/handler/openai_prism_workspace.go) 和 [gateway.go](../backend/internal/server/routes/gateway.go)：认证、计费/并发、解析与路由。
- [gateway_cache_prism.go](../backend/internal/repository/gateway_cache_prism.go) 和 [gateway_cache_prism_session.go](../backend/internal/repository/gateway_cache_prism_session.go)：Redis 存储。
- 前端 Create/Edit/BulkEdit 账号弹窗：协议、凭据与独立桥接设置。

本次修复验证（2026-09-18，独立分支 `fix/prism-turn-state`）：

| 检查 | 状态 |
| --- | --- |
| service、handler、routes 定向回归 | `-run 'Prism|SSEToJSON|NonStreaming'` 通过；覆盖同步终态、opaque turn_state、失败不重放、默认模型、简写消息、纯文本历史及转换后的 JSON Content-Type |
| 标准 Responses 输出回灌 | 覆盖空 annotations/logprobs；非空注释、未知字段、文件输入保留原结构 |
| Create/Edit 账号、i18n 完整性测试 | 119/119 通过；vue-tsc、变更文件 ESLint 和生产构建通过 |
| Linux 嵌入式后端与 Docker 镜像 | 构建通过，实际运行版本经容器二进制核对 |
| 9999 自动认证界面 | 实际 HTTP 静态资源与构建字节一致，包含自动获取 Prism 会话说明 |
| 真实账号普通 access token 自动认证 | 通过；没有预填 Prism 会话令牌，也自动取得会话 Cookie |
| 公网 `/v1/responses` 两轮纯文本对话 | 通过；请求 `gpt-5.6-sol`，首轮 SSE 返回 `PRISM_OK`，次轮携带完整历史返回 `731`，均为 completed |
| `previous_response_id` + 增量消息语义连续性 | 未通过；上游 Codex session 重建，当前必须携带完整文本历史 |
| 上游模型选择 | 指定账号拒绝 `gpt-5.5` 和旧 HAR 的 `gpt-6-astra`；按用户选择请求 `gpt-5.6-sol`，未设置冒名映射 |
| usage | 真实响应未提供统计，保留 null，不估算或伪造 token 数 |
| 本次后端全量单测、golangci-lint | 未重跑全量单测；golangci-lint 未执行。上述结果为本次定向回归 |

初始主版本曾完成后端全量单测、repository 回归和 184 项前端定向测试；这些历史结果不代替本次变更验证。上传后 Y 文档挂接、生成文件回写、冲突和故障处理仍以离线回归为依据，本次未在线验收上传/编译/渲染完整链路。客户端工具桥接遵循率、长期登录有效期及模型输出稳定性也未作在线承诺；当前不宣称支持未知工具注册、完整浏览器编辑器或所有 Responses 图像/文件输入形式。
