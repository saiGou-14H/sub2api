# WebCodex V6 全量迁移：项目进度评估

本报告第1–24节保留历史，第25节记录第27阶段原生Skill列表。累计27条代码／功能修复（Server11、DSH16）＋3条fixture-only；Go行为基线 `746e98015112dfdd67837d2b72fa60015a96b0e4`，DSH `a6bb656769050a320bb8046e7bc43b903b831135`。通用scanDir与SkillList consumer已提交，File真实5／20、剩余15项，双方同步codec8／8，Go Filecodec5不变。最末listpolicy25＋Loader14＝39与先前client28分开，不称67一次aggregate；真实E2B未执行。listing P2同提交修复，旧Skill read空cwd／间接ENOTDIR差异待验证。13大包none fully complete、15%（10%～20%）、Project0／7、Computer0／19、G2file2／native3of22合规HTTP400，无完整持久MCP／生产替换不变。当前见第25节及[状态总表](./WEBCODEX_V6_DEVELOPMENT_STATUS.zh-CN.md)，准确构建／测试范围见[实施进度](./WEBCODEX_MIGRATION_PROGRESS.zh-CN.md)。本轮正常保存镜像，不重跑产品tests或doc-sync aggregate。

## 1. 结论与统计口径（第 1–8 节为文件阶段历史快照）

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

## 14. 十六条阶段历史快照：管理入口、Job 数据和 PGID 私有通道

累计已提交 16 条代码／功能修复（Server 6、DSH 10），新增三条实际保存点：

| 提交 | 归属 | 当前已完成的独立范围 |
|---|---|---|
| `a616f55e88bc37608ddcf883b980942be168190b` | Server | 宿主账户管理 Runner 凭据：create／register_hash／list／revoke 四条 POST，14 文件 |
| `af6b3d1fafa11b78f26f7c75af404c09e90b7a1c` | DSH | 原 Job DTO／context 11 字段／12 个精确生命周期及 start_process_job／stop_job 独立 codec |
| `894376ebbe9ae491a5c3161af1dc1905ab97b793` | DSH | PGID native-image 通过 fd3 私有 Unix socket 交换请求和回执，18 文件 |

当前代码基线为 Server `a616f55e88bc37608ddcf883b980942be168190b` 和 DSH `894376ebbe9ae491a5c3161af1dc1905ab97b793`。完整功能表见[状态总表](./WEBCODEX_V6_DEVELOPMENT_STATUS.zh-CN.md)，具体实际回执见[实施进度最新阶段](./WEBCODEX_MIGRATION_PROGRESS.zh-CN.md#第十四至十六条阶段公开凭据管理job-数据与私有-socket)。

公开凭据管理已经实现：JWT Bearer、当前宿主 role、BackendModeUserGuard、同一 GlobalPanelRateLimit，四管理路径 audit 整体正文省略、no-store，configuredMaxBodyBytes 不再固定 64 KiB。SQL 只存 hash，`wc_agent_`＋64 hex 明文仅一次返回；原 defaults／scopes／status ordering／list／revoke owner 与 kind、canonical `sub2api_user_ID`／BIGINT 外键保留。任务 477 离线 Go unit／race 的相关 server／middleware／repository／protocol／cmd/server selection 通过，但使用 real host＋fake SQL。显式旧凭据全量导入、跨 account family 独立 hash 检查及真实 PG 迁移／约束／事务尚未完成。

Job codec 保留原数据与状态、64 位无损，local fixture 不依赖 sibling checkout。初次 106 项（Job 80＋client 26）、tsc／lint／gates 通过；六类同步 consumer 仍拒 Job，没有 Job 执行或传输能力。实际 Rust oracle（工作区引用：`.scratch/job-serde-oracle-20260914/REPORT.md`）使用 serde 1.0.228／serde_json 1.0.150，39 probe、Cargo exit 0，确认 positional struct arrays／单 key null unit maps、非 default Option 数组缺 slot 拒绝、middle default 不移动字段；inventory 只验证空 jobs，activity 只验证 serde。TS／Go 修复及共享 golden fixtures 扩展进行中；没有最终 SHA，不计完成，外层 generic RunnerRequest arrays／非 Job 结构旧 gap 保留，不称全 serde parity。

PGID native-image 一份 ≤8 MiB request＋EOF、最多两份各 ≤16 KiB response；最终 argv／cwd／env 不经文件／命令行，helper 关闭目标 FD ≥3，`/proc/fd` reopen 为 ENXIO，stdout 无法伪控制。仅 pre-supervisor chdir 为 known-not-started，accept 后通用错误 unknown，不重放 PATH。scope 仍用文件，正式 Runner RO／WW 仍拒绝。最终六文件 132 通过／2 跳过，source／plain Node built 6／6，typed lint／JSDoc 通过，正常 hooks 0 错误／2 项 unused-disable 警告。C 未改且未重跑 C matrix；十三条阶段 340／11 为历史回执，不能混加。

bwrap 实测（工作区引用：`scratch/bwrap-probe-20260827/REPORT.md`）以自有官方 0.11.0 非 setuid `build/bwrap`（SHA-256 `28ba628600c9de65808348daa60e7fc1b6f745a01cd483d5a698c6afb5e8bc60`）证明原 RO／WW profile＋fd3 source／built 四格可行，72 个证据断言覆盖实际文件效果：RO write／create／direct truncate 拒绝，WW 只 workspace 允许。没有 `--preserve-fds` 选项；namespace 可用，缺 binary 不能视为 kernel 硬阻塞，Landlock ABI1 direct truncate 不足仍成立。binary 仅保留供开发测试，未全局安装。

生命周期探针（工作区引用：`scratch/bwrap-probe-20260827/LIFECYCLE.md`）另有 78 项证据断言：取消 outer bwrap 丢目标退出回执，保持 started＋unknown；无 live survivors，但 init zombies 由 fixture subreaper 回收，不代表 provider reap all。early profile failure 是 ECONNRESET→unknown，不是 clean EOF。仅 PGID 探针通过，provider auto／linux-scope FD 转发与正式 Runner 功能未验收。

在研 Go legacy 结构化 process Job registry／HTTP job_update／default-off、独立 `max_jobs_per_runner` config，拒绝全部 reconciliation／inventory／log_snapshot；DSH formal bwrap native confinement Service／provider／当前 Runner consumer 使用显式 PGID 配置、不自动降 owner。均尚无 SHA／验收；Runner JobManager／FIFO／update 发送尚未开始。待 parent 提供最终 R0fix／GoR0 SHA 和计数／测试回执，才调整为 18 条保存点。

工程估算继续约 15%（10%～20%），不因提交数增加重算权重。13 个大工作包无一全部完成，native 3／22、file-only 2／22；合规 G2 注册仍 HTTP 400／zero exec，File 3／20、Project 0／7、Computer 0／19。部分进行中工作与已提交保存点分别统计，不能称全功能已完成或可替换生产。

本次三份根文档先供审查，未 stage／commit／同步 `--write`。两仓库均有其他 agent 未提交修改，不声明干净；parent 明确协调后才生成两侧各五文件白名单快照并分别 docs commit，文档保存不增加代码数量。无产品测试重跑、push、deploy、模型、数据库或生产操作。

## 15. 当前十八条阶段：两端 Job R0 完成

第 17 条 DSH `a5ed7187a781310ae33314488b68bcb6e32d73b3`（`fix(runner): accept original Job serde input forms`，13 文件，父提交 `894376ebbe9ae491a5c3161af1dc1905ab97b793`）及第 18 条 Server `cc736b7c16d5a5325c50aed90382e1b1aa210f68`（`feat(webcodex): preserve original Job protocol data`，15 文件，父提交 `a616f55e88bc37608ddcf883b980942be168190b`）均已提交。累计 18 条（Server 7、DSH 11），两条完整 SHA 是 R0 功能阶段基线，包含最新验证补充的当前基线见第 16 节；第 14 节等待 R0 SHA 的文字属于历史快照。

共享 fixture 为 90 DTO／16 family／12 operations，SHA-256 `0bec4ca7c4e443160498835ca070019a4fcd98110e0478233b75edb7fcb01718`，包含 direct oracle rows 1–7、35–39 共 12 条。fixture cmp＋exact oracle 对照通过。Job 的 positional struct arrays／单 key null unit maps 等语义已修复；外层 RunnerRequest 数组和 general 非 Job 替代形式仍有 gap，不称 all-serde parity。

最终 parent 回执为任务 514 DSH 五 specs 487／487（Job 161＋protocol 280＋client 26＋client safety 7＋config 13），两文件 lint／noEmit 均 exit 0；任务 515 Go protocol 30 top＋175 subtests、runner 59 top＋118 subtests 全部通过。两条 opt-in interop 初因未设 checkout skip，任务 517 显式设置 DSH_RUNNER_CHECKOUT 仅补这两条并通过（0.19s／0.09s，package 0.281s）。三组文档配对、mdlinks 1533／wrap 1540／Notes 307、正常 hooks 通过。测试计数不与旧阶段混加，文档保存不重跑产品测试。

这两条完成原 Job 数据与输入形式，未完成 Job 执行链；同步 consumer 不接 Job，Runner JobManager／FIFO／update 发送尚未开始。Go process Job registry／HTTP job_update 和 DSH formal bwrap Service／provider／consumer 继续 in progress，无最终 SHA／验收，不计数。native 3／22、file-only 2／22、G2 HTTP 400／zero exec、File 3／20、Project 0／7、Computer 0／19 不变。整体约 15%（10%～20%），13 个工作包仍无一全部完成，不提高为全功能或生产替换完成。

三份根文档按授权同步为两侧各五文件快照，并执行默认核验；暂不 stage／commit，等待 parent 下一步协调两条 docs 提交。两侧仍有其他 agent 未提交改动，不声明工作树干净。详细回执见[实施进度](./WEBCODEX_MIGRATION_PROGRESS.zh-CN.md)，当前功能表见[状态总表](./WEBCODEX_V6_DEVELOPMENT_STATUS.zh-CN.md)；文档保存不增加代码数量，无 push／deploy／model／DB／production。

## 16. 当前验证补充：18 功能／修复＋2 条 test 提交

当前基线为 DSH `1be7718659c239eaa9e441ba3ca064ceb6277657`（`test(runner): cover positional Job decimal lexeme`）和 Server `a8b34712b56172884a1256f80b1afb37665da4cb`（`test(webcodex): cover positional Job decimal lexeme`）。两端各仅一个 fixture 文件的一行，补原已有 TS 单测覆盖的 `1.0` 数组 integer 负向 golden，无实施范围扩展；仍为 18 个代码／功能修复（Server 7、DSH 11），两条验证不增加功能序号和工程完成度。

共享 fixture 当前 91 DTO／16 families／12 operations，双端 SHA-256 `a5a6a41542f2e382a32ba25fd287f83a33dd310bb29fadacbec1099d776497dc`。最新 TS Job 162／162、Go 共享 fixture 0.005s、cmp／diff 通过。第 15 节的 90 DTO、旧 digest、五 specs 487／487 为新增此 fixture 前的准确历史；未执行 488 项全组重跑，不拼成虚构的新回执。外层 RunnerRequest 数组和 general 非 Job 替代形式仍有 gap，Job 执行和完整 G2 未完成。

当前约 15%（10%～20%）估算与 13 工作包无一全量完成不变；其他并行实现仍 in progress，不计功能。根三文档与两侧五文件镜像同步保存“18 功能／修复＋2 验证”阶段并默认核验，不 stage／commit，等待 parent 审查最终 diff。

## 17. 十九条阶段历史：Server legacy process Job 已提交

第 19 个功能／修复是 Go `efa85831ef08995c54ab63dd33bc57afd7106acb`（`feat(webcodex): manage legacy structured process Jobs`，23 文件），父提交 `a8b34712b56172884a1256f80b1afb37665da4cb`。累计 Server 8＋DSH 11＝19，另两条 fixture test 提交不计功能。当前 Go 基线为本提交，DSH 仍为 `1be7718659c239eaa9e441ba3ca064ceb6277657`；第 1–16 节 Go Job 在研描述保留为历史。

Server 已实现 legacy 结构化 process Job 的独立 records、Start／Get／List／Log／Stop 与原 job_update 第五条 transport 路径，另四条凭据 management 共九路径。真实 managedVerifier 校验 scopes／owner／group／active instance／request；poll 释放 pending 并保留 dispatch binding；Stop 防重复、queue 满不改 state、terminal first latch。原 optional sequence 仅记录，保留 finished fallback；双流 256 KiB、绝对 cursor／tail reset，List 20～100、900s 按 Server observed TTL。独立 max_jobs_per_runner 必须显式正数、无 pending 回退，enabled 现在要求六项正数限制，disabled 不建依赖。

Host project／scope 授权仍前置；没有新增用户 dispatch HTTP 或 MCP，没有 Runner JobManager、持久化／跨进程恢复；Close 不保证远端停止。inventory／reconciliation／log_snapshot／script／validation／detached／SSH 继续拒绝。原 G2 strict 与 DSH native 3／22 拒绝不变，不能把 Server 内存 registry 功能记为完整异步 Job 执行闭环。

最终回执：任务 530 离线 Go unit／race 对 config／runner／server 使用 `^Test(WebCodexRunner|Job)`，分别 1.054s／1.479s／1.237s PASS；旧同步 queue／HTTP 选择回归任务 516 为 1.299s PASS。管理路由初选 `^TestAgentTokenManagement` 命中零项，不是覆盖；改 `^TestWebCodexAgentToken` 后 server race PASS 1.131s。parent 提交执行者核对 23 白名单路径、diff／index 空且无后续源码修改，Go 在该收尾点除 mirror／scratch／旧日志外功能树干净；不扩大为整个工作树干净。

共享 fixture 仍为 91 DTO／16 families／12 operations，SHA-256 `a5a6a41542f2e382a32ba25fd287f83a33dd310bb29fadacbec1099d776497dc`，最新 TS Job 162／162、Go fixture 0.005s／cmp／diff 回执保留；旧 487 项是新增 fixture 前历史，无 488 全组重跑。正式 bwrap 仅部分 source／built 验收、仍 in progress，无最终提交不计功能；Runner JobManager／FIFO／update 发送未开始。整体约 15%（10%～20%）、13 大工作包无一全部完成，其他能力计数与全量目标不变。

根三文档和镜像保存为 19 功能／修复＋2 fixture 验证阶段，原脚本同步及默认／diff 检查后不 stage／commit，等待 parent 协调最终快照。无产品测试重跑、push／deploy／model／DB／production。

## 18. 二十条阶段历史：受限 native 执行已提交

第 20 个独立功能／修复为 DSH `655b2a643e49020f32641839753d45edf631ff06`，`feat(runner): execute confined native images through private bootstrap`，62 文件、+1230／-88，normal hooks 本地提交。累计 Server 8、DSH 12，共 20；另 2 fixture-only 不计功能。Server 固定 code `efa85831ef08995c54ab63dd33bc57afd7106acb`，第十九阶段 docs 已保存于 Go `4a58b994def92dec8372dc0725f4b31f62bdb69b` 和 DSH `d017b44b6eccba005c0d0126cb3b1f2042343002`；后续并行源码即使提交也不暗增此快照计数。私有 target channel 是既有功能，此次新增正式受限集成。

D2 受限 process 的已完成子集为 `SandboxService.prepareNativeImage` 闭包、Linux actual bwrap／full enforcement＋显式 `nativeImageContainment: 'process-group'`、local wrapper bootstrap-only 与 Runner consumer，最终 argv／cwd／env 经 fd3。default／PTY 拒绝 defined field，E2B 两入口在远程资源前拒绝；bwrap default PATH 捕获为 absolute executable，用于 generic／exact／wrap，generic `/bin/true`。Runner 在原 signal deadline 外以 performance elapsed 检查阻塞 prepare 后、spawn 前预算，超预算 not_started 且 spawn 0。源码与 README／新增 Note／API／config／type-equiv 已同步。

D2 尚未完整完成：pkg 不支持 confined，unconfined 保留；已验 source／plain Node artifact。auto／scope confined、linux-scope FD、profile／环境快照及真实 macOS／Windows／musl／arm64 仍缺。WW /tmp 遮蔽 bootstrap absolute argv／file URL／realpath 且未被 workspace bind 恢复时提前拒绝；不广 mount，不承诺找全 transitive imports，未识别依赖仍 possible unknown。无 valid target receipt 不虚成功；process-group 脱组限制保留，no live PGID 不等于 all zombies reaped，Python subreaper 仅 fixture。DSH dc745 JobManager／FIFO／update 集成正在进行、未提交；本基线 process.ts 仍四参数且无 Job onStarted。Go 854 reconciliation 进行中、未提交，固定 Server inventory／reconciliation／log_snapshot 仍拒绝；完整 Job 执行／停止／恢复及持久结果待验收。

准确验证范围：source 五 specs 208 passed／0 skip（native confinement 15／sandbox 46／Runner process 39／E2B mock subprocess 72／terminal 36）；最后 `/bin/true` 后 sandbox 46 再通过、2 文件 lint 0／0，不累加。此前 matcher 仅测试修正后 10 文件 typed lint 0／0、blocking prepare 单例 1 pass／38 filtered 分列。官方 bwrap 0.11.0 plain Node Loader RO／WW 各 6 HTTP＝12 requests，TERM accepted＋unknown、no-live detection／Python no-children；bash 44 admission-only 的 owned /tmp 拒绝＋PATH capture 不计进 12，旧 source 143／289 和 scratch 72＋78 不相加。

候选从 d017 导入 exact owned files 并排除 Job，通过 Runner／subprocess-local／sandbox-local／E2B 相关 tsc；Cordis 99 artifacts 0 change＋freshness、config freshness、421 type-equiv pairs／export JSDoc 通过，10 generated 与 main 相同，五包 artifacts 支持 smoke；候选 cleanup 后仅 main worktree。文档 mdlinks 1537、mdwrap 1544、budgets 8、doc-typecheck 80 blocks 通过；doc-quick 历史 15／16 的 heading anchor 失败修正后 doc-standard 12／12 成功，不能称 doc-quick 全组重跑通过。normal hooks 13 pairs、16 文件 staged lint 0 errors／3 旧 unused-disable warnings，notices／whitespace／vendor 通过。详细回执与源路径见[第二十阶段实施进度](./WEBCODEX_MIGRATION_PROGRESS.zh-CN.md#第二十条阶段正式受限-native-执行已提交)。

不因一个功能重新加百分比：整体仍约 15%（10%～20%），13 大工作包无一全部完成。File 3／20、Project 0／7、Computer 0／19、G2 native 最多 3／22／file-only 2／22、真实 Go 注册 HTTP 400 不变；R0 91 DTO／16 families／12 operations 与原 39 serde probes 不变，无持久 MCP 闭环及生产替换。本次由未修改的原同步脚本保存两侧各五文件白名单镜像，默认及镜像 diff 核验后 normal hooks docs-only 本地提交；不重跑产品测试，不 push／deploy／真实 E2B／model／credential／service。

## 19. 二十一条阶段历史：Server 同实例 process Job 对账

Go `40817b71370bea996e21519a24268fae03b87338`（`feat(webcodex): reconcile same-instance process Jobs`）已本地提交，15 文件、+1544／-34。当前功能 21＝Server 9＋DSH 12，另 2 fixture-only 不计功能；DSH 仍固定 `655b2a643e49020f32641839753d45edf631ff06`，dc745 Runner Job 执行／FIFO／update 集成进行中、未提交，不计第 22 项。

S2／D2 Job 新增已提交子集是同 Server／同实例、已知已授权且已派发 process Job 的原库存与快照恢复。`job_recovery_grace_seconds` 独立显式正数才允许 reconciliation，默认 0 拒绝；MaxJobs／MaxPending 各自独立。全量 typed inventory 预检后，锁内原子核对 owner／group／client／instance／request／context 再应用。active 在 terminal 前，最多 64＋64＝128 条，完整 Rust 等价 JSON 含 metadata 上限 1 MiB，HTML／U+2028／U+2029 与字面反斜杠转义计数正确；每 snapshot stream 64 KiB，饱和 cursor 算术且不倒退。

sequenced 状态只接受 canonical Runner-owned 状态与正序号，finished 等于 terminal，completed 要求 exit 0，active 无 exit／duration；terminal 不可改，旧序号忽略，snapshot 与 chunk／tail 互斥，同实例 capability 降级拒绝。显式 offline 或观测 stale 从首次观测开启固定 grace；每 Job 一个有界可取消 timer，恢复／terminal／Close 取消，迟到合法 snapshot 也不能逃过过期 lost。无效授权／库存 shape 不修改状态；恢复期间 stop 拒绝，authoritative running 恢复后可再 stop。Get／List（包括过滤）／Log 显示 recovering，内部原 12 states 保留，terminal 优先；900 秒 TTL 从 Server first terminal observation 起算。

仍缺退出条件：未知／过期／Server restart／workflow／validation／SSH／detached 跨实例库存拒绝，无持久恢复。cap=false 保留原 inventory／log_snapshot 拒绝与 legacy optional sequence／tail reset／finished fallback，不能泛称所有库存仍拒绝或完整 Job 闭环已成。G2 原 22 位未放宽，真实 DSH native 3／22 注册仍 HTTP 400；真实跨端 G2／MCP／持久结果未验收。

验证回执分开记录：原主体离线 Go 1.27.1 `-tags unit -race -count=1` runner 78 top＋182 sub PASS、2 条原 DSH opt-in interop SKIP；protocol 30 top＋176 sub PASS；server／config 的 `^TestWebCodexRunner` 分别 17 top＋23 sub、4 top＋17 sub PASS。新增 reconciliation 测试主体 17 runner top＋最后 public overlay 1＝18 runner top，路由 1 top＋6 sub、config 1 top＋5 sub，包含于对应选择范围，不另加为总覆盖。最后 overlay 窄 race 为 15 top＋18 sub、无 skip、1.338s，验证 Get／List／Log 经 inventory／snapshot 恢复及 terminal、内部状态不变；不累加为全套总数，protocol／config 未为 overlay 重跑。gofmt／diff checks 与正常提交通过，提交收尾 index 空；本轮文档准备不重跑产品测试。源引用与详情见[第二十一阶段实施进度](./WEBCODEX_MIGRATION_PROGRESS.zh-CN.md#第二十一条阶段server-同实例-process-job-对账)。

整体仍约 15%（10%～20%），13 大包 none fully complete；File 3／20、Project 0／7、Computer 0／19、R0 91 DTO／16 families／12 operations、原 39 serde probes 不变。未做真实 DB、外部模型、生产或 push。本次仅准备根三文档和每侧五文件镜像，sync／default／diff 核验后不 stage／commit；既有 docs20 为 Go `18056603442a4001ca0d6a6fc7ab72dc7b9876e3`、DSH `ba6e42579504c21e3a7494a83d48bf34467a4fdc`。后续收到完整第 22 阶段资料才统一保存两条 docs 提交。

## 20. 二十二条阶段历史：Runner process Job 执行已提交

DSH `944634f0b1b311f0ecba124ceb9681bc12f15322`（`feat(runner): execute and deliver structured process Jobs`），33 文件、+1870／-115，previous 为 docs20 `ba6e42579504c21e3a7494a83d48bf34467a4fdc`。累计功能 22＝Server 9＋DSH 13，fixture-only 仍 2；Go 固定 `40817b71370bea996e21519a24268fae03b87338`。Go file_skill_read_file 下一片与两份外包 type fixture 修正尚无最后提交回执，不计第 23 项或新 test 提交。

D2 Job 已有原 start_process_job／stop_job、独立 provider、HTTP poll／FIFO／private Host ToolRuntime／actual native process／legacy job_update 单发送者链路，无 Agent／Session／ctx.jobs／模型。processJobs 显式可选对象，省略关闭；Schemastery optional union 的 source／Loader 验证通过。Job timeout 1–3600 秒、独立 local max 3600000 ms，不受 sync 1–120 秒限制。private onStarted 要 actual started receipt 且两个 PipeTail reader attached 才发布 running 和原 working／process_running／runner_execution；原四类进程结果映射 failed／lost／timeout／stopped／completed，terminal immutable。queued stop 先 splice 不启动；running stop 真实 cancel，受限 target-exit 丢失保留 lost／outcome_unknown，不虚 stopped。

concurrency 1–64 FIFO，active 64／terminal 64 与 900 秒历史、四生命周期更新、双 stream 各 64 KiB；records／bindings／bytes／body／storage 全信封预算，首 stop binding 预留及 terminal／卸载 owner 预留释放。Symbol.for 进程内 owned identity hash／count 账本跨 provider reload／卸载／duplicate imports 保留，旧 request／Job 不再执行、冲突拒绝，aggregate maxBindings／maxBindingBytes 耗尽拒新 admit，配置降低仍识别旧 duplicate；global 无 Context／handle／snapshot。pending updates 与 snapshots 不重建，身份抑制不等于结果恢复。

HTTP 单 worker 发送 immutable replacement tails／null chunks，同 body retry 不执行第二次；无 inventory／log_snapshot／reconciliation 广告，legacy 无 seq fence，超时旧 HTTP 仍可能晚到。因此 Go21 与本片尚非跨端 sequenced 闭环。永久 poll／send 失败 abort 并 await cleanup，硬容量耗尽可能只有 local sanitized status，poll 保 stop，不承诺 Server 拒绝 ACK。script／detached／SSH／validation Job 未实现，structured_execution_jobs 仍 false。

最后 primary 88／88 零 skip＝manager 33＋jobs client 4＋actual source Loader 8＋process 43；官方 bwrap RO／WW 由 Python owned subreaper 收全 no final children。latest plain Node built full／unrestricted、RO、WW 三 policy smoke 均 PASS，含 Job 121 秒 decoder、literal argv／stdin、真实 exit127、queued／running stop、dup／unload cleanup／受限 unknown；同实例 main Loader HMR 后旧 marker／running 仍 once，新 Job 可执行。较早 216 pass（162＋26＋7＋21）零 skip 为另一范围且部分重合，不与 88 相加。

421 type-equiv／export JSDoc／相关 tsc 通过，API99 catalog／config／paired zh／subsystem 同步。doc-sync 原 33／34，唯一 doc-typecheck 的 Host 测试类型问题；Job 自身和两份旧 Runner fixture 已随 944 修复，余下 pwsh-sandbox constructor Config 与 subprocess-local exactOptional／terminal env 两份外包测试修正待最终回执，不能称叶检查修后 PASS 或 aggregate 全绿；正常 Job hooks 最终细节待 receipt。源码及完整证据见[第二十二阶段实施进度](./WEBCODEX_MIGRATION_PROGRESS.zh-CN.md#第二十二条阶段runner-process-jobs-执行与-legacy-发送)。

整体约 15%（10%～20%）不变，13 大包 none fully complete，File 3／20、Project 0／7、Computer 0／19，G2 files 2／22／native 最多 3／22 硬拒400；R0 91 DTO／16 families／12 operations、原 39 probes 不变，无持久 MCP 闭环／生产替换。仅准备根三文档与镜像，sync／default／diff check 后不 stage／commit，等待后续 File 成对完成最后统一保存；不重跑产品 tests，不读真实 credentials，无外部模型／DB／E2B／生产／push。

## 21. 二十三条阶段历史：Go skill-file 路由已提交

Go `32f2d5fb45770f0bfc86a9bf38e237ccf7d3eeec`，`feat(webcodex): route native skill file reads`，10文件、+393／-11。功能23＝Server10＋DSH13、fixture-only增至3（后续45487类型修正），DSH基线固定944634f0b1b311f0ecba124ceb9681bc12f15322。本片仅新增 file_skill_read_file 第七类同步 codec／Invocation 与 FileRead 路由；原27字段请求／九字段File payload保留，content opaque、options／path业务验证在Runner执行层，Jobdecoder仍ErrUnsupported。DSH镜像与执行准备中，File实际3／20，不计4／20或第24项。

新增独立schema1 skill-file-read.json为source-derived、no Rust oracle，35 cases含9 accept，另16 deferred File kinds与2 source output examples；SHA-256为`3441d675f3765b7bc88f50e3d75b6a3e1bf31c2469497bf24e2a8e4d2ba100f9`。这份表不混入原R0／Jobs91 DTO／16 families／12 operations与39 oracle probes，输出示例不代表执行能力。原20项File分母与原wire名称保留。

离线Go1.27.1普通-count1：初次protocol／runner特定File／Generation／Job分类selector PASS 0.008s／0.007s；最后fixture变更后3 top TestSkillFileRead protocol PASS 0.004s，35 cases／16 deferred由subtest覆盖。无新增并发，因此本片未跑-race；聚焦重跑不累加。gofmt／diff／cached检查及正常10文件白名单提交通过；源码与fixture链接见[第二十三阶段实施进度](./WEBCODEX_MIGRATION_PROGRESS.zh-CN.md#第二十三条阶段go-skill-file-同步-codec-与路由)。

仍约15%（10%～20%）、13大包none fully complete，File3／20、Project0／7、Computer0／19、G2files2／native最多3of22硬拒400，无完整MCP／持久结果／生产替换。未做真实credentials／DB／models／G2互操作／push；doc-typecheck修后叶检查与Job hooks最终回执见下文，aggregate未重跑。仅root／mirrors同步检查，不stage／commit、不重复产品tests，待DSHFile24再统一保存。

### 第三条 fixture-only 与最终检查补充

DSH `45487eef33e72103f97813a95e5f313840949b94`（`test: align subprocess fixtures with typed configuration`），parent944634，2files+8／-3，只改pwsh-sandbox constructor fixture type与subprocess-local native confinement的executionMode省略／PTYfields。它是第三条fixture-only，不增加23功能，DSH行为基线保留944634。constructor初次compilefail已按union实配型修复，max-len失败换行修复，最终无未解决失败。

Host `pnpm exec tsc -b tsconfig.host.json` PASS；`pnpm run doc-typecheck`完整该叶（含build:lib:host＋contracts-ready）PASS：80blocks compiled、78ignored、798type-equiv-catalog、956paired derivatives。native confinement15／15 PASS，typed lint两file0error0warn，normalhooks／diffcheck通过；历史doc-sync33／34后的单叶成功不等于aggregate重跑。pwsh spec收集需要真实PowerShell，未运行其spec，Host type build覆盖type-only改动。执行者jobs88–94已全收、index空、仅父四mirror dirty是提交收尾事实。

Job944 normalhooks准确为6 named stagedtranslationpairs、14 stagedsource lint0warn0error、whitespace/vendor全过；33files+1870／-115。DSHFile dab配套实现已开始，未有第24项SHA／验收，不计功能或File4／20。本轮只记录已收回执，不重复产品检查；rootmap两行源行为校正为单独文档更正，不加功能。

## 22. 二十四条阶段历史：Go Skill package listing codec／路由

Go `746e98015112dfdd67837d2b72fa60015a96b0e4`，`feat(webcodex): route native Skill package listings`，parent32f2d5、11files+373／-11。功能24＝Server11＋DSH13，test-only3；DSH功能基线944634、fixtureHEAD45487不变。Go同步八类、File codec5／20；DSH已提交仍六类同步，reader尚未提交，File实际3／20，不计第25项或File5／20。

产品四行变化使listing进入同步Invocation并移出deferred，Job decoder仍ErrUnsupported，registry使用FileRead gate。原RunnerRequest27／FilePayload9保留，options为opaque字符串；path必须.agents/skills与limit1–257在未来executor检查，wire不提前检查业务。non-null range、write-only hash／prefix、create_dirs=true拒绝，unknown kind独立分类。Server enqueue／poll／result fixture与file_read=false gate已验证，原generation准入不变，无包扫描实现。

独立skill-packages-list.json schema1／source-derived／noRustoracle：38cases＝10accept＋18canonicalinvalid＋1unknown＋9wirereject，另15deferred与3executor-only outputexamples，SHA `a445511e3884b382e9179bb365577f47d8e6612a7a323e0c7371d59779fb3050`。旧read35fixture摘要3441d675f…与字节未变，Go read测试仅更新listing的同步支持／JobUnsupported预期，DSHread镜像未提交不能称双端验证。原R0jobs91／16／12与39oracle保持独立。

离线Go1.27.1普通-count1指定selector，protocol PASS0.015s／runner PASS0.007s；无新增并发，不跑-race或full，gofmt／diff／cached／normalcommit通过，执行者jobs101／102全收。精确命令与11路径引用见[第二十四阶段实施进度](./WEBCODEX_MIGRATION_PROGRESS.zh-CN.md#第二十四条阶段go-skill-package-listing-路由)。不将DSHreader开发中source／built检查加入完成证据。

工程估算仍15%（10%～20%）、13大包none fully complete、Fileactual3／20、Project0／7、Computer0／19、G2原bits／native最多3／file2of22硬拒400，无完整MCP／持久闭环或生产替换。本轮只准备root三文档与五文件镜像，不stage／commit、不碰DSHindex、不改rootmap或sync脚本、不重跑产品tests，无生产／模型／DB／凭据／push；待DSH SkillFile25正式SHA／验证再统一docs保存。

## 23. 二十五条阶段历史：DSH Skill reader已提交

以下保留33c456时点证据；当时ENOTDIR差异已由后继bd5216修复，Note计数已由32eea45校正，最新见第24节。

DSH `33c456f3a9ba487265cac42c70b473c2f5fbffbd`，`feat(runner): read native Skill package files`，parent45487、30files+969／-134。功能25＝Server11＋DSH14、fixture-only3，Go746不变；File实际4／20、DSH同步codec7、Go同步8／Filecodec5。listing仍Go-only，需最小FS provider有界nofollow扫描，实际未达5／20；剩余listing＋其他15项共16项。

原27wire／9payload／11stdout保持，generic content opaque、具体options exact两位置array／object／known duplicate拒／unknown忽略／整数词法u64。私有RangeScan共用basic与Skill，旧basic六字段／legacy输出／whole maxbudget未改；Skill48KiB默认选区、min(policy,192KiB)无floor、完整stdout min(policy,512KiB)。fullrawsha／严格UTF8含范围外、BOM／NUL／CRLF／尾CR／空行／1-based2000／u64sat。FS cwd／package lstat／拒最终package软链／canonical containment／requested与canonical秘密规则，允许包内资源软链；file_bytes为prestat、stable-version retry为明确宿主加强，无快照承诺。

candidate call id观察在最终ToolRuntime与outer预算通过、owner/call未abort才emit，finally drop、失败无absence；同Host成功可写、异Host／postexec取消／outeroverflow不授。旧basic观察时机不改，不能声称所有read早emit修复。无FSseam／Agent／Session／ctx.jobs／capbits／默认composition增量；Job业务不变。已知P2 ENOTDIR被FS_LOCAL折叠NOTFOUND，普通文件/child及package父普通文件错回skill_file_not_found，原应skill_path_invalid；独立26尚未提交，33不含review修复。

验证分组不相加重复：codec520（79＋279＋162）；newrange10＋basic74；Skillpolicy42覆盖取消／写观察／metadata retry／iterator cleanup／owner drain；旧filepolicy25＋deadline5；realLoaderfiles11含同request三policy；client27含readbitfalse／baseline22／G2400。Runner tsc-b／tsdown／plainNode built三策略PASS，typedlint9changed及追加2files0error／precommit10fileslintPASS；doc-typecheck80blocks／type-equiv421／API99fresh／exportJSDoc PASS。named5pairs＋modulepair／precommit6pairs、mdlinks1539／mdwrap1546／budgets／readme318／noteformat／classification310 PASS。modulegraph基线漏Runner，补三生成docs后freshpass，包含本功能不另计；无完整doc-sync aggregate重跑。

read35fixture Go／DSH cmp＋SHA `3441d675f3765b7bc88f50e3d75b6a3e1bf31c2469497bf24e2a8e4d2ba100f9`已通过，含9accept／另16historicaldeferred与2源码例，无Rustoracle／Go执行证据；oldjobs91／16／12与39oracle不变。源引用与最终分组回执见[第二十五阶段实施进度](./WEBCODEX_MIGRATION_PROGRESS.zh-CN.md#第二十五条阶段dsh-原生-skill-包文件读取)。整体15%粗10–20%、13大包无一完整、Project0／7／Computer0／19／G2file2native最多3of22硬拒400、无生产替换／持久MCP闭环不变。仅root／mirrors准备与检查，不stage／commit、不重跑产品checks、不poll26、不改rootmap，待修复SHA最终保存。

## 24. 二十六条阶段历史：Skill非目录错误码修复

DSH `bd5216c4e45ff25c2a70ccd5a51881815d89c2ea`（fix(runner): distinguish non-directory Skill ancestors），parent33c456，5files+46／-4，为第26条代码／修复；后继 `32eea451266c00303c8bf23ea90319eb4e6364d9`仅三份Note文档计数，不加功能。Go746不变，总26＝Server11＋DSH15、test-only3，File实际4／20／DSH同步7／Go同步8／Go Filecodec5不因错误码修复增加。

当时ENOTDIR P2已解决：私有checkAncestors使用已有provider lstat检查包／资源祖先，symlink resolve＋stat确认目录，非目录skill_path_invalid，真正缺失skill_file_not_found，最终package symlink仍escape。两新增真实FS fixtures覆盖普通文件SKILL.md/child与package父.skills普通文件，并覆盖symlink-to-file/child、目录symlink资源允许／true missing／失败无观察与write拒绝。没有公共FS API／Nodefs业务IO改变，也未改变旧basic read早观察。

最新policy44＋Loader11＝55 PASS；Runner tsc-b、typedlint2、tsdown／plainNodebuilt同request三policy、named Note pairwrite/check／diff、normal fixcommit pair／2filelint／whitespace PASS。无重复520codec／84range，与25的policy42不相加，Loader也有重合。32eea45将Note表述改为remaining16 including Skill listing／包括Skill列表在内其余16项，4＋16＝20，namedpair／diff／precommit pair／whitespace PASS，无额外产品checks。精确源引用与job回执见[第二十六阶段实施进度](./WEBCODEX_MIGRATION_PROGRESS.zh-CN.md#第二十六条阶段skill非目录祖先错误码修复与最终保存)。

工程估计保持15%（10%～20%）、13大包none fully complete、Project0／7／Computer0／19、G2file2native最多3of22合规HTTP400、无完整持久MCP／生产替换。SkillList仍Go-only，FS有界nofollow支持仍为下一缺口。本次核验root／mirrors后各正常保存实际四个镜像变更（3md＋manifest），不强写INDEX、不包含NOTICE／scratch／日志／rootmap，不改原同步脚本，不重跑产品tests或34项doc-sync aggregate，无push／部署／真实凭据操作。

## 25. 当前二十七条阶段：Skill列表真实执行

DSH `a6bb656769050a320bb8046e7bc43b903b831135`，63files+1555／-64，`feat(runner): list native Skill packages`。总27＝Server11＋DSH16、fixture-only3，Go746行为基线保持；docs仅successor。File实际5／20、剩余15，两侧同步codec8／8，GoFilecodec5不变；不将provider fake四调用者适配或同commit P2另加功能。

FS scanDir完整Definition／local／fs-sandbox继承／E2B provider／Runner consumer完成directdir nofollow全扫描，不resolve子目标或读取内容。目录／symlink候选先计数、再invalidUTF8跳过与tuple去重，仅保留UTF8名字／种类最小limit，BOM保留，truncated=count>limit，原exactpath／limit1–257／对象或exact1array、EOF无partial success。原listing无max_bytes／maxOutput／512KB限制，额外Host maxResultBytes约束完整FSmetadata及outerresult；任何outcome无FsObserved／不授权写，owned清理，真实E2B未验收。

listing间接ENOTDIR吞空已在同commit修复，local FS_NOT_FOUND保留cause，真正missinglink仍empty，空cwd拒unavailable；旧Skill read空cwd／间接ENOTDIR差异仍待验证，bd5216历史保持。listing38fixture同Go、SHA a445511e3884b382e9179bb365577f47d8e6612a7a323e0c7371d59779fb3050，10accept／18invalid／1unknown／9wireReject及另15deferred／3源码输出例，无Rustoracle。旧read35／jobs字节不变，历史read listing deferred现consumer接受但不改旧snapshot，原wire27／File9／Job12状态保持。

验证范围不累加：codec604（85＋79＋162＋278）；最末listpolicy25＋Loader14＝39，先前client28独立。localscan13、resolve／oldlistDir选中26与123未运行分清；E2Bfilesystem75／remotehelper44分别PASS，仅owned local Linux fake carrier。Hosttypebuild143／fs-local和Runner leaves153／E2Btsc PASS；typedlint新15仅一个test5处void报错，改undefined后1file162PASS，其余14首轮无错，Runner／E2B及正常commit11pairs／26TS lint／whitespace／vendor PASS。

有效built为159 programmatic tsdown精确五targets、真实host typertPlugin、.js匹配exports／Runner三entry；154／157 CLI未匹配、158.mjs无效built已分析纠正，不计有效证据。161普通Node built-files-smoke同request[2]/max_bytes0三policy PASS，list→blindwrite拒→Skillread→write，source另maxOutput1。不称全Hostbundle／平台矩阵。424type-equiv与paired derivatives／99catalogfresh／exportJSDoc／doc-typecheck80blocks（USE_BUILD_OUTPUT=1，无Emit）、pairs／mdlinks1543／wrap1550／Notes312／README-subsystem gates／budgets8(163)PASS，非34gate aggregate。完整配置、来源与准确回执见[第二十七阶段实施进度](./WEBCODEX_MIGRATION_PROGRESS.zh-CN.md#第二十七条阶段原生skill-package-listing与有界目录扫描)。

工程估计15%（10%～20%）不变，13大包none complete、Project0／7／Computer0／19、native3/file2of22与完整G2HTTP400不变。后续剩余15File和旧read差异，仍无完整持久MCP闭环／生产替换。本轮仅root三页及镜像sync／default／diff／cached／正常docs提交，不修改业务／Notes／生成API或rootmap，不重跑产品checks，不push／服务／秘密／模型／E2B。