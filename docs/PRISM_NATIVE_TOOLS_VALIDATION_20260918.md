# Prism 原生 OpenAI 工具能力实测

日期：2026-09-18。当前源码 `22d232bdb`，9999 运行镜像 `local/sub2api:prism-fix-b7d4785c4`。本轮依照用户最新要求，只测试原生接口，不使用、不启用文本提示工具桥接。

## 结论与范围

在用户授权账号 36、请求模型 `gpt-5.6-sol` / `medium` 的实测中，`/api/llm/response_with_tools_start` + `/api/llm/response_with_tools_status` 能执行远端内置工具，但标准 OpenAI 客户端自定义工具未生效，`function_call_output` 输入也未进入当前提示。因此不能把这条接口直接当成支持客户端 `tools → function_call → function_call_output` 的标准 OpenAI 工具通道。

这是当前账号、模型、已观测端点和已测试字段的结论，不是对 Prism 所有内部端点或未来版本的断言。没有猜测或探测不存在证据的私有工具注册、submit outputs 端点。

本轮没有改变生产业务代码、账号设置或部署。桥接检查前后均为 false。没有运行提示信封、工具定义转文本或输出解析桥接。

## 验证方法

- 使用刚才授权测试账号产生的私有认证 Cookie 和自建 sandbox 状态，直接请求 Prism；没有使用 HAR 凭据。
- 绕过 sub2api 的本地“关闭桥接时拒绝 tools”判断，也不经过其输出白名单过滤，直接检查 Prism 原始 JSON。这是独立诊断脚本，不是放宽线上校验。
- 每项使用独立 conversation ID，同一自建测试项目；工具 schema 和测试数据均为合成内容。
- start 发送 HAR 已确认的 input、metadata、conversationId，并直接添加标准 OpenAI tools/tool_choice 字段；status 原样回传不透明 turn_state。
- 检查开始、pending、完成状态，以及原始 `response.payload.output` 的类型，不能把 HTTP 200 或消息中的 JSON 当作工具调用。
- 自定义函数为 `lookup_validation_value`，参数对象要求一个 `lookup_id` 字符串，`additionalProperties=false`、`strict=true`。函数结果只在客户端测试数据中存在。

## 5 项真实原生测试

| 测试 | 声明/输入 | 原生结果 | 耗时 |
| --- | --- | --- | --- |
| Responses 指定函数 | tools 为扁平 function 定义，tool_choice 为 type=function/name，parallel_tool_calls=false | HTTP 200 success，output 仅 message；模型说该工具不可用，没有 function_call | 14.56 秒 |
| Chat 格式指定函数 | tools 的定义放在 function 对象内，tool_choice 使用 function.name | HTTP 200 success，output 仅 message；模型说该工具不可用，没有 function_call | 7.22 秒 |
| 参数校验对照 | tools 故意为字符串而不是数组，tool_choice 指向未声明函数 | 仍 HTTP 200 success；输出是一段带 tool/arguments 的普通 message 文本，没有协议调用项 | 20.06 秒 |
| 原生内置工具对照 | 不声明自定义工具，请内置执行工具计算 182 × 217 | pending 进度出现 exec，最终 message 精确返回 39494 | 14.76 秒 |
| function_call_output 输入 | 合成合法结构的前次 function_call、匹配 call_id 的 function_call_output 和后续 user；tool_choice=none | HTTP 200 success；未读出唯一测试值，回答 No function_call_output supplied. | 8.20 秒 |

五项原始终态 `output` 都只有一条 `message`，没有 `function_call` 或 `custom_tool_call`，也没有需要客户端行动的中间态。

参数校验对照说明 tools/tool_choice 在该路径并未按标准工具接口的方式校验或生效；普通消息即便写成 `{"tool":...,"arguments":...}`，仍不是可直接交给 SDK 的原生工具调用。本轮没有解析或执行这段文本。

结果回传项是独立的输入兼容性探测，不是已经发生过一次真实自定义调用的完整闭环。由于前面未返回任何可供客户端执行的原生调用，真实调用闭环未成立，不能声称已完成后半段协议验证。

## start 状态的补充证据

只比较字段与布尔匹配，不公开私有 turn_state：

- 两种 tools 声明中的专用 description 均未出现在返回的 `turn_state.prompt` 中。这不能单独证明工具不会通过其他内部字段传递，但与实际“工具不可用”和非法工具字段未校验的结果一致。
- 合成工具结果的唯一值确实位于请求 input 的 function_call_output 中；同一值不在 start 返回的 `turn_state.prompt` 中。模型也未读出该值。
- 内置执行工具出现在 `codex_live_progress.toolCalls`，属于远端执行进度；终态没有要求客户端再执行一次。

## HAR 交叉核对

来源：`/www/shop/prism.openai.com_2026_09_16_19_39_42.har`。仅解析，未用其中的任何认证信息请求上游。

| 项目 | 观察结果 |
| --- | --- |
| start | 8 次；tools/tool_choice/parallel_tool_calls 均 0/8，input 共 18 项全为 message |
| status | 55 次，请求只有 request_id 和 turn_state |
| 状态 | 8 started、47 pending、8 completed；8 个完成结果均 success |
| 完成 output | 8 次各 1 个 message；没有客户端 function_call/custom_tool_call 或工具结果项 |
| 等待客户端执行/回传 | 没有 requires_action/required_action，也没有捕获 submit outputs 类端点 |
| 进度工具 | exec_command 13 条、view_image 2 条可见记录；按请求/调用/行索引去重后相同，不保证等于服务端全部执行次数 |
| 进度结构 | line_index、call_id、name、call_type、status、arguments_preview、source；call_type=function_call 位于进度，不能当成客户端执行命令 |

可解析的参数预览中，exec_command 使用 cmd/max_output_tokens，view_image 使用 path；这是捕获结构，不是注册接口或完整 JSON Schema。部分 preview 不能解析为完整 JSON，不可可靠重放。

HAR 本身没有发送自定义工具，单靠它既不能证明支持，也不能证明不支持；当前结论结合了上述直接提交工具字段的真实请求。

## 后续边界

此次不以强制透传、解析模型普通文本或转发远端已执行工具来冒充原生工具支持，不修改账号桥接开关。若后续继续优化，可依据公开进度完善内置工具展示；标准客户端工具能力则需要已证实的原生注册/暂停/回传协议，或另行决定是否使用可选桥接。

临时认证状态、请求体中的 sandbox 授权和原始响应仅留于 0700 的诊断目录，验证收尾删除；此报告不含凭据。现有 9999 与 10000 容器不重启。
