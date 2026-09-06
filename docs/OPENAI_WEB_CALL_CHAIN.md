# OpenAI Web/Codex 当前调用链

本文档记录 `sub2api` 当前 OpenAI 兼容接口的实际调用链，包括 Web Prompt
Tool 桥接和 WebSocket handoff。文档只描述协议和代码路径，不包含 access
token、Cookie、上传签名或其他认证材料。

## 总体流程

```text
客户端 API Key
  -> /v1/chat/completions 或 /v1/responses
  -> API Key 鉴权、请求校验、计费/并发/安全检查
  -> 分组账号调度（模型、能力、粘性会话、限流、健康状态）
  -> 根据账号 openai_transport 选择 Web 或 Codex
  -> 获取账号 access_token
  -> 上游请求与协议转换
  -> 标准 OpenAI SSE/JSON 响应
  -> 用量、计费、日志、重试和 failover
```

## 1. 公共入口

`POST /v1/chat/completions` 进入
[openai_chat_completions.go](../backend/internal/handler/openai_chat_completions.go)
的 `OpenAIGatewayHandler.ChatCompletions`。

`POST /v1/responses` 进入
[openai_gateway_handler.go](../backend/internal/handler/openai_gateway_handler.go)
的 `OpenAIGatewayHandler.Responses`。

入口层负责：

- 从中间件上下文读取网关 API Key 和用户身份。
- 读取、限制大小并校验 JSON 请求体。
- 校验 `model`、`stream`、`service_tier` 等公共字段。
- 执行安全审计、余额/计费资格和用户并发检查。
- 解析分组级模型映射。
- 通过 `SelectAccountWithSchedulerForCapability` 选择可用账号。
- 在上游失败时执行同账号重试或切换账号。

Chat Completions 选择账号后调用
`GatewayService.ForwardAsChatCompletions`；Responses 选择账号后调用
`GatewayService.Forward`。

## 2. Web/Codex 协议分流

账号的协议选择字段是：

```json
{"openai_transport":"web"}
```

相关逻辑位于
[account.go](../backend/internal/service/account.go) 和
[openai_web_transport.go](../backend/internal/service/openai_web_transport.go)
的 `OpenAITransport`、`IsOpenAIWebTransport`、`UsesOpenAIWebProtocol`。

分流规则：

- 只有 OpenAI OAuth 或 Setup Token 账号可以使用 Web transport。
- `openai_transport=web` 进入 ChatGPT Web conversation 协议。
- `openai_transport=codex` 或字段缺失时保留 Codex Responses 协议。
- 字段缺失/非法默认按 Codex 处理，兼容历史账号。
- API Key 账号不会因为该字段被切换到 Web 协议。

两条服务层入口会在 Codex 标准化之前切入 Web：

```go
// Chat Completions
forwardChatCompletionsViaOpenAIWeb(...)

// Responses
forwardResponsesViaOpenAIWeb(...)
```

对应文件：

- [openai_gateway_chat_completions.go](../backend/internal/service/openai_gateway_chat_completions.go)
- [openai_gateway_forward.go](../backend/internal/service/openai_gateway_forward.go)

## 3. Chat Completions Web 链路

```text
Chat Completions 请求
  -> ChatCompletionsRequest
  -> ChatGPT Web conversation payload
  -> ChatGPT Web SSE 或 WebSocket stream
  -> Responses SSE
  -> Chat Completions SSE/JSON
```

`forwardChatCompletionsViaOpenAIWeb` 会：

1. 解析 Chat Completions 请求。
2. 兼容处理发送到 Chat URL 的 Responses 形状请求。
3. 校验 Web 不可表达的参数。
4. 解析并规范化 Web 模型 slug。
5. 获取 access token。
6. 调用 `OpenAIWebTransport.Do`。
7. 流式请求交给 `handleChatStreamingResponse`，将 Responses 事件转换为
   `chat.completion.chunk`。
8. 非流式请求缓冲响应，再转换为 Chat Completions JSON。

## 4. Responses Web 链路

```text
Responses 请求
  -> ResponsesRequest
  -> ResponsesToChatCompletionsRequestWithOptions
  -> ChatGPT Web conversation payload
  -> ChatGPT Web SSE 或 WebSocket stream
  -> 标准 Responses SSE/JSON
```

`forwardResponsesViaOpenAIWeb` 会：

1. 只接受标准 `/v1/responses` 路径。
2. 校验 Web transport 能够处理的公共字段。
3. 将 Responses 请求转换为内部 Chat Completions 结构。
4. 获取 access token 并调用 Web transport。
5. 流式请求复用 Responses 响应处理器。
6. 非流式请求将上游 SSE 汇总成标准 Responses JSON。

`previous_response_id`、`store`、非默认 `service_tier` 等无法在 Web 会话中
保持语义的字段会被拒绝，而不是静默伪造。

## 5. access token 和浏览器请求

网关通过 `GatewayService.GetAccessToken` 获取账号凭证。OAuth/Setup Token
账号读取其 `access_token`，随后由
`buildOpenAIAuthenticationHeaders` 生成 Bearer 认证头。

Web transport 位于
[openai_web_transport.go](../backend/internal/service/openai_web_transport.go)，
复用网关已有的 HTTP upstream、代理、Cookie jar 和 OAuth 插件扩展点，并使用
浏览器风格的请求头和 Chrome impersonation 客户端。

## 6. ChatGPT Web 握手

`OpenAIWebTransport.Do` 的主要顺序为：

```text
GET /
  -> 解析脚本来源和 data-build
POST /backend-api/sentinel/chat-requirements/prepare
  -> 生成 proof-of-work / Turnstile 材料（如需要）
POST /backend-api/sentinel/chat-requirements/finalize
  -> 获得 requirements token
POST /backend-api/f/conversation/prepare
  -> 获得 conduit_token
POST /backend-api/sentinel/ping
  -> 发送会话/令牌存在性信号（best effort）
POST /backend-api/f/conversation
  -> 获得直接 SSE 或 stream_handoff
```

实现函数包括 `Bootstrap`、`GetRequirements`、`prepareConversation`、
`pingSentinel`、`buildConversationRequestWithBody` 和 `Do`。

## 7. Web 请求体转换

Web 请求体由 `buildConversationPayload` 构造，包含：

- `action=next`、`messages`、规范化后的 `model`。
- `parent_message_id`、`conversation_mode` 和 `conversation_origin`。
- SSE、历史训练开关、客户端上下文、时区和 WebSocket 请求 ID。
- `reasoning_effort` 转换后的 `thinking_effort`。

消息转换规则：

- `developer` 映射为 Web `system`。
- 文本进入 `content.parts`。
- `assistant` 历史用于过滤 Web 端可能重复回放的内容。
- Prompt Tool 模式下，`tool` 消息映射为可关联的用户文本。
- Web 不支持的 `temperature`、`top_p`、`max_tokens`、
  `max_output_tokens` 等字段只做公共契约校验，不发送到 Web 上游。
- `stop`、`store`、无效 `reasoning` 等不可转换字段会返回标准
  `invalid_request_error`。

## 8. 附件上传

支持的公共输入包括 `image_url`、`input_image`、`file`、`input_file`、
`file_id` 和 `file_data`。

远程附件 URL 不在当前适配范围内；新附件需要 Base64 Data URI，或直接传入
已有的 ChatGPT `file_id`。

新附件使用 Plus Web composer 的 ACE 上传流程：

```text
POST /backend-api/files
  -> 获得 file_id 和 signed upload_url
PUT signed upload_url
  -> 上传原始字节，不附带 Bearer token
POST /backend-api/files/process_upload_stream
  -> 等待文件处理/索引完成
conversation payload
  -> 引用 file_id
```

图片会进入 `image_asset_pointer`，普通 PDF、TXT、ZIP 等文件进入
`metadata.attachments`。transport 层接受任意 MIME 类型；当前单文件上限为
32 MiB，单条消息最多 16 个附件，具体解析能力仍由 ChatGPT Web 上游决定。

## 9. Web 模型目录

`GET /v1/models` 由
[gateway_handler.go](../backend/internal/handler/gateway_handler.go) 调用
`GatewayService.GetAvailableModels`。

对于 Web 账号，模型目录流程为：

```text
检查账号目录缓存
  -> 必要时请求
     /backend-api/models?iim=false&is_gizmo=false&supports_model_picker_upgrade_presets=true
  -> 规范化并保存账号级模型目录
  -> 合并分组内所有 Web 账号的可用模型
  -> 追加 auto 默认选择器
```

模型发现实现位于
[openai_web_models.go](../backend/internal/service/openai_web_models.go)。

因此：

- `auto` 是默认选择器，不代表只有一个可用模型。
- 具体模型以 `/v1/models` 当前返回为准。
- 不存在固定五项上限，未来新增模型可以被目录发现和合并。
- 指定模型在调度阶段和 Web payload 构造阶段都会再次校验。

## 10. stream_handoff 和 WebSocket

新版 Web 模型可能先在 HTTP SSE 中返回 `stream_handoff`，再把正文发布到
用户 WebSocket：

```text
初始 HTTP SSE
  -> 解析 conversation_id、turn_exchange_id、topic_id
GET /backend-api/celsius/ws/user
  -> 获得 websocket_url
建立 WebSocket
  -> subscribe topic_id
接收 conversation-turn-stream
  -> 提取 stream-item / encoded_item
  -> 去重并重新拼接为 SSE
```

旧式直接 SSE 和新式 WebSocket handoff 都由
`prepareConversationResponseBody` 统一处理。

## 11. 响应转换

`newOpenAIWebResponsesBodyWithPromptTools` 将 Web 原始事件转换为标准
Responses 事件，处理 `append`、`replace`、`message` 和
`delta_encoding`，并过滤非 assistant 内容及 Web 私有标记。

普通文本会生成标准事件序列：

```text
response.created
  -> response.output_item.added
  -> response.content_part.added
  -> response.output_text.delta
  -> response.output_text.done
  -> response.content_part.done
  -> response.output_item.done
  -> response.completed
```

Chat Completions 路径再使用
`ResponsesEventToChatChunks` 生成 `chat.completion.chunk`。

如果 Web stream 正常结束但没有提取到 assistant 文本，会返回
`upstream_empty_response`，避免把空结果误报为成功。

## 12. Prompt Tool

系统设置 `enable_openai_web_prompt_tools=true` 且请求包含工具时：

```text
OpenAI tools/tool_choice
  -> 严格 Schema 校验
  -> 生成 protocol、nonce、schema_hash
  -> 注入内部 system prompt
  -> 从 Web 请求删除原生 tools/tool_choice
  -> Web 模型输出严格 JSON 工具信封（event/start/end 起止信号，可包在前后文或 markdown 中）
  -> 校验 nonce、Schema、工具名和参数
  -> 转换为标准 function_call/custom_tool_call/tool_calls
```

实现位于
[openai_web_prompt_tools.go](../backend/internal/service/openai_web_prompt_tools.go)。

Prompt Tool 是协议桥接，不在网关内执行实际工具。客户端或上层 Agent 执行
工具后，把匹配的 `function_call_output` 或 `custom_tool_call_output` 作为下一轮
请求传回。工具结果会按 `call_id` 编码为网页端用户文本，工具调用历史会被编码
为网页端可理解的 `function` 或 `custom` 上下文。

### 12.1 注册和规范化

- Responses 的 `additional_tools` 会与顶层 `tools` 合并；注册项不会被误当成聊天消息。
- `namespace` 子工具在网页侧使用稳定的安全扁平别名，回程恢复裸工具名和独立
  `namespace` 字段；冲突或超过 64 字符的名称会拒绝或使用带哈希后缀的别名。
- `custom` 工具使用自由文本 `input`，其内部校验仍使用 `{ "input": "..." }`
  的严格包装 Schema；动态 `format` 描述会纳入 Prompt 指令和 Schema hash。
- 网页模型可能返回 `calls` 或历史兼容的 `tools` 数组；两者只能出现一个，且
  每个调用都必须命中本次请求的 nonce、Schema hash、工具类型和参数 Schema。

### 12.2 标准 Responses 事件

普通 function 工具产生 `response.function_call_arguments.delta/done`，custom
工具产生 `response.custom_tool_call_input.delta/done`。两者都包含完整的
`response.output_item.added`、`response.output_item.done` 和
`response.completed` 生命周期；custom 项的公开输出类型为
`custom_tool_call`，参数使用自由文本 `input`，不会伪装成 function 参数。

Prompt Tool 对直接 SSE 和 `stream_handoff + WebSocket` 使用同一套分类器，因此
私有协议标记不会泄漏到 Chat Completions 或 Responses 客户端。

## 13. 错误、限流和计费

Web 请求错误统一经过 `handleOpenAIWebForwardError` 和
`handleOpenAIWebHTTPResponseError`：

- 参数不支持或 Schema 无效：返回 400 `invalid_request_error`。
- Sentinel/交互挑战：返回可识别的 Web challenge 错误。
- 上游 HTTP 错误：根据状态码决定重试或 failover。
- Web 429 使用独立的 `web_rate_limited` 状态。
- Codex 账号使用独立的 `codex_rate_limited` 状态。
- 流式响应即使客户端断开，也会尽量继续排空上游以完成用量统计。
- 成功或部分成功结果通过 `OpenAIForwardResult` 进入异步用量记录和计费。

## 14. 关键文件索引

- [openai_chat_completions.go](../backend/internal/handler/openai_chat_completions.go)：Chat Completions 公共入口和账号选择。
- [openai_gateway_handler.go](../backend/internal/handler/openai_gateway_handler.go)：Responses 公共入口和账号选择。
- [openai_gateway_chat_completions.go](../backend/internal/service/openai_gateway_chat_completions.go)：Chat 路由、Web Chat 转发和 Chat SSE 转换。
- [openai_gateway_forward.go](../backend/internal/service/openai_gateway_forward.go)：Responses 路由、Web Responses 转发和错误处理。
- [openai_web_transport.go](../backend/internal/service/openai_web_transport.go)：Web 握手、请求构造、附件、SSE/WebSocket 适配。
- [openai_web_models.go](../backend/internal/service/openai_web_models.go)：账号级 Web 模型发现和缓存。
- [openai_web_prompt_tools.go](../backend/internal/service/openai_web_prompt_tools.go)：Prompt-based tool calling 协议。
- [account.go](../backend/internal/service/account.go)：账号类型和 Web/Codex 选择规则。
- [gateway_service.go](../backend/internal/service/gateway_service.go)：分组模型目录和账号可用性聚合。
