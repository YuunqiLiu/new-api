# Dashboard 前端发布与回退

**当前状态：2026-10-18 已上线并通过浏览器验证。** 已完成 32 项回归、最终变更涉及的 12 项复验、完整类型检查、13 文件 lint/格式检查及生产构建；已完成独立 Caddy 路由、资源、回退及 SSE 跨 reload 验证。服务器构建触及内存上限并已退出，后续仅传输本地产物，不在生产主机再构建。

本次方案将修复后的前端交给 Caddy 提供，API 继续由现有 `new-api-routing`、`127.0.0.1:3019` 处理。仅覆盖已知页面的 GET/HEAD 和明确的公共资源。登录域名、Cookie、OAuth 浏览器回调地址及 `/api` 同源请求保持不变。未知路径及其他方法继续进入原后端。

## 发布资料

- 构建输出：`/home/deploy/worktrees/new-api-dashboard-20261018/web/dist`
- 不可覆盖的发布目录：`/srv/new-api-frontend/releases/dashboard-20261018`
- 准备入口：`prepare_frontend_release.py`，不 reload 生产。
- 应用入口：`apply_frontend_release.py --state <prepare 输出的 state.json>`。
- 准备目录位于工作树的 `deployment/frontend-prepared-<id>/`。保留 `state.json`、原始字节备份 `source.Caddyfile`、`candidates/` 内的 candidate/rollback/original、文件哈希清单及校验日志。它们不是公开静态资源。
- 每次应用记录在准备目录的 `apply-<id>/result.json` 和受保护的日志中。不能仅凭脚本退出或文件存在判断上线成功，须检查结果及公网验证。

准备阶段校验 HTML 引用、完整文件哈希、Caddy 用户读取权限及两份配置。应用阶段校验原配置哈希和 owner/mode，原子替换后只执行 `systemctl reload caddy`，再比对公网 HTML、入口资源、API 版本、后端容器 ID/PID/StartedAt 和二进制哈希。

**本次前端操作禁止重启、停止或强制终止后端；也不通过重启 Caddy 完成切换。** 若校验失败，保留证据并处理具体原因，不以重启服务、清除用户浏览器存储或修改账务配置代替修复。

## 分阶段回退

应用后验证失败，脚本会尝试装载已校验的 `Caddyfile.frontend-rollback`：新页面请求恢复旧后端内嵌 HTML，新发布资源仍由 Caddy 提供，旧资源仍可回原后端获取。这样已打开新页面的用户仍可加载后续按需下载的 chunk。

不要立即删除新发布目录，也不要只恢复原 Caddyfile 后就删除新资源。浏览器可能继续运行新版本页面；失去其哈希资源会导致跳转时白屏。需要同时保留新旧两代资源，直到已打开的相关会话刷新或已按明确的保留策略退出。后续替换后端二进制时，也要考虑它目前承担的旧资源回退，不能假定那些资源会永久存在。

## 使用已保存资料安全恢复

以下是人工复核流程，不增加另一套执行脚本；现有 `frontend_release.py` 提供 `sha`、`atomic_install` 等辅助函数。

1. 确认准备目录和发布目录属于这次变更。重新核对目标配置的 SHA256 与 `state.json`、发布文件哈希、后端身份基线，以及目标配置的 `caddy validate` 结果。
2. 持有同一把部署锁 `/run/lock/new-api-frontend-deploy.lock`，在原子替换前再次读取实际 Caddyfile。此锁属于协作式锁，仍须防止不遵守该锁的并行人工编辑。
3. 按明确的预期状态做 CAS；不使用强制复制覆盖未知内容：

   | 恢复动作 | 当前配置必须匹配 | 目标配置 |
   |---|---|---|
   | 新页面回退 | `candidate_sha256` | `Caddyfile.frontend-rollback`，匹配 `rollback_sha256` |
   | 确认修复后恢复新页面 | `rollback_sha256` | 已复核的 candidate，匹配 `candidate_sha256` |
   | 最终移除 overlay | 当前已识别的 candidate 或 rollback 哈希 | 原始字节备份 `source.Caddyfile`，匹配 `original_sha256`；先解决新旧资源保留问题 |

4. 使用现有 `atomic_install(目标字节, 预期当前哈希, state)` 保留 owner/mode，原子替换并落盘，再 reload Caddy。哈希、权限或基线不符时停止，先检查差异；自动回退也不会覆盖管理员的新配置。
5. 校验公网页面哈希、所需新旧资源可用性、API 版本及后端身份不变。回退使用 `old_html_sha256`，新页面使用 `new_html_sha256`。保存结果；失败时报告准确状态，不声称已恢复成功。

`apply_frontend_release.py` 的入口仅接受仍匹配 `original_sha256` 的初次应用。已处于 candidate 或 staged rollback 时，不能反复运行该入口绕过保护；按上述已保存状态复核并恢复。

## 后续整体升级必须同时处理 overlay

**此 Caddy overlay 一旦上线会持续生效，更新 New API 容器不会自动移除它。** 如果后续仅升级后端内嵌前端，Caddy 仍可能返回本次旧页面和旧资源，造成前后端版本不一致。

后续整站升级必须将 overlay 作为同一发布计划的一部分：要么构建与目标后端匹配的新前端，发布到新的不可覆盖目录并更新页面路由清单；要么在完成资源兼容与回退安排后移除 overlay，改由目标后端提供页面。更新 API 监听端口时，也要同步审查 overlay 的回退上游。

本次页面白名单来自当前 `routeTree.gen.ts`。未来新增页面、外部前端、插件页面、不同路径前缀、外部资源地址或运行时分析脚本注入，均需重新核对；未覆盖的页面会落回后端，不能把本次白名单当作永久通配规则。构建必须保持同源 API 和根路径资源约定，并在真实浏览器检查登录、OAuth 回调、刷新、页面跳转及延迟加载。

实际发布完成：221 个产物及 13 个修复源码和 routeTree 全部哈希匹配；Caddy candidate/rollback 均校验通过。公网 HTML、入口资源、API 版本和后端身份核验通过。浏览器实测模型 29 天日汇总显示 285,496 次调用、¥44,696.83；用户统计同范围总额一致，从天切换周后起止时间完全不变。7 天/29 天接口真实范围已核验。准备状态保存在 `deployment/frontend-prepared-a94126025086/state.json`。
