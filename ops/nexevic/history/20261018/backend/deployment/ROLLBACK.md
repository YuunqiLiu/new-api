# 兼容回滚操作说明

本文是操作手册，编写时未执行回滚。适用基线为 2026-10-18 已完成的上线；执行前重新核对现场状态。

## 当前状态和回滚边界

| 项目 | 当前值 |
| --- | --- |
| 入口后端 | `127.0.0.1:3019`，容器 `new-api-routing` |
| 当前正式 Compose | `/home/deploy/deploy_new_api/docker-compose.yml` |
| 当前后台所有者 | `new-api-routing`，最终角色 `master` |
| 当前后台启动标记 | `/home/deploy/deploy_new_api/routing-20261018/handover/start` |
| scheduler 环境文件 | `/home/deploy/new_api_channel_scheduler/scheduler.env` |
| scheduler 当前目标 | `API_BASE=http://127.0.0.1:3019` |
| scheduler 状态文件 | `/var/lib/new-api-channel-scheduler/state.json` |
| 原容器 | `new-api` 已 `exited 0`，`restart=no`；不得重新启动 |
| 兼容回滚镜像 | `localhost/new-api:nexevic-routing-compatible-rollback-20261018` |
| 兼容镜像 ID | `sha256:b0e8cc9257d1545cfc63bfb708b7b2e2a56bcdb2ef1edbd1fef5f64f36ba98c6` |
| 兼容二进制版本 | `b5bedec2d-routing-compatible-rollback` |
| 兼容二进制 SHA256 | `dcbf13f89538efba5c0af3b34acd6ac9534c7cc401af46225d84575ff362e4ee` |

兼容版本保留高峰硬门控、恢复 CAS、缓存构建保护、流式错误保护和 HTTP/退款/计费排空保护。它恢复旧的渠道选择算法；旧算法的权重和重试优先级语义可能不同，不能继续承诺新算法的 `4:3:3` 请求分流比例。权重始终不代表 Token 消耗比例。

必须保留当前数据库、渠道状态及 `status_version`、用户余额、消费日志和 scheduler 状态。不得恢复上线前数据库快照，不得全量覆盖渠道对象，不得回到原始无保护镜像。保留 `status_version` 列。新买的独立套餐、上线后的额度消耗和人工停用都属于需要保留的当前状态。

## A. 只有配置问题时

优先仅修正明确有问题的字段，避免更换运行实例。

1. 保存当前相关字段及其 `status_version`、操作者和时间，再与上线配置变更记录比较。备份用于比对，不用于全量重放。
2. 暂停 scheduler timer 并等待已启动的 oneshot 自然结束，方法见 B1；约定此短窗口内不并行编辑相关渠道。
3. 写入前重新读取渠道并核对版本和目标字段。与记录不一致即停止，重新判断是否有人工操作或额度变化。只提交需要修正的字段，例如 `PUT /api/channel/` 的 `id/priority/weight`；不要回传 `status`、`status_version`、Key、`other_info`、用量或整份旧对象。
4. **通用渠道 PUT 没有配置 CAS 语义**：携带 `status_version` 不会建立前置条件，该字段会被忽略。要求严格原子性时应使用经过审查的条件更新路径，按 `id + 当前版本` 匹配、递增版本，并在同一事务内维护 abilities；不要临时执行只改 channels 的 SQL。使用现有管理 API 时，必须保持变更窗口互斥，写后立即核对版本和字段，遇到并发变化停止后续操作。
5. 涉及模型、分组、优先级、权重时，用现有管理 API 更新以维护 abilities 和缓存。选项只回退确认有问题的单个键；不要整包恢复 options。
6. 做最小验证后恢复原 scheduler timer。渠道的人工停用、系统额度禁用和重置时间不因配置回退而自动恢复。

## B. 更换为兼容回滚镜像

### B1. 保存现场并暂停新一轮恢复扫描

先只读核对当前容器、镜像和健康状态；检查 3020 未被占用。备份当前 Compose、Caddyfile、scheduler 环境文件和状态文件到带时间的受保护目录，文件权限 `0600`、目录 `0700`，不要输出其中的凭据。状态文件应在 oneshot 结束后备份。

```sh
docker inspect new-api-routing --format 'state={{.State.Status}} exit={{.State.ExitCode}} image={{.Image}}'
docker inspect new-api --format 'state={{.State.Status}} exit={{.State.ExitCode}} restart={{.HostConfig.RestartPolicy.Name}}'
docker image inspect localhost/new-api:nexevic-routing-compatible-rollback-20261018 --format '{{.Id}}'
curl --fail --silent http://127.0.0.1:3019/api/status
ss -ltn '( sport = :3020 )'
systemctl stop new-api-channel-scheduler.timer
systemctl show new-api-channel-scheduler.service --property=ActiveState --property=SubState --property=Result
```

停止 timer 不会取消已经启动的 oneshot。若 service 仍为 `activating/active`，等待它自然结束；不要 stop/kill 该 service。若结束状态为失败，先核对失败原因和状态文件完整性，再继续。等待期间保留 3019 入口和后台所有权。

### B2. 准备独立的 3020 最终 master

从**当前正式 Compose**复制 `new-api-routing` 服务生成一个受保护的待审文件，例如 `/home/deploy/deploy_new_api/rollback-20261018/compose-staged.json`，保留当前数据库、Redis、网络和依赖定义。同时保留原 `new-api-routing` 服务；新增服务按下表修改，不执行整项目重建。

| 字段 | 新服务值 |
| --- | --- |
| service / container_name | `new-api-routing-rollback` |
| image | 上表预存兼容回滚镜像 |
| published port | `127.0.0.1:3020:3000` |
| NODE_TYPE | `master`，启动时即确定，切流后不重建晋升 |
| NODE_NAME | `new-api-routing-rollback-20261018` |
| SKIP_DB_MIGRATION | `true` |
| CHANNEL_TEST_ENABLED | `false`，移除 `CHANNEL_TEST_FREQUENCY` |
| NEXEVIC_GLM_PEAK_CHANNEL_IDS | `6,7,14,18` |
| NEXEVIC_LEGACY_SCHEDULER_CHANNEL_IDS | 不设置；一次性旧记录接管已经完成 |
| BACKGROUND_TASKS_START_FILE | `/handover/start` |
| /handover 挂载源 | `/home/deploy/deploy_new_api/routing-rollback-20261018/handover` |
| /app/logs 挂载源 | `/home/deploy/deploy_new_api/routing-rollback-20261018/logs` |
| ACCOUNTING_FAILURE_DIR | `/accounting-failures` |
| /accounting-failures 挂载源 | `/home/deploy/deploy_new_api/routing-rollback-20261018/accounting-failures` |
| SHUTDOWN_TIMEOUT_SECONDS | `600`，这是重复告警周期，不是强杀许可 |
| stop_grace_period | 保留 `24h`，但本手册排空不用有限超时的 `docker stop` |

新目录权限 `0700`。**启动前确认新的 `handover/start` 不存在**；不要把 3019 已存在的 marker 挂给新实例。若该路径曾用于别的切换，换一个新的独立目录并同步调整 Compose，不要盲删已有 marker。

核对文件差异后，只启动新增服务：

```sh
docker compose -p deploy_new_api -f /home/deploy/deploy_new_api/rollback-20261018/compose-staged.json config --quiet
docker compose -p deploy_new_api -f /home/deploy/deploy_new_api/rollback-20261018/compose-staged.json up -d --no-deps new-api-routing-rollback
curl --fail --silent http://127.0.0.1:3020/api/status
```

应返回版本 `b5bedec2d-routing-compatible-rollback`，日志应表明后台任务等待 handover marker。不要创建 marker；3019 仍是后台所有者。新节点的本地缓存、请求计费和实例心跳可运行；这不代表已取得共享维护任务所有权。

### B3. 对 3020 做最小 canary

复用已审查的有界 canary：套餐渠道、短输出、短时限的专用临时 Token，普通 SSE 和强制工具调用 SSE 各一次，检查完整结束、正确工具参数和单次计费；结束删除该 Token。不测试四条 GLM 官方或 DeepSeek 官方按量兜底。

现有 `/home/deploy/worktrees/new-api-routing-20261018/deployment/canary.py` 的本地地址白名单仅允许 **3019**，不能直接改参数假称测试了 3020。先复制为此次回滚专用脚本，仅把 `validate_base` 中本地端口白名单从 3019 改为 3020，审查这一处差异；保留模型/渠道白名单、临时 Token 清理和禁止重定向逻辑。

```sh
python3 /home/deploy/deploy_new_api/rollback-20261018/canary-rollback.py --stage pre --with-tools --api-base http://127.0.0.1:3020
# 确认上一步只读计划中的套餐渠道正确后，才执行该有界 canary：
python3 /home/deploy/deploy_new_api/rollback-20261018/canary-rollback.py --stage pre --with-tools --api-base http://127.0.0.1:3020 --execute
```

任一 canary 失败就保持 3019 入口、新 marker 缺省。不要用自动循环重试扩大消耗，也不要为了让测试通过恢复人工停用渠道。

### B4. 验证 Caddy 配置并切新请求

复制当前 `/etc/caddy/Caddyfile` 为同目录待审文件，仅把 `api.nexevic.com` 的目标从 `127.0.0.1:3019` 改为 `127.0.0.1:3020`。保留其他站点、压缩、TLS 和现有配置。确认精确命中一个目标，保存当前 Caddyfile；不要运行旧的首次上线 `cutover.py`，它针对 3000→3019。

```sh
caddy validate --config /etc/caddy/Caddyfile.rollback-prepared --adapter caddyfile
```

校验及 diff 审查通过后，保留原属主/权限，将待审文件原子替换为 `/etc/caddy/Caddyfile`，再执行：

```sh
systemctl reload caddy
curl --fail --silent https://api.nexevic.com/api/status
```

公开入口应返回兼容回滚版本。对公开入口做同样有界的复核，确认临时 Token 已删除。现有 HTTP/SSE 请求继续由 3019 完成；已做的独立代理验收验证了被 hold 的 SSE 请求跨 reload 后完整结束且不重放，这不代表任意客户端均绝对不会断线，也不覆盖 WebSocket 实时语音。

如果入口复核失败且 3019 尚未收到终止信号，可把该站点 upstream 字段改回 3019、验证并 reload。不要恢复旧数据库或所有渠道配置。**一旦 3019 已开始排空，就不能再把新请求送回它**；先修复兼容实例或准备另一个保留保护的新实例。

### B5. 排空 3019，并核验账务成功

确保 Caddy、内部调用方和 scheduler 都不再把新请求送到 3019。此时新 marker 仍缺省。禁用 3019 的自动重启，然后只发送 TERM：

```sh
docker update --restart=no new-api-routing
docker kill --signal=TERM new-api-routing
docker inspect new-api-routing --format 'running={{.State.Running}} status={{.State.Status}} exit={{.State.ExitCode}}'
```

这里 `--signal=TERM` 是应用的优雅排空入口，不能改为 SIGKILL。应用依次等待 HTTP、已开始的维护任务、异步退款/计费任务、批量计费写入。`600` 秒到期只记录暂停告警并继续等待；不要据此运行强杀、带强杀截止的 stop、`compose down` 或删除容器。

核对该次退出的日志阶段：`HTTP requests drained`、`maintenance tasks drained`、`accounting tasks drained`、`batch accounting drained`，随后正常 `server exited`。必须同时确认容器不再运行、退出码为 `0`，没有待核对的账务写入/退款失败。连接数为零、短时间没有新日志或仪表盘保存成功都不能替代这些证据。

若出现 `reconciliation required`、`unconfirmed database writes` 或账务失败记录，保留进程、账务记录和数据库现状，先核对实际提交结果；不要盲目重放增减额度。此时继续保留 3020 HTTP 服务，但不得启动新后台所有者。不要仅因服务看起来安静就创建新 marker。

### B6. 转交后台、scheduler 和正式 Compose

只有 B5 全部满足后执行此阶段。

1. 从经过验证的 staged Compose 生成最终文件：保留数据库/Redis等定义及 `new-api-routing-rollback`，移除 `new-api-routing` 与原始 `new-api` 的服务定义；其已退出容器留存且 `restart=no`。验证最终文件后，备份当前正式 Compose，将最终文件原子替换到 `/home/deploy/deploy_new_api/docker-compose.yml`，保留受保护权限。不要通过一次全项目 `up` 或 `down`完成交接。
2. 核对新实例正在运行、版本正确、新 marker 仍缺省，原两个实例都已退出且不会自动重启。
3. 在新的 host 目录创建 `/home/deploy/deploy_new_api/routing-rollback-20261018/handover/start`，权限 `0600`，记录交接时间。兼容实例无需重启即可开始维护任务。核对仅此实例获得后台所有权。**删旧 marker 不能停止已经启动的后台循环**；本流程靠 3019 实际排空退出完成交接。
4. 仅修改 `/home/deploy/new_api_channel_scheduler/scheduler.env` 的 `API_BASE=http://127.0.0.1:3020`，保留 API 凭据、时段门控配置及其他现有设置，保持原属主和 `0600`。不要覆盖 scheduler.py、状态文件或锁文件，不要重新打开一次性 legacy adoption。
5. 已无旧 oneshot 时，启动一次服务验证，再恢复 timer：

```sh
systemctl start new-api-channel-scheduler.service
systemctl show new-api-channel-scheduler.service --property=ActiveState --property=SubState --property=Result
# 确认本次自然结束且 Result=success 后：
systemctl start new-api-channel-scheduler.timer
systemctl is-active new-api-channel-scheduler.timer
docker compose -p deploy_new_api -f /home/deploy/deploy_new_api/docker-compose.yml config --quiet
```

scheduler 继续使用当前 `state.json` 中的版本和重置时间，仅处理系统禁用渠道。确认人工停用没有被恢复，未知重置仍按至少 15 分钟探测，GLM 在上海时间 13:30–18:00 不探测。不要为了验证而临时批量启用渠道。

## 完成记录

记录镜像 ID、公开入口版本、canary 结果及临时 Token 清理结果、Caddy 目标端口、旧候选的完整排空及 exit 0 证据、新 marker 的交接时间、scheduler 的目标/状态文件路径与首次执行结果、正式 Compose 的校验结果。

兼容镜像构建记录位于 `/home/deploy/worktrees/new-api-routing-rollback-20261018/artifacts/`；对应测试日志位于该目录旁的 `validation/rollback-tests.log`。构建时六个包测试通过，门控/CAS/drain 关键源码与主候选一致。保留这些记录以及现场备份；备份不作为恢复旧余额和旧渠道状态的依据。
