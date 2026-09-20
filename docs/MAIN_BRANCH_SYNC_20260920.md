# saiGou 开发分支同步 main（2026-09-20）

## 范围与基线

用户要求将saigou的main合并到其他分支。本轮以刚完成官方0.2.7合并且已验证的本地main `aecc764cf3320c6ca0830a9174836f7cbdd7c384` 为固定来源；该版本包含STATE账号隔离、策略/复验/观测、Web和Prism功能。官方合并验证详见 `docs/UPSTREAM_MERGE_20260920.md`。

通过 `git ls-remote --heads origin` 及 `git fetch origin` 核对仓库 `https://github.com/saiGou-14H/sub2api.git`：main之外实际发布的开发分支只有以下三个。本轮不更改历史backup/sync分支或未发布的其他功能分支。

| 目标分支 | 同步前HEAD | 同步方式 |
| --- | --- | --- |
| feat/prism-transport | 914295ea94fd4b7b00e3d5a58ca9a0eaa51ee271 | 原历史已全部包含于main，fast-forward到aecc764cf |
| fix/prism-turn-state | b78dd86a4ecc74a1291d14efb30c6a5a59c4de5e | 原历史已全部包含于main，fast-forward到aecc764cf |
| mpc-server | 1cc8536278a984544e35324f8fd06a2a5b0f5346 | 保留30个独立开发提交，非快进合并main |

三个同步前HEAD已保存在主开发副本的backup/prism-transport-before-main-20260920、backup/prism-turn-state-before-main-20260920、backup/mpc-server-before-main-20260920。本轮为本地分支合并，没有push或生产部署。

主开发副本 `/root/project-development/A2AMesh/source-sync/sub2api` 的两个Prism分支最终与main指向同一commit，因此其代码树完全相同，复用该main刚完成的验证，不重复执行相同测试。mpc-server先在该副本合并、验证，再将验证后的结果快进同步到已有 `/root/project-development/A2AMesh/integration/sub2api` 开发副本。

## mpc-server合并与兼容边界

Git自动合并成功，零文本冲突；只有wire_gen.go和config.go需要自动交织。前端和backend/internal/service目录与main完全一致，未重新改动或构建前端，使用上一轮完全相同的已验证产物。

独立只读审查确认：main的PluginKVStore、插件账号目录、CodexTicket runtime/handler、启动/退出清理均保留；WebCodex的provider、默认关闭配置、启用资源校验、managed credential鉴权、host user/owner/scope/client/撤销/过期复核、panel JWT/guard/limiter/audit/no-store、HTTP shutdown接线均保留。没有修改既有WebCodex功能语义或实现新功能。

四份同前缀迁移无需改名。migration runner按完整filename+checksum执行与记账，顺序为238_opencode_go_platform.sql、238_purge_unlimited_user_platform_quotas.sql、238_webcodex_api_keys.sql、238b_content_moderation_engine_meta.sql。已有旧WebCodex迁移记录不会导致新增文件被max-version逻辑跳过。实际新库验证结果见下。

## 本轮验证

验证使用Go1.27.1及已存在模块缓存，专属/dev/shm/sub2api-mpc-main-sync-20260920保存编译缓存和临时文件，避免填满根磁盘。不删共享Go缓存。

- 针对合并交界运行配置、全部WebCodex协议/Runner、完整repository、server及其middleware/routes、cmd/server、migrations unit测试。命令：`go test -mod=readonly -p 1 -ldflags='-s -w' -tags unit ./internal/config ./internal/webcodex/... ./internal/repository ./internal/server/... ./cmd/server ./migrations -count=1 -timeout=360s`。9包全部PASS：config0.414s、protocol0.034s、runner0.611s、repository3.756s、server1.814s、middleware0.029s、routes8.643s、cmd/server0.016s、migrations0.003s。日志 `/tmp/sub2api-mpc-main-sync-tests.log`。
- 合并提交为 `1a0f1763f302ff438007ae934a96c9e81b1d1f66`，两个父提交为原mpc-server的1cc853627与main的aecc764cf。
- 静态嵌入构建PASS，二进制 `/tmp/sub2api-mpc-main-sync-server`，Version=0.2.7-mpc-sync.1a0f1763f，SHA256 `9b8d3e9af32c5c0db8eac8d8e5bd5901e71833484fd53779cc77f7e296122c7a`。日志 `/tmp/sub2api-mpc-main-sync-build.log`。
- 独立Docker --internal网络、新PG18/Redis的12组冒烟全部PASS：fresh启动和迁移；SQL确认wc_api_keys十二列/宿主用户外键及四份238迁移独立记录；Runner和managed-token接口默认404；main新增schema；admin401；默认off；CAS/代理保护；controller版本；账号CRUD保留和参数拒绝；逐账号策略/状态隔离与无秘密；嵌入assets；正常退出。结果 `/tmp/sub2api-mpc-main-sync-smoke-result.json`，脚本 `/tmp/sub2api-mpc-main-sync-smoke.py`。这是新库验证，不是对生产历史数据的升级演练。

本轮不把main已有的前端2444项及全后端unit证据冒称为mpc-server全量重跑；对相同代码树复用既有证据，对交界代码进行上述新验证。没有真实Runner节点/上游模型请求或生产数据库迁移。

## 文件与部署保护

主开发副本原有untracked SYNC_REPORT.md保留；integration副本原有.scratch三文件及backend-service-final.jsonl保留，更新前记录SHA256并在快进后核验。不修改其他独立工作树、DSH仓库、生产9999/10000或账号开关。临时验证使用一次性PG/Redis和无外网Docker network。
