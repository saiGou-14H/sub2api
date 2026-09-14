# WebCodex V6 全量迁移：项目进度评估

本报告第 1–8 节保留文件适配器阶段的历史评估快照，第 9–11 节记录后续进程、认证及 Linux 启动修复，第 12 节记录 Server 凭据持久化与默认关闭路由装配，第 13 节记录 DSH OS API 接受与精确目标退出回执。当前累计 13 条功能提交（Server 5、DSH 8），Server 基线为 `9e4683d19c6d716592bdfb680af2a16cb4f263d0`，DSH 基线为 `4c3570bd5b103e956c88f6a38bf9140bec1a4e85`；第 1–12 节中的未提交、无可信启动回执及歧义 127 描述均是当时快照，当前对应行为以第 13 节为准。当前逐项状态见[开发状态总表](./WEBCODEX_V6_DEVELOPMENT_STATUS.zh-CN.md)，详细验证见[实施进度](./WEBCODEX_MIGRATION_PROGRESS.zh-CN.md)。目标是将 WebCodex Server 原生迁入 sub2api、Runner 原生迁入 DSH，覆盖 V6 完整功能并完成真实原生 MCP 验收。历史区间是人工工程估算，不能把旧快照中的能力描述当作当前行为；本次文档保存未重新运行功能测试或连接生产数据库。

## 1. 结论与统计口径

**总体工程完成度建议按约 15% 管理，合理估算区间为 10%～20%。完整产品端到端尚未通过验收，当前不能替换生产 WebCodex。**

15% 是人工工程量估算，不是自动统计的覆盖率、原工具通过率或工时消耗比例。项目尚无经双方确认的逐功能工时／故事点基线；不同工作包的难度和既有宿主能力复用程度不一致，不能声称精确到个位百分点。

| 观察维度 | 结论 | 含义 |
|---|---|---|
| 原定义与迁移设计 | 已形成完整范围映射；全量机器 fixture 对照仍不完整 | 设计较成熟，不等于运行功能已交付 |
| Server 全量迁移 | 工程估算约 5%～15% | 主要是 Go 协议与内存 Runner registry／HTTP handler；未接入生产业务装配 |
| Runner 全量迁移 | 工程估算约 10%～20% | 正式 Host／FS 基础、有限协议消费及三项原生文件操作 |
| 完整产品工程 | 约 15%，参考区间 10%～20% | 同时考虑定义、两侧迁移、网页融合、跨系统验收和迁移上线 |
| 用户可用的完整新产品链路 | 尚无通过证据 | 尚未形成 ChatGPT 原生 MCP → sub2api 业务授权／事务 → DSH → 持久结果回传闭环 |
| 生产替换条件 | 未满足 | 不能卸载旧 WebCodex 后仅靠两宿主提供约定全功能 |

总体与两侧百分比不是简单平均：总体包含单独计入的定义／设计工作，也包含尚未实施的网页融合和上线验收。当前“端到端未通过”不等于“没有代码成果”；反过来，组件测试通过也不能证明产品链路通过。

本次用作估算检查的权重如下。它们是评估假设，不是用户事先确认的交付权重；以后建立正式工作量基线时应替换。

| 不重叠的工程部分 | 暂用权重 | 完成度估算 | 依据 |
|---|---:|---:|---|
| 定义冻结、源码映射、契约 fixture 准备 | 10% | 70%～85% | V6 全范围已有映射，完整工具／子操作 fixture 尚未迁完 |
| Server 功能迁移与宿主接入 | 30% | 5%～15% | 两个新 Go 包已有验证，主要业务／数据／装配缺失 |
| Runner 功能迁移，含必要 Host 改造 | 35% | 10%～20% | 基础与三个文件操作完成；多数操作族、恢复和平台验收缺失 |
| 网页工具模式、Connector 配置与两侧 UI | 15% | 0% | 现有 Prompt Tool 功能属于宿主旧能力，未见 V6 模式实现 |
| 跨产品验收、数据导入和上线退出旧系统 | 10% | 0%～5% | 已有有限跨语言对照，但 G0、数据导入和切流均未完成 |

用这些区间加权约为 12%～21%；考虑权重本身的不确定性，报告为约 15%、10%～20% 的粗略区间。不能由此推导剩余时间恰好是已花时间的固定倍数。

## 2. 当前真正完成了什么

### 2.1 原协议与 Go／TypeScript 对照

- 原 RunnerRequest 的 27 个顶层字段已保留，继续使用原 `kind`、snake_case 名称和实例／请求身份。
- 首批六类同步操作已实现 canonical codec：`run_shell`、`run_process`、`run_script`、`file_read`、`file_write`、`file_list`。
- 必填字段、null／缺失／零值、默认值、互斥 payload、Unicode 及 i64／u64 极值已有聚焦测试。
- Go ↔ TypeScript 已有跨语言往返检查；部分能力被真正的 Go generation-2 校验拒绝，也有验证。
- 其他已知操作明确拒绝。保留 JSON 槽位不代表嵌套领域已经完成语义验证或执行实现。

证据：[Go protocol 说明](../../backend/internal/webcodex/protocol/README.md)、[Go DTO](../../backend/internal/webcodex/protocol/types.go)、[Go 操作解码](../../backend/internal/webcodex/protocol/operation.go)、DSH codec（工作区引用：`integration/deepseek-harness/packages/runner/runner/src/protocol.ts`）。

### 2.2 Server 轮询与权限组件

- 原 register、poll、result、offline 四个 POST handler 已实现。
- 内存 registry 支持节点、排队、单次交付、结果接收、同实例重连、实例替换、下线、取消和关闭。
- 已验证凭据投影上的 kind、scope、client、owner 与访问组检查已有实现；模型 API key 不自动获得 Runner 权限。
- 已派发事实不会因 waiter 取消而被改成“未开始”；结果接收只代表内存 waiter 接收，不是持久确认。

这些是可供宿主装配的组件。正常 sub2api 启动路径没有挂载这四个路由，也没有真实凭据适配器或生产业务调用者向 registry 派发任务。

证据：[runner 说明](../../backend/internal/webcodex/runner/README.md)、[HTTP handler](../../backend/internal/webcodex/runner/http.go)、[认证投影](../../backend/internal/webcodex/runner/auth.go)、[registry](../../backend/internal/webcodex/runner/registry.go)、[生产路由装配](../../backend/internal/server/router.go)。

### 2.3 DSH 正式 Host 执行与文件系统基础

- 正式 HostToolOwner、私有 scope、guard、ToolRuntime 生命周期已接通，没有伪造 Agent、Session 或模型 turn。
- 文件观察按真实 owner 归属，策略拒绝不能通过改走底层文件 API 绕开。
- 文件系统新增原始字节流、完整原文件 hash 条件和 `createDirs` 选项，并适配 local／sandbox／E2B provider。
- 原始字节流保留 BOM、NUL 和无效 UTF-8，消费方决定有损或严格解码；资源关闭与取消已覆盖。
- 前台 shell 的 managed-range 收敛基础已改进，但尚未形成原 Runner 的 shell／process／script 执行实现。

这些改造降低了后续迁移成本。DSH 原有 shell、Jobs、ACP、LSP 等服务还需要接入原 Runner 的协议、状态与归属，不能直接记为迁移完成。

证据：Host 工具 API（工作区引用：`integration/deepseek-harness/packages/core/tools/src/index.ts`）、文件系统 API（工作区引用：`integration/deepseek-harness/packages/fs/fs/src/index.ts`）、[阶段回执](./WEBCODEX_MIGRATION_PROGRESS.zh-CN.md)。

### 2.4 三项原生文件操作

`@deepseek-ai/dsh-runner/files` 已完成当前范围内的真实本地文件执行：

| 操作 | 已验证的主要行为 |
|---|---|
| `file_read` | 显式 cwd 与路径限制、legacy 原始字节上限、有损 UTF-8、范围读取完整严格校验与 SHA-256、精确六字段结果、真实 present／absent 观察 |
| `file_write` | 观察意图与 hash 条件同时校验、缺省 `create_dirs=false`、原子替换、返回 UTF-8 字节数、只读与非 enforcing provider 拒绝 |
| `file_list` | basename＋提供方 cwd 的 lstat、符号链接分类、UTF-8 字节排序、空目录 LF、完整结果上限 |

文件请求忽略线路 `timeout_secs`，只受本地执行上限限制。取消等待已进入操作收尾，并保留 I/O 失败；真实写入可能已提交后才返回 aborted。卸载撤销 owner，排空调用并回收注册。

已有差异明确保留：DSH 原子目录项替换与 Rust 截断 inode 不完全相同；provider 规范化后的某些错误分类不同；最终外部写者竞争未被声明为全局 CAS。实际 E2B／Windows 端到端行为尚未验收。

证据：文件 executor（工作区引用：`integration/deepseek-harness/packages/runner/runner/src/files.ts`）、读取算法（工作区引用：`integration/deepseek-harness/packages/runner/runner/src/file-read.ts`）、包限制（工作区引用：`integration/deepseek-harness/packages/runner/runner/README.md`）。

## 3. 可直接核实的数量

| 统计对象 | 当前结果 | 分母和限制 |
|---|---:|---|
| RunnerRequest 顶层字段 | 27／27 | 顶层字段保留，不等于全部嵌套领域实现 |
| 本批六类同步 codec | 6／6 | 仅选定的首批六类 |
| 本批六类原生执行 | 3／6 | read／write／list；不是全 Runner 50% |
| 原 File 子操作 | 3／20 | 还有 17 个文件变体未迁入 executor |
| 原 Project 子操作 | 0／7 | 当前 Runner 原生实现未覆盖 |
| 原 Computer 子操作 | 0／19 | 当前 Runner 原生实现未覆盖 |
| 原顶层操作家族 | 17 族中只有 File 部分覆盖 | 不能把 File 族记为完整完成；codec 涉及命令族不等于执行覆盖 |
| G2 必需能力已声明真值 | 2／22，约 9.1% | file_read、file_write；file_list 复用读能力；不是全项目完成度 |
| 首批四条 Server handler | 4／4 | 组件存在 |
| 四条 handler 生产挂载 | 0／4 | 当前正常 sub2api router／DI 未接入 |
| 原 41 张表对应的生产语义迁入 | 未找到已落地项的证据 | 当前 SQL／Ent／装配检查；未连接真实数据库；不要求一定新建 41 张表 |
| ProjectConnector 的 14 项能力 | 未形成生产实现链路 | 原 typed 输入映射是设计，不是已实现 service |
| 完整新产品 G0 | 未通过 | 没有真实 ChatGPT 原生 MCP 成功闭环证据 |

41 张原表中的 `users`、`api_keys` 等应与宿主适配，不是机械复制。sub2api 已有同名表没有原凭据 kind／scope／client 绑定语义，不能直接计成 2／41。

原分母见Runner 操作定义（工作区引用：`source-sync/webcodex/crates/webcodex-core/src/runner_operation.rs`）、G2 能力定义（工作区引用：`source-sync/webcodex/crates/webcodex-core/src/runner_protocol.rs`）、Server 全量映射（工作区引用：`WEBCODEX_SERVER_MIGRATION_MAP_V6.zh-CN.md`）。这些指标存在包含关系，不能相加或等权平均。

## 4. G2 为什么仍不能注册

原 generation-2 要求 22 个基础能力全部为真。当前文件 executor 仅声明其中 2 个。

| 基础能力分组 | 要求数 | 当前已声明 |
|---|---:|---:|
| 文件读写、产物导出、结构化删除、文本 occurrence 编辑 | 6 | 2 |
| Jobs、async Jobs、async shell Jobs | 3 | 0 |
| 结构化验证、Cargo 计数、Go JSON／tool／packages | 5 | 0 |
| 结构化 process、script、internal POSIX、execution Jobs | 4 | 0 |
| LSP 导航、call hierarchy | 2 | 0 |
| 项目生命周期、路径注册 | 2 | 0 |
| 合计 | 22 | 2 |

因此，单独完成 process／script 后也不能立即宣称 G2 可用，还需 Jobs、验证、LSP、项目及其他基础文件能力。当前能力声明类型本身只允许初期的五个字段，后续必须随真实实现扩充，不能直接把缺失能力标为 true。

真实 Loader 文件测试允许测试用 HTTP Server 接受部分能力，以验证本地文件执行。Go ↔ DSH 检查则证明合规 Server 会返回 HTTP 400、执行次数为零。二者验证不同目标，并不矛盾。

## 5. 对照 V6 的 13 个工作包

本表沿用开发设计第 19 节（工作区引用：`SUB2API_DSH_DEVELOPMENT_DESIGN.zh-CN.md#19-全量功能工作包`）。状态以完整工作包退出标准为准，不按一个目录或测试文件计完成。

| 工作包 | 当前状态 | 已有成果 | 尚未满足的主要退出条件 |
|---|---|---|---|
| R0 原契约清单 | 大部分定义工作完成，验证清单部分完成 | 固定源码、41 表／操作家族映射、首批 fixture | 全工具／子操作 schema 与跨语言 fixture 全覆盖 |
| S1 Auth／Store | 授权组件起步 | Runner Principal、kind／scope／owner／client 规则 | 宿主身份适配、原表语义／事务、OAuth／project 修复、数据导入 |
| S2 MCP／Registry | 部分完成 | 内存 registry、四条轮询 handler | 生产挂载、真实认证、MCP、Generic ToolRuntime、WS／inventory |
| D1 Runner core | 部分完成 | 六类 codec、polling、正式 owner、真实 Loader／guard／cleanup | 全 invocation 家族、完整能力声明与状态投影、Host Job／审批 |
| D2 Files／Process／Job | 文件基础片完成 | read／write／list、raw hash／createdirs、取消 | 其余文件操作、shell／argv／script、Jobs 与日志恢复 |
| S3 Connector | 尚未形成迁移实现 | 原输入／事务映射 | task／run／approval／execute／check／finish／review 完整闭环 |
| W1 Workspace／Artifacts | 尚未形成迁移实现 | 可复用 FS 基础 | Git／worktree／checkpoint、完整上传下载／发布／恢复 |
| V1 Validation／LSP | 尚未形成迁移实现 | 宿主已有部分可复用服务 | 原 adapter、计数断言、导航／impact、文档版本与能力一致 |
| A1 Workflow／Agent | 尚未形成迁移实现 | 原模型与调用链分析 | ledger／message／wake／AgentTask／A4a／coding 运行与恢复 |
| E1 可选扩展 | 尚未形成迁移实现 | 原功能清单、宿主可复用基础 | memory／skills／MCP／plugin／SSH／computer／cleanup 的原行为 |
| T1 Transport parity | 部分完成 | 同步 HTTP polling 与部分跨语言对照 | WS／QUIC、job_update／persistent_shell_result、流控与恢复一致 |
| I1 Web 融合 | 尚未形成迁移实现 | 现有宿主 Prompt Tool／账号能力 | web_mcp resolver、两入口防重复执行、Connector 配置、UI、G0 |
| M1 上线迁移 | 尚未实施 | 迁移和回滚方案 | 原数据／凭据导入、排空、切流、退出旧系统与回滚演练 |

按这些退出条件，目前没有一个完整大工作包可以无条件记为全量验收完成；其中已有多个可单独验收和提交的功能片。这是按范围定义统计的结果，并非否定已完成代码。

## 6. 测试能证明和不能证明的内容

以下均为已有回执，本次分析没有重新运行测试。

| 验证 | 已有结果 | 支持的结论 |
|---|---|---|
| Go protocol／registry race | 通过 | 所实现 Go 组件行为与并发 |
| Go ↔ TS 两项检查 | 通过 | 首批字段／整数往返一致、部分能力拒绝 |
| 较早 Runner 全套 | 9 文件，427／427 | 当时版本的包测试，不等于全 WebCodex 功能 |
| 最终原生文件聚焦 | 38／38 | policy 25、Loader 8、deadline 5 |
| 最后测试配置修正 | policy 25／25 | 前述 38 项中的重跑，不增加总数 |
| FS local／sandbox／service | 分批 231 项通过，1 项 Windows 跳过 | 文件系统 provider 与策略基础 |
| FS 消费方 | 339／339 | 与前组重叠 89 项，不能直接相加 |
| E2B 文件 provider／helper | 94／94 | 控制器替身与本地 Linux 脚本，不是真 E2B 服务 |
| 配置生成器 | 32／32 | 新插件子入口发现与排除规则 |
| 根 Host tsc、包构建、plain Node Loader smoke | 通过 | 类型和构建产物可用，真实本地文件可执行 |
| 文档配对与类型编译 | 相关检查通过 | 双语／生成 API／文档代码一致性 |
| 全量 doc-sync aggregate | 未最终整套重跑 | 不宣称全量 aggregate 通过 |
| ChatGPT G0、Windows、真实 E2B／生产切流 | 未验收 | 不得用 mock 或编译结果替代 |

测试通过率回答“已写出的测试是否通过”。它不能回答“尚未迁移的功能完成了多少”。后续验收应记录功能源码、组件测试、宿主装配、持久性／恢复、真实端到端五类证据；不同功能按实际需求选择必要证据。

## 7. 剩余关键路径与建议开发顺序

### 7.1 两条并行主线

**Server 主线：S1 → S2／S3。**先实现真实宿主身份／凭据和原项目、任务、run、执行、审批记录的 repository／事务，再将 Runner handler 和业务调用挂入正式装配，随后完成 MCP／Generic ToolRuntime／ProjectConnector。审批消费、执行预留和审计必须同事务；凭据有效不等于具有项目执行授权。

**Runner 主线：D1／D2 → G2 必需能力。**在现有 Host／FS 基础上迁入真实 argv process、script 的提供方临时文件租约、shell 与 Jobs；同时推进基础文件编辑／删除／artifact、结构化验证、LSP 和项目注册。按真实已实现能力逐项扩充声明，直至 22 项全部成立。

两侧都具备必要能力后，通过真实 Go Server 与 DSH 的成功执行链路验证身份、任务、结果和取消。当前禁止的部分能力注册不能为了提前演示而放宽。

### 7.2 产品验收主线

完成必要 Server／Runner 业务链路后，接入两个 Web 网关入口的工具模式选择、原 ClientWindow／project 关联和账号 Connector 配置。`web_mcp` 必须禁用 Prompt Tool 注入／解析，避免同一动作被上游 MCP 与客户端重复执行。

G0 使用不进入 prompt 的随机文件内容，验证真实 ChatGPT 调用、用户／项目／节点／窗口隔离、并发、续接、撤销、审批、取消和结果回传。所有原生能力与扩展仍按 V6 清单继续迁移；先后顺序不表示删除可选功能。

最后执行原数据导入报告、凭据映射、在途任务排空、切流与回滚验证。只有两宿主独立运行且不依赖旧 Rust 执行核心，才达到目标产品退出条件。

### 7.3 主要难度

- Jobs、持久 shell、ACP：原身份、双流、单一收集者、取消、重启后不重执行以及 unknown 的正确保留。
- 审批／执行事务与外部效果：数据库回滚不能撤销已经写入的文件或启动的进程。
- 文件批量／patch／checkpoint／artifact：部分发布、hash、权限、符号链接和回滚失败。
- 执行环境：本地 sandbox 不能证明远端 E2B／SSH 的同等限制，须有同执行环境 provider。
- Web 原生 MCP：发现或配置成功不等于实际 tools/call，更不等于生产 ChatGPT 支持续接。

剩余约 80%～90% 包含不少高复杂度工作。目前尚未进入“只剩 UI 和上线联调”的阶段。没有完整工时基线和 G0 外部能力实测，不能负责任地承诺精确完工日期。

## 8. 本次分析及提交约定

本次读取了两侧迁移映射、开发设计、当前源码与已有回执；Server 重点检查了生产 router／DI、SQL／Ent、认证接口与 Web 网关，Runner 对照冻结定义核实 17／20／7／19／22 个分母。没有读取真实凭据、连接生产数据库或触发真实模型／E2B。

[实施进度记录](./WEBCODEX_MIGRATION_PROGRESS.zh-CN.md)开头和组件表中的文件适配器旧状态已校正；旧设计文档中的“尚未开始代码”描述属于设计交付时的快照，不能覆盖当前源码和阶段回执。

用户已明确要求每完成并验证一个独立功能就创建一条本地 Git 提交。现有已完成迁移代码已整理为 7 条本地功能提交：Server 2 条、DSH 5 条；提交不改变本报告的完成度估算。

| 仓库／分支 | 提交 | 功能 |
|---|---|---|
| sub2api／`mpc-server` | `0d62b747b` | 原 Runner 协议与六类请求编解码 |
| sub2api／`mpc-server` | `14fa79363` | 权限组件、内存 registry 与四条轮询 handler |
| DSH／`mcp-runner` | `1ba089ff8f` | 前台 shell 等待受管理进程范围停稳 |
| DSH／`mcp-runner` | `6f35daf695` | Host 执行 owner、scope、策略与消费者 |
| DSH／`mcp-runner` | `9f4616fdaa` | 原始文件流、SHA 条件与 createDirs 写入 |
| DSH／`mcp-runner` | `aa048eb14c` | Config 生成器识别显式插件子入口 |
| DSH／`mcp-runner` | `74b24600ad` | Runner 原轮询、原生文件与共享集成 |

最终核对：DSH 工作区和暂存区干净；Server 没有已跟踪差异，唯一未跟踪文件为既有 `backend-service-final.jsonl`，未纳入提交。正常 Git hooks 与最终 diff 检查通过；hooks 报告 3 项 unused suppression 警告、零 lint 错误。功能测试沿用前述阶段回执，没有为提交动作重复执行。准备提交只删除两份 Apache 许可证文件各一个多余 EOF 空白行，未改运行时、测试或配置源码；DSH 对原 155 个变更路径的内容和模式指纹检查支持这一点。

整理期间文件系统双语引用先因 Runner Note 尚未进入暂存区而被 hook 拒绝；将未改动的引用三件套放入最后的 Runner 集成提交后通过，没有绕过 hook。未推送、部署或切换服务。

操作过程另有一项参数规范偏差：部分工具调用错误地带入了被要求省略的 `sandbox_permissions` 字段。调用在既有 unrestricted 环境内执行，没有触发审批或扩大有效访问范围；这不符合本次明确的参数约束。

## 9. 后续开发：POSIX 原生结构化进程

在第 1–8 节评估快照之后，已完成 `@deepseek-ai/dsh-runner/native` 的文件／POSIX `run_process` 组合入口，并建立第八条本地功能提交：DSH `mcp-runner` 的 `1435db0d70d0579b60bfe8e88d1b9e21dd9eb056`，`feat(runner): execute structured POSIX processes through Host tools`。累计 Server 2 条、DSH 6 条，未推送或部署。

真实 argv、独立 stdin、非零／信号退出、两种超时、cwd／guard／sandbox、受管范围退出、有界双流和完整 JSON 结果已接入正式 Host ToolRuntime。原生入口声明三个 true 基础位，file-only 仍为两个；22 位完整准入没有放宽。process profile／环境快照与 Windows／OEM 行为尚未等价，shell/script、Jobs、业务持久化和 G0 仍未完成。

验证包括 Runner 整包 501/501、最终 helper 53/53 回归（与整包重叠）、包含测试的根 Host 类型检查、28 文件 type-aware lint 零警告／零错误，以及源码／构建产物文档编译和普通 Node 真实 Loader 文件→进程冒烟。详细顺序、范围和限制见[进程阶段回执](./WEBCODEX_MIGRATION_PROGRESS.zh-CN.md#原生-posix-结构化进程本阶段验证完成)。正常 Git hooks 通过；首次 hook 的较窄 lint 另报一项已有 unused suppression 警告，零错误，未绕过检查。提交后 DSH 工作区与暂存区干净；Server 仅保留原有未跟踪 `backend-service-final.jsonl`。

本次没有重算整体工程权重，继续沿用约 15%、10%～20% 的粗略评估，不把这个独立子集标记为完整 run_process、完整 generation 2 或生产替换完成。

## 10. 后续开发：managed Agent Token 与宿主身份

第九条本地功能提交已完成：sub2api `mpc-server` 的 `861bb9e78fe1ba7fe1192c7514ada97733bb12e9`，`feat(webcodex): verify managed Runner credentials with host identities`。累计 Server 3 条、DSH 6 条，未推送或部署。

新增验证器按原 token 字节做 SHA-256 查找，校验撤销／到期与当前启用宿主用户，保留原 scope 拆分、顺序、重复及 kind 区别。新 managed subject／owner 使用 V6 允许的宿主不可变 ID 确定性编码，禁止按可修改且不唯一的展示用户名绑定节点。旧数据仍需要显式映射和相关 owner／subject 重写；原用户凭据不会升级为 Agent Token，额外／admin scope 不能越过精确授权。代码提供 repository／用户解析接口，实际凭据存储、迁移、router／DI 及公开启用配置尚未实现。

新增 17 个顶层测试；Runner 和 protocol 两包完整本地 race 通过，最后 HTTP BOM／额外 scope 补例后的聚焦认证 race 也通过（与完整验证重叠，不累加测试总数）。测试只使用公开固定 token 向量、假 repository 和内存 HTTP；旧 loopback exchange 运行，两项 DSH 跨语言测试因未配置 checkout 自然 skip。详见[认证阶段回执](./WEBCODEX_MIGRATION_PROGRESS.zh-CN.md#原-managed-agent-token-验证器本阶段验证完成)。五个功能文件已提交，Server 仅保留原有未跟踪 `backend-service-final.jsonl`。

同时对原 JobManager 的核查确认，不能把 DSH 通用 JobRegistry 的 Agent／Session 归属、生成 ID 和单消费日志直接当作原 Runner Job。先补源码证实的进程启动事实，再迁移原队列、停止、双流快照与更新／重连对账。核查还发现 Linux ENOEXEC 的隐式 shell 回退差异，正在独立修复；上一阶段 POSIX 和沙箱执行回执是历史快照，不能推断此次严格启动修复已完成受限执行。

本节没有重算整体工程权重，仍沿用约 15%、10%～20% 的粗略估计。G0、完整 G2 与生产替换均未完成。

## 11. 后续修复：Linux ENOEXEC 与启动失败未知状态

第十条本地功能提交为 DSH `mcp-runner` 的 `cf374cda9bc06a08e6d548d8e4f6cf32b19339bb`，`fix(runner): enforce Linux native-image launch semantics`。累计 Server 3 条、DSH 7 条。相关源码／测试、7 组双语文档三件套与既有 owning Note 已进入提交，DSH 工作区干净；Server 保留原有未跟踪日志。根目录两份进度记录不属于这两个 Git 仓库，单独保留于开发工作区。

原 WebCodex 测试规定 Linux 无 shebang 文本返回 ENOEXEC，而 DSH 共享 provider 存在 `/bin/sh` 回退。本次增加内部 `native-image` 执行模式和 provider 支持事实，Linux scope／PGID 两条路径落实 execve 无回退，旧消费者省略模式时行为不变。Runner 根据实际 provider 支持声明结构化进程能力；不支持时仅保留文件位，22 项 G2 要求不变。

这次修复同时揭示并明确收紧了两项能力边界。其一，当前私有启动文件还不能在受限沙箱内正确使用；read-only／workspace-write 的 Runner 进程请求在策略解析后直接拒绝，恢复正向执行仍需兼容控制传输。其二，没有可信启动回执时，丢失错误报告的 bootstrap 127 与目标真实退出 127 无法区分，因此严格模式把两者保守归为 `outcome_unknown`。前述第 9 节的 POSIX／沙箱阶段回执是历史快照，不能继续解释为当前完整结果或受限执行等价。

最终五组相关回归合计 174 通过、2 因真实沙箱后端不可用而跳过；根 Host 类型检查、最后 type-aware lint、相关包构建和普通 Node 真实 Loader／HTTP 冒烟通过。冒烟覆盖文件、正常进程、ENOEXEC 拒绝及真实目标 127 的未知结果。两项 Go↔DSH 跨语言 race 在最新开发代码上补跑通过，证明原六类 wire 往返和部分能力注册仍被拒绝；不证明真实凭据数据库接线或完整 G2 执行。七组文档配对、链接／换行／预算／Note 格式检查通过；完整仓库测试、完整 doc-sync 与真实 G0 未运行。正常 pre-commit hooks 通过，较窄 staged lint 有 4 项既有 unused suppression 警告、0 错误。

后续优先恢复受限 strict 执行并补可信启动事实，再接原 Job 队列／停止／更新／库存；Server 并行补现有宿主用户和 managed 凭据 repository、迁移与默认关闭路由接线。详细回执与未完成项见[最新实施进度](./WEBCODEX_MIGRATION_PROGRESS.zh-CN.md)。不重算此前约 15%、10%～20% 的粗略工程评估，不把独立修复称为迁移全量完成；未推送、部署、读取真实凭据或调用模型。

## 12. 后续开发：Server 原凭据存储与默认关闭路由

在前十条功能提交之后，Server 增加两条独立本地功能提交，累计 12 条（Server 5、DSH 7）：

| 提交 | 已完成范围 |
|---|---|
| `b3614bba306253eb53299f7bb2abbeb6274d7329` | 原 managed 凭据 SQL repository／嵌入迁移、当前宿主 User 适配 |
| `9e4683d19c6d716592bdfb680af2a16cb4f263d0` | 四条原 polling 路由的默认关闭配置、实际 router／Wire 装配与退出回调 |

原 `api_keys` 以 `wc_api_keys` 命名迁入，保留全部 12 个字段及 NULL／有符号 Unix 秒生命周期。物理 `user_id` 是既有宿主 `users(id)` 的 BIGINT 外键，在 repository 边界投影为 `sub2api_user_<正整数ID>`；原认证 `UserID` string 契约不变，无第二套用户或模型 key 授权域。实现原 Insert／GetByID／GetByHash／首次撤销时间保留／last-used 更新，并通过真实 `UserRepository.GetByID` 检查同 ID、`DeletedAt` 与 `IsActive()`。这不等于已实现公开签发、管理或导入流程。

四个 `/api/shell/agent/{register,poll,result,offline}` 路径已进入正常 Server 装配；默认 `webcodex_runner.enabled=false` 时不创建 registry，也不挂这些路由。显式启用必须提供五项正数限制：`max_runners`、`max_pending_per_runner`、`online_window_seconds`、`max_body_bytes`、`max_token_bytes`；秒数还须可表示为 Go duration，没有任意生产运行默认值。允许来源的 CORS OPTIONS 在开／关状态都可返回 204，不授予认证；未认证 POST 分别为 401／404。退出使用 `RegisterOnShutdown` 异步调用幂等 `Close`，不宣称 HTTP Shutdown 等待回调完成；测试另行等待 registry waiter 结算。

最终证据为原凭据 repository／迁移结构与真实 User repository＋Ent 的 SQLmock race（任务 408）、配置环境隔离／真实 CORS／mounted 认证／全部既有 HTTP ingress 聚焦 race（任务 423），以及父代理报告的实际 `cmd/server` 编译成功（任务 421，no-tests）。SQLmock 使用假记录；无真实 PostgreSQL DDL／外键／并发事务执行，无生产服务启动。这些回执证明已实现范围和宿主类型装配，不证明完整业务权限、持久结果或 G0。

当前 Server 后续顺序为：凭据签发／管理／显式导入与真实 PostgreSQL 迁移验收；原项目／任务／run／执行／审批事务及真实业务派发；registry 持久投影、其他凭据族、MCP／Generic ToolRuntime／ProjectConnector。单语句撤销不代表多记录事务，独立认证查询不保证与并发撤销原子；不取消已经派发的远端工作。DSH 新阶段仍在进行且未计入提交或能力统计，当前限制继续按第 11 节和状态总表保留。

整体仍按约 15%、10%～20% 粗略管理，未重算权重；13 个大工作包尚无一个满足全部退出条件，22 项待办及剩余 17 项 File 操作均不缩减。历史第 1–11 节保留各阶段当时的未完成描述；当前 Server 状态以上述提交和[状态总表](./WEBCODEX_V6_DEVELOPMENT_STATUS.zh-CN.md)为准。未推送、部署、连接真实凭据／模型／数据库；详细源映射和命令见[实施进度](./WEBCODEX_MIGRATION_PROGRESS.zh-CN.md)。

## 13. 后续开发：DSH OS API 接受与精确目标退出回执

第十三条本地功能提交为 `4c3570bd5b103e956c88f6a38bf9140bec1a4e85`（DSH，59 文件，任务 450 提交成功）。累计功能提交 13 条（Server 5、DSH 8）；Server 基线仍为 `9e4683d19c6d716592bdfb680af2a16cb4f263d0`。第 1–12 节保留历史原文；其中未提交、缺启动回执、真实目标 127 尚未知的判断不再代表当前 DSH。

可选且不 reject 的 started 回执提供 OS API 接受／已证实拒绝／未知，目标退出另由精确子进程结果报告。仅 typed C 证明拒绝并确认受管范围退出后才返回 not_started；已发生效果后的 errno 外形错误仍是 unknown，不继续 PATH 重放。真实目标退出 127 返回 completed／127。首次 target-exit 锁存后，即使交换文件丢失或 carrier 迟到错误也保留结果；目标终态与范围清理继续分别报告。私有状态与启动错误读取分别限 256 B／16 KiB，nofollow／nonblock；允许已打开后 unlink 的 inode 链接数为 0，拒绝硬链接数大于 1。

实现通过实际 Node addon 的 posix_spawn，支持范围为 Linux glibc 2.34+ x64／arm64，真实运行验证仅有 x64。回执证明 OS API 接受，不保证 exec 观察、目标 main／就绪或模型行为，不使用 ptrace。旧 flock 符号 ABI 兼容检查不等于实跑旧 glibc；完整 musl／macOS／Windows／E2B、完整 tarball、真实 systemd／confinement 矩阵未完成。

最终 local provider 与 Runner 测试合计 340 通过／11 项平台或 confinement 跳过（任务 448），其中 Runner process 37 通过、native composition 14 通过／2 跳过均包含在合计内；重建后的实际 Node source／built 入口 4／4，C tag 修正后的 native C＋host packed entry 32／32（任务 443）另行通过。各组不相加为总覆盖率。TypeScript／完整 type-aware lint 0 错误；配对 772、JSDoc／diff 通过。提交 staged lint 有 3 项既有 disable comment 警告（index.ts:248、spawn-runner.ts:290、spawn.ts:602），对应注释不在本功能 diff 中，不能称提交零警告。第三方 generator 无净 diff，提交后代码工作树干净。较早 type-equiv 418 块＋418 派生块、Note 格式 307、mdlinks 1533 均为已收取的独立回执；最新进度快照另行核验内容、manifest、路径和锚点。最终 x64 addon 的 readelf 只列出 GLIBC_2.2.5／2.4／2.15，没有 GLIBC_2.34 加载依赖，仍不宣称实跑旧 glibc。

下一项 D2 仍是受限 strict 最终目标原语与私有 FD 控制传输。read-only／workspace-write 继续拒绝；现有 strict 仅约束 argv[0]，外包 wrapper 不能证明最终目标。bwrap 的 tmpfs /tmp 会遮蔽宿主交换路径；现有 Sandbox confine 与 Linux Subprocess stdio 没有保留控制 FD／内侧 helper 的完整约定。只读设计建议由 provider 安排 wrapper 内的受信 helper，再对最终目标保留 closefrom(3) 并独立回传事实；相关 API 是提案，不是现有能力。实现与真实 backend／scope 证明完成前，不移除此待办；随后仍需 Job／profile／run_shell／run_script 闭环。

Native 仅在能力可用时声明 3／22，file-only 2／22；三项 File 操作与剩余 17 项不变。整体仍按约 15%、10%～20% 粗略管理，13 个工作包尚无一个完成全部退出条件，22 行待办完整保留。本功能不代表完整 G2、原生 MCP 闭环或生产可替换。此前快照 docs 提交 `4ae8b0231`／`1bc5f42614` 仅记录第十二条阶段；最新十三条快照按状态总表的生成与校验机制保存，具体提交以各分支 Git 历史及 manifest 为准，docs 提交不增加功能数。
