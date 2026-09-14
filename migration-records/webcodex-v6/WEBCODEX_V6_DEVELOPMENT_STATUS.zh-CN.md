# WebCodex V6 开发状态总表

核对日期：2026-09-14。本文记录该日期的开发快照；[实施进度](./WEBCODEX_MIGRATION_PROGRESS.zh-CN.md)保留验证回执，[项目评估](./WEBCODEX_V6_PROGRESS_ASSESSMENT.zh-CN.md)保留估算口径和历史阶段。V6 范围依据 开发设计（工作区引用：`SUB2API_DSH_DEVELOPMENT_DESIGN.zh-CN.md`）、Server 映射（工作区引用：`WEBCODEX_SERVER_MIGRATION_MAP_V6.zh-CN.md`）及 Runner 映射（工作区引用：`WEBCODEX_RUNNER_MIGRATION_MAP_V6.zh-CN.md`）。本文中的百分比不是测试覆盖率，也不是逐功能等权计算。

## 1. 当前结论与代码版本

整体按约 15%、10%～20% 的粗略区间管理；没有重新计算工作量权重。累计 13 条功能提交（Server 5、DSH 8），但 13 个 V6 大工作包尚无一个完成全部退出条件。已有真实本地文件和 Linux 进程子集，尚无完整 ChatGPT 原生 MCP → sub2api 业务授权／事务 → DSH → 持久结果回传闭环，不能替换生产 WebCodex。

| 对象 | 开发分支 | 本次核对的代码版本 |
|---|---|---|
| sub2api Server | `mpc-server` | `9e4683d19c6d716592bdfb680af2a16cb4f263d0` |
| DSH Runner | `mcp-runner` | `4c3570bd5b103e956c88f6a38bf9140bec1a4e85` |
| 冻结 WebCodex 参考 | `source-sync/webcodex` | `97ad66949a859174911c2f6da2ff1063be98bfa9` |

后续保存进度文档产生的 docs 提交不增加功能提交数，也不提高工程完成度。以上代码版本用于标定本次记录的行为，文档提交会位于其后。只在 integration 开发副本中实施功能，未推送、部署、修改生产或使用真实模型／凭据测试。

## 2. 可核实的完成维度

| 维度 | 当前进度 | 限制 |
|---|---|---|
| 架构与全范围映射 | 已形成 V6 设计及逐族映射 | 完整机器 fixture 尚未迁完 |
| RunnerRequest 顶层字段 | 27／27 保留 | 嵌套业务槽位不代表已验证／可执行 |
| 首批同步请求编解码 | 6／6 | run_shell、run_process、run_script、file_read、file_write、file_list |
| 原生实际执行 | 三项文件＋Linux process 子集 | shell／script 尚无原 Runner 执行适配 |
| File 子操作 | 3／20 | read／write／list，其余 17 项待开发 |
| Project 子操作 | 0／7 | 尚未迁入完整操作 |
| Computer 子操作 | 0／19 | 尚未迁入完整操作 |
| G2 必需能力声明 | 支持 native-image 的 Linux 3／22；file-only 2／22 | 合规 Go registry 仍返回 400，不能伪报其余能力 |
| Server polling handler | 4／4 已接默认关闭 router／DI | 显式启用才挂载 register／poll／result／offline；无业务派发／持久结果 |
| managed Agent Token 验证 | 原 12 字段 SQL repository／迁移、当前宿主用户与认证装配已完成 | 真实 PostgreSQL 约束／事务未验收；公开签发／管理／导入未实现 |
| 业务持久化、导入和持久结果 | 未完成 | registry 及去重保留为进程内状态 |
| 真实 G0 与生产替换 | 未完成 | 配置发现与 mock 不替代真实原生 MCP 验收 |

## 3. 已完成的十三个功能提交

| 序号 | 仓库 | 功能 | 提交 |
|---|---|---|---|
| 1 | Server | 原 Runner 协议、六类请求编解码、字段／Unicode／64 位整数保真 | `0d62b747b` |
| 2 | Server | 认证投影、四条 polling handler、内存节点 registry／单次派发／结果／重连与替换 | `14fa79363` |
| 3 | Server | managed Token 原字节哈希、撤销／到期、kind／scope、宿主不可变身份验证 | `861bb9e78` |
| 4 | DSH | 前台直接结果与受管进程范围退出分别确认，清理未确认不虚报成功 | `1ba089ff8f` |
| 5 | DSH | 正式 HostToolOwner、私有 scope、guard、ToolRuntime 和 owner 观察／撤销 | `6f35daf695` |
| 6 | DSH | FS 原始字节流、完整文件 hash 条件、createDirs 及 provider 适配 | `9f4616fdaa` |
| 7 | DSH | Config 生成识别显式插件子入口，files／native 配置有正式归属 | `aa048eb14c` |
| 8 | DSH | opt-in HTTP polling、进程内请求／结果保留及三项原生文件执行 | `74b24600ad` |
| 9 | DSH | 字面 argv、stdin、双流上限、超时／取消／退出／清理的原生进程基础 | `1435db0d70` |
| 10 | DSH | Linux ENOEXEC 无隐式 shell、按实际支持声明能力、歧义 127 保留未知 | `cf374cda9b` |
| 11 | Server | 原 12 字段 managed 凭据 SQL repository／迁移及当前宿主 User 适配 | `b3614bba3` |
| 12 | Server | 四条原 polling 路由默认关闭配置、实际 router／DI 与退出回调 | `9e4683d19` |
| 13 | DSH | native OS API 接受／精确目标退出回执、typed 拒绝证据、终态锁存与有界私有交换 | `4c3570bd5` |

原生文件操作已经覆盖范围读取、编码／字节限制、完整 hash、观察后写入、目录创建控制、权限拒绝、符号链接分类、排序和整体结果上限。FS 提供方接口已适配并不代表远端 E2B 真实执行或并发外部写入下的所有原子保证已经验收。

## 4. 当前进程与认证限制

1. read-only／workspace-write 的 Runner 进程请求在策略解析后拒绝，不调用 sandbox.confine 或 spawn；恢复正向执行需要 strict 最终目标原语与兼容沙箱的私有 FD 控制传输。
2. native Node addon 使用 Linux glibc 2.34+ 的 posix_spawn，支持范围为 x64／arm64，实际运行验证仅覆盖 x64。macOS／Windows／E2B 与 musl 未覆盖；共享消费者省略模式时的原行为保留。显式 shebang 有效，ENOEXEC 不隐式进入 shell。
3. 可选且不 reject 的 started 回执表达 OS API 接受、已证实拒绝或未知，另报告精确目标退出；真实目标退出 127 返回 completed／127。仅 typed C 已证实拒绝且受管范围确认退出后才返回 not_started；发生效果后即使错误外形像 errno 也保留 unknown，不继续 PATH 重放。首次 target-exit 已锁存，随后文件丢失或 carrier 迟到错误不撤销目标事实。这不保证 exec 观察、目标 main／就绪或模型行为，不使用 ptrace。
4. 当前 Runner 未知结果分支不返回双流，低层 collector 仍可保留已收集内容。未知结果不能触发盲重试，清理结束不等于原执行从未开始。
5. managed Token 记录按规范字符串 sub2api_user_<正整数宿主ID> 解析，按不可变 ID 核对当前启用用户；旧数据必须显式映射并重写关联 owner／subject，不能按展示用户名自动合并。
6. 原 kind=user／空 kind 验证后仍是非 Runner transport 身份；全部已存 scope 原样拆分，额外／admin scope 不能越过精确授权。原凭据 SQL repository／嵌入迁移及默认关闭路由装配已实现，公开签发／管理／导入和真实 PostgreSQL 约束／事务验收未完成。
7. `wc_api_keys` 保留原 12 字段；物理 `user_id` 是宿主 BIGINT 外键，认证投影仍是规范 string。宿主 resolver 检查当前 User 的 ID／DeletedAt／IsActive，不从用户名或模型分组取得权限。四条路径仅在 `webcodex_runner.enabled=true` 且五项正数限制有效时挂载；CORS 204 不表示认证或启用成功。HTTP shutdown 回调异步触发幂等 Close，不等于等待回调完成，也不停止已派发远端工作。

## 5. 待开发工作逐项清单

| 序号 | 工作包 | 当前基础 | 待完成与退出条件 |
|---|---|---|---|
| 1 | R0 原契约 | 原字段与六类 codec | 全部操作家族、嵌套 DTO、Job 更新、默认值与错误语义的 Rust／Go／TS fixture 对照 |
| 2 | S1 身份／存储 | managed verifier、原 12 字段 SQL repository／迁移、当前宿主 User 适配 | 公开签发／撤销管理／显式导入、真实 PostgreSQL 约束与迁移验收、OAuth／project credential 独立验证、原业务表和多记录事务 |
| 3 | S2 MCP／Registry | 内存 registry／四条路由默认关闭配置与正式 router／DI | 真实业务授权／派发、原 MCP／Generic ToolRuntime、inventory 与持久投影 |
| 4 | D1 Runner core | Host owner／scope／guard／文件观察 | 其余 invocation、长期执行归属、正式人审响应与持久审计、各 direct 家族零模型验证 |
| 5 | D2 受限 process | Linux unrestricted 严格启动、OS API 接受／精确目标退出回执 | 受限 strict 最终目标原语与私有 FD 控制传输、profile／环境快照及平台结果等价 |
| 6 | D2 shell／script | 已有 wire codec | 原 shell 路由／policy、解释器选择、临时脚本与清理、内部 POSIX、SSH 不回退本地 |
| 7 | D2 Job | 完成源码核查，无 Job 执行实现 | 原 job_id、agent_queued、FIFO／并发槽、stop、first-terminal、双流快照、update_seq、库存／对账与 detached durable handoff |
| 8 | D2／W1／E1 File | read／write／list | 剩余 17 项原 File 子操作及匹配、hash、配额、部分失败、恢复语义 |
| 9 | S3 Connector | 全范围映射 | task／run／审批／command／check／finish／review，审批消费与执行预留／审计同事务，不重复执行 |
| 10 | W1 Workspace／Artifacts | 底层 FS／进程基础 | 七项 Project 操作、Git／worktree／checkpoint、完整产物上传下载／发布／恢复、项目禁用清理 |
| 11 | V1 Validation | 可复用 subprocess | 原固定 argv／adapter／结构化报告，Cargo 测试计数与 Go JSON／tool／packages，零测试与基础设施失败不混淆 |
| 12 | V1 LSP | 可复用宿主服务 | 原导航／诊断／符号／hover／引用／call hierarchy、坐标／版本／路径过滤与重启 |
| 13 | A1 Workflow／Agent | 原业务映射 | ledger／communication／wake／AgentTask／A4a／CodingAgent／ACP 的身份、claim、prompt、取消与恢复 |
| 14 | E1 扩展 | 宿主局部基础可复用 | memory／SkillStore／MCP Gateway／native Plugin／SSH／RunnerConfig／cleanup／Computer 19 项，逐族适配与权限／实例验证 |
| 15 | T1 传输 | 同步 HTTP polling | WebSocket／QUIC、异步 Job／persistent-shell 结果、流控、库存、断线／实例替换／持久去重与未知结果核查 |
| 16 | D2／T1 Persistent Shell | 无原协议适配 | open／exec／status／close、复合身份、busy 串行、独立双流、profile／cwd、回收、项目禁用与 SSH 断链 |
| 17 | I1 Web 模式 | 未实施 | compatibility／web_mcp，后者关闭 Prompt Tool 注入／解析，原窗口／项目／任务关联，两入口无双执行／静默降级 |
| 18 | I1 配置与 UI | 未实施 | Connector 配置／验证／撤销、sub2api Vue 与 DSH 的节点／项目／任务／审批／结果入口 |
| 19 | 跨产品验收 | 有限组件与 Go↔TS 检查 | 正式装配下认证→业务权限→文件／进程／Job→持久结果→取消／撤销／恢复，达到全部 22 项 G2 准入 |
| 20 | G0 | 无真实新产品证据 | ChatGPT 原生 Connector 读取随机文件、多用户／项目／节点／窗口隔离、续接、人审／撤销、取消和结果回传 |
| 21 | 平台矩阵 | 真实本地 Linux 子集 | 可用真实沙箱、user-systemd／cgroup、macOS、Windows／PowerShell／OEM、真实 E2B／SSH；缺条件如实记录 |
| 22 | M1 上线迁移 | 未实施 | 原数据／凭据导入演练、引用／状态核对、排空／未知核查、凭据切换、升级／回滚，停旧进程后两产品独立验收 |

以上 22 行是对 13 个 V6 工作包的进一步拆分，不是新加的 22 个功能包，也不应与 G2 的 22 个能力位混为一谈。可选扩展保留在全量目标中，分期不等于永久删除。

### 5.1 剩余 17 项 File 操作

1. file_project_overview
2. file_delete_project_files
3. file_write_project_file
4. file_apply_text_edits
5. file_apply_patch
6. file_save_project_artifact
7. file_read_project_artifact_metadata
8. file_read_project_artifact
9. file_read_project_artifact_export_chunk
10. file_artifact_upload_begin
11. file_artifact_upload_chunk
12. file_artifact_upload_finish
13. file_artifact_upload_abort
14. file_checkpoint_create
15. file_checkpoint_restore
16. file_skill_list_packages
17. file_skill_read_file

## 6. 优先顺序

| 顺序 | Runner 主线 | Server 主线 |
|---|---|---|
| 1 | 受限 strict 最终目标原语与私有 FD 控制传输 | 凭据签发／管理／显式导入及真实 PostgreSQL 迁移验收 |
| 2 | 原 process Job／stop／update／库存闭环 | 原业务授权／派发与持久 registry／结果 |
| 3 | shell／script、编辑／删除／产物等 G2 基础 | 原项目／任务／执行／审批事务 |
| 4 | Validation／LSP／项目生命周期、完整 G2 | MCP／Generic ToolRuntime／ProjectConnector |
| 5 | 真实联调、恢复、平台矩阵 | Web 模式／Connector／UI／G0 |
| 6 | 原可选扩展与全量验收 | 导入、切流、回滚、退出旧系统 |

## 7. 已有验证与本次文档保存边界

- 认证新增 17 个顶层行为测试，Go Runner／protocol 两包完整本地 race 通过；最终认证增例后聚焦 race 通过。
- `cf374cda9b` 阶段五组相关回归 174 通过、2 跳过；跳过项是因本机沙箱后端不可用而未执行的普通共享消费者正向验证，严格 Runner 拒绝回归实际通过。
- 两条本地启动路径使用真实 bootstrap；scope fixture 的 systemd manager 被替换，不能声称真实 cgroup containment 已验收。
- 根 Host 类型检查、最终 type-aware lint、相关包构建、普通 Node 的真实 Loader／HTTP 文件／进程／ENOEXEC／127 冒烟通过。
- 两项最新 Go↔DSH race 通过，覆盖原六类及 64 位整数往返和部分 G2 注册仍被拒绝，不代表完整执行链路成功。
- `cf374cda9b` 阶段七组双语文档配对及相关文档检查通过；正常功能提交 hooks 通过，当时较窄 staged lint 有 4 项既有 unused suppression 警告、0 错误。
- Server 原凭据／迁移结构／真实 User repository＋Ent SQLmock race 通过（任务 408）；默认关闭配置／真实 CORS／mounted 认证及既有 HTTP ingress 聚焦 race 通过（任务 423）。实际 cmd/server 编译通过（任务 421，no-tests）；这些不替代真实 PostgreSQL 约束／事务、业务派发或生产服务验收。
- DSH 回执功能已提交 `4c3570bd5b103e956c88f6a38bf9140bec1a4e85`，59 文件，任务 450 成功。
- 完整 local provider 测试集与 Runner 测试合计 340 通过、11 项平台／confinement 跳过（最终任务 448）；其中 Runner process 37 通过、native composition 14 通过／2 跳过，均已包含在合计中。重建后的实际 Node source／built 入口 4／4；C tag 修正后的 native C＋host packed entry 32／32（任务 443）另行通过。
- TypeScript 与完整 type-aware lint 0 错误，双语配对 772、JSDoc 和 diff 检查通过。提交 staged lint 另有 3 项既有 disable comment 警告（index.ts:248、spawn-runner.ts:290、spawn.ts:602），对应注释不在本功能 diff 中；不能将提交描述为零警告。第三方 generator 已运行且无净 diff，提交后代码工作树干净。
- 较早的 type-equiv 418 个块＋418 个派生块、Note 格式 307、Markdown 链接 1533 均通过；进度文档保存另行核验生成器内容、manifest、相对路径及显式 HTML／标题锚点，不将此前的 11 份 Markdown／160 个链接回执冒充新快照回执。
- 私有 status 限 256 B、startup error 限 16 KiB；读取 nofollow／nonblock，允许已打开后 unlink 的 inode 链接数为 0，拒绝硬链接数大于 1。最终 x64 构建的 readelf 检查只列出 GLIBC_2.2.5／2.4／2.15，无 GLIBC_2.34 加载依赖；这不等于实跑旧 glibc。完整 musl／macOS／Windows／E2B、完整平台 tarball、真实 systemd 与 confinement 验收仍缺失。
- 这些检查范围有重叠，不累加成全项目覆盖率；保存文档不增加功能数，也不重复已通过的功能测试。

## 8. 本地与 Git 保存方式

本地维护入口是工作区根的本文、实施进度和项目评估。两个 integration 仓库分别在 `migration-records/webcodex-v6/` 保存同一批进度快照、索引及 manifest；通过独立 docs 提交纳入各自开发分支，不推送。manifest 记录来源文件 SHA-256、落盘版本 SHA-256 和本次代码基线。

快照仅重定位 Markdown 引用：同批文档互相链接；本仓库源码使用仓库内相对链接；另一仓库、冻结源码和未随快照导入的设计文档保留明确的工作区路径文字。这些工作区引用不是独立克隆内已经包含的文件。更新根文档后需显式重新同步和核验，manifest 与 Git diff 提供对照，不声明后台自动同步。

本次十三条功能的快照按上述机制在两侧保存，具体 docs 提交由各分支 Git 历史和 manifest 对照；不在来源正文嵌入自身文档提交的 SHA。此前 `4ae8b0231`／`1bc5f42614` 仅记录第十二条阶段，属于历史快照。
