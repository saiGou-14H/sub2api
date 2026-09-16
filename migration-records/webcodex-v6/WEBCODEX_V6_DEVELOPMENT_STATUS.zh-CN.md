# WebCodex V6 开发状态总表

核对日期：2026-09-14。本文记录该日期的开发快照；[实施进度](./WEBCODEX_MIGRATION_PROGRESS.zh-CN.md)保留验证回执，[项目评估](./WEBCODEX_V6_PROGRESS_ASSESSMENT.zh-CN.md)保留估算口径和历史阶段。V6 范围依据 开发设计（工作区引用：`SUB2API_DSH_DEVELOPMENT_DESIGN.zh-CN.md`）、Server 映射（工作区引用：`WEBCODEX_SERVER_MIGRATION_MAP_V6.zh-CN.md`）及 Runner 映射（工作区引用：`WEBCODEX_RUNNER_MIGRATION_MAP_V6.zh-CN.md`）。本文中的百分比不是测试覆盖率，也不是逐功能等权计算。

## 1. 当前结论与代码版本

整体按约 15%、10%～20% 的粗略区间管理；没有重新计算工作量权重。累计 29 条代码／功能修复提交（Server 12、DSH 17），另有 3 条仅补 fixture 的 test 验证提交，不增加功能数；原 R0／Job 共享 fixture 为 91 DTO／16 families／12 operations（见第 11 节），新增独立 skill-file fixture 不混入此计数（见第 16 节）。但 13 个 V6 大工作包尚无一个完成全部退出条件。已有真实本地文件和 Linux 进程子集，尚无完整 ChatGPT 原生 MCP → sub2api 业务授权／事务 → DSH → 持久结果回传闭环，不能替换生产 WebCodex。

| 对象 | 开发分支 | 本次核对的代码版本 |
|---|---|---|
| sub2api Server | `mpc-server` | `ffae2865cc826f97131a45a89a63e7df2f9a3892` |
| DSH Runner | `mcp-runner` | `5f2cccb7212099a50c26d31127e4d1322639ef63` |
| 冻结 WebCodex 参考 | `source-sync/webcodex` | `97ad66949a859174911c2f6da2ff1063be98bfa9` |

后续保存进度文档产生的 docs 提交不增加功能提交数，也不提高工程完成度。DSH行为基线为上表5f2cccb；本次保存第29阶段根文档及镜像，正常提交各侧实际四个docs文件，不增加功能数。既有bd5216 Skill read祖先修复与32eea45计数文档保留在历史，后续进度docs只是documentation successor。已包含类型 fixture 提交 `45487eef33e72103f97813a95e5f313840949b94`（parent 944634，2 文件、+8／-3），作为第三条 fixture-only，不增加功能。Host type build 与修后 doc-typecheck 完整该叶已通过；历史 doc-sync 33／34 未重跑 aggregate。只在 integration 开发副本工作，未推送、部署、修改生产或使用真实模型／凭据测试。

## 2. 可核实的完成维度

| 维度 | 当前进度 | 限制 |
|---|---|---|
| 架构与全范围映射 | 已形成 V6 设计及逐族映射 | 完整机器 fixture 尚未迁完 |
| RunnerRequest 顶层字段 | 27／27 保留 | 嵌套业务槽位不代表已验证／可执行 |
| 同步请求编解码 | Go九类；DSH八类 | Go File codec6／20含overview路由，DSH File执行5／20；无新共享JSONfixture，原wire27／File9／Job12状态不变 |
| 原生实际执行 | 五项文件＋Linux process 子集 | basic3＋Skill read＋Skill list；空 cwd已验证原本拒绝；本次新增回归，未改生产cwd处理，间接ENOTDIR误码由28修复 |
| File 子操作 | 5／20 | read／write／list／skill read／skill list，剩余15项待执行 |
| Project 子操作 | 0／7 | 尚未迁入完整操作 |
| Computer 子操作 | 0／19 | 尚未迁入完整操作 |
| G2 必需能力声明 | 支持 native-image 的 Linux 3／22；file-only 2／22 | 合规 Go registry 仍返回 400，不能伪报其余能力 |
| Server transport handler | 5／5 已接默认关闭 router／DI | register／poll／result／offline／job_update；另有四条凭据管理路径，共九条；无用户 dispatch HTTP／MCP 或持久结果 |
| Server process Job | legacy records／Start／Get／List／Log／Stop／job_update 及同 Server／同实例已派发 process Job 对账恢复已提交 | Host project／scope 授权前置；reconciliation cap=false、未知／过期／重启／跨实例及未支持家族库存拒绝；Runner legacy process Job 执行与发送已提交，但不广告 reconciliation，无持久化／跨进程恢复 |
| managed Agent Token 验证及公开管理 | 原 12 字段 SQL repository／迁移、当前宿主用户认证及四条公开管理 POST 已实现 | 显式旧凭据导入、跨 account family 独立 hash 验证及真实 PostgreSQL 约束／事务未验收 |
| 业务持久化、导入和持久结果 | 未完成 | registry 及去重保留为进程内状态 |
| 真实 G0 与生产替换 | 未完成 | 配置发现与 mock 不替代真实原生 MCP 验收 |

## 3. 已完成的二十九个代码／功能修复提交

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
| 20 | DSH | 正式 prepareNativeImage／本地 private bootstrap／Runner 受限 native 执行 | `655b2a643e49020f32641839753d45edf631ff06` |
| 21 | Server | 同实例 process Job 库存／快照对账、固定 grace 与公开 recovering 投影 | `40817b71370bea996e21519a24268fae03b87338` |
| 22 | DSH | 原 process Job FIFO／真实 native 执行／停止／legacy job_update 单发送者 | `944634f0b1b311f0ecba124ceb9681bc12f15322` |
| 23 | Server | file_skill_read_file 七类同步 codec／Invocation 与 FileRead 路由 | `32f2d5fb45770f0bfc86a9bf38e237ccf7d3eeec` |
| 24 | Server | file_skill_list_packages 第八类同步 codec／FileRead 路由及独立 fixture | `746e98015112dfdd67837d2b72fa60015a96b0e4` |
| 25 | DSH | 原Skill包文件读取、严格范围扫描／完整hash、最终成功观察及共享fixture | `33c456f3a9ba487265cac42c70b473c2f5fbffbd` |
| 26 | DSH | 区分Skill非目录祖先与真正缺失，修复确定性错误码 | `bd5216c4e45ff25c2a70ccd5a51881815d89c2ea` |
| 27 | DSH | 通用有界nofollow目录扫描及原生Skill package listing | `a6bb656769050a320bb8046e7bc43b903b831135` |
| 28 | DSH | SkillRead保留间接ENOTDIR路径错误；空cwd仅补原拒绝行为回归 | `5f2cccb7212099a50c26d31127e4d1322639ef63` |
| 29 | Server | file_project_overview同步codec／FileRead路由，无原生overview执行 | `ffae2865cc826f97131a45a89a63e7df2f9a3892` |

原生文件操作已经覆盖范围读取、编码／字节限制、完整 hash、观察后写入、目录创建控制、权限拒绝、符号链接分类、排序和整体结果上限。FS 提供方接口已适配并不代表远端 E2B 真实执行或并发外部写入下的所有原子保证已经验收。

## 4. 当前进程与认证限制

1. read-only／workspace-write 已支持 Linux actual bwrap／full enforcement＋显式 `nativeImageContainment: 'process-group'` 的受限 native 执行：`SandboxService.prepareNativeImage` 返回闭包，本地 provider 仅包装私有 bootstrap，最终 argv／cwd／env 经 fd3。auto／linux-scope 仍拒绝 confined；pkg 不支持 confined，仅 source／plain Node artifact 已验证。WW 的 /tmp 遮蔽 bootstrap argv 绝对路径／file URL 及 realpath 且未被 workspace bind 恢复时提前拒绝；不扩大 mount，也不承诺找全 transitive imports，未识别依赖仍可能 unknown。没有 valid target receipt 不虚报成功。
2. native Node addon 使用 Linux glibc 2.34+ 的 posix_spawn，支持范围为 x64／arm64，实际运行验证仅覆盖 x64。macOS／Windows／E2B 与 musl 未覆盖；共享消费者省略模式时的原行为保留。显式 shebang 有效，ENOEXEC 不隐式进入 shell。
3. 可选且不 reject 的 started 回执表达 OS API 接受、已证实拒绝或未知，另报告精确目标退出；真实目标退出 127 返回 completed／127。调用 spawn 之前的策略／prepare 拒绝或阻塞 prepare 超预算可返回 not_started；进入启动后，仅 typed C 已证实拒绝或 PGID pre-supervisor chdir 已知拒绝，且受管范围确认退出后才返回 not_started；发生效果后即使错误外形像 errno 也保留 unknown，不继续 PATH 重放。首次 target-exit 已锁存，随后文件丢失或 carrier 迟到错误不撤销目标事实。这不保证 exec 观察、目标 main／就绪或模型行为，不使用 ptrace。
4. 当前 Runner 未知结果分支不返回双流，低层 collector 仍可保留已收集内容。未知结果不能触发盲重试，清理结束不等于原执行从未开始。
5. managed Token 记录按规范字符串 sub2api_user_<正整数宿主ID> 解析，按不可变 ID 核对当前启用用户；旧数据必须显式映射并重写关联 owner／subject，不能按展示用户名自动合并。
6. 原 kind=user／空 kind 验证后仍是非 Runner transport 身份；全部已存 scope 原样拆分，额外／admin scope 不能越过精确授权。原凭据 SQL repository／嵌入迁移、默认关闭路由及公开签发／注册 hash／列出／撤销管理已实现；显式旧凭据全量导入、跨 account family 独立 hash 验证及真实 PostgreSQL 约束／事务验收未完成。
7. `wc_api_keys` 保留原 12 字段；物理 `user_id` 是宿主 BIGINT 外键，认证投影仍是规范 string。宿主 resolver 检查当前 User 的 ID／DeletedAt／IsActive，不从用户名或模型分组取得权限。五条 transport 路径仅在 `webcodex_runner.enabled=true` 且六项正数限制有效时挂载；新增独立 `max_jobs_per_runner` 必须显式为正，不回退 pending 限制；reconciliation 另要求显式正数 `job_recovery_grace_seconds`，默认 0 拒绝该能力，负数或不可表示的时长无效；disabled 不创建依赖；CORS 204 不表示认证或启用成功。HTTP shutdown 回调异步触发幂等 Close，不等于等待回调完成，也不停止已派发远端工作。

## 5. 待开发工作逐项清单

| 序号 | 工作包 | 当前基础 | 待完成与退出条件 |
|---|---|---|---|
| 1 | R0 原契约 | 双方原八类同步codec已提交，Go新增overview第九类／Filecodec6；read35／list38共享fixture核验，overview无新JSONfixture | 全部操作家族、嵌套 DTO、Job 更新、默认值与错误语义的 Rust／Go／TS fixture 对照；source-derived fixture不是Rust oracle，旧Skill read空cwd已验证原本拒绝，间接ENOTDIR由28修复 |
| 2 | S1 身份／存储 | managed verifier、原 12 字段 SQL repository／迁移、当前宿主 User 适配及公开凭据管理 | 显式旧凭据全量导入、真实 PostgreSQL 约束与迁移验收、OAuth／project／其他 account family 独立验证、原业务表和多记录事务 |
| 3 | S2 MCP／Registry | 内存 registry／五条 transport 路由、legacy process Job 与同实例已知 process Job 库存对账 | 真实业务授权／派发、原 MCP／Generic ToolRuntime、其他库存家族、重启与持久投影 |
| 4 | D1 Runner core | Host owner／scope／guard／文件观察 | 其余 invocation、长期执行归属、正式人审响应与持久审计、各 direct 家族零模型验证 |
| 5 | D2 受限 process | Linux actual bwrap／full＋显式 process-group 的 prepareNativeImage／provider／Runner consumer 已提交，source／plain Node 已验证 | linux-scope FD、auto owner、pkg confined、profile／环境快照及平台结果等价；保留脱组与未知结果限制 |
| 6 | D2 shell／script | 已有 wire codec | 原 shell 路由／policy、解释器选择、临时脚本与清理、内部 POSIX、SSH 不回退本地 |
| 7 | D2 Job | Server process Job 管理／同实例恢复，以及 Runner FIFO／真实 native process／stop／legacy job_update 单发送者已提交 | Runner 库存／sequenced reconciliation、script／detached／SSH／validation Job、真实 G2 跨端闭环、持久化与恢复待完成；进程内身份抑制不恢复结果 |
| 8 | D2／W1／E1 File | read／write／list／skill read／skill list实际5／20；通用scanDir完整directdir nofollow扫描与最小limit保留已接Runner，不是截全量listDir数组 | 剩余15项及恢复语义待验收；真实E2B未跑，旧Skill read空cwd已验证原本拒绝，间接ENOTDIR由28修复；listing不产生FsObserved、不授写权，原无max_bytes／maxOutput／512KB限制，仅额外Host完整结果预算 |
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

### 5.1 剩余 15 项 File 操作

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

## 6. 优先顺序

| 顺序 | Runner 主线 | Server 主线 |
|---|---|---|
| 1 | process Job legacy 执行已提交；继续库存／sequenced 更新与恢复、script 及受限平台缺口 | Skill read／listing已提交；继续剩余15项File；overview需FS warnings及同execution world受控Git，Go29 codec／路由已提交，DSH执行未完成；显式旧凭据导入及真实 PostgreSQL 验收 |
| 2 | 原 process Job／stop／update／库存闭环 | 原业务授权／派发与持久 registry／结果 |
| 3 | shell／script、编辑／删除／产物等 G2 基础 | 原项目／任务／执行／审批事务 |
| 4 | Validation／LSP／项目生命周期、完整 G2 | MCP／Generic ToolRuntime／ProjectConnector |
| 5 | 真实联调、恢复、平台矩阵 | Web 模式／Connector／UI／G0 |
| 6 | 原可选扩展与全量验收 | 导入、切流、回滚、退出旧系统 |

## 7. 已有验证（十三条及更早阶段历史回执）

本节保持当时测试和工作树状态；当前第二十九条阶段见第 22 节。历史“提交后干净”不表示目前两仓库没有其他 agent 未提交工作，历史沙箱不可用也不否定本轮 bwrap 实测。

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

本次保存二十九条功能／修复＋三条fixture-only，root与镜像按原脚本核验后正常docs-only提交。第9–21节为历史，第22节为当前阶段。上次保存二十七条功能／修复＋三条 fixture-only 阶段，原脚本同步与默认检查每仓库五文件，正常提交实际四个变更文件（3 Markdown＋manifest），INDEX不强写。既有 docs20 为 Go `18056603442a4001ca0d6a6fc7ab72dc7b9876e3`、DSH `ba6e42579504c21e3a7494a83d48bf34467a4fdc`。本快照固定顶部源码基线；第 9–21 节为历史，第 22 节为当前阶段，不声明整个工作树干净。

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

## 12. 十九条阶段历史：Server legacy 结构化 process Job

第 19 个功能／修复为 Server `efa85831ef08995c54ab63dd33bc57afd7106acb`，`feat(webcodex): manage legacy structured process Jobs`，准确 23 文件，父提交 `a8b34712b56172884a1256f80b1afb37665da4cb`。当前计数 Server 8＋DSH 11＝19，另两条 test 提交不计功能；Go 基线为本提交，DSH 仍为 `1be7718659c239eaa9e441ba3ca064ceb6277657`。第 9–11 节 Go Job 在研的描述属于历史。

Server 已有独立 records、Start／Get／List／Log／Stop，以及原 job_update 第五条 transport 路径（另四条凭据 management，共九路径）。真实 managedVerifier 校验 scopes／owner／group／active instance／request；poll 释放 pending 槽并保留 dispatch binding。Stop 防重复，队列满不改状态；终态 first latch；原 legacy optional sequence 仅记录，保留 finished fallback。双流各按 256 KiB 有界日志、绝对 cursor 与 tail reset 处理，List 限制 20～100，900s TTL 按 Server observed 时间计算。独立 `max_jobs_per_runner` 必须显式为正，不从 pending 值回退；enabled 现在要求六项正数限制，disabled 不创建依赖。

Host project／scope 授权前置，未增加用户 dispatch HTTP 或 MCP；无 Runner JobManager、持久化或跨进程恢复，Close 不保证远端停止。inventory／reconciliation／log_snapshot／script／validation／detached／SSH 继续拒绝，原 G2 strict 与 DSH native 3／22 注册拒绝不变。正式 bwrap 仍 in progress，仅有实际 source／built 部分验收，未提交、不计功能。

最终实际回执：任务 530 离线 Go `-tags unit -race`，config／runner／server 选择 `^Test(WebCodexRunner|Job)` 全通过，分别 1.054s／1.479s／1.237s；旧同步 queue／HTTP 选择回归任务 516 为 1.299s 通过。管理路由初选 `^TestAgentTokenManagement` 命中零项，不算覆盖；改实际名称 `^TestWebCodexAgentToken` 后 server race 通过（1.131s）。parent 提交执行者核对 23 个白名单路径、diff／index 空且无后续源码修改；这是该提交收尾回执，不声明包含镜像／scratch／旧日志的整个工作树干净。

当前共享 fixture 保持 91 DTO／16 families／12 operations，SHA-256 `a5a6a41542f2e382a32ba25fd287f83a33dd310bb29fadacbec1099d776497dc`，TS Job 162／162 与 Go fixture 0.005s 为最新 fixture 回执；487／487 是新增 fixture 前历史，没有 488 全组重跑。约 15%（10%～20%）、13 工作包无一全量完成、其余 File／Project／Computer 数量不变。三根文档和两侧镜像同步十九条阶段并默认／diff 核验，仍不 stage／commit，不重跑产品测试。

## 13. 二十条阶段历史：正式 confined native 集成

第 20 个独立功能为 DSH `655b2a643e49020f32641839753d45edf631ff06`（`feat(runner): execute confined native images through private bootstrap`），62 文件、+1230／-88，normal hooks 本地提交。累计 Server 8＋DSH 12＝20，另 2 条 fixture-only 不计功能。Go 源码仍固定 `efa85831ef08995c54ab63dd33bc57afd7106acb`；十九阶段 docs Go `4a58b994def92dec8372dc0725f4b31f62bdb69b`、DSH `d017b44b6eccba005c0d0126cb3b1f2042343002` 已提交。首次 target private channel 属于既有功能，不重复增加序号。

正式 `prepareNativeImage` 闭包、Linux actual bwrap／full＋显式 process-group、本地 private bootstrap 与 Runner consumer 已提交；target argv／cwd／env 走 fd3。default／PTY 拒绝 defined field，E2B 两入口在远程资源前拒绝。bwrap executable 捕获 absolute default PATH，generic／exact／wrap 共用，generic 用 `/bin/true`。Runner 原 signal deadline 外，增加 performance elapsed 在阻塞 prepare 后 spawn 前复查，超预算 not_started 且 spawn 0。README／新增 confined-native-images Note 与 API／config／type-equiv 已同步。

pkg confined、auto／scope、linux-scope FD、profile／环境快照及真实 macOS／Windows／musl／arm64 仍待验收；只证明 source／plain Node artifact，unconfined 保留。WW /tmp 遮 bootstrap 绝对 argv／file URL／realpath 且无 workspace bind 恢复时提前拒绝，不广 mount，不保证找全 transitive imports，仍 possible unknown；无 valid receipt 不虚成功。process-group 脱组限制与 no live PGID 不等于 all zombies reaped 保留，Python subreaper 仅 fixture。process.ts 仍四参数，无 Job onStarted；DSH dc745 Job 集成、Go 854 reconciliation 均进行中且未提交，不计数。固定 Server 继续库存／对账／log_snapshot 拒绝。

已收取 source 五 specs 208 passed／0 skip（15＋46＋39＋72＋36）；最后 `/bin/true` 改动后 sandbox 46／0 与 2 文件 lint 0／0 是重复聚焦回执。此前测试 matcher 修正后的 10 文件 typed lint 0／0、blocking prepare 1 pass／38 filtered 不混加。官方 bwrap 0.11.0 的 plain Node Loader RO／WW 各 6 HTTP＝12 requests，含 TERM accepted＋unknown／no-live detection／Python no-children；bash 44 额外 owned /tmp admission-only＋PATH capture 不并入 12。旧 source 143／289、scratch 72＋78 均保留为历史，不加总。

隔离候选从 d017 导入 exact owned files 并排除 Job，相关四组 tsc 通过；Cordis 99 artifacts 生成 0 change＋freshness、config freshness、421 type-equiv pairs／export JSDoc 通过，10 generated 与 main 逐字一致；候选已 cleanup，仅 main worktree，五包 artifacts 支持 smoke。mdlinks 1537／mdwrap 1544／budgets 8／doc-typecheck 80 blocks 通过；doc-quick 15／16 的历史 heading anchor 失败修后 doc-standard 12／12 成功，未声明 doc-quick 整组重跑。hooks 13 pairs 一致，16 文件 staged lint 0 errors／3 旧 unused-disable warnings，notices／whitespace／vendor 通过。完整回执及源码引用见[第二十阶段实施进度](./WEBCODEX_MIGRATION_PROGRESS.zh-CN.md#第二十条阶段正式受限-native-执行已提交)。

R0 fixtures 91／16 families／12 operations、原 39 serde probes 不变；约 15%（10%～20%）、13 大包无一完成、File 3／20、Project 0／7、Computer 0／19、native 最多 3／22／file-only 2／22、真实 Go G2 HTTP 400 均不变。无生产替换或持久 MCP 闭环。本次仅根三文档及每仓库五文件镜像同步／默认检查／镜像 diff 核验与 normal hooks docs-only 本地保存，不重跑产品测试，不 push／deploy／调用真实 E2B／model／credential／service。

## 14. 二十一条阶段历史：同实例 process Job 对账

第 21 个独立功能为 Go `40817b71370bea996e21519a24268fae03b87338`（`feat(webcodex): reconcile same-instance process Jobs`），15 文件、+1544／-34，正常本地提交完成。功能 21＝Server 9＋DSH 12，另 2 fixture-only 不计；DSH 基线仍 `655b2a643e49020f32641839753d45edf631ff06`，dc745 JobManager／FIFO／update 执行集成进行中未提交，不计第 22 项。

`job_recovery_grace_seconds` 独立正数启用 reconciliation，默认 0 拒绝；MaxJobs／MaxPending 独立。只接受同 Server／同实例已知、已授权且 dispatched 的 process Jobs，全 typed inventory 预检后锁内原子核对 owner／group／client／instance／request／context。库存 active 在 terminal 前，64 active＋64 terminal／128 total，完整 Rust 等价 JSON（含 HTML／U+2028／U+2029／literal 反斜杠转义）上限 1 MiB；snapshot 每 stream 64 KiB、saturating cursor 且不得回退。

正 seq、canonical Runner-owned 状态、finished 等于 terminal、completed exit 0、active 无 exit／duration；terminal 不可变，stale seq 忽略，snapshot 禁 chunk／tail 混用，同实例 cap 降级拒绝。offline／观测 stale 启动固定 grace，每 Job 有界可取消 timer，恢复／terminal／Close 取消，合法迟到 snapshot 仍不能逃过 expiry lost。invalid auth／库存 shape 无 mutation；恢复期间 stop 拒绝，authoritative running 恢复后可再 stop。Get／List（含筛选）／Log 显示 recovering，内部原 12 Runner states 不变；900 秒 TTL 从 Server first terminal observed 起算。

cap=false 时 inventory／log_snapshot 仍拒绝，旧 tail reset／optional seq／finished fallback 保留；unknown／expired／restart／workflow／validation／SSH／detached 跨实例库存仍拒。无持久化、跨端真实 G2／MCP 闭环或生产替换。现有受限 native 的 source／plain Node、显式 process-group 及 auto／scope／pkg／平台限制继续有效。

原主体离线 Go 1.27.1 unit／race／count1：runner 78 top＋182 sub PASS、2 原 DSH opt-in interop SKIP；protocol 30 top＋176 sub PASS；server／config `^TestWebCodexRunner` 分别 17 top＋23 sub、4 top＋17 sub PASS。新增 recon 主体 17 runner top＋最后 public overlay 1＝18 runner top，路由 1 top＋6 sub、config 1 top＋5 sub。最后 overlay 窄 race 15 top＋18 sub、无 skip、1.338s，覆盖公开 Get／List／Log inventory／snapshot 恢复和 terminal、内部状态不变；不累加成全套总数，protocol／config 未为 overlay 重跑。gofmt／diff／正常提交通过，源码提交收尾 index 空。细节与源引用见[第二十一阶段实施进度](./WEBCODEX_MIGRATION_PROGRESS.zh-CN.md#第二十一条阶段server-同实例-process-job-对账)。

整体约 15%（10%～20%），13 大包无一 fully complete；File 3／20、Project 0／7、Computer 0／19、G2 native 最多 3／22／file-only 2／22、真实 Go HTTP 400 不变。R0 91 DTO／16 families／12 operations 与原 39 serde probes 不变。仅同步 root 三文档与两侧五文件镜像并默认／diff 检查，不 stage／commit、不重跑产品测试，不读真实 credentials，无真实 DB／外部模型／生产／push；准备完成即释放写占用。

## 15. 二十二条阶段历史：Runner process Jobs

DSH `944634f0b1b311f0ecba124ceb9681bc12f15322`，`feat(runner): execute and deliver structured process Jobs`，33 文件、+1870／-115，previous 为 `ba6e42579504c21e3a7494a83d48bf34467a4fdc`。功能 22＝Server 9＋DSH 13、fixture-only 2；Go 暂仍 `40817b71370bea996e21519a24268fae03b87338`，file_skill_read_file 下一片进行中未计第 23 项。两份外包 type fixture 修正待最终 test commit 回执，不先增加计数。

原 start_process_job／stop_job 经独立 provider、HTTP poll／JobManager FIFO／private Host ToolRuntime／actual native process／legacy job_update 单发送者已接通，无 Agent／Session／ctx.jobs／模型。显式 processJobs 对象省略即关闭，source／Loader 验证 optional union；Job timeout 1–3600 秒、local max 3600000 ms，不走 sync 1–120 秒。private onStarted 只有 actual started receipt 且双 PipeTail reader attached 才 running＋原 working／process_running／runner_execution；原四进程状态映射 Job failed／lost／timeout／stopped／completed、terminal immutable。queued stop 先 splice 不启动；running stop 真实 cancel，受限 carrier 无 target-exit 必须 lost／outcome_unknown，不虚 stopped。

concurrency 1–64 FIFO、active64／terminal64／900秒、四固定生命周期更新、双 stream 64 KiB，records／bindings／bytes／body／storage 全信封预算。首 stop binding 预留、终态／卸载完成释放本 owner 预留。Symbol.for 账本跨 provider reload／卸载／duplicate imports 保留 owned identity hash／count，旧 request／Job 不重执行、冲突拒，aggregate maxBindings／maxBindingBytes 耗尽拒新 admit，配置降低仍识别旧 duplicate；global 不保留 Context／handles／snapshots。pending updates／snapshots 不重建，旧重复只抑制执行，不伪造结果回传。

HTTP single worker 用 immutable replacement tails／null chunks，同 body retry 不重执行；不广告 inventory／log_snapshot／reconciliation。legacy 无 seq fence，超时旧 HTTP 可能迟到，不能拼成 GoSeq 跨端已通。永久 poll／send 失败 abort 并 await cleanup；容量硬耗尽可能仅 local sanitized status，poll 保 stop，不承诺 Server 拒绝 ACK。无 script／detached／SSH／validation Job，structured_execution_jobs 仍 false；D2 全族与持久恢复未完成。

primary 最后一 run 88／88 零 skip（33 manager＋4 jobs client＋8 actual source Loader＋43 process）；官方 bwrap RO／WW 用 Python owned subreaper 收全 no final children，不等于产品回收所有脱组后代。latest plain Node built full／unrestricted、RO、WW smoke 均 PASS，涵盖121秒 decoder／literal argv stdin／真实127／queued running stop／dup／unload cleanup／受限 unknown；同实例 main Loader HMR 旧 marker／running once 且新 Job 可执行。较早 216 pass（162＋26＋7＋21）零 skip 与 88 范围不同且重合，不相加。

421 type-equiv／export JSDoc／相关 tsc 通过，API99 catalog／config／paired zh／subsystem 同步。doc-sync 原33／34，唯一 doc-typecheck 引出 Host 测试类型错误；Job自身／两份旧 Runner fixture 随944修复，pwsh-sandbox constructor Config 与 subprocess-local exactOptional／terminal env 外包测试修正仍待最终回执，不宣称叶检查修后 PASS 或 doc-sync aggregate 全绿。正常 Job hooks 最终细节待 dc receipt。源引用与详细回执见[第二十二阶段实施进度](./WEBCODEX_MIGRATION_PROGRESS.zh-CN.md#第二十二条阶段runner-process-jobs-执行与-legacy-发送)。

File 3／20、Project 0／7、Computer 0／19、R0 91 DTO／16 families／12 operations、39 serde probes、G2 files2／native最多3of22硬拒HTTP400不变；13大包none fully complete、整体约15%（10%～20%），无生产替换或持久MCP闭环。本次只sync／default／mirror diff check，不stage／commit、不重复产品tests，不读真实credentials，无DB／模型／E2B／生产／push；后续File成对完成再统一保存docs。

## 16. 二十三条阶段历史：Go skill-file codec 与 FileRead 路由

Go `32f2d5fb45770f0bfc86a9bf38e237ccf7d3eeec`（`feat(webcodex): route native skill file reads`），10文件、+393／-11已提交；功能23＝Server10＋DSH13、fixture-only增至3（第三条为后续45487类型测试修正）。DSH固定`944634f0b1b311f0ecba124ceb9681bc12f15322`，skill-file镜像与执行准备中不计第24项，File仍3／20。

Go新增file_skill_read_file为第七类同步codec／Invocation并使用FileRead路由，原27字段RunnerRequest／九字段File payload不改；content仍opaque字符串，options／path业务验证由Runner执行层负责；Job decoder对此类仍ErrUnsupported。独立schema1 source-derived fixture skill-file-read.json有35 cases（9 accept）、另16 deferred File kinds和2 source output examples，无Rust oracle；SHA-256 `3441d675f3765b7bc88f50e3d75b6a3e1bf31c2469497bf24e2a8e4d2ba100f9`。不混原R0／Jobs91／16families／12operations与39oracle，不把source examples计执行。

离线Go1.27.1普通-count1初次protocol／runner特定File／Generation／Job classify selector PASS 0.008s／0.007s；最后fixture变化后3 top TestSkillFileRead protocol PASS 0.004s，35 cases／16 deferred于subtest覆盖。本片无新增并发、未跑-race，聚焦重跑不累加；gofmt／diff／cached检查及正常10文件白名单commit通过。源码与独立fixture见[第二十三阶段实施进度](./WEBCODEX_MIGRATION_PROGRESS.zh-CN.md#第二十三条阶段go-skill-file-同步-codec-与路由)。

File3／20而非4／20、Project0／7、Computer0／19、G2原bits与files2／native最多3of22硬拒400、整体15%（10%～20%）及13大包none fully complete均不变。原20项File wire清单不扩名，无真实credentials／DB／models／G2互操作／push。原22阶段待回执项已由下述fixture-only补充更新，doc-sync aggregate仍未重跑；只sync／default／mirror diff check，不stage／commit、不重复产品tests，待DSHFile24一起最后docs保存。

### 类型 fixture 与 Job hooks 最终回执（不增加功能）

DSH `45487eef33e72103f97813a95e5f313840949b94`，`test: align subprocess fixtures with typed configuration`，parent `944634f0b1b311f0ecba124ceb9681bc12f15322`，仅2文件、+8／-3：pwsh-sandbox/tests/sandbox.spec.ts constructor fixture type，以及subprocess-local/tests/native-confinement.spec.ts省略executionMode／PTY字段。第三条fixture-only，功能仍23；行为基线保留944634。初次constructor编译失败已改用union实配类型，max-len失败已换行修复，最后无未解决失败。

最终Host `pnpm exec tsc -b tsconfig.host.json` PASS；`pnpm run doc-typecheck`完整该叶（build:lib:host＋contracts-ready）PASS，80 blocks compiled／78 ignored／798 type-equiv-catalog／956 paired derivatives。native confinement15／15 PASS，typed lint两文件0 errors／0 warnings，normal hooks／diff check通过。历史doc-sync33／34加修后single leaf PASS，未重跑aggregate。pwsh spec收集会调用真实PowerShell，因此未跑其spec，type-only修改由Host type build验证；执行者jobs88–94全收、index空，仅父四mirror dirty为该收尾点回执。

Job944最终normal hooks：6 named staged translation pairs、14 staged source lint 0 warnings／0 errors、whitespace／vendor全过，33files+1870／-115。DSHFile dab已开始Go32f2d5配套执行实现，仍待第24项，不增加File3／20或功能数。本轮仅补文档与镜像，不重新运行上述产品检查。

## 17. 二十四条阶段历史：Go Skill package listings

Go `746e98015112dfdd67837d2b72fa60015a96b0e4`（`feat(webcodex): route native Skill package listings`），parent32f2d5，11files+373／-11已提交。功能24＝Server11＋DSH13、test-only3；DSH功能基线944634／fixtureHEAD45487不变。Go八类同步codec、File codec5／20，DSH已提交基线六类同步；reader未提交，不计25，File实际仍3／20。

四行产品逻辑新增file_skill_list_packages并移出deferred，Jobdecoder仍ErrUnsupported、registry复用FileRead gate；原27请求／九字段FilePayload不改，options opaque，path `.agents/skills`／limit1–257业务验证留executor。non-null range／write-only hash／prefix／create_dirs=true拒绝，unknown kind独立分类。真实Server enqueue／poll／result fixture及file_read=false gate通过，generation不变；没有包扫描，不计实际File5／20。

独立schema1 source-derived／noRustoracle listing fixture为38cases（10accept／18canonicalinvalid／1unknown／9wirereject）＋另15deferred／3executor-only source outputexamples，SHA `a445511e3884b382e9179bb365577f47d8e6612a7a323e0c7371d59779fb3050`。旧read35fixture SHA3441d675f…字节未改，仅Go read测试更新listing的新sync成功／JobUnsupported期望；DSHread镜像尚未提交，不称共享验证。原R0jobs91／16families／12operations与39oracle不混新表。

离线Go1.27.1普通-count1指定SkillPackagesList／SkillFileRead／FileOperations／Job分类／PublicJSONFixtures／capabilities／同步registry拒Job／Generation selector：protocol PASS0.015s、runner PASS0.007s。本片无新增并发，没有-race或full；gofmt／diff／cached／normalcommit通过，执行者jobs101／102已收。完整命令与源引用见[第二十四阶段实施进度](./WEBCODEX_MIGRATION_PROGRESS.zh-CN.md#第二十四条阶段go-skill-package-listing-路由)。

整体15%（10%～20%）、13大包none fully complete、File实际3／20／Project0／7／Computer0／19、G2native最多3／file2of22硬拒HTTP400不变。DSHreader仍进行中，不记开发中检查为最终完成。仅root／mirrors同步检查，不stage／commit、不碰DSHindex，不改rootmap／sync脚本，不重跑产品tests，无生产／模型／DB／凭据／push；待25最终回执再统一正常docs保存。

## 18. 二十五条阶段历史：Skill文件读取已提交

本节为33c456时点证据；当时已知ENOTDIR差异及Note计数措辞，现分别由第26项bd5216和纯文档32eea45解决，最新结论见第19节。

DSH `33c456f3a9ba487265cac42c70b473c2f5fbffbd`（`feat(runner): read native Skill package files`），parent45487、30files+969／-134。功能25＝Server11＋DSH14、fixture-only3，Go746不变。File实际4／20，DSH同步7，Go同步8／Filecodec5；listing仍Go-only，不计实际5／20，剩余listing＋其他15项共16项。

原27wire／九字段payload／11字段Skill stdout保留，generic content opaque，具体options支持exact两位置array／object、known duplicates拒绝／unknown忽略／整数词法u64。私有RangeScan共用于basic read和Skill，旧六字段输出／whole预算不变；Skill默认48KiB选区、min(policy,192KiB)无floor、完整stdout min(policy,512KiB)，full raw SHA／strict UTF-8含范围外字节、BOM／NUL／CRLF／尾CR／空行／1-based2000／u64sat。

FS provider处理cwd／package lstat／拒最终package软链／canonical containment／requested与canonical秘密规则，资源包内软链可；file_bytes为prestat，stable-version retry是宿主加强而非原Rust二次限流，无快照承诺。candidate按call id只在最终ToolRuntime＋outer完整预算＋owner/call未abort后emit，finally drop，失败不记absence；同Host成功可写、异Host／postexec取消／outeroverflow不授。旧basic read观察时机不改，不能称所有早emit已修。不增FSseam／Agent／Session／ctx.jobs／capbit／默认composition，Job业务不变。

已知P2待26：FS_LOCAL将ENOTDIR折叠NOTFOUND，普通文件/child或package父组件普通文件误回skill_file_not_found，冻结应skill_path_invalid。33c456未包含reviewfix；26独立修复进行中，不能宣称已解决。listing仍须有界nofollow provider，不用截数组替代。

最终回执分列：codec520＝Skill79＋protocol279＋Job162；newrange10＋basicrange74；Skillpolicy42；旧filepolicy25＋deadline5；realLoaderfiles11含新3policy同request；client27含readbitfalse／baseline22／G2400，均PASS。Runner tsc-b／tsdown／plainNode built-files-smoke同request三策略PASS；typedlint9changed＋追加2files0error，normal precommit10files lintPASS；doc-typecheck80blocks、type-equiv421／API99fresh／exportJSDoc PASS。named5pairs＋modulepair／precommit6pairs、mdlinks1539／mdwrap1546／budgets／readme318／noteformat／classification310 PASS；modulegraph漏Runner节点的三生成docs修复随本功能提交，freshpass，不另计功能。非完整doc-sync重跑，不将重复选择累加。

read35共享fixture双端cmp／SHA `3441d675f3765b7bc88f50e3d75b6a3e1bf31c2469497bf24e2a8e4d2ba100f9`通过，9accept＋另16historicaldeferred／2源码例，无Rustoracle／Go执行证据；oldjobs91／16families／12operations与39oracle不变。详见[第二十五阶段实施进度](./WEBCODEX_MIGRATION_PROGRESS.zh-CN.md#第二十五条阶段dsh-原生-skill-包文件读取)。13大包none完整、15%（10%～20%）、G2file2／native最多3of22硬拒400不变；仅准备sync／default／mirror diff check，不stage／commit、不重复产品tests、不poll26、不改rootmap，待26最终回执保存。

## 19. 二十六条阶段历史：非目录Skill祖先错误码已修复

DSH行为基线 `bd5216c4e45ff25c2a70ccd5a51881815d89c2ea`，`fix(runner): distinguish non-directory Skill ancestors`，parent33c456，5files+46／-4。后继HEAD `32eea451266c00303c8bf23ea90319eb4e6364d9`仅三份Note计数文档校正，不增功能。Go746不变，总26＝Server11＋DSH15、test-only3；File实际4／20、DSH同步7／Go同步8／Go Filecodec5。当前无ENOTDIR pending，listing仍未真实执行。

私有checkAncestors以现provider lstat检查包与资源父组件，symlink resolve＋stat跟随目录，非目录→skill_path_invalid、真正缺失→skill_file_not_found，最终package symlink依旧escape。未改basic read／公共FS API、无Nodefs业务IO。两新增real FS fixtures覆盖SKILL.md/child及package父.skills普通文件，附加symlink-to-file/child、目录symlink资源允许、true missing／失败无观察与write拒绝。旧basic read早观察不属本片范围。

最新policy44＋Loader11＝55 PASS；Runner tsc-b／typedlint2 PASS；tsdown＋plainNode built同Skill request三policy PASS；named Note pairwrite/check／diff及正常fix commit pair／2filelint／whitespace PASS。未重复520codec／84range，不把25的policy42与最新44相加，Loader亦重合。后继32eea45修remaining16 including Skill listing（中文包括Skill列表内其余16），4＋16＝20；namedpair／diff／normal hooks PASS、未重复产品checks。源码及执行者job回执见[第二十六阶段实施进度](./WEBCODEX_MIGRATION_PROGRESS.zh-CN.md#第二十六条阶段skill非目录祖先错误码修复与最终保存)。

本次按原脚本同步和核验各侧五文件，仅正常提交实际四个镜像变更（3md＋manifest），INDEX未改不强写；不纳入NOTICE／scratch／日志／rootmap，不改sync脚本。不重跑产品tests／doc-sync aggregate，不push／部署或使用真实凭据。整体15%粗10–20%、13大包无一完整、File4／20、Project0／7、Computer0／19、G2file2native最多3of22合规HTTP400不变，SkillList有界nofollow FS缺口留待后续。

## 20. 二十七条阶段历史：原生Skill列表已提交

DSH `a6bb656769050a320bb8046e7bc43b903b831135`，`feat(runner): list native Skill packages`，63files+1555／-64。总27＝Server11＋DSH16，fixture-only3另计；Go746行为基线不变，后继docs不加功能。File实际5／20、剩余15项，两侧同步codec8／8，Go Filecodec5不变。

通用FS scanDir Definition／local／fs-sandbox继承／E2B provider与真实Runner consumer已接通：完整directdir扫描，不follow子项／resolve子目标／读内容；目录与symlink候选先计数再跳invalidUTF8／tuple去重，只retain limit最小UTF8名字／种类，BOM保留，truncated=count>limit，exact path／limit1–257／对象或exact1array保持；EOF前无partial success。原SkillList无max_bytes／maxOutput／512KB限制，只有Host maxResultBytes完整FSmetadata及outer预算；任何outcome不发FsObserved，不授写。FS／E2B owned清理，真实E2B未跑。

同commit修listing .agents→ordinary/child间接ENOTDIR：local FS_NOT_FOUND保留cause，Runner不吞空，真missinglink仍empty，空cwd拒unavailable；不额外计功能。历史bd5216保留，旧Skill read空cwd／间接ENOTDIR仍待验证，不能说被本片修复。list38共享fixture逐字同Go，SHA a445511e3884b382e9179bb365577f47d8e6612a7a323e0c7371d59779fb3050，38＝10accept／18invalid／1unknown／9wireReject，另15deferred／3源码例，无Rustoracle；旧read35／jobs字节未改，旧read历史listing deferred现consumer接受，原wire27／File9／Job12状态不变。

验证分列：codec604＝85＋79＋162＋278；最末listpolicy25＋Loader14＝39 PASS，先前client28独立不是67一次aggregate。localscan13 PASS后仅测试void→undefined类型修正；resolve／旧listDir选中26 PASS、123未按selector运行。E2Bfilesystem75／helper44各PASS，使用本地fake carrier而非真实E2B。Host typebuild（143）／最终fs-local与Runner leaves（153）／E2B tsc PASS；新15files typedlint首轮仅一test的5处类型报错，修后该文件（162）PASS、其余14首轮无错；Runner／E2B lint及commit11pairs／26TS／whitespace／vendor PASS。

有效built为programmatic tsdown五target（159），真实typertPlugin host、ESM/es2024/.js与exports一致，Runner三entry；先前CLI154／157未匹配及158.mjs不作为有效built证据。普通Node built-files-smoke（161）同request[2]/max_bytes0三policy PASS：list后blindwrite拒、旧Skillread后write；source另maxOutput1。不是全Hostbundle或平台矩阵。424type-equiv／paired derivatives、API99fresh、exportJSDoc、doc-typecheck80blocks使用built types/noEmit、namedpairs／links1543／wrap1550／Notes312／README-subsystem／budgets8（163）PASS，无34gate aggregate。精确配置、源链接及各回执见[第二十七阶段实施进度](./WEBCODEX_MIGRATION_PROGRESS.zh-CN.md#第二十七条阶段原生skill-package-listing与有界目录扫描)。

整体15%（10%～20%）、13大包无一完整、Project0／7／Computer0／19、native3／file-only2of22与合规G2HTTP400保持，下一步剩余15File与旧read差异验证。本轮只root／mirrors同步核验、正常docs-only提交，不重跑产品tests、不改业务／Notes／API／rootmap，不push／实际服务／凭据／模型／E2B。

## 21. 二十八条准备历史：SkillRead间接ENOTDIR修复

DSH `5f2cccb7212099a50c26d31127e4d1322639ef63`，parent145dec5344，9files+61／-17，`fix(runner): preserve indirect Skill path errors`。总28＝Server11＋DSH17、fixture-only3；Go746、File5／20、双方codec8／8与G2位不变。空 cwd已验证原本拒绝、映射skill_path_invalid；本次新增三policy回归，未改生产cwd处理。真正修复是SkillRead missing判断排除FS_NOT_FOUND内的ENOTDIR cause，间接symlink→ordinary/child的资源target／parent／包parent返回skill_path_invalid，真missinglink仍file_not_found；无新FS API／Go变化。

初167为47pass／3fail复现间接路径错误，修后169为Skillpolicy50（44＋6）＋Loader14＝64 PASS；168测试lint／170 Runner tsc-b／171 README与旧Note两pairs／172两files type-aware PASS。173 programmatic tsdown仅Runner三entry .js并接普通Node built-files-smoke三policy，每policy六request（list、空cwd拒、间接资源拒、blindwrite拒、有效read、按policy写）PASS。174正常commit两pairs／两fileslint／whitespace／vendor PASS，父jobs全收。未重复604codec／全Hostbundle／全suite／docsaggregate，不扩大Windows／E2B结论。证据见[第二十八阶段准备](./WEBCODEX_MIGRATION_PROGRESS.zh-CN.md#第二十八条阶段准备保留skillread间接路径错误)。

下一步overview需FS skipUnreadable／nonUTF8 warnings及同execution world受控git ls-files -z tracked过滤；原depth默认2/clamp1–4、limit200/clamp20–500，当前scanDir尚不足以实现其结果语义。Go29仅在另一worker准备，未有实际提交回执，不能增加codec／功能数或第六项执行。当前只改根三页，不sync镜像／stage／commit，不动业务／Notes／生成API／rootmap／脚本，等待29真实回执统一保存。

Next只读证据补充：overview先git ls-files -z完整NUL索引过滤，仅失败／空index才fallback，超Hostbudget不得当无Git；现scanDir不支持排序前缀相关non_utf8／unreadable／symlink warnings，limit500后再excluded／Git过滤会漏项，需要新有界metadata扫描机制但尚未定案，未决定scanDirPage。E2B subprocess SDK2.29.1回调前累积全部stdout／stderr，consumer cap不足证明远程Git有界，需远端限额或拒未支持组合；无实际E2B验证，不计功能或测试。

## 22. 当前二十九条阶段：Go overview codec／路由

Go `ffae2865cc826f97131a45a89a63e7df2f9a3892`，`feat(webcodex): route project overview requests`，parentf4e321c384d8046f63b3468c2a2ea691f28aa8b4、10files+208／-15。总29＝Server12＋DSH17、test-only3；DSH行为5f2cccb保持。Go同步9／Filecodec6，DSH同步8／实际File5／20剩15，ProjectOverview属File族，Project0／7、Computer0／19不增。

生产仅operation.go同步File case／knownDeferred、jobs_operation.go两Job decoder仍ErrUnsupported、registry FileRead gate三点。原wire27／payload9不变，content opaque，nil／invalid JSON／duplicate options可透传、缺cwd/content generic valid；nonwrite／line与Job／command／stdin／typed payload冲突拒绝，canonical copy防后wire pointer mutation。无业务options parser／Git执行，默认clamp仍属未来executor。两新project_overview_test.go分别两大public API测试，覆盖codec/canonical/Job及owner/G2/cap/noqueue/atmostonce/poll/suppliedstdout→pending；旧read/list消费预期更新，deferred仍以deleteprojectfiles验证拒绝。

无新增共享JSONfixture／SHA／Rustoracle，旧jobs／read35／list38字节git diff核验未变；两README准确9／6，不改NOTICE，保留SPDX与引用。父最终完整diff review、gofmt八Go文件空输出、diff／cached／fixture检查、176正常commit均通过且job已收。实际离线Go1.27.1前台指定selector普通-count1 protocol0.018s／runner0.009s PASS，无job id，不是race／全suite；本轮不重跑。完整命令与源码链接见[第二十九阶段](./WEBCODEX_MIGRATION_PROGRESS.zh-CN.md#第二十九条阶段go-projectoverview同步路由)。

Next为DSH overview原生consumer、有界metadata warnings语义和同execution world受控Git；scanDirPage未定案，先截500再过滤会漏项，Git完整NUL索引超Host预算不能fallback。E2B callback前全输出累积缺口未修，无实际provider验收。28空cwd只验证空字符串原拒绝并加回归、生产cwd不改。整体15%粗10–20%、13大包none、G2file2native3of22 HTTP400保持。本次root及镜像sync／default／diff／cached后正常docs-only保存，不改业务／rootmap／script／旧untracked，不重复产品tests／全Host／docaggregate，无外部操作。
