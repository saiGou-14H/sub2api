# WebCodex → sub2api / DSH：V6 实施进度

> 当前阶段：累计 20 条代码／功能修复提交（Server 8、DSH 12），另有两条 fixture-only 验证提交不增加功能数。Server 代码基线 `efa85831ef08995c54ab63dd33bc57afd7106acb`，DSH 代码基线 `655b2a643e49020f32641839753d45edf631ff06`。Server legacy process Job records／Start／Get／List／Log／Stop 与 job_update 已提交；正式 Linux bwrap／full＋显式 process-group 的受限 native Service／provider／Runner consumer 已提交。Go 854 reconciliation 与 DSH dc745 JobManager／FIFO／update 集成仍进行中、未提交，不计完成；本快照 Server inventory／reconciliation／log_snapshot 仍拒绝。共享 fixture 保持 91 DTO／16 families／12 operations；整体约 15%（10%～20%），完整 G2／G0、持久 MCP 闭环与生产替换未完成。

逐项已完成／待办见[开发状态总表](./WEBCODEX_V6_DEVELOPMENT_STATUS.zh-CN.md)；工程完成度估算和历史快照见[项目进度评估](./WEBCODEX_V6_PROGRESS_ASSESSMENT.zh-CN.md)。本页保留各阶段的具体实现与验证回执。

本页阶段按发生顺序保留历史，包括“RO／WW 一概拒绝”“正式 bwrap 在研”和“Job 尚未开始”；当前以顶部摘要、当前实现表与末尾第二十条阶段为准。两侧可能有其他 agent 未提交改动，本次只保存已指定代码基线的五文件白名单 docs-only 本地提交。

## 开发位置与范围

| 仓库 | 分支 | 开发目录 | 创建起点 |
|---|---|---|---|
| sub2api | `mpc-server` | `integration/sub2api` | `d9f5b7d75ecb9b8bd944326a0f3b161b1c217ee1` |
| DSH | `mcp-runner` | `integration/deepseek-harness` | `b733bc9c812b0202d137ebd7b238f52fab7ad81f` |

用户最终指定的 Server 分支拼写是 **`mpc-server`**。`mcpserver` 是已更正的旧名。原字段、功能和状态以 V6 开发设计（工作区引用：`SUB2API_DSH_DEVELOPMENT_DESIGN.zh-CN.md`） 为依据；固定 WebCodex 源码为 `source-sync/webcodex` 的 `97ad66949a859174911c2f6da2ff1063be98bfa9`。实现只修改 `integration/` 开发副本。现有验证完成的迁移代码已按功能建立 20 条本地代码／功能修复提交（Server 8 条、DSH 12 条），Server 当前代码基线为 `efa85831ef08995c54ab63dd33bc57afd7106acb`，DSH 为 `655b2a643e49020f32641839753d45edf631ff06`（包含两条不增加功能数的 test 验证补充）；文件阶段清单及后续进程提交见[项目进度评估](./WEBCODEX_V6_PROGRESS_ASSESSMENT.zh-CN.md)；未推送、部署、修改运行中的 `/opt/deepseek-harness` 或生产服务，未读取真实 Runner / 模型凭据。

## 本批代码（当前二十条阶段）

| 位置 | 当前实现 | 尚不代表 |
|---|---|---|
| [Go protocol](../../backend/internal/webcodex/protocol/README.md) | 原注册、视图、poll、result、offline DTO；27 项 RunnerRequest 顶层字段；严格 required/null/default、整数和 Unicode 解码；六类直接请求的闭合 Invocation；原 Job DTO／生命周期及 serde 输入形式 codec | 全部 family/nested domain 已验证或可以执行；外层 RunnerRequest 数组和 general 非 Job 替代形式仍有 gap |
| [Go runner](../../backend/internal/webcodex/runner/README.md) | 原 Bearer 凭据类型/scope/client/owner/group 检查；managed token 原哈希、撤销／到期及宿主不可变身份验证；原 12 字段 SQL repository／迁移和当前宿主用户适配；公开 create／register_hash／list／revoke 管理；内存 registry 与 legacy process Job 独立 records／Start／Get／List／Log／Stop；五条 transport 路由（含 job_update）已接默认关闭配置／DI，另四条凭据管理共九路径；启用需六项正数限制 | 真实 PostgreSQL 约束／事务验收、显式全量导入、跨 account family 独立 hash 检查、用户 dispatch HTTP／MCP 或持久执行记录 |
| DSH runner（工作区引用：`integration/deepseek-harness/packages/runner/runner/README.md`） | Cordis `runnerRuntime`；原 HTTP polling、同步请求/结果编解码及进程内保留；独立 Job DTO／start_process_job／stop_job codec；可选 `./files` 原生文件和 `./native` 文件／Linux 严格进程组合入口；OS API 接受与精确目标退出回执，PGID 用私有 fd3 socket；Linux actual bwrap／full＋显式 process-group 的 prepareNativeImage／private bootstrap／Runner consumer 已提交；scope 仍用文件；能力位依据 provider 支持 | 完整 generation-2 准入、auto／scope／pkg confined、shell/script、process profile／环境／Windows 等价、持久 Host 审批；JobManager／FIFO／update 发送进行中未提交，非全 serde parity |
| [Go↔TS 检查](../../backend/internal/webcodex/runner/interop_test.go) | 六类请求跨语言往返、64 位整数极值、真实 Go 接口拒绝 DSH 部分能力注册 | ChatGPT 原生 Connector、真实文件/命令执行的端到端成功 |

六类直接请求为 `run_shell`、`run_process`、`run_script`、`file_read`、`file_write`、`file_list`。六类均有协议/分发入口；原生执行已覆盖后三类文件操作及当前 Linux `run_process` 子集（Linux actual bwrap／full＋显式 process-group 的受限 native 路径已提交，其余受限组合仍拒绝）。不将传输测试中的 fake executor 或 fabricated result 计为真实执行能力，也不将当前 process 子集记为完整跨平台等价。其他已知 family 明确返回 unsupported；全量能力仍保留在迁移计划中。

## 必须保留的源码行为

- 原 `kind` 和 `agent_instance_id` / `agent_protocol_generation` JSON 字段不改名，不引入新 NodeProtocol、租户、claims 或执行 epoch 字段。Go 导出成员为 `AgentInstanceID` / `AgentProtocolGeneration`。
- **generation 2 注册必须显式满足原 22 项基础能力**。仅声明 shell 和基础文件能力的 DSH 客户端当前会被拒绝；不存在 partial-feature 旁路，也不把未实现能力伪报为 true。
- 结构化 process/script 的能力位独立于原始 shell 能力位；shell=false 不禁用已明确支持的 typed argv/script。
- Runner transport 不接受普通模型 API key。offline 使用 `agent:register` scope，poll 使用 `agent:poll`，result 使用 `agent:result`。
- 同实例重连不重发已派发请求。不同实例替换不继承旧请求；旧实例不能取请求或提交结果。原访问组在任何重注册中都不变，包括 bootstrap 发起的新实例替换。
- 全局观察权限不赋予其他 owner 的受管节点执行权。取消、下线或替换后仍保存原 `Dispatched` 事实；已派发请求的超时不等于“未启动”。
- `RunnerView` 使用原 `online` / `stale`，`pending_requests` 是尚未交付的队列长度。HostContext 按原规则规范化，仅是描述信息。
- result 的成功响应仅表示内存 waiter 接收，**不是 durable ACK**。当前没有数据库事务、跨进程恢复或持久去重保证。
- 文件与进程真实执行必须先打通 DSH 的正式非 Agent owner。不能伪造 Agent、创建空 turn 或绕过 ToolRuntime/policy 来做出“能跑”的示例。

## 验证记录

已完成的 Go 检查（使用本地 Go 1.27.1）：

```sh
GOTOOLCHAIN=local GOMODCACHE=/root/project-development/A2AMesh/source-sync/toolchains/gomodcache /root/project-development/A2AMesh/source-sync/toolchains/go1.27.1/go/bin/go test -race ./internal/webcodex/...
DSH_RUNNER_CHECKOUT=/root/project-development/A2AMesh/integration/deepseek-harness GOTOOLCHAIN=local GOMODCACHE=/root/project-development/A2AMesh/source-sync/toolchains/gomodcache /root/project-development/A2AMesh/source-sync/toolchains/go1.27.1/go/bin/go test -run TestDSH -v ./internal/webcodex/runner
```

Go 协议与 registry 检查通过；后续权限修正后，registry race 回归也通过。跨语言两项检查通过：字段/整数往返一致；部分能力注册返回 HTTP 400，执行器调用次数为 0。Go loopback 服务只使用测试凭据，结果由 fixture 提供。

DSH 协议与传输阶段的 5 文件检查 **325/325 通过**，覆盖协议词法保真、HTTP/Loader 生命周期、跨 Server 缓存隔离、文件错误状态和不支持类别的提前拒绝；所有 shell `job_context` 在进入本地回调前被拒绝。该阶段 package 类型检查、13 文件 type-aware lint、Host 构建与 plain Node 无执行器 Service 生命周期冒烟通过。原生文件阶段的较新回执单列于后文。

早期两次 `doc-sync` aggregate 均为 **30 项通过、4 项失败**；失败项修正后均已逐项复测通过，完整 aggregate 未重跑，不宣称全量通过。最终相关检查回执归于各阶段记录。源码归属和许可证随两个目标目录保留：WebCodex 改编材料继续适用 Apache-2.0，DSH 独立 Host 集成保留 MIT。

DSH 新 package 的 `pnpm-lock.yaml` 只增加 workspace importer。全仓 `--lockfile-only --offline` 因无关 `apps/cli` 依赖缺离线 metadata 而失败，未改写原锁记录；按现有 workspace 链接补入 importer 后，以下冻结离线检查通过，未变更外部依赖版本：

```sh
pnpm --filter @deepseek-ai/dsh-runner install --lockfile-only --frozen-lockfile --ignore-scripts --offline
```

## Host 基础阶段验证

- 工具／scope／文件／沙箱聚焦 run 共 685 项：684 通过，1 项旧事件夹具缺少 `scope`；修正后该 invariant suite 3/3 通过。新增 Host owner、真实 Loader 文件和嵌套 PTC 最终聚焦检查 13/13 通过。
- Bash 与 PowerShell 工具消费者分别 90/90 和 66/66 通过。PowerShell 的 Host 符号链接工作区回归先复现 2 项失败，改为使用 canonical policy root 后整份 66 项检查通过，保留 Agent 原行为。
- Bash 前台清理覆盖真实 TERM-resistant 后代的直接退出、取消和超时，以及 provider 成功、失败和不可观察范围；PowerShell 的无解释器生命周期检查通过。真实 PowerShell 用例因本机没有解释器跳过，不能代替 Windows 验证。
- 手工 `ToolExecution` 夹具更新后，全量 `tsc -b tsconfig.host.json` 通过。相关源文件及测试的 type-aware lint 分批通过。Agent instructions／core invariant／shell-env 三份消费者测试最初 174 通过、1 项 root 权限假设失败；该只读夹具与原 sandbox chmod 夹具在仅移除 `cap_dac_override`／`cap_dac_read_search` 后各自通过，没有放宽产品策略或改掉断言。
- 全量 Host 编译通过后，11 个受影响 Host package 已完成 tsdown 构建。初次按 npm 名称过滤没有匹配配置，改用精确 workspace 路径过滤后成功。plain Node 仅导入构建后的 `lib/`，验证真实文件读后修改、未观察写入拒绝和 Host 撤销，且没有 Agent／Session／模型服务；冒烟检查通过。`tool-fs` 新增的四个测试 workspace 依赖已写入 importer；冻结离线锁检查通过，未改变外部依赖版本。
- 尚存环境验证限制：一次扩大 shell 检查中的第一轮 Agent cwd identity 用例缺少 `native/system/packages/linux-x64/bin/glibc/system.node`，未补建该无关 native artifact；不宣称所有 shell 集成或平台矩阵已通过。

## 正式 Host 执行基础

`ToolExecutionInput` 接受互斥的 Agent 或 `HostToolOwner`。Host owner 包含可信的固定工作区和撤销信号，同一对象同时作为注册 `ScopeKey` 与文件观察身份；它来自同进程提供方，不从 Runner JSON 反序列化。底层继续复用现有工具、文件、沙箱和进程服务，不创建 Agent、Session 或空 turn。

| 源码位置 | 当前实现与边界 |
|---|---|
| core/tools（工作区引用：`integration/deepseek-harness/packages/core/tools/src/index.ts`） | readonly `exec.scope` 路由查找、guard、展示、执行事件和 timeout；调用方与 Host 撤销信号在策略前融合，环绕层换信号后仍可撤销，结果结算释放监听器。Host 在实际工具体前再次检查单调 guard；嵌套 PTC 保留 owner，但不伪造 Session 日志 |
| core/scope（工作区引用：`integration/deepseek-harness/packages/core/scope/src/index.ts`） | 复用 `ScopeKey = object`、`createScope` 与效果释放；生成的工具事件解析器读取 `exec.scope`；旧 Agent 事件夹具同步迁移 |
| fs-observation-policy（工作区引用：`integration/deepseek-harness/packages/fs/fs-observation-policy/src/index.ts`） | 观察身份为 `actor.agent?.session ?? actor.host`；同一路径的不同 Host 独立观察，写前不自动补观察，保留未读写入、内容变化和 CAS 检查 |
| sandbox-policy（工作区引用：`integration/deepseek-harness/packages/sandbox/sandbox-policy/src/index.ts`） | 根目录优先级为 Session cwd → 可信 `workspaceRoot` → 部署 fallback，并经过 canonicalization；原 mode 优先级不变，提供根目录不授权扩大权限 |
| tool-fs（工作区引用：`integration/deepseek-harness/packages/fs/tool-fs/src/session-cwd.ts`）、tool-bash（工作区引用：`integration/deepseek-harness/packages/shell/tool-bash/src/index.ts`）、tool-pwsh（工作区引用：`integration/deepseek-harness/packages/shell/tool-pwsh/src/index.ts`） | 文件和前台命令使用 Host 工作区与站立策略；受限 PowerShell Host 调用以 canonical policy root 解析默认及相对 cwd。Host 后台调用在环境收集和启动进程/Job 前被拒绝，避免生成无 owner 的 detached job |
| bash-local（工作区引用：`integration/deepseek-harness/packages/shell/bash-local/src/index.ts`）、pwsh-local（工作区引用：`integration/deepseek-harness/packages/shell/pwsh-local/src/index.ts`） | 捕获直接进程结果后，终止残留后代并等待同一受管范围停稳；provider 拒绝也执行清理，等待不复用已取消的调用信号；无法观察受管范围则拒绝，不虚报已停稳 |
| user-approval（工作区引用：`integration/deepseek-harness/packages/interaction/user-approval/src/index.ts`） | Agent 审批 API、turn 约束和日志保持不变；Host 的 ask/扩大权限仍被拒绝。正式 Host answerer、持久 asked/decided 审计和 Host Job 所有权尚待实现 |

真实 Loader 文件组合测试（工作区引用：`integration/deepseek-harness/packages/fs/tool-fs/tests/host-composition.spec.ts`）通过测试专用 `cordis.yml` 挂载现有 ToolRuntime、文件提供方、观察和沙箱策略；验证实际读后修改、创建文件、跨 Host 观察隔离、工作区外写入拒绝、只读拒绝和撤销后无写入。全局 PTC 与 Host native 作用域并存；测试没有挂载 Agent、SessionStore 或模型服务。这证明了 Host 基础链路，不代表原 Runner 六类操作已经落地。

注销顺序由提供方负责：拒绝新调用，撤销并等待真实工作停止，再释放 scope。观察记录使用 WeakMap；被撤销的 owner 不能重新入场。进程停稳能力受所选 subprocess 提供方可观察范围约束，不宣称它能捕获超出既有隔离保证的任意后代。

## 原生执行语义与剩余差异

- 原 `file_read` 的公开 Runner 线路校验只接受 `start_line`／`end_line` 同时省略，或同时提供且满足 `start_line > 0`、`end_line >= start_line`；只提供一个边界会在准入时拒绝。文件处理函数先检查目标位于 cwd 内，再仅在两个边界都存在时进入严格范围读取；这一内部选路条件不放宽公开输入规则。无范围路径使用 `std::fs::read` 与 `String::from_utf8_lossy`：按原始字节数限制，保留 BOM/NUL，无效 UTF-8 替换为替代字符。范围路径需要单次打开的原始字节流、完整原始文件 SHA-256，以及覆盖选定范围之外的严格 UTF-8 校验。范围 stdout 只含 `{format, content, sha256, total_lines, start_line, limit}`，不包含内部 `FileReadRange` 的 `returned_lines`、`end_line`、`has_more` 或游标，也不采用 skill 输出封装。原生文件适配器保留这两条读取路径及其 1-based 行范围、输出上限和换行规则；现有文本工具的 BOM/NUL 处理不能直接代替。源码依据：冻结 Runner 文件操作（工作区引用：`source-sync/webcodex/crates/webcodex-runner/src/webcodex_runner/files.rs`）第 334–407 行；公开准入见规范操作校验（工作区引用：`source-sync/webcodex/crates/webcodex-core/src/runner_operation.rs`）第 1392–1396 行与 registry 校验（工作区引用：`source-sync/webcodex/crates/webcodex-runner-registry/src/validation.rs`）第 376 行起。
- 原 `file_write` 没有 overwrite 字段；`create_dirs: false` 不创建父目录，hash 前置条件比较原始文件字节且大小写不敏感。需要在提供方目标锁内校验原始摘要和调用方已有观察记录产生的写入意图，再执行写入。提供方不会自动记录观察，也不通过写前自动读取绕过 DSH 观察策略。
- 原 `file_list` 使用 lstat 区分链接，目录后缀 `/`，最终名称按 UTF-8 词法排序，空目录输出保留换行；现有文本工具显示结果不等价。
- `run_process` 必须使用真实 argv 和 sandbox.confine，不能拼成 shell 字符串；`run_script` 需要提供方所在执行环境中的私有脚本文件、独立 stdin、正确解释器和清理，不能假定远程提供方与 Host 共用临时目录。
- 文件操作忽略原 `timeout_secs`，仅采用本地执行上限。process/script 仍需协调命令超时的零值/至少一秒规则，并区分 `timed_out`、真正的未启动和 `outcome_unknown`；transport 取消投影不能替代真实执行语义。

## 原始字节与写入条件集成

本节记录文件系统提供方基础，其回执与后文原生适配器分开。共享 FileSystem API（工作区引用：`integration/deepseek-harness/packages/fs/fs/src/index.ts`） 增加 `streamBytes`，以单次打开的文件或远端响应传递原始字节；FsWriteOptions（工作区引用：`integration/deepseek-harness/packages/fs/fs/src/types.ts`） 是 `writeText` 的第六个参数，表达 `createDirs` 与 `expectedExistingSha256`，不增加 Runner 线路字段。

- 原始流保留 BOM、NUL、无效 UTF-8；消费方根据原操作分支选择有损文本解码或完整严格 UTF-8 校验，并负责范围输出和保留量限制；提供方负责延迟获取与 EOF、失败、取消、提前 return 的资源释放。打开来源不等于抵御外部原地写入的快照。
- 摘要条件检查完整已有文件的原始字节，大小写不敏感；空字符串也是已提供条件。缺失文件或摘要不匹配在暂存前拒绝，摘要与已有观察意图必须同时满足。hash 匹配不扩大沙箱权限，也不通过写前合成观察允许盲覆盖。
- `createDirs: false` 不创建目标祖先目录；仍允许在已存在父目录下使用私有暂存目录。local 执行非递归暂存，sandbox 原样转发条件；E2B 使用非递归同级目录与 Python stdin 描述符写入，不调用会递归建目录的 SDK `files.write`。E2B 的 `diffBasisMaxBytes` 默认 10 MiB，分别作为覆盖 diff 两侧的排他上限；`fileCommandTimeoutMs` 默认 60000，限制远端文件辅助进程执行时间。
- 目标锁只串行化同一提供方实例的写入。检测到外部变化时拒绝，不宣称跨进程、提供方实例或硬链接的全局 CAS。DSH 保留原子目录项替换，与 Rust 截断已有 inode 的行为不同：其他硬链接保留旧内容，替换依赖父目录权限；本地 POSIX 与 E2B Linux 新文件采用仅所有者可访问的 `0600`，本地 Windows 新文件继承目标目录 DACL，替换时保留已有目标的安全描述符，详见本地文件提供方（工作区引用：`integration/deepseek-harness/packages/fs/fs-local/README.zh.md`）。

文件系统双语参考（工作区引用：`integration/deepseek-harness/docs/subsystems/filesystem.zh.md`）与现有 Runner 架构记录（工作区引用：`integration/deepseek-harness/.agents/notes/implemented/architecture/2026-09-14-runner-polling-executor.zh.md`）记录接口和取舍。generation 2 仍要求 22 项原始基础能力全部实现，当前部分客户端继续被拒绝。

本轮提供方与消费方聚焦验证回执：

- local／sandbox／共享服务共 7 个测试文件：分批验证合计 231 项不同用例通过、1 项 Windows 专用用例跳过（不是一次合并运行）；包括 raw-storage 38、filesystem 77、fsio 70、win32 4、fs service 12、containment 5、sandbox 25。最终负向错误分类跟进中，raw-storage 38 与 sandbox 25 在同一次运行中 63/63 通过，新增检查明确区分缺失父目录、非目录祖先、权限及其他 I/O 失败并保留原始原因。此前 10 文件 type-aware lint 无问题；跟进的 3 文件 lint、文件系统 scoped tsc 与改动空白检查均通过。
- 降低 root 读写绕过能力后的 sandbox 初次运行因 `/root` 下夹具的 `0550` 目录无法 `mkdtemp` 而失败；使用 fs-sandbox 下自有 TMPDIR 重跑后通过，保留工作区外／临时目录外拒绝检查，临时目录及编译缓存已清理。这不是产品权限规则的放宽。
- 消费方闭环单独验证 5 个文件、339/339 通过：local filesystem 77、共享 fs 12、tool-fs 74、agent-instructions 155、skill 21。归属方原样重跑此前命令，使用 `capsh --drop=cap_dac_override,cap_dac_read_search` 降低 root 读写绕过能力；没有测试或代码改动，最终退出码为 0，输出已收集，早先语法及 root 权限运行失败已闭环。这 339 项与上述提供方统计重叠 89 项（77 + 12），不得直接相加为不同用例总数；后续 250/250 仅重跑其中 tool-fs／agent-instructions／skill 三组，不增加不同用例数量；E2B 的 94 项另行独立记录。
- E2B 共 94/94 通过：74 项提供方检查、20 项辅助进程检查，后者包含 7 项真实本地 Linux 脚本执行。5 文件 type-aware lint 与项目类型检查无问题。测试覆盖未知启动、PID 列表及 kill 失败保留 `FS_IO_ERROR`、已知退出不重复 kill、有符号时间戳、描述符与路径身份检查。E2B 控制器使用测试替身，没有调用真实 E2B 服务或使用真实凭据。

文件系统基线集成回执（先于原生 Runner `./files` 最终审阅，不替代该入口的类型、构建及行为验证）：

- `pnpm exec tsc -b tsconfig.host.json --pretty false` 在本轮文件系统源稳定后通过。`pnpm exec tsdown -F packages/fs/fs -F packages/fs/fs-local -F packages/fs/fs-sandbox -F packages/e2b/fs-e2b` 四包构建通过。
- 使用普通 Node 直接导入构建后的 JS，通过多块原始字节、取消、摘要与观察意图并用、大小写不敏感摘要、`createDirs:false`、硬链接保留旧内容、沙箱拒绝与第六参数转发检查；E2B 构建产物导入及默认配置检查通过，没有远端调用。最终错误分类的补充产物冒烟通过：父目录在暂存前被替换为普通文件时，返回 `FS_NOT_DIRECTORY` 并保留原始 `ENOTDIR` cause，外部内容不变且没有暂存残留。首次基础冒烟因根目录未声明 `@deepseek-ai/cordis` 依赖而停在导入阶段，改为显式导入 `vendor/cordis/lib/index.js` 后通过；补充冒烟初次因 Node 的嵌套 cause 对象使用精确匹配而失败，改为逐字段断言后通过。两项修正均只改变冒烟脚本，临时数据已清理。
- Cordis 生成首次因 `FsWriteOptions` 未登记文档归属而失败，在 `scripts/gen-cordis-catalog.ts` 登记 `filesystem.md` 后重生成成功；99 个生成文件／区域新鲜度通过。Config 目录生成及新鲜度通过，E2B 两项配置已加入双语目录。
- `verify-type-equiv`：417 个类型块及 417 个中文派生块通过；7 组本轮文档配对已重新记录，全局 `verify-translation-pairing` 771 对通过。`verify-md-links` 1531 文件、`verify-md-wrap` 1538 文件、`verify-agent-note-format` 306 笔记、`verify-package-readme-model-experience` 266 README 均通过；本轮修改的 `git diff --check` 通过。
- 原生审阅前，源码与产物两种文档编译模式均通过（80 个编译块、78 个忽略块、791 个 type-equiv／目录块、949 个配对派生块）。早期 Runner 文件未齐备导致的类型错误已在该阶段闭环；该回执不代替后文最终 native 集成检查。

## 原生 Runner 文件适配器集成（本阶段验证完成）

独立 `@deepseek-ai/dsh-runner/files` 入口以稳定 Host owner 和私有 native ToolRuntime scope 执行 `file_read`、`file_write`、`file_list`，复用已有文件与沙箱策略，不创建 Agent、Session 或模型请求。其稳定 Config 为 `workspace`、`maxOutputBytes`、`maxInvocationBytes`、`maxResultBytes`、`maxReadAttempts`，由生成配置目录（工作区引用：`integration/deepseek-harness/docs/config-catalog.zh.md#deepseek-aidsh-runnerfiles`）拥有；没有加入已发布默认组合。

- 文件读要求显式 cwd；范围准入仍为双边有效正整数且有序，或同时省略，单边拒绝。无范围按原始字节上限有损解码并保留 BOM/NUL；范围路径完整严格校验 UTF-8、计算原始 SHA-256，输出精确六字段，保留 192 KiB 内容与 256 KiB 序列化限制。成功稳定读取与权威缺失分别产生 present/absent 观察，读取错误采用不含路径的分类，包括原始 `EACCES`／`EPERM`；保留提供方规范化差异，例如祖先为文件时本地提供方可能给出 `not_found`，而 Rust 为 `io_error`，不声称错误逐字节等价。
- 写入需要已有观察意图与原始 hash 条件同时通过；缺失 `create_dirs` 仍为 false。列表以条目 basename 和提供方规范目录 cwd 调用 lstat，按 UTF-8 字节排序并保留空目录 LF，忽略原始 `max_bytes`，只增加 native `maxResultBytes` 完整结果限制；提供方目录数组仍完整物化。原子替换、硬链接和最终外部竞争限制沿用上节，不修改共享文件行为。
- 文件执行完全忽略信封 `timeout_secs`，仅使用 DSH 本地 `executionTimeoutMs`；命令请求继续采用原命令超时语义。内部工具在成功路径前抛出带错误的 FileResult，取消不覆盖提供方 I/O 失败；卸载撤销 owner 并排空工作，清理不依赖 Cordis 并发 disposer 顺序。仅 `file_read`、`file_write` 声明 true，`shell` 为 false；generation 2 的 22 项 required bits 不变，合规 Server 仍在执行前返回 HTTP 400。
- 最终源码的聚焦回归 **38/38 通过**：files-policy 25、files-composition 8、client-file-deadline 5。取消检查包含真实写入已提交后延迟返回、owner 在此期间撤销：外部结果为 aborted，实际已提交字节仍被验证，不能将取消结果解释为未写入。较早版本的 Runner 整套 9 文件 **427/427 通过**（包含 helper 74）；最终跟进仅重跑受影响的三份测试，不能把 38 与 427 相加，也不声称最终源码整套重跑。原生归属方完成包级 tsc、scoped typed lint（0 warnings / 0 errors）、差异检查、tsdown 和公开叶入口的 plain Node 真实 Loader 文件写入冒烟；冻结离线锁检查通过。Loader 仅模拟 HTTP Server，没有生产部署、真实 E2B 或模型调用。
- 生成器独立回归 **32/32 通过**，两文件 type-aware lint 为 0 warnings / 0 errors。Config 与事件关系图已生成并同步中文，现有 Runner Agent Note、子系统及包／测试 README 完成内容审阅。
- 最终根 Host 编译 `pnpm exec tsc -b tsconfig.host.json --pretty false` 通过。根编译发现策略测试中四处不完整配置；直接调用 `Files.Config({ workspace })` 仍不满足其静态输入类型，最终由测试侧完整的 `Files.Config` 类型化配置修正，未改变运行时配置或弱化类型。修正后的 files-policy **25/25 通过**、单文件 typed lint 为 0 warnings / 0 errors、`git diff --check` 通过；四项 POSIX 路径解释用例显式跳过 Windows，本轮未执行 Windows 验证。这 25 项属于上文 38 项的重跑，不增加不同用例总数。仅测试配置变化，已通过的运行时构建和产物冒烟回执仍适用。

- 最终文档集成：仅重新记录 Config、事件关系图、Runner 子系统、现有 Agent Note、包 README、测试 README 六组变更配对。全局配对 771 组、链接 1531 文件、换行 1538 文件、笔记格式 306 份通过。源码与产物两种 `doc-typecheck:contracts-ready` 均通过，各为 80 个编译块、78 个忽略块、792 个 type-equiv／目录块、950 个配对派生块。稳定 API 的 Config、Cordis 99 区域、类型等价 417+417 块、关系图 6 份新鲜度回执继续有效；子系统 51 组、模型体验 266 README、笔记分类 306 份、预算 8 份、文档引用 2707 文件检查通过。未重跑全量 `doc-sync`。

文件阶段之后已接入下节的 POSIX process 子集；script 仍缺少 FS 提供方临时文件租约和清理原语。E2B 的本地 Host 沙箱不能强制远端工作区或只读限制。持久 Host 审批、Job 所有权、22 项基础能力、Server 实际身份/持久化集成和 G0 外部验证均继续保留。

## 原生 POSIX 结构化进程（本阶段验证完成）

本地功能提交为 DSH `mcp-runner` 的 `1435db0d70d0579b60bfe8e88d1b9e21dd9eb056`：`feat(runner): execute structured POSIX processes through Host tools`。42 个文件包含实现、回归、配置／依赖、来源许可和双语文档。正常 Git hooks 通过；首次 hook 的较窄 lint 报一项已有 unused suppression 警告、零错误。文档最后补充合入同一条未推送提交；最终 DSH 工作区与暂存区干净，Server 保留既有未跟踪日志。

新增 `@deepseek-ai/dsh-runner/native` 显式叶入口，将原文件工厂和私有 Host 进程工具组合为唯一 Runner executor；原 `./files` 入口继续可用。原 `run_process` 可执行文件、参数边界、空参数、Unicode、独立 stdin、cwd、超时与原结果字段保持分离，不拼接 shell。源码为 native.ts（工作区引用：`integration/deepseek-harness/packages/runner/runner/src/native.ts`） 与 process.ts（工作区引用：`integration/deepseek-harness/packages/runner/runner/src/process.ts`），配置及兼容边界由包参考（工作区引用：`integration/deepseek-harness/packages/runner/runner/README.zh.md#native-posix-processes`）拥有。

- `SubprocessRuntime.supportsHostConfinement` 基类 false、本地 true、E2B false。受限执行同时要求真实同 Host 执行环境、可用 sandbox 和 full enforcement；getter 不证明完整进程树约束，FS/subprocess 同环境配对仍由组合负责。没有修改任何运行中的组合或服务。
- stdout/stderr 持续排空并各自保留有界原始字节尾部，UTF-8 输出连同截断标记计入上限；完整结果另计 JSON 转义。修正了 BOM 尾部偏移的 Rust→JS 算术差异。声明的沙箱启动失败规则单独流式匹配 stderr，不受用户输出上限影响；只有符合退出码／诊断且清理确认的启动失败才报告 `not_started`。
- 正常／非零／信号退出为 `completed`；确认清理的超时为 `timed_out`；通用 spawn、I/O 或清理不确定性为 `outcome_unknown`。有界原始 JSON 通过 `HarnessError` 的专用 code/message 保留执行证据，真实 ToolRuntime 与传输的取消不抹除可信失败；completed 与取消竞争仍保守投影 unknown。卸载撤销 owner 并等待回调结束，unknown 不承诺全部后代已经停止。
- 原生入口声明三项 true：`file_read`、`file_write`、`structured_process_argv`；file-only 为两项。generation 2 仍要求 22 项，合规注册仍拒绝部分能力。profile／环境快照、Windows／OEM 输出及完整进程协议等价未完成；当前使用提供方清理后的继承环境，裸名称 PATH 查找可能与请求 cwd 不同。

本轮验证回执：

- Runner 整包 **11 文件、501/501 通过**，含 helper 53、真实 Loader/HTTP 10 和原文件／协议／轮询回归；拥有者本地 snapshot 固定真实非零退出、argv 和 stdin 的回传字段。最终 lint 修正后的 helper **53/53 再次通过**，属于同一组用例，不能相加。
- 根 Host `tsc -b tsconfig.host.json` 通过；最后 helper 调整后，根 Host `tsc -p tsconfig.host.json --noEmit`（含测试）及 Runner 包构建型 tsc 均通过。28 文件 type-aware lint 最终 **0 warnings / 0 errors**，差异空白检查通过。中途编译失败为缺失 project reference 和测试只读属性／异步回调类型，均已修正；仅清理精确识别的编译旁产物，没有删除源文件。
- Runner、subprocess、subprocess-local 三包构建通过，`built-native-smoke.mjs` 与 `built-files-smoke.mjs` 均通过。最后 helper 修正后再次构建 Runner，公开 `@deepseek-ai/dsh-runner/native` 的 plain Node 真实 Loader 文件→进程→stdin 冒烟通过。所有 HTTP 仅为自有 loopback 测试 Server，无真实 Runner／模型凭据、生产调用或 E2B 服务。
- 冻结离线 `pnpm install --offline --frozen-lockfile --ignore-scripts --filter @deepseek-ai/dsh-runner` 通过，仅新增现有 workspace 链接。新能力 getter 的三份聚焦提供方检查 3/3 通过（97 项不相关测试跳过）；不作为 E2B 或平台矩阵验收。
- 双语包／测试参考、现有 Agent Note、Runner/subprocess 子系统与 Config 目录同步。Config、Cordis 99 区域、六份关系图、六组本轮文档配对及链接检查通过；源码与产物两种文档编译各通过 80 个编译块、793 个 type-equiv/catalog 块由各自检查器负责。导出 JSDoc、路径别名与包依赖检查通过；未重跑完整 `doc-sync` aggregate。

## 原 managed Agent Token 验证器（本阶段验证完成）

Server 已建立独立本地提交 `861bb9e78fe1ba7fe1192c7514ada97733bb12e9`：`feat(webcodex): verify managed Runner credentials with host identities`。[credential.go](../../backend/internal/webcodex/runner/credential.go) 的 `NewAgentTokenAuthenticator` 实现原 token 字节的 SHA-256 查找、撤销与 Unix 秒到期检查、当前宿主用户核验和原 `last_used_at` 尽力更新，直接返回既有 HTTP handler 的 Authenticate 回调。依赖通过窄 repository／HostUserResolver 接口注入，没有接入真实凭据数据库、router 或 DI，没有新账户表或模型调用。

`user_id` 仍为字符串；新记录使用 V6 允许的 `sub2api_user_<正整数宿主ID>` 确定性表示。验证器严格逆解析后仅按不可变 ID 查询宿主用户，要求存在、未删除且启用，并核对返回 ID 一致；展示用户名、角色、计费组不参与节点授权。旧 WebCodex 数据必须显式映射原 user ID 并一致重写相关 owner／subject／授权引用，当前不自动导入、不按用户名合并，也不承诺原 owner 字符串直接兼容。

原数据库 `kind=agent` 映射为 `agent_token`；`user`／旧空 kind 在验证通过后映射为不能访问 Runner transport 的 `api_token`，由既有 registry 返回 403；未知 kind 不升级。scope 按原 `scopes_vec` 保留全部已存项、顺序和重复值，验证阶段不重复签发时的 allowlist；只有精确 operation scope 才授予对应权限，`admin`／未来 scope／BOM 不产生额外权限。项目凭据、OAuth、共享 key、bootstrap 和 account credential 的独立验证链仍未实现；本 managed 记录不承载 project grant 或 shared-key group。

最终验证回执：新增 **17 个顶层认证行为测试**，覆盖固定公开 SHA-256 向量、精确字节／长度、到期相等、撤销、当前用户、规范 ID、取消、并发及错误脱敏；内存 HTTP fixture 覆盖完整 register→poll→result→offline、scope／owner／client 隔离、拒绝不消费待处理请求、撤销后拒绝结果和 G2 部分能力仍返回 400。完整 Runner／protocol 两包 race 通过；最后只补 HTTP 的 BOM／额外 scope 证据后，聚焦认证 race 再通过，生产代码和 protocol 未再变化。两项 DSH 跨语言用例因未配置 checkout 自然 skip，旧 loopback exchange 实际运行；不把模拟结果记为执行闭环。

```sh
env GOPROXY=off GOSUMDB=off GOTOOLCHAIN=local CGO_ENABLED=1 GOMODCACHE=/root/project-development/A2AMesh/source-sync/toolchains/gomodcache DSH_RUNNER_CHECKOUT= /root/project-development/A2AMesh/source-sync/toolchains/go1.27.1/go/bin/go test -race -count=1 ./internal/webcodex/runner ./internal/webcodex/protocol
env GOPROXY=off GOSUMDB=off GOTOOLCHAIN=local CGO_ENABLED=1 GOMODCACHE=/root/project-development/A2AMesh/source-sync/toolchains/gomodcache DSH_RUNNER_CHECKOUT= /root/project-development/A2AMesh/source-sync/toolchains/go1.27.1/go/bin/go test -race -count=1 -run '^TestAgentCredential' ./internal/webcodex/runner
```

两条命令均从 `integration/sub2api/backend` 运行，退出码为 0；`gofmt -l` 和 diff 空白检查通过。源码、两份专用测试、README 和 NOTICE 共五个文件纳入该提交，Server 只保留原有未跟踪 `backend-service-final.jsonl`。实际用户 repository 接线、凭据存储与迁移、默认关闭的启动配置、业务 scope／项目权限路径仍需后续独立实现；没有读取真实凭据、连接真实数据库、推送、部署或执行 G0。

## Linux 原生进程格式与启动失败语义（本阶段验证完成）

DSH 已建立独立本地提交 `cf374cda9bc06a08e6d548d8e4f6cf32b19339bb`：`fix(runner): enforce Linux native-image launch semantics`。43 个相关源码／测试／文档文件纳入提交，包含新 native-image 回归。正常 pre-commit hooks 通过；较窄的 staged lint 报告 4 项既有 unused suppression 警告、0 错误，没有绕过检查。提交后 DSH 工作区和暂存区干净。

Job 准入核查发现上一进程阶段遗漏了平台差异。原 ManagedChild 测试（工作区引用：`source-sync/webcodex/crates/webcodex-process/tests/managed_child.rs`）第 652–691 行要求保留各平台 `Command::spawn` 行为：Linux 对无 shebang 的可执行文本返回 `ENOEXEC`；macOS 可以沿平台标准库路径执行它。DSH 共享 bootstrap 的旧行为包含 `/bin/sh` 回退，因此仅传入结构化 argv 不能保证原 Linux 行为。

本次为普通 SubprocessSpawnSpec 增加 Host 内部 `executionMode: 'native-image'` 和基类默认 false 的 `supportsNativeImageExecution`。本地 Linux 在 libc／bootstrap 可用时支持，PGID 和 scope 两条路径均把模式传到原 execve helper；严格模式禁止 ENOEXEC shell 回退，显式 shebang 仍有效。省略模式的共享消费者保留原行为；macOS、Windows 和 E2B 不支持此新模式，E2B 在远端资源获取前拒绝。没有增加 WebCodex wire 字段或放宽 G2。

Runner 根据真实 provider 支持声明 `structured_process_argv`，不再无条件宣称三个 true 位；不支持时文件入口仍可保留两个文件位。单次请求在 lookup 前复查启动支持。当前严格进程路径仅支持 `danger-full-access`；read-only／workspace-write 在调用 sandbox.confine 或 spawn 前明确拒绝。私有启动文件与沙箱挂载／临时目录的兼容传输尚未完成，不能把严格启动外层 wrapper 等同于严格启动内层目标，也不能把此前阶段的沙箱执行快照当作当前受限进程已可用。

交叉审查补修了错误通道丢失路径：请求被消费后，如果启动错误文件也无法发布，bootstrap 的 127 不能被当成目标已完成。严格模式在缺少错误报告时将裸退出码 127 保守映射为 `outcome_unknown`，包括目标自身真实返回 127 的情况；这项限制需由后续可信启动回执解决。已明确报告的 ENOEXEC 同样保持未知，不能据此自动重试。当前 Runner 既有未知结果分支不返回双流，低层 collector 仍保留已收集内容；没有伪造 not_started 或引入启动成功 ACK。

最终修改的五组行为回归合计 **174 通过、2 跳过**：本地 native-image／linux-scope／spawn 和 Runner process／native-composition。两项跳过均为普通共享消费者的真实沙箱正向验证，本机后端不可用；严格 Runner 的两种受限策略拒绝测试实际通过。新增故障发布回归使用私有文件删除和模拟退出事件；两个路径的真实目标退出 127、ENOEXEC 无副作用标记、显式 shebang、argv／双流和范围清理均有验证。scope fixture 替换 systemd manager，只证明真实 bootstrap 路径，不证明真实 cgroup containment。

包含测试的根 Host `tsc -b tsconfig.host.json` 通过；最后九个 TS 文件 lint 修复一项 void 箭头表达式后全部通过，0 警告／0 错误。三个相关包完成最终 tsdown 构建，普通 Node 的真实 Loader／本地 HTTP 冒烟实际完成文件写入、正常进程、ENOEXEC 拒绝和真实目标退出 127 的未知结果；没有 Agent、Session 或模型服务。此前实现阶段另通过九组 299 测试、私有模式与 E2B 增例后的两组 108 回归，以及源码／构建 bootstrap 3/3；这些范围重叠，不累计为独立总数。最终构建共享 chunk 为 `spawn-DeQ1MVTG.js`，包发布列表使用 `lib/*.js` 以包含它；只做 dry-run，没有发布归档或上传。

收尾时在两份最新开发代码上补跑认证阶段暂缓的两项跨语言用例：设置 `DSH_RUNNER_CHECKOUT=/root/project-development/A2AMesh/integration/deepseek-harness`，使用同一离线 Go 工具链执行 `go test -race -count=1 -run '^TestDSH' ./internal/webcodex/runner`，退出码 0（1.237s）。六类请求及 64 位整数往返、真实 Go loopback 拒绝部分 G2／执行次数为零均通过。这不代表 managed token 已接真实数据库，也不代表完整 G2 执行成功。

相关 README、测试说明、subprocess 子系统、既有 owning Note 及七组双语配对记录已完成最终检查；Markdown 链接 1531、换行 1538、预算 8、Note 格式 306 均通过。此前类型等价 417、公开 JSDoc、Cordis 目录 99 检查通过；未运行完整 doc-sync、整个仓库测试或真实外部 API。可信启动回执、受限进程正向路径、完整 Job、process profile／环境快照和其他平台等价仍未实现。

## Job 后续实现的源码核查

本轮对照固定 WebCodex 源码与 DSH `1435db0d70`，确定先保留 Runner 自有的原 JobManager，再通过私有 Host ToolRuntime 完成准入、执行与停止。现有 `ctx.jobs` 的 Agent owner、`<kind>-N` 身份、立即启动与单 consuming string 日志接口不能直接表示原协议；仅扩展 Host owner 仍会留下第二套原版状态和双流记录，因此当前没有修改共享 JobRegistry。依据为 DSH Job 类型（工作区引用：`integration/deepseek-harness/packages/jobs/jobs/src/types.ts`）与本地 registry（工作区引用：`integration/deepseek-harness/packages/jobs/jobs-local/src/index.ts`），以及原 Runner JobManager（工作区引用：`source-sync/webcodex/crates/webcodex-runner/src/main.rs`）第 4108–4300、4461–4574 行。

- 已证实的共享执行缺口是**受管启动成功回执**。原 shell.rs（工作区引用：`source-sync/webcodex/crates/webcodex-runner/src/webcodex_runner/shell.rs`） 第 2608–2639 行在 ManagedChild 启动成功且双流读取器附着后调用 `on_started`，JobManager 才发布 `running`。DSH SubprocessHandle（工作区引用：`integration/deepseek-harness/packages/subprocess/subprocess/src/types.ts`）只有流、`done`、终止和受管范围退出等待；异步提供方返回 handle 不证明受管启动完成。后续补此事实须完整实现 Service／Provider／Consumer，通用错误仍不能证明 `not_started`，不得借助 helper PID 或 pipe 存在伪造成功。回执证明受管命令启动，不能证明业务程序成功或所有后代均受控。
- 原 `StartProcess` 复用同步进程 helper，只发布启动与最终结果，没有 legacy shell Job 的周期输出轮询。进程 helper 继续独占两个 pipe 的读取，Job manager 消费归一化后的独立 stdout/stderr；不用额外日志 reader 争抢单一 cursor。原 Job 双流日志各保留 64 KiB，并附带 `first_retained_line`／`next_line`；它与同步进程结果的输出上限不是同一个值。
- 原 Job 初态是 `agent_queued`，使用 Server 提供的 `job_id`，保留并发槽和 FIFO。原映射为 `not_started→failed`、`outcome_unknown→lost`、`timed_out→timeout`、`completed+stop_requested→stopped`、正常零退出且无错误→`completed`，其他完成退出→`failed`；不能替换为 DSH `killed` 等通用 Job 状态。queued stop 不启动进程；terminal stop 重放原序号快照，不能改写终态或重执行。
- accepted Job 的执行 owner 要与已返回的 start 调用分离，保留 Host 撤销和独立 stop 控制；实际执行仍经过同一可信 Host ToolRuntime。当前 polling 顺序等待最终结果，必须增加接受后继续 poll 与唯一 update collector，才能接收 stop。传输失败只能重试同序号更新，不重放执行；更新背压不得阻塞 pipe 排空。原 64 个 active inventory、64 条 terminal 历史、完整 inventory 字节预算与同进程重连对账仍需迁移；detached Job 的跨进程持久化属于另一项完整功能。

本节是后续实现约束与已证实缺口记录，**没有新增 Job 执行能力、启动回执 API 或 Job 路由**。下一执行阶段先补受管启动事实，再实现原 `start_process_job`／`stop_job`／更新和库存闭环，保留 profile／环境快照、原状态、去重、终态与清理要求；不以此核查替代行为测试。

## 后续顺序与完成条件（十三条阶段历史计划，最新补充见末章）

1. 正式 Host scope、guard、文件观察、sandbox 与前台进程基础已落地；保持不满足原完整能力基线时不能注册。
2. OS API 接受与精确目标退出回执已落地；先补受限 strict 最终目标原语与私有 FD 控制传输，再实现原 Job FIFO／停止／双流快照／更新／库存闭环；继续补齐 process profile／环境／Windows 行为及 run_shell／run_script 语义，落实正式 Host 交互/审批审计。直接调用的模型调用数保持为零。
3. Server 已接入既有宿主用户解析、managed 凭据 repository／迁移及默认关闭 router／DI；下一步补公开签发／管理／显式导入、真实 PostgreSQL 迁移验收和原项目／任务／执行／审批事务与业务派发，继续禁止按可变展示用户名绑定。
4. 随后补齐 generation-2 所需全部 22 项基础能力及测试、原业务表与事务、registry 持久投影、MCP/OAuth、Generic ToolRuntime 和 ProjectConnector 的独立调用路径；全量准入前持续保留真实受限执行与恢复验证。
5. 接入 `compatibility` / `web_mcp` 模式；后者不调用 Prompt Tool bridge，不静默降级或重复执行。
6. 按两份 V6 源码映射继续原可选功能：SSH、persistent shell、AgentTask/ACP、MCP/plugin、memory/skills、computer、恢复与取消等；分期不表示删除功能。
7. G0 需要真实 ChatGPT 原生 Connector 的随机文件、项目/节点/窗口隔离、续接、审批/撤销、取消及结果回传证据；配置发现和 mock 不算通过。当前没有进行这一外部验证，也未申请部署。

完整产品验收不能由“codec 有类型”“fake executor 返回成功”“服务能启动”替代。本文件记录阶段进展，不覆盖 V6 全功能目标，也不宣称可交付生产。

## Server 原 managed 凭据持久化（本阶段验证完成）

本地功能提交 `b3614bba306253eb53299f7bb2abbeb6274d7329`（`feat(webcodex): persist managed credentials with host users`）将原 managed `api_keys` 的 12 个字段保留在 `wc_api_keys`，使用宿主 SQL pool 和嵌入迁移机制。实现为 [repository](../../backend/internal/repository/webcodex_api_key_repo.go)、[User resolver](../../backend/internal/repository/webcodex_host_user.go)及[迁移 238](../../backend/migrations/238_webcodex_api_keys.sql)，完整原字段／生命周期出处见[存储说明](../../backend/internal/webcodex/runner/CREDENTIAL_STORAGE.md)。

字段为 `id,user_id,name,key_hash,key_prefix,created_at,last_used_at,revoked_at,scopes,expires_at,kind,allowed_client_id`。原 string 凭据 ID、唯一 hash、NULL 与有符号 Unix 秒、全部 scope／原 kind／nullable client 绑定保留。仅物理 `user_id` 改为既有 `users(id)` 的 BIGINT 外键，repository 向认证器投影规范 string `sub2api_user_<正整数ID>`；原 `AgentCredentialRecord.UserID` 契约不变，无影子用户、明文模型 key 或新计费／授权分组。

原 `Insert`／`GetByID`／`GetByHash`／`Revoke`／`UpdateLastUsed` 已实现；撤销用原 `COALESCE` 保留首次时间，通过 PostgreSQL `UPDATE RETURNING` 合并原更新／读取。宿主 resolver 每次调用真实 `UserRepository.GetByID`，检查同 ID、`DeletedAt == nil` 与 `IsActive()`，即使跳过 Ent 软删除过滤也不放行已删除用户。没有公开签发／撤销管理／导入流程；旧用户和关联 owner／subject 必须显式映射重写。各 SQL 方法不加入外层 Ent 事务，不宣称认证查询与并发撤销原子，也不广播取消已派发工作。

任务 408 的最终离线命令从 `integration/sub2api/backend` 运行并通过：

```sh
GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off GOMODCACHE=/root/project-development/A2AMesh/source-sync/toolchains/gomodcache /root/project-development/A2AMesh/source-sync/toolchains/go1.27.1/go/bin/go test -race -count=1 ./internal/repository ./migrations -run '^TestWebCodex'
```

repository 1.065s、migrations 1.009s，退出码 0。覆盖完整 NULL Insert／GetByID 往返、零／负值／int64 极值、原字段、精确查询参数、撤销／last-used SQL、规范身份投影、错误／取消、实际 User repository＋Ent 的 SQLmock 适配。DDL 检查仅为嵌入结构检查；没有执行真实 PostgreSQL 迁移、外键或并发事务验证。`gofmt` 与 diff 空白检查通过。

## Server 默认关闭的 polling 路由装配（本阶段验证完成）

本地功能提交 `9e4683d19c6d716592bdfb680af2a16cb4f263d0`（`feat(webcodex): mount authenticated polling routes`）通过 [ProvideWebCodexRunner](../../backend/internal/server/webcodex_runner.go)、既有 router／HTTP server 及实际生成的 Wire 图接入四条原 `/api/shell/agent/{register,poll,result,offline}` 路径。使用同一宿主数据库／User repository 和同一 runtime；不引入模型 APIKey 或 JWT 的替代 Runner 权限，也不提供业务 dispatch endpoint。

`webcodex_runner.enabled` 默认 false；关闭时不创建 registry 或执行凭据查询、不挂四条路由。启用必须显式配置 `webcodex_runner.max_runners`、`max_pending_per_runner`、`online_window_seconds`、`max_body_bytes`、`max_token_bytes` 五项正数；这些项均位于 `webcodex_runner` 下，默认 0 在启用时无效，秒数另检查 duration 溢出。完整环境变量映射见[Runner README](../../backend/internal/webcodex/runner/README.md)。原 scope／kind／owner／client 和完整 G2 准入未放宽。

真实全局 CORS 中间件允许来源的 OPTIONS 可在启用／关闭时都返回 204，但无认证 POST 分别为 401／404，预检不授予认证、不产生凭据查询。HTTP `RegisterOnShutdown` 异步调用幂等 registry `Close`；测试在 Shutdown 之后另行等待 waiter 结算，不能声称 Shutdown 等待 callback 完成。当前 registry 无独立进程／连接／后台任务；硬 Close、启动 panic 和生产停机没有独立验收，也不据此宣称持久恢复或远端工作已停止。

任务 423 的最终离线命令通过，并特意带入外部启用变量验证测试隔离：

```sh
WEBCODEX_RUNNER_ENABLED=true GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off GOMODCACHE=/root/project-development/A2AMesh/source-sync/toolchains/gomodcache /root/project-development/A2AMesh/source-sync/toolchains/go1.27.1/go/bin/go test -tags unit -race -count=1 ./internal/config ./internal/server -run '^Test(WebCodex|ProvideHTTPServer|ConfigureTrustedProxies|HTTPServer)'
```

config 1.028s、server 2.818s，退出码 0；覆盖真实 SQL repository＋测试 User 的 mounted 认证生命周期、缺失／非 Agent／禁用用户／错误脱敏／大小上限／G2 拒绝、真实 CORS 开关矩阵和无 DB 查询计数、关闭 waiter，以及既有六项 HTTP ingress 检查。真实宿主 User 路径另由任务 408 覆盖。另以同一离线工具链执行 `go test -run '^$' ./cmd/server`，实际入口编译通过（0.021s，`no tests to run`）；这是生成后 Wire 装配的编译证据，不是启动服务或生产验收。`gofmt`、最终 diff 检查通过，相关任务输出已收取。

累计功能提交现为 12 条（Server 5、DSH 7），当前 Server 代码基线为上述路由提交。前面十条历史阶段记录保持原样；DSH 新回执尚在进行／未提交，未计入本次能力或提交统计。真实 PostgreSQL 验收、公开凭据生命周期、导入、多记录业务事务、项目授权／派发、持久 registry／结果、完整 G2 和 G0 继续保留；未推送、部署、使用真实凭据／模型／数据库。整体约 15% 的评估口径不变。

## DSH OS API 接受与精确目标退出回执（本阶段验证完成）

本地功能提交 `4c3570bd5b103e956c88f6a38bf9140bec1a4e85` 修改 59 文件，任务 450 提交成功；累计 13 条功能提交（Server 5、DSH 8）。Server 当前基线 `9e4683d19c6d716592bdfb680af2a16cb4f263d0` 不变。前文各阶段保留当时事实，缺少启动回执、真实 127 尚未知及回执未提交的旧描述由本节更新。

实际 native spawn（工作区引用：`integration/deepseek-harness/native/system/packages/entry/src/spawn.c`） 通过 Node addon 调用 posix_spawn；supervisor（工作区引用：`integration/deepseek-harness/packages/subprocess/subprocess-local/src/native-supervisor.ts`） 保留精确子进程并回报 OS API 接受与目标退出。Runner consumer（工作区引用：`integration/deepseek-harness/packages/runner/runner/src/process.ts`） 使用可选且不 reject 的 started 回执，区分 started／not-started／unknown；只有 typed C 已证实拒绝并确认受管范围退出后才给出 not_started。发生效果后的 errno 外形错误不能成为未启动证据，也不继续 PATH 重放。真实目标退出 127 返回 completed／127；首次 target-exit 锁存，之后文件丢失或 carrier 迟到错误不撤销结果。目标终态和范围清理仍独立，未知结果不能自动重放。

私有 status 限 256 B、startup error 限 16 KiB；有界读取采用 nofollow／nonblock，允许已打开后 unlink 的 inode 链接数为 0，拒绝硬链接数大于 1。支持范围是 Linux glibc 2.34+ x64／arm64，实际运行证明仅覆盖 x64。回执是 OS API 接受事实，不是 exec 观察、目标 main／就绪或模型行为保证，不使用 ptrace。

已收取的最终验证回执如下；保存进度文档不重复运行已通过的功能测试：

- 完整 local provider 测试集与 Runner 测试合计 340 通过、11 项平台／confinement 跳过（最终任务 448）；其中 Runner process 37 通过、native composition 14 通过／2 跳过，均已包含在合计中。
- 重建后的实际 Node source／built 入口 4／4；C tag 修正后的 native C＋host packed entry 32／32（任务 443）另行通过，仍适用于最终提交。
- TypeScript 与完整 type-aware lint 0 错误，双语配对 772、JSDoc／diff 检查通过。提交 staged lint 有 3 项既有 disable comment 警告，位置为 index.ts:248、spawn-runner.ts:290、spawn.ts:602；对应注释不在本功能 diff 中，无错误，不能称提交零警告。
- 第三方 generator 已运行且无净 diff；任务 450 提交完成后代码工作树干净。
- 较早 type-equiv 418 个块＋418 个派生块、Note 格式 307、mdlinks 1533 通过；最终进度快照另行核验生成器内容、manifest、相对路径与锚点，和此前 11 份 Markdown／160 个链接的阶段回执区分。

最终 x64 构建的 readelf 仅列出 GLIBC_2.2.5／2.4／2.15，没有 GLIBC_2.34 加载依赖；这不等于真实旧 glibc 运行。完整 musl／macOS／Windows／E2B、完整平台 tarball、真实 user-systemd／confinement 验收仍缺。原 Linux scope fixture 不替代真实 manager／cgroup。read-only／workspace-write 继续在 Runner 策略解析后拒绝，不能把此次回执功能算作受限执行恢复。

下一项 D2 需实现受限 strict 最终目标原语与私有 FD 控制传输。当前 Subprocess strict 仅作用于 argv[0]，Sandbox confine 返回包装 argv 而没有控制 FD 保留／内侧 helper 语义；Linux stdio 仅有三条标准流，bwrap workspace-write 的 tmpfs /tmp 会遮蔽宿主请求路径。只读设计建议由 provider 将受信 helper 放在 wrapper 内侧，传递独立有界控制 FD，再由 helper 严格启动最终目标并关闭目标的 FD ≥3；不开放宿主临时目录为可写。相关描述符／能力 API 尚是提案，需由 Sandbox 定义／local provider、Subprocess 定义／local provider 和 Runner consumer 同步实现并证明真实 backend／scope 组合；未知／partial／不支持组合继续拒绝。之后仍需原 Job、profile／环境、run_shell／run_script 闭环。

Native 在实际支持时仍仅 3／22，file-only 2／22；File 3 项与剩余 17 项不变。整体约 15%、13 个工作包尚无一个满足全部退出条件、22 行待办及完整 G2／G0 目标不变。此前 docs 快照提交 `4ae8b0231`／`1bc5f42614` 只记录第十二条阶段；十三条功能的进度通过相同生成器保存到两侧最新 docs 提交，manifest 与 Git 历史共同标定来源及代码基线。

## 第十四至十六条阶段：公开凭据管理、Job 数据与私有 socket

本阶段累计 16 条代码／功能修复提交（Server 6、DSH 10）。实际已提交的保存点为：

| 序号 | 仓库 | 完整 SHA | 已完成范围 |
|---|---|---|---|
| 14 | Server | `a616f55e88bc37608ddcf883b980942be168190b` | `feat(webcodex): manage runner credentials through host accounts`，14 文件 |
| 15 | DSH | `af6b3d1fafa11b78f26f7c75af404c09e90b7a1c` | `feat(runner): preserve original Job protocol data` |
| 16 | DSH | `894376ebbe9ae491a5c3161af1dc1905ab97b793` | `feat(subprocess): use private socket for native launch receipts`，18 文件 |

Server 四条 `POST /api/agent-tokens/{create,register_hash,list,revoke}` 公开管理已实现，不能继续写成未实现。实际接入 JWT Bearer／当前宿主 role／BackendModeUserGuard／同一 GlobalPanelRateLimit；audit 对这四条路径整体省略正文，并设置 no-store。正文采用 configuredMaxBodyBytes，不再固定 64 KiB。SQL 仅存 hash；`wc_agent_`＋64 hex 新明文只返回一次。保留原 defaults、scopes、status ordering、list／revoke 的 owner／kind 约束，以及 canonical `sub2api_user_ID` 与 BIGINT 外键。来源和剩余限制已在该提交的 [README](../../backend/internal/webcodex/runner/README.md)、[NOTICE](../../backend/internal/webcodex/runner/NOTICE)和[存储说明](../../backend/internal/webcodex/runner/CREDENTIAL_STORAGE.md)记录。任务 477：离线 Go `-tags unit -race -count=1` 对 server／middleware／repository／protocol／cmd/server 的相关 selection 通过；这是 real host＋fake SQL，不是真实 PostgreSQL。显式旧凭据全量导入、跨 account family 独立 hash 验证、真实 PG 迁移／约束／事务仍未完成。

DSH Job 提交保留原 Job DTO、context 11 字段、12 个精确生命周期和原状态，提供独立 `start_process_job`／`stop_job` 编解码。六类同步 consumer 仍拒绝 Job，没有新增 Job 执行或传输；local fixtures 支持独立 clone，不依赖 sibling checkout，64 位整数无损。初次验证为 106 项（Job 80＋client 26），tsc／lint／gates 通过。后续实际 Rust oracle 揭示的 TS／Go 差异正在修复，这份初次回执不能作为最终共享 fixtures 或全 serde parity 的验收。

DSH PGID native-image 现在用 fd3 私有 Unix socket，单个 ≤8 MiB request＋request EOF、最多两条各 ≤16 KiB response；最终 argv／cwd／env 不经文件或命令行。helper 关闭最终目标 FD ≥3，`/proc/fd` reopen 为 ENXIO，stdout 不能伪造控制回执。linux-scope 仍走文件；当前正式 Runner 的 read-only／workspace-write 仍拒绝。仅 pre-supervisor chdir 已知拒绝属于 known-not-started，accept 后通用错误保留 unknown，不重放 PATH。最终六文件 132 通过／2 跳过，source／plain Node built 6／6；typed lint／JSDoc 通过，正常 commit hooks 0 errors、2 项 unused-disable warnings。C 未变化，本轮未重跑 C matrix；上一阶段 340 通过／11 跳过仅为历史，不与本轮混加。

### 实际 Rust oracle 与 bwrap 探针的证据边界

已实际读取Job serde oracle 报告（工作区引用：`.scratch/job-serde-oracle-20260914/REPORT.md`）：39 probe，精确 serde 1.0.228／serde_json 1.0.150，实际 Cargo exit 0。struct positional arrays 和单 key null unit maps 可接受；无 default 的 Option 在数组中缺 slot 会被拒绝，middle default 不会让后续字段移位。inventory 仅测空 jobs，activity 未调用业务 canonical 校验。TS／Go R0 和共享 golden fixtures 扩展进行中，最终 SHA／计数／测试待 parent 回执；外层 generic RunnerRequest arrays 与非 Job 结构旧 gap 保留，不声称全部 serde 等价。

已实际读取bwrap REPORT（工作区引用：`scratch/bwrap-probe-20260827/REPORT.md`）和LIFECYCLE（工作区引用：`scratch/bwrap-probe-20260827/LIFECYCLE.md`）。自有官方 bwrap 0.11.0 非 setuid 构建位于 `scratch/bwrap-probe-20260827/build/bwrap`，SHA-256 `28ba628600c9de65808348daa60e7fc1b6f745a01cd483d5a698c6afb5e8bc60`，保留供 development test，无 global install。原 RO／WW profile＋fd3 的 source／built 四格均成功，72 个证据断言核实 host 文件效果：RO write／create／direct SYS_truncate 拒绝，WW 只 workspace 允许。0.11.0 没有 `--preserve-fds` 选项，正常继承 fd3 已可用；namespace 可用，缺 binary 不是 kernel 硬阻塞；Landlock ABI1 的 direct truncate 限制仍不足。

生命周期另有 78 个证据断言。取消 outer bwrap 会丢失目标退出回执，保留 started＋unknown；没有 live survivors，但 namespace init zombies 由 fixture subreaper 回收，不是 provider reap-all。early profile failure 实际 ECONNRESET→unknown，不能改称 clean EOF 或 not_started。source／built 指 helper 入口，不表示 owner 选择；这些用例直接走 PGID，未验收 provider auto／linux-scope FD 转发、真实 user-systemd、owner 全后代 reaping 或完整取消竞争。探针不构成正式 Runner 受限执行功能。

### 尚在进行与下一次文档保存

Go legacy 结构化 process Job registry／HTTP job_update／default-off 与独立 `max_jobs_per_runner` config 正在实施；reconciliation／inventory／log_snapshot 全部拒绝。DSH 正式 bwrap native confinement Service／provider／当前 Runner consumer 正在实施，采用显式 PGID 配置，不自动降 owner。两项均未收到 SHA／验收，不计已提交或能力；Runner JobManager／FIFO／update 发送尚未开始。

当前精确代码基线为 Server `a616f55e88bc37608ddcf883b980942be168190b`、DSH `894376ebbe9ae491a5c3161af1dc1905ab97b793`；parent 后续提供 R0fix／GoR0 最终 SHA 后，再精确更新至 18 条保存点。native 3／22、file-only 2／22、合规 G2 HTTP 400／zero exec、File 3／20、Project 0／7、Computer 0／19 不变；整体约 15%（10%～20%），13 个大工作包没有一个全量完成。

本次先保存根三文档供审查，未 stage／commit，未运行同步脚本 `--write`。两个 integration 仓库都有其他 agent 未提交改动，不声明工作树干净。parent 明确协调后才同步每仓库五个白名单文件并分别建立 docs 提交；docs 不增加代码数量。本次不重跑产品测试，无 push／deploy／模型／数据库／生产操作。

## 第十七至十八条阶段：两端 Job R0 数据与 serde 输入形式

本阶段已收到最终 SHA 与验证回执，累计 18 条代码／功能修复提交（Server 7、DSH 11）。上节的 R0 in progress 和未同步说明为十六条阶段历史；当前代码基线如下：

| 序号 | 仓库 | 完整代码基线 | 提交与范围 | 父提交 |
|---|---|---|---|---|
| 17 | DSH | `a5ed7187a781310ae33314488b68bcb6e32d73b3` | `fix(runner): accept original Job serde input forms`，13 文件 | `894376ebbe9ae491a5c3161af1dc1905ab97b793` |
| 18 | Server | `cc736b7c16d5a5325c50aed90382e1b1aa210f68` | `feat(webcodex): preserve original Job protocol data`，15 文件 | `a616f55e88bc37608ddcf883b980942be168190b` |

两端保留原 Job 数据、生命周期与独立操作 codec，落实 39 probe Rust oracle（工作区引用：`.scratch/job-serde-oracle-20260914/REPORT.md`）确认的 Job struct positional arrays／单 key null unit maps、非 default Option 缺 slot 拒绝及 middle default 不移位。共享 golden fixture 包含 90 DTO／16 family／12 operations，SHA-256 `0bec4ca7c4e443160498835ca070019a4fcd98110e0478233b75edb7fcb01718`；12 条 direct oracle 对应报告 rows 1–7、35–39。fixture cmp 和 exact-oracle 校验通过。外层 RunnerRequest 数组与 general 非 Job 替代形式仍有已知 gap，不声称 all-serde parity。

parent 收取的最终验证回执：

- 任务 514：DSH 五 specs 487／487，通过组成是 Job 161、protocol 280、client 26、client safety 7、config 13；两文件 lint exit 0、noEmit exit 0。
- 任务 515：Go protocol 30 个顶层测试＋175 个 subtests、runner 59 个顶层测试＋118 个 subtests 均 PASS。两条 opt-in interop 初因未设置 checkout 跳过，不能记初次全执行。
- 任务 517：显式设置 `DSH_RUNNER_CHECKOUT`，只补前述两条 interop，均 PASS（0.19s／0.09s，package 0.281s）；不重算为另一份独立功能覆盖。
- 三组文档配对、mdlinks 1533／wrap 1540／Notes 307、正常 hooks 均通过。共享 fixture 比较及精确 oracle 对照通过；这些回执与历史 106、132／2、340／11 分别记录，不混加。

同步 consumer 仍不接受 Job；本阶段增加原数据保真和编解码，未增加 JobManager／FIFO／update 发送。G2 native 3／22、file-only 2／22，合规注册仍 HTTP 400／zero exec。Go legacy 结构化 process Job registry／HTTP job_update／default-off 与独立 max_jobs_per_runner、DSH formal bwrap native confinement Service／provider／当前 Runner consumer 仍在进行中，无最终 SHA／验收，不计数；前者仍拒 reconciliation／inventory／log_snapshot，后者仍要求显式 PGID、不自动降 owner。正式 Runner RO／WW 拒绝及真实 PG、G0 等限制不变。

本次按授权更新根三文档并运行原同步脚本 `--write` 和默认检查，生成两个 integration 仓库各五个白名单快照文件；未 stage／commit，等待 parent 避开并行功能提交的 index 使用后协调两条 docs 提交。两仓库仍有其他 agent 未提交改动，不声称干净。文档保存不增加代码数量，未重跑产品测试，无 push／deploy／model／DB／production 操作。

## 验证补充：positional Job decimal lexeme（不增加功能数）

当前阶段为 18 个代码／功能修复提交（Server 7、DSH 11）加两条独立验证提交，未扩展实施范围：DSH `1be7718659c239eaa9e441ba3ca064ceb6277657`，`test(runner): cover positional Job decimal lexeme`；Server `a8b34712b56172884a1256f80b1afb37665da4cb`，`test(webcodex): cover positional Job decimal lexeme`。两端各仅一个 fixture 文件的一行，新增原已有 TS 单测覆盖的 `1.0` 数组 integer 负向 golden，故不增加功能序号。这两条 test SHA 为当前代码基线，上一节表格保留 R0 功能阶段基线。

当前共享 fixture 为 91 DTO／16 families／12 operations，双端 SHA-256 `a5a6a41542f2e382a32ba25fd287f83a33dd310bb29fadacbec1099d776497dc`。最新验证回执是 TS Job 162／162 和 Go 共享 fixture 0.005s，通过 cmp／diff；没有重跑 488 项全组。上一节 90 DTO 与五 specs 487／487（Job 161）的记录准确属于增加此 fixture 前的历史，不改写为新全组回执。direct oracle 12 条对应和外层 RunnerRequest 数组／general 非 Job 替代形式已知 gap 保留，不称 all-serde parity。

同步 consumer 不接 Job，G2 native 3／22 仍拒绝，其他并行 Go Job registry／HTTP job_update、正式 bwrap Service／provider／consumer 保留 in progress，不计数。根三文档和两侧各五文件镜像同步保存此“18 功能／修复＋2 验证”阶段并默认核验；不 stage／commit，等待 parent 确认 diff，不声明两仓库工作树干净。

## 第十九条阶段：Server legacy 结构化 process Job 管理

已提交 `efa85831ef08995c54ab63dd33bc57afd7106acb`（`feat(webcodex): manage legacy structured process Jobs`），准确 23 文件，父提交 `a8b34712b56172884a1256f80b1afb37665da4cb`。累计 19 功能／修复（Server 8、DSH 11）＋2 fixture 验证补充；当前 Go 基线为本提交，DSH 基线继续 `1be7718659c239eaa9e441ba3ca064ceb6277657`。上文 Go Job 在研是历史，不再代表当前 Server。

Server legacy process Job 已实现独立 records 与 Start／Get／List／Log／Stop；原 job_update 为第五条 transport 路径，与四条凭据管理路径合计九条。更新入口使用真实 managedVerifier、精确 scopes／owner／group／active instance／request 校验。poll 释放 pending 容量但保留 dispatch binding；Stop 防重复，队列满时不更改 Job 状态，terminal first latch 保留首个终态。原 legacy optional sequence 仅记录，不提升为新式对账保证；finished fallback 保留。双流 256 KiB 有界保留，按绝对 cursor 与 tail reset 处理；List 限制 20～100，900s 生命周期按 Server observed TTL 计算。

新增独立 `max_jobs_per_runner`，必须 explicit positive，没有 pending 值回退。`webcodex_runner.enabled=true` 现在要求六项正数限制：max_runners、max_pending_per_runner、online_window_seconds、max_body_bytes、max_token_bytes、max_jobs_per_runner；disabled 不构造依赖。此前五项限制是路由阶段历史。

调用前仍要求 Host project／scope 授权，本次无用户 dispatch HTTP 或 MCP。inventory／reconciliation／log_snapshot／script／validation／detached／SSH 继续拒绝，原 G2 strict 未放宽，DSH 3／22 注册继续拒绝。没有 Runner JobManager、持久化或跨进程恢复；Close 不能保证已派发远端工作停止。该 Server 内存功能不等于跨产品 Job 执行闭环。

最终验证由 parent 收取，本次文档整理没有重跑：

- 任务 530，离线 Go `-tags unit -race ./internal/config ./internal/webcodex/runner ./internal/server -run '^Test(WebCodexRunner|Job)'`，config 1.054s、runner 1.479s、server 1.237s 全 PASS。
- 旧同步 queue／HTTP 选择回归任务 516 通过（1.299s）。
- 最后因新增 required 配置补核管理路由：`^TestAgentTokenManagement` 命中 0 项，不能算覆盖；改真实名称 `^TestWebCodexAgentToken` 后 server race PASS（1.131s）。
- 最终提交执行者核对 23 个白名单路径，diff／index 空，无后续源码修改。Go 功能树在该收尾点除 mirror／scratch／旧日志外干净；这不是整个工作树干净声明。

共享 fixture 当前仍是 91 DTO／16 families／12 operations，双端 SHA-256 `a5a6a41542f2e382a32ba25fd287f83a33dd310bb29fadacbec1099d776497dc`。最新 fixture 回执 TS Job 162／162、Go 共享 fixture 0.005s／cmp／diff 保留；487 全组是新增 fixture 前历史，未虚构 488 全组回执。外层 RunnerRequest 数组／general 非 Job 替代形式 gap 继续保留。

正式 bwrap Service／provider／consumer 仍 in progress，仅有实际 source／built 部分验收，未收到最终功能 SHA，不计入 19；Runner JobManager／FIFO／update 发送未开始。根三文档与镜像同步十九条阶段，原脚本默认检查和白名单 diff 检查后暂不 stage／commit，等待 parent 协调最终快照；不重跑产品测试，无 push／deploy／model／DB／production。

## 第二十条阶段：正式受限 native 执行已提交

DSH `655b2a643e49020f32641839753d45edf631ff06`，`feat(runner): execute confined native images through private bootstrap`，62 文件、+1230／-88，经 normal hooks 本地提交。累计 20 个独立功能／修复（Server 8、DSH 12），另 2 条 fixture-only 不增加功能数。第十九阶段 docs 已保存为 Go `4a58b994def92dec8372dc0725f4b31f62bdb69b` 与 DSH `d017b44b6eccba005c0d0126cb3b1f2042343002`；本快照固定 Go code `efa85831ef08995c54ab63dd33bc57afd7106acb` 与上述 DSH code，不因并行提交更新计数。首次 target native private channel 已属于十九阶段内的既有成果，本次只增加正式 confined 集成这一功能。

### 已提交范围与限制

`SandboxService.prepareNativeImage` 提供闭包，要求 Linux actual bwrap／full enforcement 及显式 `nativeImageContainment: 'process-group'`；local provider 的 wrapper 仅包受信 private bootstrap，最终 target argv／cwd／env 通过 fd3 传递。default／PTY 对 defined field 拒绝，E2B 两个入口在远程资源获取前拒绝。bwrap 的 default PATH 捕获为 absolute executable，供 generic／exact／wrap 一致使用，generic 探测 payload 为 `/bin/true`。Runner 保留原 signal deadline，另以 performance elapsed 在阻塞 prepare 后、spawn 前复查；超预算返回 not_started，spawn 次数为 0。

pkg 明确不支持 confined，unconfined 保留；已证明的是 source 与 plain Node artifact。workspace-write 的 tmpfs /tmp 遮蔽 bootstrap argv 中绝对路径／file URL 或其 realpath，且未被 workspace bind 恢复时提前拒绝；不扩大 mount，不承诺识别全部 transitive imports，未识别依赖仍 possible unknown。没有 valid target receipt 不虚报成功。auto／linux-scope 仍拒绝 confined；macOS／Windows／musl／arm64 真实未验收。process-group 无法保证约束脱组后代，no live PGID 不等于 all zombies reaped；Python subreaper 仅测试 fixture，不是产品回收保证。

源位置为 Runner process（工作区引用：`integration/deepseek-harness/packages/runner/runner/src/process.ts`）、local spawn（工作区引用：`integration/deepseek-harness/packages/subprocess/subprocess-local/src/spawn.ts`）、sandbox-local（工作区引用：`integration/deepseek-harness/packages/sandbox/sandbox-local/src/index.ts`）与E2B subprocess（工作区引用：`integration/deepseek-harness/packages/e2b/subprocess-e2b/src/index.ts`）。提交包含新增 `2026-09-14-confined-native-images` Note、README、API／config／type-equiv 同步，以及 `built-confined-native-smoke.mjs`、`fixtures/native-subreaper.py`。本代码基线 process.ts 仍是四参数、没有 Job onStarted；DSH dc745 的 JobManager／FIFO／update 集成进行中未提交。Go 854 reconciliation 同样进行中未提交；固定 Server 基线仍拒 inventory／reconciliation／log_snapshot，不能将产品工作树当完成依据。

### 实际验证回执（本次文档整理不重跑产品测试）

- 最终 source 五 specs 共 208 passed、0 skip：native confinement 15、sandbox 46、Runner process 39、E2B mock subprocess 72、E2B mock terminal 36。最后 payload 改 `/bin/true` 后完整 sandbox 46 再通过、0 skip，随后 2 文件 lint 0 errors／0 warnings；该 46 属于重跑，不加到 208。此前 matcher 仅测试修正后 10 文件 typed lint 0／0，blocking prepare 单例 1 pass／38 filtered，分别记录不累加。
- 官方 bwrap 0.11.0 实测（bash 38）：custom PATH-only generic／exact 捕获；plain Node Loader RO／WW 各 6 次 HTTP，共 12 requests；TERM accepted＋unknown、no-live detection 与 Python no-children 检查。bash 44 是额外 admission-only owned /tmp bootstrap 提前拒绝＋PATH capture，不能并入 12 requests。历史 source 143／旧 289、scratch probe 72＋78 均不累加。
- 隔离候选从 d017 导入 exact owned files、排除 Job，再通过 Runner／subprocess-local／sandbox-local／E2B 相关 tsc。Cordis 99 artifacts 生成 0 change 且 freshness 通过，config freshness、421 type-equiv pairs、export JSDoc 通过；10 generated 与 main 逐字一致。候选已 cleanup，仅保留 main worktree；五包 artifacts 支持此次 smoke。这不是含未提交 Job 的全树验证。
- 文档 mdlinks 1537、mdwrap 1544、budgets 8、doc-typecheck 80 blocks 通过。doc-quick 历史为 15／16，heading anchor 失败修正后 doc-standard 12／12 成功；没有 doc-quick 整组重跑全绿的回执。
- normal hooks：13 pairs 一致，16 文件 staged lint 0 errors／3 项既有 unused-disable warnings；notices／whitespace／vendor 检查通过。不称零 warnings。

共享 R0 fixture 仍为 91 DTO／16 families／12 operations，digest `a5a6a41542f2e382a32ba25fd287f83a33dd310bb29fadacbec1099d776497dc`；原 39 serde probes、外层 RunnerRequest arrays／general 非 Job gap 不变。整体约 15%（10%～20%）不重算；File 3／20、Project 0／7、Computer 0／19，13 大包无一全量完成；G2 支持 native 最多 3／22、file-only 2／22，真实 Go 合规注册仍 HTTP 400。无持久 MCP 闭环或生产替换，无 push／deploy／真实 E2B／model／credential／service 操作。

本次根三文档由未修改的 `sync_webcodex_progress.py --write` 同步，默认模式核验每仓库五个白名单文件，并仅对镜像目录做 diff 检查；授权双仓库 normal hooks docs-only 本地提交 `docs: record confined native execution progress`。INDEX 若无差异不产生提交变更；实际 docs SHA 与变更文件数由提交回执记录，不把 docs 计成功能。