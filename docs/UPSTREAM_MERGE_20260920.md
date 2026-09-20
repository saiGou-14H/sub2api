# 官方 upstream/main 合并验证记录（2026-09-20）

## 合并范围

用户要求在本地main合并官方fork源最新版本。仓库 `/root/project-development/A2AMesh/source-sync/sub2api` 的origin是 `https://github.com/saiGou-14H/sub2api.git`，官方来源upstream是 `https://github.com/Wei-Shaw/sub2api.git`。

本轮fetch固定目标 `fbb9006adef852c46f0c7f18b0a8a740722cfac7`，提交时间 `2026-09-20 14:57:35 +0800`，标题 `Merge pull request #7402 from Wei-Shaw/fix/7268-openai-http2-keepalive`。版本文件为0.2.7。共同祖先 `98d86915becae9fe9491a91ffc6defd5235c8d2b`；官方侧283个尚未合入提交，569文件、33125新增行/2330删除行。目标指的是本轮fetch时点，不代表未来持续跟随远端。

本地合并前main为 `22ce8f564155eb377804ccd0bb28e177ad460237`。保护分支 `backup/main-before-upstream-20260920-22ce8f564` 指向它。合并提交 `99a19620e` 的两个父提交分别为原本地main和官方目标；随后以 `c83fae761` 单独提交WS测试同步修复。采用非快进合并，保留全部本地提交历史，没有rebase、硬重置或强推。既有未跟踪SYNC_REPORT.md保持原样，SHA256 `b123a7a906a62c142ab9f0d5348658e4bc01d04b4e54afb60f2e648c42f3d649`。

## 冲突与兼容

只有两处显式文本冲突：

- `backend/cmd/server/wire_gen.go`：保留上游提前构造OllamaCloudUsage并传入RateLimitService的新依赖顺序，删除旧位置的重复定义；保留本地CodexTicketHandler参数、runtime初始化和退出清理。上游PluginKVStore及SetAccountDirectory接线也保留。
- `frontend/src/views/admin/SettingsView.vue`：保留上游siteBillingMode逻辑，以及本地Web prompt tools开关和Codex设置组件入口。

其余自动合并后，重点静态核对：Web/Prism提前分流仍在；HTTP/透传/WS HTTP bridge在最终body和身份头之后执行STATE注入；原生WS后续启用的重连检查、按账号/身份/模型/策略scope隔离、Pro/Team与复验守护保留。专用采集HTTP1/禁keepalive/独立池不受上游OpenAI HTTP2保活修复影响。独立只读审查未发现上述范围有确定接线丢失或API适配错误。

Go依赖沿用上游升级后的go.mod/go.sum，工具链为已安装Go1.27.1（go.mod最低1.27.0）；前端package及lock本轮未更改，保留fork已有pnpm9.15.9声明与安全pin，未重新安装依赖或解锁版本。

## 验证结果

- 前端全量单worker Vitest：303文件、2444测试PASS，无跳过，154.01s。日志 `/tmp/sub2api-upstream-merge-frontend-vitest.log`。
- Linux部署检查：docker-compose-security、docker-compose-gateway-env、docker-runtime-resources、Caddyfile cache四项PASS。
- Apple Container脚本 `bash -n`通过。其行为测试因GNU/Linux不支持测试内BSD `stat -f '%Lp'`而中止；官方CI该任务指定macos-15，未改脚本规避平台限制，不能声明macOS实机验收。
- 前端typecheck、lint:check、vue-tsc -b和Vite生产构建PASS（1060 modules、18.91s）；构建前i18n检查额外3项PASS。因限定前端源码范围，构建输出 `/tmp/sub2api-upstream-merge-frontend-dist`，随后同步到正常Go嵌入目录；179文件/177assets、index的7个本地引用全部存在。前端完整日志为 `/tmp/sub2api-upstream-merge-frontend-{typecheck,lint-retry,build}.log`。
- 全后端首次执行 `go test -mod=readonly -p 1 -ldflags='-s -w' -tags unit ./... -count=1 -timeout=360s`，全部包完成，唯一失败为service中的上游新增 `TestOpenAIGatewayService_ProxyResponsesWebSocketFromClient_SameCodexThreadStillPreempts`；其余包通过，包括handler37.836s、admin0.398s、repository3.778s、routes8.545s、migrations0.003s、pluginapi0.004s。原始日志 `/tmp/sub2api-upstream-merge-backend-unit.log` 保留，不能将这次失败运行称作全绿。
- 该失败是测试同步问题：production有意异步向旧连接发送1013关闭帧，让新连接无须等待关闭握手；测试却在新连接完成后立即放行旧上游的完成事件，造成完成帧与关闭帧竞争。仅调整测试helper：不同线程放行旧请求；同线程保持旧请求在飞直到读取真实关闭帧，再清理gate。未改业务抢占语义，保留1013/精确reason/恰好一个preempted及不同线程都正常完成的全部断言，没有sleep或忽略错误。
- 修复后两个线程场景各重复30次，共60次PASS（service0.084s）；日志 `/tmp/sub2api-upstream-ws-preempt-targeted.log`。随后完整service复验PASS，190.919s，日志 `/tmp/sub2api-upstream-merge-backend-service-final.log`。其余包源码未变，沿用本轮首次全包运行中的通过结果；最终所有backend unit包均有通过记录，没有跳过或屏蔽失败用例。未重新运行全量integration/race或Go lint，不能把这些表述为本轮已通过。
- 静态二进制构建PASS：`CGO_ENABLED=0 go build -mod=readonly -p 1 -tags embed -ldflags='-s -w -X main.Version=0.2.7-upstream.c83fae761 -X main.Commit=c83fae761 -X main.BuildType=source'`。产物 `/tmp/sub2api-upstream-merge-server`；SHA256 `0094a4eeb54ea1d7e47a6d07333021f9118069fd63bb0be2a40272a38b4828bf`。嵌入本轮新前端，版本输出确认0.2.7-upstream.c83fae761；这是测试产物，未创建生产镜像。日志 `/tmp/sub2api-upstream-merge-build.log`。
- 隔离冒烟10组PASS：Docker --internal网络、新PostgreSQL18与Redis、fresh启动迁移健康；实际查询新engine_meta=JSONB、OpenCode quota CHECK、无三档无限额行；401鉴权、默认off、CAS/代理保护、控制版本、账号CRUD保留、账号状态/策略隔离与非法输入拒绝、嵌入assets、正常退出。结果 `/tmp/sub2api-upstream-merge-smoke-result.json`，脚本 `/tmp/sub2api-upstream-merge-smoke.py`。全程无真实上游请求，资源在finally清理；这证明全新数据库迁移，不代表生产历史数据的升级演练。

官方新增三份迁移涉及OpenCode平台约束、清理三档限额均NULL的user_platform_quotas行，以及可空content_moderation_logs.engine_meta；验证使用全新临时数据库，不连接或迁移生产库。

## 运行边界

本轮只进行源码合并和本地验证，不推送、不部署9999或10000，不操作生产账号开关或真实上游模型请求。生产9999仍运行此前 `local/sub2api:codex-turn-state-f46e71fa5`；此前本地STATE增强与本次官方合并均未自动上线。
