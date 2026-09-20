# Codex turn-state 开发与验证记录

## 开发范围

- 基线：`saiGou-14H/sub2api origin/main@b78dd86a4ecc74a1291d14efb30c6a5a59c4de5e`（开始开发时重新fetch确认）。
- 独立分支：`feat/codex-turn-state-main-20260920`，不跟踪推送目标。
- 工作区：`/root/project-development/sub2api-codex-turn-state-main-20260920`。
- 原main工作区与旧 `feat/codex-turn-state` 工作区保留；未并入另一个Prism account-test修复分支。
- 阅读原架构文档和参考仓库 `gylive/ccodex-sleep-state@b18fabf9ad8e9d7af7d9d0306b623ba6091a39d6`。旧本地实现逐项移植到main，重新验证后按功能提交，并修复本轮审查发现的边界问题。
- 未推送、未部署生产、未导入生产数据库/账号、未发真实模型探测。

## 已实现功能

- settings全局CAS配置、选择现有代理、事务保护及版本失效。
- 账号显式布尔开关及稳定身份隔离；新建/编辑/批量界面。
- 固定worker、队列32、分页100、30秒任务期限、共享探测预算。
- Redis leader租约、6秒控制新鲜度、版本与owner提交围栏；过期票据和旧身份不能注入。
- 每账号独立监视任务，2秒轮询与2秒查询界限；关闭/删除/身份/支持类型变化或检查失败取消，并join退出。普通取消不记失败。
- 有界SSE解析，仅完成事件加目标长度匹配才缓存；HTTP1专用采集transport且不复用连接，不重定向，不回退直连。
- 采集认证和配额拒绝共享有限冷却，按account+identity而不按model/revision/proxy；普通失败按模型退避。
- 普通/透传HTTP及WS HTTP bridge接入；V2 compaction_trigger与旧compact跳过实验而保留echo guard；按真实最终出站模型匹配。
- 状态UI区分保存、应用、局部计数、代理与有限错误分类；异步读取/代理测试可取消、CAS冲突不会覆盖草稿。
- Shutdown先取消并等待采集退出，再释放依赖；不将join超时伪装成已退出。

## 本地功能提交

| 提交 | 内容 |
|---|---|
| `1fa365a68` | 全局设置、版本API与代理事务保护 |
| `c5f8cb58d` | 前端设置、代理选择、账号开关 |
| `b3ffbe64c` | 账号opt-in与稳定身份隔离 |
| `b6a400ef6` | UI timeout/retry错误分类 |
| `f5602f1b9` | 有界采集、Redis围栏缓存 |
| `807715523` | 专用HTTP1探测与SSE校验 |
| `5144464ad` | HTTP/WS、Wire与生命周期集成 |
| `97a9c00f6` | 共享冷却与SSE配额分类 |
| `ccac3e431` | compact V2排除、真实最终模型回归 |
| `39f5b34ae` | 采集禁用连接复用 |
| `209e1d6fd` | 账号在途取消与取消错误隔离 |
| `f46e71fa5` | 已观察拒绝跨代保留、限制发布失败时暂停采集 |

功能代码HEAD为`f46e71fa5`，最终文档提交见本分支 `git log origin/main..HEAD`。

## 验证环境与命令

Go工具链为 `/root/project-development/A2AMesh/source-sync/toolchains/go1.27.1/go/bin`，实际 `go test` 使用现有模块缓存，不更新依赖。资源约束：`GOMAXPROCS=2`、`-p 1`；Go构建串行运行。

```sh
export PATH=/root/project-development/A2AMesh/source-sync/toolchains/go1.27.1/go/bin:$PATH
export GOMODCACHE=/tmp/sub2api-pr7315-review/gomodcache
export GOCACHE=/tmp/sub2api-pr7315-review/gobuildcache
export GOTMPDIR=/tmp/sub2api-pr7315-review/buildtmp
export GOMAXPROCS=2 GOGC=80 GOMEMLIMIT=3200MiB

go -C backend test -p 1 -tags unit ./internal/service ./internal/repository ./internal/handler/admin ./internal/server/routes ./cmd/server -count=1 -timeout=360s
```

前端在本工作区 `frontend` 目录执行 `corepack pnpm`，版本9.15.9。开发阶段临时软链复用主工作区依赖，无安装、无lockfile变更；交付前移除软链。

```sh
corepack pnpm exec vitest run src/api/admin/__tests__/codexTicket.test.ts src/components/account/__tests__/codexTurnState.spec.ts src/components/settings/__tests__/OpenAICodexTicketSettings.spec.ts src/components/account/__tests__/BulkEditAccountModal.spec.ts src/components/account/__tests__/CreateAccountModal.spec.ts src/components/account/__tests__/EditAccountModal.spec.ts src/views/admin/__tests__/AccountsView.bulkEdit.spec.ts
corepack pnpm run typecheck
corepack pnpm run build
```

前端最终相关回归：7文件、272测试通过；构建另外执行localeKeyCompleteness 3项并通过。全部19个变更TS/Vue文件ESLint通过。构建的Browserslist陈旧数据、既有bundle体积提示不阻止构建，未因此修改依赖。

浏览器验证使用已安装Playwright/Chromium，只挂载本功能组件，API全模拟并禁止对外请求。1440×1000桌面和390×844手机视口，检查代理选择、保存、展开高级设置、无横向溢出和无pageerror。截图：`/tmp/codex-main-20260920-desktop.png`、`/tmp/codex-main-20260920-mobile.png`。临时loopback预览已停止，两个临时入口文件已删除。截图未经过视觉模型审阅；页面断言来自真实浏览器DOM/交互。

设置/代理实际PostgreSQL事务回归使用testcontainers临时数据库与Redis，`-tags integration ./internal/repository -run CodexTicket`已通过，不连接生产数据。

## 本轮发现及处理

1. 原账号开关只阻止最终提交，无法取消已发probe。新增有界监视与取消join，并保留失败写回前fresh检查。存储调用期间取消不误记commit/retry故障。
2. 逐模型/版本退避可被切换模型或配置绕过。引入同账号身份共享冷却；明确SSE稳定quota错误与capacity区分，不按自由文本分类。
3. 共享限制仍使用generation fence会在配置切换时丢掉已观测拒绝。最终实现将真实拒绝作为单独的单调限制，在取消早退之前有限收尾；票据与普通retry仍受原生命周期保护。
4. 新增真实Forward测试最初错误假设passthrough重写普通账号模型映射。核对main约定后改测试：透传保持wire模型；业务实现未因此改变。
5. 原实现允许H1连接复用，与原架构要求不同；本轮仅对采集profile设置DisableKeepAlives，真实两次本地请求验证不同连接、H2不上协商，业务池继续复用。
6. 初次前端命令从repo root运行触发错误pnpm11。确认frontend packageManager后改为在frontend使用corepack pnpm9.15.9，未改版本声明。
7. 一轮全包测试期间共享冷却合同正在调整，repository旧的“旧revision不得延长限制”断言失败；在最终代码冻结后重新验证，不能将那轮当最终通过结果。

## 已知限制

- 跨轮重用turn-state属于实验行为。长度与格式、TTL只提供本地控制，不验证上游签名、不保证质量改善/套餐提升/消除限流。
- 正常账号取消约2–4秒加调度余量；完全落在轮询间隔内的关闭再开启可能未被观察。不能撤销上游已经发生的计算。
- 显式真实上游拒绝即使同时取消也应保留有限共享限制；普通Canceled而无拒绝信息不生成限制。
- 已发出的并发请求不能追溯撤销；共享限制约束发布之后的新采集admission。Redis持久化失败且进程消失时，内存里的未持久事实不能凭空跨进程恢复。
- 运行计数为本实例观察值；没有实现整集群票据枚举、全局健康断言。
- 选用固定出口代理不保证新连接更换IP；不导入订阅、不给真实账号自动轮换出口、不自动重放正式生成。
- 全功能在隔离模拟环境验证；真实账号模型权限及实验效果未测试。

## 最终结果

冻结后的最终后端全包验证通过（`/tmp/codex-main-20260920-final-backend.log`）：

| 包 | 结果 |
|---|---|
| internal/service | PASS，168.051s |
| internal/repository | PASS，3.438s |
| internal/handler/admin | PASS，0.386s |
| internal/server/routes | PASS，8.593s |
| cmd/server | PASS，0.016s |

race定向检查通过（`/tmp/codex-main-20260920-race.log`）：service 7.845s、repository 1.183s，未报告数据竞态。覆盖`CodexTicket|CodexProbe|CodexHarvest`。首次race依赖构建使系统盘低于1GiB，主动停止并仅移除该任务`go-build660972108`临时目录；保留缓存后改用专属`GOTMPDIR=/dev/shm/codex-main-20260920-race`，降低编译内存并以`-ldflags='-s -w'`重试通过。未清理其他构建缓存或服务数据。

```sh
mkdir -p /dev/shm/codex-main-20260920-race
GOTMPDIR=/dev/shm/codex-main-20260920-race GOGC=50 GOMEMLIMIT=2200MiB \
  go -C backend test -race -p 1 -ldflags='-s -w' -tags unit ./internal/service ./internal/repository \
  -run 'CodexTicket|CodexProbe|CodexHarvest' -count=1 -timeout=240s
```

静态嵌入构建通过：`CGO_ENABLED=0 go -C backend build -p 1 -tags embed -ldflags '-s -w -X main.Version=0.2.4-codex-turn-state.f46e71fa5 -X main.Commit=f46e71fa5' -o /tmp/codex-main-20260920-server ./cmd/server`。

产物`/tmp/codex-main-20260920-server`，SHA256为`7b86dda3a9498e7155a7af362b4855a7972317e95a649437a1e691f5a5145f4f`。

收尾仅清理经两次核对、二进制内含本轮独立工作区绝对路径的403个Go缓存产物（5,520,032,812字节）。未清理整个共享缓存，未删源码、测试日志或最终二进制；系统盘余量恢复至6.2GiB，二进制校验值不变。之后重新测试会重新编译这些本工作区包。

最终二进制在专属Docker `--internal`网络、全新PostgreSQL和Redis上冒烟全部通过（`/tmp/codex-main-20260920-smoke-result.json`）：

1. 新库启动、全部迁移、health正常。
2. 新管理员路由未认证返回401。
3. 默认全局关闭、revision为0。
4. CAS旧版本返回409、代理脱敏、不允许删除仍被选择的代理。
5. 代理凭证变化提升revision，后台控制器应用新版本。
6. 账号新建开关、编辑保留、批量关闭及非布尔值拒绝。
7. 嵌入HTML与入口JS/CSS资源正常提供。
8. 应用正常退出，采集生命周期有序收尾。

该测试只使用临时管理员/假账号/假代理，全部外部网络隔离，不访问真实模型。临时容器和网络已删除，专属tmpfs目录已删除，前端依赖软链与预览入口已移除。原main工作区仍为`b78dd86a4`，其既有未跟踪`SYNC_REPORT.md`校验值保持`b123a7a906a62c142ab9f0d5348658e4bc01d04b4e54afb60f2e648c42f3d649`。
