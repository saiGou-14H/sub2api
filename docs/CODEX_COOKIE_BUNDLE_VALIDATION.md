# Codex 同响应票据与 Cookie 组合：本地验证

基线：`aecc764cf3320c6ca0830a9174836f7cbdd7c384`。开发分支：`feat/codex-cookie-pin-20260920`。验证日期：2026-09-22。本记录对应本地功能提交；未推送、未更新9999/10000、未调用真实模型或读取真实账号Cookie。

## 实现结果

- 从同一次捕获响应读取票据头和两个允许的Cookie（`cflb`、`oailb`）；拒绝残缺、重复、删除、无效、过期或作用范围不符的组合。其他Cookie不进入缓存。
- 校验HTTPS目标、域、host-only来源、路径匹配和值；保留验证有效期所需的元数据。`Max-Age` 优先于 `Expires`，会话Cookie只有本地240秒上限，不据此断言上游有效期。
- 捕获和业务出口复验均检查HTTP200、完整SSE完成事件、completed状态和精确模型匹配。复验使用原捕获包；复验响应不覆盖原包中的任何Cookie或state。
- 组合按配置版本、账号ID、稳定上游身份、模型及业务策略/出口隔离，原子替换。生命周期从捕获响应头开始，消耗SSE及复验用时不重新续期；组合寿命取配置TTL、240秒和两个Cookie有效期的最短值，并投影到Redis时钟域。
- 注入时同时设置票据和Cookie请求头，清除大小写不同的旧头键，避免合并客户端Cookie；注入请求禁用重定向。失效回执增加内部BundleID，保护同state、同捕获时间但Cookie已替换的新组合。
- 新配置默认 `required / TTL=240 / refresh_before=210`；旧配置缺少模式字段时兼容 `optional`，保留既有TTL/刷新值。管理API接受并严格验证新字段；前端可切换模式，固定错误分类不回显秘密。

参考：[446599/ccodex-rotate@76490e235c82be1d54337c916c96026353490cac](https://github.com/446599/ccodex-rotate/tree/76490e235c82be1d54337c916c96026353490cac)。未引入参考项目的代理池、自动换IP或业务请求重放。

## 后端验证

使用本机Go1.27.1，模块按仓库go.mod/go.sum校验；缺失依赖准备完成后，测试阶段 `GOPROXY=off GOSUMDB=off`。由于根盘空间较少，编译缓存及临时目录位于 `/dev/shm/codex-cookie-check`，通过 `-exec 'sh /tmp/codex-cookie-go-exec.sh'` 将测试二进制临时复制到可执行的 `/tmp`，结束即删除。未使用生产数据库或Redis。

在 `backend` 目录执行的测试主体：

```sh
go test -json -p 1 -tags=unit \
  -exec 'sh /tmp/codex-cookie-go-exec.sh' \
  ./internal/service ./internal/repository ./internal/handler/admin \
  -run 'CodexTicket|CodexProbe|Codex.*Ticket|Codex.*Cookie' -count=1
```

| 测试包 | 顶层测试通过 | 子用例通过 | 结果 |
| --- | ---: | ---: | --- |
| internal/service | 107 | 198 | PASS |
| internal/repository | 26 | 27 | PASS |
| internal/handler/admin | 7 | 12 | PASS |
| 合计 | 140 | 237 | 无失败或跳过 |

最终JSON事件流保存于本机临时文件 `/tmp/codex-cookie-regression.jsonl`，顶层与子用例分别计数，不混称377个独立测试函数。另一次 `internal/server` 编译成功，但筛选条件下无匹配测试，不计入通过测试数量。

新增用例覆盖：同响应配对、复验不替换、残缺包不能跨响应补齐、错误模型/不完整响应清除包、名称/域/路径/有效期边界、Max-Age优先级和溢出上限、秘密不进入状态/metadata、大小写重复请求头清理、Cookie最早过期、复验后不重新起算TTL、账号/模型/策略隔离，以及相同state与捕获时间下的Cookie轮换/精确失效。既有配置CAS、预算、冷却、账号取消、HTTP/WS桥、观测与失效回归一并执行。

验证过程中修正的失败：

- 离线依赖缓存不完整：先下载缺失的公开模块，再运行离线测试；这些失败发生于测试执行之前。
- 旧模拟响应缺少Cookie、旧过期记录仍使用1小时TTL：按测试目标明确采用旧兼容配置或提供完整合成Cookie。
- 新增30秒刷新下限影响旧的立即提前刷新用例：已移除固定下限，按实际截止时间和配置提前量调度；刷新早于捕获时间时以捕获时间为下界，共享预算/冷却继续生效。
- Redis往返断言把同一时刻的Local/UTC表示误当成差异：合成样本统一使用UTC后比较完整包。

## 前端验证

在 `frontend` 目录：

```sh
pnpm run typecheck
pnpm exec vitest run \
  src/components/settings/__tests__/OpenAICodexTicketSettings.spec.ts \
  src/components/account/__tests__/CodexAccountState.spec.ts \
  src/api/admin/__tests__/codexTicket.test.ts \
  src/i18n/__tests__/localeKeyCompleteness.spec.ts
```

类型检查通过；四个测试文件共69项通过（设置28、账号32、API6、翻译3）。最终补充Cookie解释文案后，设置28项和翻译3项再次通过。用例包括旧字段遗漏按optional保存、新默认required/240/210、切换模式不覆盖自定义时间、非法模式拒绝及cookie_missing固定文案。

`gofmt` 检查与 `git diff --check` 均通过。独立只读审查未发现额外具体问题。

## v0.4.8 对照与补强（2026-09-22）

本轮基线为本地 `e84af1202`。已fetch并核对上游标记 `v0.4.8`，实际提交为 `013428031bae8890e3eb9907ab27cd39d1731f13`；上一版 `v0.4.7` 为 `cfac7e7`。对照[发布差异](https://github.com/446599/ccodex-rotate/compare/v0.4.7...v0.4.8)：

- v0.4.7新增的独立账号Cookie罐会被任何上游响应刷新，包括312响应，随后可能与原目标票据一起注入。
- v0.4.8在Probe和ServeHTTP两处默认只允许目标长度响应刷新Cookie罐；`cookie_refresh_all=true` 可恢复旧行为。该版本还修正了过期票据弹窗无法触发的问题，与sub2api管理页状态机制不同。
- 该版本README仍保留“任何上游响应都更新Cookie”的旧描述，且Cookie罐依旧按账号合并名称值。本地不照搬该独立罐设计：原实现已按账号/模型/策略保存同响应完整组合，只有完整成功且复验通过的候选才能替换；不引入全响应刷新开关。

本地补强集中于 `codex_ticket_probe.go`：

1. 捕获头到达时一起取得state快照与Cookie，复验响应不再解析或返回Cookie。复验仍解析完成状态、实际模型和312信号。
2. 发送复验前只进行一次Cookie校验与请求头生成，避免两次取时跨过到期点后意外发出只带state的复验。
3. `optional` 模式带Cookie的原包也在复验结束时重新检查期限，过期直接返回 `cookie_missing` 并清除结果；原控制器已会拒绝过期组合，本次使探测结果和错误分类同样准确。未带Cookie的旧兼容模式仍可使用。

Redis读取的耗时补偿已存在于缓存层，本轮保持原实现，避免重复扣减时间。

新增 `codex_ticket_frozen_cookies_test.go`：连续模拟已有包、失败续期、正式注入、再次成功采集与整包替换。失败阶段覆盖捕获312、复验312、两阶段错误模型、两阶段截断及残缺Cookie对；验证错误响应和成功复验响应的Cookie均不会写入旧包或后续新包。正式业务响应另覆盖目标长度、312、无state、失败312、错误模型，确认响应Cookie从不进入缓存，并保留按回执失效与其他账号隔离。另以Go `testing/synctest` 虚拟时钟验证required/optional模式的原包在复验期间过期，无真实等待或模型请求。

最终使用上文同一Go定向回归命令，缓存和临时目录为 `/dev/shm/codex-v048-check`，JSON结果为 `/tmp/codex-v048-regression-final.jsonl`：

| 测试包 | 顶层测试通过 | 子用例通过 | 结果 |
| --- | ---: | ---: | --- |
| internal/service | 110 | 212 | PASS |
| internal/repository | 26 | 27 | PASS |
| internal/handler/admin | 7 | 12 | PASS |
| 合计 | 143 | 251 | 无失败或跳过 |

本轮新增3个顶层测试、14个子用例。首次编译期间曾撤回服务层重复时钟补偿，运行结果仍含先前读取到的不符合缓存契约的延迟模拟测试；已移除该错误模拟并对最终源码重新执行完整定向回归，上表只计最终执行结果。`gofmt`、`git diff --check`通过。本轮未修改前端，前端69项验证属于上一提交的记录，不算本轮重跑。

## 验证边界

全部模型响应为合成数据，Redis为本地miniredis，数据库配置测试使用模拟仓库/SQL mock。没有真实账号探测、生产发布、完整全仓库测试或生产前端打包。上述结果验证本地协议处理和隔离规则，不证明上游一定采纳292、Cookie绑定有效期固定为240秒、目标模型能力或订阅权益发生变化。
