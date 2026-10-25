# 固定生产目录与受控切换

2026-10-25 22:37（北京时间）已切换，22:40 已确认旧实例正常排空并完成后台交接。生产目录为 `/srv/new-api`；独立源码仓库为 `/home/deploy/projects/new-api`，分支 `nexevic/main`。源码仓库 `.git` 为独立目录，不再依赖旧 worktree。生产镜像未升级：仍为 `localhost/new-api:nexevic-routing-20261018`，启动前验证其 image ID。

## 目录和入口

```text
/srv/new-api/
  compose.yaml             root-only，生产部署入口（JSON 语法的合法 YAML）
  config/                  root-only 的 app/postgres/redis/scheduler.env
  bin/channel_scheduler.py 已发布的调度脚本
  runtime/data/            应用文件
  runtime/logs/            应用日志，含原实例排空后的日志副本
  runtime/accounting-failures/  记账异常留存，不自动重放
  runtime/handover/start   仅在旧主实例完全排空后创建
  runtime/scheduler-state/ deploy 可写的调度状态
  frontend/releases/      不可覆盖的网页发布副本
  frontend/current        指向当前发布的相对软链接
  backups/layout-*/       私有备份、原始配置、迁移阶段证据
```

PostgreSQL 继续使用命名卷 `deploy_new_api_pg_data`，网络继续使用 `deploy_new_api_new-api-network`，二者均显式声明 external。Compose 项目名保持 `deploy_new_api`。不移动数据库物理文件、不重建数据库/Redis、不轮换密码或会话密钥。

PostgreSQL/Redis 的 Docker 创建标签仍保留旧 Compose 路径；容器启动不读取该文件，本次没有为改标签而重建它们。以后维护应显式指定新入口。Redis 当前没有独立持久化挂载，本次保持原状；如需持久化和恢复演练，应另行安排，不能在此次迁移中偷偷重建。

固定应用容器名 `new-api-production`，仅绑定 `127.0.0.1:3020`。旧 `new-api-routing` 的 3019 端口用于切换期原连接排空，不再作为正式入口。Caddy 仅修改 `api.nexevic.com` 的网页根目录和上游端口；域名、TLS、认证及其他站点规则不变。

日常仅操作应用服务：

```sh
sudo docker compose -p deploy_new_api -f /srv/new-api/compose.yaml up -d --no-deps --no-build new-api-production
```

不要使用 `down -v`，不要不经评估执行全栈 `up`、pull 或重启 PostgreSQL/Redis。根级历史 Dockerfile/Compose 不是固定生产入口。私有文件不能复制到 Git。

## 此次操作顺序

`production_layout.py` 每个动作都需显式执行，默认无自动步骤：

1. `backup`：保存原容器身份、配置、完整 Git refs 和工作区补丁、私有历史发布资料，以及 PostgreSQL custom-format 备份。`pg_restore --file=/dev/null` 完整读通归档；这不是向生产数据库执行恢复。
2. `prepare --state ...`：生成私有 Compose/env、预复制运行文件和 221 个网页产物，校验哈希、Caddy candidate 和读取权限；不复制后台启动 marker。
3. `launch --state ...`：启动同版本新应用，后台任务仍被 marker 阻止；数据库/Redis 不动，流量仍走旧实例。
4. 新端口 API 和流式 canary 通过后，`switch --state ...` 暂停调度 timer，确认当前调度已结束，CAS 更新 Caddy 并 reload。旧 SSE 保持原连接，新请求进入新实例。
5. 公网验证通过后，`drain --state ...` 禁用旧应用自动重启并发送 SIGTERM。**不使用 SIGKILL、强制 rm 或有强杀截止时间的 stop。** 若长连接仍在执行，等待并报告，不强制结束。
6. 确认旧实例退出码 0，以及 HTTP/后台任务/退款记账/批量账务四个排空阶段均有证据后，`finalize --state ...` 才创建新后台 marker、安装新调度 unit 并恢复 timer。

预检还发现原正式 Compose 遗漏了运行中的 `NEXEVIC_LEGACY_SCHEDULER_CHANNEL_IDS`。切换前已通过 `synchronize_environment` 补齐并重新验证候选容器，最终新旧运行环境仅 `NODE_NAME` 不同；不是顺手改变业务调度参数。

动作不是可反复盲跑的安装器；每阶段状态写入 root-only 结果文件，失败后先检查证据，不覆盖未知现状。完整备份和旧目录均保留。短期 canary token 使用独立 UUID 名、模型/额度限制及 15 分钟有效期，finally 删除，不打印 key 或模型正文。

## 回退边界

- 切换前：旧生产完全不动，可以停用新候选实例；不要删其账务异常证据。
- 切换后但旧实例尚未发 TERM：先把 Caddy 恢复为备份中的旧 API 根目录/3019，上游确认后恢复旧调度 timer；新实例若已有请求仍需自行排空。
- 旧实例已退出：不能直接启动另一个后台主实例。先暂停调度，关闭新后台入口并让新实例完整排空，确认账务与任务结束后，再恢复旧配置/旧实例/旧入口/旧调度配置。单纯切回端口不是完整回退。
- CAS 检测到 Caddy 或 systemd 配置被他人改动时停止；不得覆盖管理员的新内容。

迁移记录 `state.json` 保存容器 ID，可据此确认 PostgreSQL/Redis 未被重建。原始备份含敏感数据，仅 root 可读；本机备份不能代替异机灾备。

## 清理边界

成功验收后仅清理已确认未被引用的依赖缓存、历史安装包和停止的构建容器。当前镜像、兼容回滚镜像、生产挂载、数据库卷及所有恢复资料保留。旧源码/快照及更早版本至少观察 24 小时后再单独清理；本脚本不自动删除它们。

本次已清理 7 个停止的构建容器、2 个未引用的 Go 缓存卷、3 份重复依赖目录和 11 个历史安装包，释放约 10.3 GB。缓存可重新下载/生成；安装包原文件已删除，未承诺逐字节恢复这些旧包。当前镜像、兼容回滚镜像、旧源码、旧日志和恢复资料全部保留。系统盘占用由约 91% 降到 68%，`df -h` 显示可用约 14G。

浏览器补查：在 NexCompute 的隔离 Chrome 中，使用现有管理员 PAT 对正式 HTTPS 入口执行只读请求，`/api/user/self`、`/api/data`、`/api/data/users` 均返回 200；截至 23:04，两个今日统计接口均累计 337 次请求。PAT 仅经 SSH stdin 进入浏览器进程内存，未写入浏览器 profile；未创建登录会话，也未修改用户凭据或认证策略。

完整浏览器交互登录**未验收完成**：隔离 Chrome 冷加载多次超时，没有获得稳定的登录页及 Casdoor 往返；也不持有用户现有浏览器 Cookie 或交互登录凭据。不能据此认定网站故障，或声称已替用户登录成功。编译后的网页内容和会话/加密密钥均未改变，后续需完成真实登录页/SSO 检查。
