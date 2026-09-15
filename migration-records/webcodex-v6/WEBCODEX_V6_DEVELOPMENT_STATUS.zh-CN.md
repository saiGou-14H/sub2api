# WebCodex V6 开发状态总表

核对日期：2026-09-14。本文记录该日期的开发快照；[实施进度](./WEBCODEX_MIGRATION_PROGRESS.zh-CN.md)保留验证回执，[项目评估](./WEBCODEX_V6_PROGRESS_ASSESSMENT.zh-CN.md)保留估算口径和历史阶段。V6 范围依据 开发设计（工作区引用：`SUB2API_DSH_DEVELOPMENT_DESIGN.zh-CN.md`）、Server 映射（工作区引用：`WEBCODEX_SERVER_MIGRATION_MAP_V6.zh-CN.md`）及 Runner 映射（工作区引用：`WEBCODEX_RUNNER_MIGRATION_MAP_V6.zh-CN.md`）。本文中的百分比不是测试覆盖率，也不是逐功能等权计算。

## 1. 当前结论与代码版本

整体按约 15%、10%～20% 的粗略区间管理；没有重新计算工作量权重。累计 19 条代码／功能修复提交（Server 8、DSH 11），另有 2 条仅补 fixture 的 test 验证提交，不增加功能数；当前共享 fixture 为 91 DTO／16 families／12 operations（见第 11 节）。但 13 个 V6 大工作包尚无一个完成全部退出条件。已有真实本地文件和 Linux 进程子集，尚无完整 ChatGPT 原生 MCP → sub2api 业务授权／事务 → DSH → 持久结果回传闭环，不能替换生产 WebCodex。

| 对象 | 开发分支 | 本次核对的代码版本 |
|---|---|---|
| sub2api Server | `mpc-server` | `efa85831ef08995c54ab63dd33bc57afd7106acb` |
| DSH Runner | `mcp-runner` | `1be7718659c239eaa9e441ba3ca064ceb6277657` |
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
| Server transport handler | 5／5 已接默认关闭 router／DI | register／poll／result／offline／job_update；另有四条凭据管理路径，共九条；无用户 dispatch HTTP／MCP 或持久结果 |
| Server legacy process Job | 独立 records、Start／Get／List／Log／Stop 和原 job_update 已实现 | Host project／scope 授权前置；无 Runner JobManager、持久化／跨进程恢复；其他 Job 族及 inventory／reconciliation／log_snapshot 拒绝 |
| managed Agent Token 验证及公开管理 | 原 12 字段 SQL repository／迁移、当前宿主用户认证及四条公开管理 POST 已实现 | 显式旧凭据导入、跨 account family 独立 hash 验证及真实 PostgreSQL 约束／事务未验收 |
| 业务持久化、导入和持久结果 | 未完成 | registry 及去重保留为进程内状态 |
| 真实 G0 与生产替换 | 未完成 | 配置发现与 mock 不替代真实原生 MCP 验收 |

## 3. 已完成的十九个代码／功能修复提交

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
| 14 | Server | 通过当前宿主账户公开创建／注册 hash／列出／撤销 Runner 凭据 | `a616f55e88bc37608ddcf883b980942be168190b` |
| 15 | DSH | 原 Job DTO／context／生命周期与独立 start_process_job／stop_job codec | `af6b3d1fafa11b78f26f7c75af404c09e90b7a1c` |
| 16 | DSH | PGID native-image 使用 fd3 私有 Unix socket 传递请求与启动／退出回执 | `894376ebbe9ae491a5c3161af1dc1905ab97b793` |
| 17 | DSH | 接受原 Job serde positional arrays／unit maps，补共享黄金 fixture | `a5ed7187a781310ae33314488b68bcb6e32d73b3` |
| 18 | Server | 原 Job 协议数据、生命周期与输入形式保真 | `cc736b7c16d5a5325c50aed90382e1b1aa210f68` |
| 19 | Server | legacy 结构化 process Job registry、Start／Get／List／Log／Stop 与 job_update | `efa85831ef08995c54ab63dd33bc57afd7106acb` |

原生文件操作已经覆盖范围读取、编码／字节限制、完整 hash、观察后写入、目录创建控制、权限拒绝、符号链接分类、排序和整体结果上限。FS 提供方接口已适配并不代表远端 E2B 真实执行或并发外部写入下的所有原子保证已经验收。

## 4. 当前进程与认证限制

1. read-only／workspace-write 的 Runner 进程请求在策略解析后拒绝，不调用 sandbox.confine 或 spawn；PGID 私有 FD 传输已提交，恢复受限正向执行仍需正式 bwrap confinement Service／provider／Runner consumer 和最终目标验收。
2. native Node addon 使用 Linux glibc 2.34+ 的 posix_spawn，支持范围为 x64／arm64，实际运行验证仅覆盖 x64。macOS／Windows／E2B 与 musl 未覆盖；共享消费者省略模式时的原行为保留。显式 shebang 有效，ENOEXEC 不隐式进入 shell。
3. 可选且不 reject 的 started 回执表达 OS API 接受、已证实拒绝或未知，另报告精确目标退出；真实目标退出 127 返回 completed／127。仅 typed C 已证实拒绝或 PGID pre-supervisor chdir 已知拒绝，且受管范围确认退出后才返回 not_started；发生效果后即使错误外形像 errno 也保留 unknown，不继续 PATH 重放。首次 target-exit 已锁存，随后文件丢失或 carrier 迟到错误不撤销目标事实。这不保证 exec 观察、目标 main／就绪或模型行为，不使用 ptrace。
4. 当前 Runner 未知结果分支不返回双流，低层 collector 仍可保留已收集内容。未知结果不能触发盲重试，清理结束不等于原执行从未开始。
5. managed Token 记录按规范字符串 sub2api_user_<正整数宿主ID> 解析，按不可变 ID 核对当前启用用户；旧数据必须显式映射并重写关联 owner／subject，不能按展示用户名自动合并。
6. 原 kind=user／空 kind 验证后仍是非 Runner transport 身份；全部已存 scope 原样拆分，额外／admin scope 不能越过精确授权。原凭据 SQL repository／嵌入迁移、默认关闭路由及公开签发／注册 hash／列出／撤销管理已实现；显式旧凭据全量导入、跨 account family 独立 hash 验证及真实 PostgreSQL 约束／事务验收未完成。
7. `wc_api_keys` 保留原 12 字段；物理 `user_id` 是宿主 BIGINT 外键，认证投影仍是规范 string。宿主 resolver 检查当前 User 的 ID／DeletedAt／IsActive，不从用户名或模型分组取得权限。五条 transport 路径仅在 `webcodex_runner.enabled=true` 且六项正数限制有效时挂载；新增独立 `max_jobs_per_runner` 必须显式为正，不回退 pending 限制；disabled 不创建依赖；CORS 204 不表示认证或启用成功。HTTP shutdown 回调异步触发幂等 Close，不等于等待回调完成，也不停止已派发远端工作。

## 5. 待开发工作逐项清单

| 序号 | 工作包 | 当前基础 | 待完成与退出条件 |
|---|---|---|---|
| 1 | R0 原契约 | 原字段与六类 codec | 全部操作家族、嵌套 DTO、Job 更新、默认值与错误语义的 Rust／Go／TS fixture 对照 |
| 2 | S1 身份／存储 | managed verifier、原 12 字段 SQL repository／迁移、当前宿主 User 适配及公开凭据管理 | 显式旧凭据全量导入、真实 PostgreSQL 约束与迁移验收、OAuth／project／其他 account family 独立验证、原业务表和多记录事务 |
| 3 | S2 MCP／Registry | 内存 registry／五条 transport 路由默认关闭配置与正式 router／DI、legacy process Job 管理 | 真实业务授权／派发、原 MCP／Generic ToolRuntime、inventory 与持久投影 |
| 4 | D1 Runner core | Host owner／scope／guard／文件观察 | 其余 invocation、长期执行归属、正式人审响应与持久审计、各 direct 家族零模型验证 |
| 5 | D2 受限 process | Linux unrestricted 严格启动、OS API 接受／精确目标退出回执及 PGID 私有 fd3；真实 bwrap 组合探针可行 | 正式 bwrap 最终目标 confinement Service／provider／consumer（进行中）、linux-scope FD、profile／环境快照及平台结果等价 |
| 6 | D2 shell／script | 已有 wire codec | 原 shell 路由／policy、解释器选择、临时脚本与清理、内部 POSIX、SSH 不回退本地 |
| 7 | D2 Job | 两端原 Job DTO／codec／serde 修复及 Server legacy process Job records／Start／Get／List／Log／Stop／job_update 已提交 | Runner JobManager／FIFO／update 发送尚未开始；跨端执行／停止闭环、库存／对账、持久化与 detached durable handoff 待实现 |
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
| 1 | 正式 bwrap native confinement Service／provider／Runner consumer（进行中，显式 PGID，不自动降级 owner） | 显式旧凭据导入、独立凭据族验证及真实 PostgreSQL 迁移验收 |
| 2 | 原 process Job／stop／update／库存闭环 | 原业务授权／派发与持久 registry／结果 |
| 3 | shell／script、编辑／删除／产物等 G2 基础 | 原项目／任务／执行／审批事务 |
| 4 | Validation／LSP／项目生命周期、完整 G2 | MCP／Generic ToolRuntime／ProjectConnector |
| 5 | 真实联调、恢复、平台矩阵 | Web 模式／Connector／UI／G0 |
| 6 | 原可选扩展与全量验收 | 导入、切流、回滚、退出旧系统 |

## 7. 已有验证（十三条及更早阶段历史回执）

本节保持当时测试和工作树状态；当前第十九条阶段见第 12 节。历史“提交后干净”不表示目前两仓库没有其他 agent 未提交工作，历史沙箱不可用也不否定本轮 bwrap 实测。

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

本次十九条代码／功能修复＋两条 fixture 验证补充的根文档按原脚本 `--write` 同步每仓库五个白名单快照文件，并以默认模式核验；未 stage／commit，须等 parent 明确协调 docs 提交。文档保存不增加代码数量，不声明整个工作树干净；历史第 9–11 节保留当时回执，当前 Server Job 阶段见第 12 节。

## 9. 本轮三条提交与真实探针（十六条阶段历史快照）

- Server `a616f55e88bc37608ddcf883b980942be168190b`：14 文件，四条 `POST /api/agent-tokens/{create,register_hash,list,revoke}` 公开管理已实现。实际 JWT Bearer、当前宿主 role、BackendModeUserGuard、同一 GlobalPanelRateLimit、四路径 audit 整体正文省略及 no-store；正文上限使用 configuredMaxBodyBytes，不再固定 64 KiB。SQL 只存 hash，`wc_agent_`＋64 hex 明文只返回一次；保留原 defaults／scopes／status ordering、list／revoke owner／kind 与 canonical `sub2api_user_ID`／BIGINT FK。任务 477 的离线 Go unit／race 聚焦 server／middleware／repository／protocol／cmd/server 检查通过；真实宿主装配配 fake SQL，不是真实 PG。显式旧凭据全量导入、跨 account family 独立 hash 检查和真实 PG 迁移仍缺。
- DSH `af6b3d1fafa11b78f26f7c75af404c09e90b7a1c`：原 Job DTO／context 11 字段和 12 个精确生命周期、原状态及独立 start_process_job／stop_job 编解码；六类同步 consumer 仍拒 Job，没有执行或传输能力。local fixture 独立 clone 无 sibling 依赖，64 位整数保真。初次 106 项（Job 80＋client 26）、tsc／lint／gates 通过；后续 Rust oracle 修复尚无最终 SHA，不把旧回执当成最终 parity。
- DSH `894376ebbe9ae491a5c3161af1dc1905ab97b793`：18 文件，PGID native-image 使用 fd3 私有 Unix socket，一份 ≤8 MiB request＋EOF、最多两条各 ≤16 KiB response。最终 argv／cwd／env 不经过文件或命令行，helper 关闭目标 FD ≥3，`/proc/fd` reopen 为 ENXIO，stdout 不能伪造控制消息；linux-scope 仍用文件，当前 Runner RO／WW 仍拒绝。仅 pre-supervisor chdir 可认定 known-not-started，accept 后通用错误保留 unknown，不重放 PATH。最终六文件 132 通过／2 跳过，source／plain Node built 6／6；typed lint／JSDoc 通过，正常 commit hooks 0 错误／2 项 unused-disable 警告。C 未变化、未重跑 C matrix；上节 340／11 是历史回执，不混加。

真实 Job serde oracle（工作区引用：`.scratch/job-serde-oracle-20260914/REPORT.md`）已实际执行 39 probe，serde 1.0.228／serde_json 1.0.150，Cargo exit 0。Job struct positional arrays、单 key null unit maps 可接受；非 default Option 缺少数组 slot 拒绝，middle default 不移动后续字段。TS／Go 修复与共享 golden fixtures 扩展进行中，待最终 R0fix／GoR0 SHA 和测试回执；外层 generic RunnerRequest arrays 及非 Job 结构旧 gap 保留，不称全 serde parity。

bwrap 实测报告（工作区引用：`scratch/bwrap-probe-20260827/REPORT.md`）与生命周期补充（工作区引用：`scratch/bwrap-probe-20260827/LIFECYCLE.md`）记录自有官方 0.11.0 非 setuid 构建，`build/bwrap` SHA-256 为 `28ba628600c9de65808348daa60e7fc1b6f745a01cd483d5a698c6afb5e8bc60`，仅保留作开发测试、未全局安装。实际原 RO／WW profile＋fd3 source／built 四格成功，72 项证据断言核验文件效果：RO 拒绝 write／create／direct truncate，WW 仅 workspace 允许；没有 `--preserve-fds` 选项。namespace 可用，不能把缺 binary 说成 kernel 硬阻塞；Landlock ABI1 对 direct truncate 不足。生命周期另有 78 项证据断言：取消 outer bwrap 丢目标退出回执，保留 started＋unknown；无 live survivors，但 namespace init zombies 是 fixture subreaper 回收，不能称 provider reap all。early profile failure 为 ECONNRESET→unknown，不是 clean EOF。仅证明探针可行，provider auto／linux-scope FD 未验收。

正式 bwrap native confinement Service／provider／当前 Runner consumer 正在实施，要求显式 PGID 配置、不自动降 owner；Go legacy 结构化 process Job registry／HTTP job_update／default-off 与独立 `max_jobs_per_runner` 配置正在实施，reconciliation／inventory／log_snapshot 全部拒绝。两项均尚无 SHA／验收，不计完成；Runner JobManager／FIFO／update 发送尚未开始。后续 parent 提供两条 R0 最终 SHA 后才可精确调整到 18 条保存点。native 3／22、file-only 2／22、合规 G2 注册 HTTP 400／zero exec、File 3／20、Project 0／7、Computer 0／19 及约 15% 总体估算均不变。

## 10. 当前十八条阶段：两端 Job R0 已提交

第 17 条 DSH `a5ed7187a781310ae33314488b68bcb6e32d73b3`，`fix(runner): accept original Job serde input forms`，13 文件，父提交 `894376ebbe9ae491a5c3161af1dc1905ab97b793`；第 18 条 Server `cc736b7c16d5a5325c50aed90382e1b1aa210f68`，`feat(webcodex): preserve original Job protocol data`，15 文件，父提交 `a616f55e88bc37608ddcf883b980942be168190b`。累计 18 条（Server 7、DSH 11），两条完整 SHA 是 R0 功能阶段基线，当前包含验证补充的基线见顶部表和第 11 节；第 9 节“等待 R0 SHA”为历史快照。

共享 golden fixture 为 90 DTO／16 family／12 operations，SHA-256 `0bec4ca7c4e443160498835ca070019a4fcd98110e0478233b75edb7fcb01718`；其中 12 条直接 oracle 对照为 rows 1–7、35–39，fixture cmp 与 exact-oracle 检查通过。已接受原 Job struct positional arrays 和单 key null unit maps，保留缺 non-default Option slot 拒绝和 middle default 不移位；外层 RunnerRequest arrays／general 非 Job 替代形式仍是已知 gap，不能称 all-serde parity。

parent 收取的最终回执：任务 514 DSH 五 specs 487／487（Job 161＋protocol 280＋client 26＋client safety 7＋config 13），两文件 lint／noEmit 均 exit 0；任务 515 Go protocol 30 top＋175 subtests、runner 59 top＋118 subtests 全通过。两条 opt-in interop 初因未设 checkout 跳过；任务 517 显式设置 DSH_RUNNER_CHECKOUT 后仅补这两条，均通过（0.19s／0.09s，package 0.281s），不与初次 skip 混算。三组文档配对、mdlinks 1533／wrap 1540／Notes 307 及正常 hooks 通过。这些是功能阶段回执，本次文档保存不重跑产品测试。

同步 consumer 仍不接 Job，G2 native 仍 3／22、file-only 2／22，合规注册 HTTP 400／zero exec。Go process Job registry／HTTP job_update 及 DSH 正式 bwrap Service／provider／consumer 继续 in progress，无最终 SHA／验收，不计新增功能；Runner JobManager／FIFO／update 发送尚未开始。18 条快照按授权同步和默认核验，暂不 stage／commit，等待 parent 协调两条 docs 提交；不宣称两仓库工作树干净。

## 11. 当前验证补充：18 个功能／修复＋2 条 test 提交

DSH `1be7718659c239eaa9e441ba3ca064ceb6277657`（`test(runner): cover positional Job decimal lexeme`）与 Server `a8b34712b56172884a1256f80b1afb37665da4cb`（`test(webcodex): cover positional Job decimal lexeme`）各仅修改一个 fixture 文件的一行，新增 `1.0` 数组 integer 负向 golden；该行为原已有 TS 单测覆盖，没有实施范围扩展。因此仍为 18 个功能／修复（Server 7、DSH 11），这两条验证附注不增加功能序号；顶部当前基线包含这两条 test 提交。

当前共享 fixture 为 91 DTO／16 families／12 operations，双端 SHA-256 `a5a6a41542f2e382a32ba25fd287f83a33dd310bb29fadacbec1099d776497dc`。最新 TS Job 162／162、Go 共享 fixture（0.005s）、cmp／diff 检查通过。第 10 节 90 DTO、旧 digest 和五组 487／487 是新增此 fixture 前的准确历史回执；没有 488 项全组重跑，不将最新聚焦测试与旧全组计数混合。原 direct oracle 12 条对照和外层 RunnerRequest 数组／general 非 Job 替代形式 gap 不变。

本次根三文档与两侧五文件快照保存为“18 功能／修复＋2 验证”阶段；同步和默认检查后仍不 stage／commit，等待 parent 审查 diff。并行 Go Job registry／HTTP job_update、正式 bwrap Service／provider／consumer 仍在研不计数，JobManager／FIFO／update 发送未开始；G2 3／22 与完整验收限制不变。

## 12. 当前十九条阶段：Server legacy 结构化 process Job

第 19 个功能／修复为 Server `efa85831ef08995c54ab63dd33bc57afd7106acb`，`feat(webcodex): manage legacy structured process Jobs`，准确 23 文件，父提交 `a8b34712b56172884a1256f80b1afb37665da4cb`。当前计数 Server 8＋DSH 11＝19，另两条 test 提交不计功能；Go 基线为本提交，DSH 仍为 `1be7718659c239eaa9e441ba3ca064ceb6277657`。第 9–11 节 Go Job 在研的描述属于历史。

Server 已有独立 records、Start／Get／List／Log／Stop，以及原 job_update 第五条 transport 路径（另四条凭据 management，共九路径）。真实 managedVerifier 校验 scopes／owner／group／active instance／request；poll 释放 pending 槽并保留 dispatch binding。Stop 防重复，队列满不改状态；终态 first latch；原 legacy optional sequence 仅记录，保留 finished fallback。双流各按 256 KiB 有界日志、绝对 cursor 与 tail reset 处理，List 限制 20～100，900s TTL 按 Server observed 时间计算。独立 `max_jobs_per_runner` 必须显式为正，不从 pending 值回退；enabled 现在要求六项正数限制，disabled 不创建依赖。

Host project／scope 授权前置，未增加用户 dispatch HTTP 或 MCP；无 Runner JobManager、持久化或跨进程恢复，Close 不保证远端停止。inventory／reconciliation／log_snapshot／script／validation／detached／SSH 继续拒绝，原 G2 strict 与 DSH native 3／22 注册拒绝不变。正式 bwrap 仍 in progress，仅有实际 source／built 部分验收，未提交、不计功能。

最终实际回执：任务 530 离线 Go `-tags unit -race`，config／runner／server 选择 `^Test(WebCodexRunner|Job)` 全通过，分别 1.054s／1.479s／1.237s；旧同步 queue／HTTP 选择回归任务 516 为 1.299s 通过。管理路由初选 `^TestAgentTokenManagement` 命中零项，不算覆盖；改实际名称 `^TestWebCodexAgentToken` 后 server race 通过（1.131s）。parent 提交执行者核对 23 个白名单路径、diff／index 空且无后续源码修改；这是该提交收尾回执，不声明包含镜像／scratch／旧日志的整个工作树干净。

当前共享 fixture 保持 91 DTO／16 families／12 operations，SHA-256 `a5a6a41542f2e382a32ba25fd287f83a33dd310bb29fadacbec1099d776497dc`，TS Job 162／162 与 Go fixture 0.005s 为最新 fixture 回执；487／487 是新增 fixture 前历史，没有 488 全组重跑。约 15%（10%～20%）、13 工作包无一全量完成、其余 File／Project／Computer 数量不变。三根文档和两侧镜像同步十九条阶段并默认／diff 核验，仍不 stage／commit，不重跑产品测试。
