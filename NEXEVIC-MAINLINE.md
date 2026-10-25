# NexEvic New API 维护主线

2026-10-25 将**已经在生产使用的 routing 后端和 dashboard 前端**纳入同一条维护主线。本次只整理 Git，没有部署、重建生产产物或重启服务。

## 唯一维护入口

- 工作树：`/home/deploy/worktrees/new-api-mainline`
- 主线分支：`nexevic/main`
- 既有远程：`origin = https://github.com/YuunqiLiu/new-api.git`
- 上游参考：`upstream = https://github.com/QuantumNous/new-api.git`
- 本地生产基线标签：`nexevic-production-20261018`

保留完整上游和原定制历史；没有覆盖 `main`、`feat/channel-plan-quota` 或旧 worktree，也没有删除旧容器/镜像。今后的功能和修复应从 `nexevic/main` 建分支，不再把独立 snapshot 仓库或部署目录当作源码主线。

**远程状态：本机暂缺 GitHub 写入凭据，推送 dry-run 报 `could not read Username`。当前提交和标签仅保存在本机，不代表已有异机备份。** 配置经授权的仓库写入凭据后，可执行 `git push -u origin nexevic/main`；不要强推或覆盖远程 `main`。本次未更改 GitHub 默认分支。

另在 `/home/deploy/backups/new-api-mainline-20261025/source.bundle` 保存独立 Git bundle，可用于恢复完整主线历史和本地基线标签；它仍是本机副本，不能代替异机备份。

**暂不推送基线标签。** 当前继承的 GitHub Actions 对所有 tag 触发 Release/Docker 发布；先审查发布工作流和镜像目标，再决定是否推送标签，不能把记录基线变成未经审批的发布。

## 源码溯源

| 层 | 原始来源 | 主线提交 |
|---|---|---|
| 完整基础历史 | 原仓库 `b5bedec2d199eee1668f5dbce02f8e1f92ebacb7` | 保留原提交及祖先 |
| 已部署百度 Responses 桥接 | 独立快照 `73da46d` 中 3 个文件 | `c3ec55c5c` |
| 定时路由、受保护恢复、记账排空 | 独立快照 `73da46d..33d6479`，45 个文件 | `b993dd4ab` |
| 已上线 dashboard overlay | dashboard 工作树中 4 个修改文件、9 个新增源码/测试文件 | `94600b40c` |
| 发布/验证脚本和来源记录 | `ops/nexevic/` | 随本次主线管理 |

`ops/nexevic/observed-production.json` 记录当前产物身份。Git 提交日期是**纳管日期**，不是伪造的历史部署日期。后端的版本字符串仍是 `b5bedec2d-routing-20261018`，不会因为纳入 Git 自动改变。

## 生产对应关系与边界

- 活跃后端为 `new-api-routing`，监听 `127.0.0.1:3019`；旧 `new-api` / `new-api-baidu` 已停止，不要为版本整理重新启动它们。
- 网页由 `/srv/new-api-frontend/releases/dashboard-20261018` 的静态 overlay 提供；管理/API 请求仍转到 routing 后端。只更新容器不会自动更新 overlay。
- 实际私有 Compose 在 `/home/deploy/deploy_new_api/docker-compose.yml`。其中有凭据，不要复制到 Git；本目录中的既有根级 Compose 来自历史源码，**不代表当前生产配置**。
- 渠道、模型映射、价格、配额和用户配置仍保存在数据库，不能仅靠 checkout 恢复；本次没有修改或导出这些敏感数据。
- 现有部署目录、数据库、Redis、日志和密钥保留原位置；此工作树不承载运行时数据。

## 后续变更流程

1. 从 `nexevic/main` 建功能分支，保留 New API / QuantumNous 许可证及署名。
2. 在独立构建机按锁文件安装依赖、运行测试和构建；**不要在内存紧张的 NexGateway 构建**。
3. 将源码提交、依赖锁、二进制/镜像/前端哈希和测试结果一起记录；前后端必须对应同一变更计划。
4. 测试通过后单独申请生产发布授权，安排流式请求排空、后台任务交接、前端静态资源兼容和回滚；Git 提交或 push 本身不构成发布授权。
5. 上线验证通过后才移动主线/新增发布记录。私有配置通过独立凭据渠道维护，不进入 Git。

## 已知问题不在本次修复范围

此前观测到部分 GLM Flash 请求的 `tool_choice`/`tools` 上游兼容错误、流式客户端提前断开及系统盘空间紧张。本次保存真实生产源码，不把“版本纳管完成”当成这些运行问题已经修复。
