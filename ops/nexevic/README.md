# NexEvic 运维源码与发布记录

这里只保存无凭据的源码、参考配置和证据，不是自动部署入口。当前源码位于 `/home/deploy/projects/new-api`，生产入口是仓库外的 `/srv/new-api/compose.yaml`。

## 内容

- `PRODUCTION-LAYOUT.md`、`layout-migration-20261025.json`：固定生产目录、当前容器和迁移结果。
- `production_layout.py`、`relocation_canary.py`：显式分阶段迁移和短期 canary；不是自动升级器。
- `cleanup_rebuildable_cache.py`：仅清理固定白名单中的重建缓存，默认 dry-run。
- `observed-production.json`：2026-10-25 初次纳管时只读核对的历史产物身份。
- `adoption-validation-20261025.json`：本次重新执行的源码核对与测试，和历史证据分开记录。
- `docker-compose.routing.example.yml`：单个 routing 服务的参考描述；不是当前完整生产 Compose，不包含数据库/Redis/SSO 配置。
- `history/20261018/backend/`：原 routing 发布脚本、调度器源码及隔离测试代码。
- `history/20261018/frontend/`：原 dashboard overlay 发布工具和来源记录。

`history/` 中脚本按原逻辑归档（沿用仓库 LF 换行规则），**禁止直接当作通用升级脚本重跑**：其中有当时的绝对路径、旧容器布局、固定渠道 ID 和真实管理写操作。重新运行可能覆盖路由/配置、改变渠道或 reload 入口。原发布说明是当时的记录，不是对当前系统状态的重新验收。本次没有执行这些脚本。

调度器归档 `backend/scheduler/scheduler.py` 与现行 `/srv/new-api/bin/channel_scheduler.py` 的 SHA256 相同：`2da26fd8d850080a046eefc8cb4ce4910003ca1934a28a54e7851e87ef32bc20`。它使用外部 `API_TOKEN`，不包含实际 Token；会执行渠道恢复和 OIDC 用户名协调，不要在测试中连接生产 API。

## 凭据与生成物

实际 Compose、环境文件、渠道 Key、测试 Token、Caddy 原始备份、数据库、账务失败记录、私有生成状态、日志、镜像和编译产物不入库。`private/`、`runtime/` 和常见敏感生成物已加入 `.gitignore`，但 ignore 不能代替提交前人工/自动检查。

参考 Compose 通过 `NEXEVIC_RUNTIME_ENV_FILE` 指向仓库外的 root-only 环境文件。`SQL_DSN`、`REDIS_CONN_STRING`、`SESSION_SECRET`、`CRYPTO_SECRET` 等只能放在那里；不得换成示例弱密码，也不得在调试日志中展开。原环境中的会话/加密密钥必须保留，不能因重建而随机替换。

示例不会创建 handover marker。后台任务启用依赖 `/handover/start`，应按单主交接流程人工确认，不能把 marker 自动创建放进常规启动步骤。示例同时保持 `SKIP_DB_MIGRATION=true`，数据库变更必须另行评估。

## 验证记录说明

归档中的 `unit-tests.log`、`integration-results.json`、`shutdown-results.json`、`caddy-reload-results.json` 和 dashboard JSON 是 **2026-10-18 的既有证据**，不是本次重跑全部集成/生产测试。源码纳管不应触发生产请求、数据库写入、渠道探测或重启。

后续验证可在独立环境运行 Go 测试：

```sh
go test -p 1 ./common ./model ./service ./middleware ./controller ./relay/channel ./relay/channel/advancedcustom
```

前端使用 `web/bun.lock` 和 `web/package.json`，按 frontend `AGENTS.md` 执行测试、类型检查、lint 和构建。隔离集成脚本只允许专用测试数据库/应用，需要另行生成短期测试凭据；不提供生产凭据作为替代。

源码首次纳管检查（目录迁移前）：7 个 Go 包通过；dashboard 6 文件共 32 项测试、完整 typecheck 和变更的 13 文件 lint 通过。全站 lint 返回 354 个错误、76 个警告，位于既有源码中；未为纳管而修改这些生产代码。当时未重新构建或部署产物，也未重跑需要临时 API Token/数据库的集成场景。

后续固定目录迁移：5 项离线安全检查通过；同版本候选和公网共完成 12 次 Chat/Responses SSE/function call 往返请求，短期测试 Token 已删除。数据库/Redis 未重启、没有升级或重建软件；详细范围和限制见 `PRODUCTION-LAYOUT.md` 与 `layout-migration-20261025.json`。
