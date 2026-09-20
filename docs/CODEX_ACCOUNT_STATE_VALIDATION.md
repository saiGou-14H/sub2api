# Codex 账号状态、注入观测与自动刷新验证

## 范围与分支

2026-09-20，用户要求在 `main` 上继续优化已发布的 Codex turn-state 功能：在账号管理列表展示实际请求头注入结果、失效自动刷新、按账号/模型查看各状态，并明确要求每个账号的 state 严格隔离，不能共用。

重新 fetch 后 `origin/main` 仍为 `b78dd86a4ecc74a1291d14efb30c6a5a59c4de5e`。先将已验证的功能分支 `feat/codex-turn-state-main-20260920@76cca68ce` 快进到本地main，再在 `/root/project-development/A2AMesh/source-sync/sub2api` 修改。保留原有 `SYNC_REPORT.md`，SHA256仍为 `b123a7a906a62c142ab9f0d5348658e4bc01d04b4e54afb60f2e648c42f3d649`。

参考仓库远端HEAD与本地一致，为 `gylive/ccodex-sleep-state@b18fabf9ad8e9d7af7d9d0306b623ba6091a39d6`。阅读 `internal/turnstate/store.go`、`internal/gateway/engine.go` 的状态、提前刷新、失效和快照机制；保留sub2api已验证的账号隔离、捕获TTL、Redis租约/版本围栏及限流规则，不引入参考项目客户端配置接管或代理池轮换。

本轮没有推送、重新部署9999、改动10000，未访问真实上游或更改生产账号配置。

## 实现与关键边界

- 账号列表新增Codex状态与最近注入提示；点击展开按模型状态、捕获/过期/刷新/退避时间、有限错误分类、最近决策及注入记录。
- `header_set`仅在实际设置出站头之后记录，不能解释为上游接收或生成成功；采集ready与请求注入记录分开显示。
- 新接口 `GET /api/v1/admin/settings/openai-codex-ticket/accounts?account_ids=1,2` 使用既有管理员鉴权；仅接受1–100个显式正整数ID并去重；仓库批量取账号，Redis单次pipeline最多1600个账号模型组合，无全库扫描。
- raw票、脱敏metadata和24小时固定窗口聚合分别存储；raw票维持原TTL，metadata只保留捕获/过期时间。观测不返回state、token或身份摘要。
- 完整隔离键包含revision、account ID、stable identity、final model。A和B即使同身份同模型也不共享；换身份/版本不展示旧记录。重新采集A不触碰B。
- 观测写失败不改变业务请求结果，使用有界context和专用短socket超时视图，共享原连接池，不创建每请求独立连接池或失控goroutine。计数为尽力观测，不能用于计费审计。
- query过程中消耗的时间计入返回服务端时钟，并在返回前重新核对control/proxy/ticket期限；异常metadata或Redis读取失败显示unavailable而非ready/disabled。
- 控制器原有2秒扫描实现提前刷新、过期和缺票自动重新采集；新增回归证明它仍受预算、普通退避和账号身份共享冷却约束。没有新增“点击查询就发模型请求”的操作。
- 前端单一调度器每5秒对当前可见页分批查询；隐藏页暂停、恢复重读、卸载/筛选/翻页取消，旧代响应失效；到期清除可用标记并重读，查询失败清除过时成功。每行严格匹配自己account ID。

## 第二阶段：state-kit参考增强

用户追加指定 [wangyunjeff/sub2api-state-kit](https://github.com/wangyunjeff/sub2api-state-kit)，固定参考版本 `d4d67c4f4cd3590dd04a88aa3c26f48747fc54fb`，本地只读克隆 `/root/project-development/sub2api-state-kit-reference-20260920`。阅读其账号配置、两阶段采集/原出口复验与请求快照守护实现，没有执行prepare脚本、覆盖整份overlay或安装外部插件。

增加显式账号策略inherit/pro/team、独立PolicyScope、采集与原业务出口两阶段严格模型校验，以及精确快照失效。state/metadata/observation按完整scope隔离，认证和429冷却保持原稳定账号身份维度，策略变更不能清掉限制。

业务观察器位于 `backend/internal/service/codex_ticket_watchdog.go`，仅接收实际注入时附加的私有receipt，成功完整响应才识别模型不符或312实验信号；不预读、修改或重放业务流，异常回调有短期限。新增透明性测试覆盖分片大小1/17/4096、SSE/JSON、失败/限流、缺字段、截断、超长帧、关闭与读错误、无receipt/客户端自带头等。Redis条件作废比较该票据的key、state和捕获时间；并发旧响应不能误删新票。

前端第二阶段提交 `bb0d4952d`，新增 `CodexTurnStatePlanSelect.vue` 与Create/Edit/Bulk集成，扩展状态详情。真实浏览器复验（全API mock）桌面和手机均通过独立Pro/Team选择、当前复验与失效记录、到期/错误清除旧可用标记；两个viewport均0pageerror、无横向溢出，每个3次状态查询。复验结果覆盖前述临时artifact路径；预览再次停止并移除入口。

## 验证记录

前端初次完整回归：9文件286测试PASS（API、composable、组件、账号开关、设置、创建、编辑、批量和AccountsView）；正式构建包含locale completeness 3项，通过类型检查及生产bundle。日志：`/tmp/codex-account-state-frontend-tests.log`、`/tmp/codex-account-state-frontend-build.log`。后续审查修订的最终结果以本文件收尾记录为准。

Playwright真实浏览器、全部API mock、禁止外部请求：1440×1000桌面与390×844手机均通过账号A/B隔离、详情、按API刷新过期状态、查询失败清除旧成功、无横向溢出与无pageerror。截图为 `/tmp/codex-account-state-{desktop,mobile}-{list,detail}.png`；结果 `/tmp/codex-account-state-browser-result.json`。验证采用本轮独立loopback组件入口，没有连接生产；验证后停止预览并删除两个临时入口。没有声称截图经过视觉模型审阅。

第二阶段最终前端：9文件344测试PASS；locale completeness额外3项PASS，vue-tsc与正式production bundle通过。最终日志 `/tmp/codex-state-kit-frontend-final-tests.log`、`/tmp/codex-state-kit-frontend-final-build.log`。`7bbf52b5a`修正混合模型详情：账号最严重汇总不否认另一个仍有效模型的实际复验结果，自己的过期/作废及账号明确不可用仍阻断通过文案。

第二阶段后端定向回归：service 7.214s、repository 0.380s、admin 0.018s通过。首次测试仅发现两处断言问题：Go时间含monotonic元信息时应以Time.Equal比较实际时间，以及io.NopCloser返回值不是指针不能用require.Same；修正相应测试后再运行下述完整回归，未降低业务断言。

五包完整 `-tags unit -p 1 -count=1`：service 169.235s、repository 3.704s、admin 0.393s、routes 8.585s、cmd/server 0.016s全部PASS，日志 `/tmp/codex-state-kit-backend-full.log`。随后service/repository定向race分别9.514s、1.739s通过，日志 `/tmp/codex-state-kit-backend-race.log`。独立只读审查未发现有明确复现路径的中高严重度阻断项。

收尾审查还修正并测试：状态接口复用GetByIDs WithProxy批量快照（最大100账号×16模型：账号批量1、Proxy逐条0、Redis batch1）；失效回调不再通过普通长socket Redis读，直接走短超时、禁重试且内部围栏的原子Lua；采集worker额外受预算一半约束，预算2/并发2时可以完成1次capture+1次verify，不把两份预算都花在候选上。预算1无法完成一轮，操作说明已明确。

最终嵌入构建成功；同一二进制在Docker `--internal` 网络、新建PostgreSQL18与Redis中通过9组真实接口/生命周期冒烟：新库迁移启动、未登录401、默认关闭revision0、CAS冲突/代理脱敏/引用保护、代理变化revision+运行时发布、账号编辑/批量保留与参数验证、受保护有界状态API、嵌入静态资源、正常退出。状态API验证包含账号A保存pro目标292、B保存team目标332、非法plan400、两个同上游身份账号独立enable与status、无secret字段、ID边界；全局关闭，未向任何真实上游发送请求。结果 `/tmp/codex-state-kit-smoke-result.json` 为PASS，临时容器和网络由finally清理。

## 本地功能提交

在快进初版到本地main之后，本轮按验证完成的独立功能提交；没有push：

- `2e48cacad`：账号列表、详情、隔离状态轮询与注入历史。
- `55ff6748e`：注入历史和独立模型到期边界。
- `5d2e246d5`：前端最严重状态聚合对齐后端。
- `f6596455a`：账号状态API与共享注入观测。
- `108adfa6a`：真实HTTP builder账号隔离回归。
- `bb0d4952d`：每账号inherit/Pro/Team配置和复验、失效详情。
- `7bbf52b5a`：混合模型详情按各模型自身验证事实展示。
- `2a741167c`：策略/业务出口隔离、两阶段probe、精确条件失效与有界预算。
- `6eaecb3fb`：实际注入receipt接线和透明完整响应守护。

最终二进制由功能提交 `6eaecb3fb` 构建，版本 `0.2.4-state-kit.6eaecb3fb`，`CGO_ENABLED=0 -tags embed`。产物 `/tmp/codex-account-state-main-server`，SHA256 `cee348a55e1aa825f97276608af929103c32138cf0e8a72b6078873dbd0bb05d`；最终前端嵌入该二进制。没有打新生产镜像或修改部署Compose。

## 开发命令

沿用Go1.27.1工具链与共享模块缓存，串行编译避免资源竞争；临时编译目录使用本轮专属 `/dev/shm/codex-account-state-build`，不清理其他缓存或服务数据。

```sh
export PATH=/root/project-development/A2AMesh/source-sync/toolchains/go1.27.1/go/bin:$PATH
export GOMODCACHE=/tmp/sub2api-pr7315-review/gomodcache
export GOCACHE=/tmp/sub2api-pr7315-review/gobuildcache
export GOTMPDIR=/dev/shm/codex-account-state-build
export GOMAXPROCS=2 GOGC=50 GOMEMLIMIT=2200MiB
go -C backend test -p 1 -ldflags='-s -w' -tags unit ./internal/service ./internal/repository ./internal/handler/admin ./internal/server/routes ./cmd/server -count=1 -timeout=360s
```

前端必须在 `frontend` 工作目录用 `corepack pnpm`（9.15.9），不修改lockfile或安装依赖。
