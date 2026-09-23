# 验收记录

验收日期：2026-08-22（Asia/Shanghai）

## 静态与测试

以下命令均以退出码 0 完成：

```bash
cd backend
go test ./...
go test -race ./...
go vet ./...
go build ./...

cd ../frontend
npm run typecheck
npm run build

cd ..
docker compose config --quiet
```

- 路由集成测试覆盖 viewer 写入 403、operator 放行 403、reviewer 放行成功、已决记录更新 409，以及色彩配置/放行决定两条不可变修订链。
- 非测试 Go 代码为 38 个文件、3143 行，符合提示词的 26-38 文件与 2700-3900 行范围。

## 空卷 Compose 与 API

先执行 `docker compose down -v --remove-orphans`，随后以 `KEEP_RUNNING=1 ./scripts/validate.sh` 从空命名卷启动。MySQL、Redis、backend、frontend 均通过 healthcheck。

脚本实际验证：

- `/healthz`、前端首页、session、runtime、overview 和四组实体列表正常。
- 创建设备并推进状态后，审计总数和迁移计数同步增加。
- viewer 对写接口和审计接口均得到 403。
- operator 可采集校样并提交 review，但接收校样得到 403；reviewer 接收成功。
- operator 创建 draft 决定后直接 release 得到 403；reviewer 放行成功并生成 v2。
- 决定详情返回 v2/v1 两条修订，操作者分别为 reviewer/operator，请求 ID 分别为 `release-review-smoke`、`release-create-smoke`。

## 内置 Browser

只使用 Codex 内置 Browser 验收，没有调用外部 Chrome。

| 页面/场景 | 实测结果 |
|---|---|
| `/presses` | 列表、搜索区、新增确认框和 `ready -> setup` 状态推进正常 |
| `/runs` | 三条批次可见，`RunStateBadge` 显示待装版/印刷中/校样中 |
| `/proofs` | `ColorTable` 展示四条读数；详情复用同一组件并显示 v3 已接收校样 |
| `/release` | 放行依据读数、相关批次状态和决定详情正常；详情显示 v2/v1 完整版本链 |
| `/audit` | 审计列表显示操作者、迁移前后状态、实体和请求 ID |
| RBAC | viewer 无新增/推进/审计入口且直达 `/audit` 被重定向；operator 隐藏复核动作；reviewer 显示放行与审计入口 |
| 响应式 | 390x844 视口下导航、指标、工具栏和滚动表格无页面级横向溢出，`documentWidth == viewport == 390` |
| 控制台 | 全流程完成后 error/warning 日志为 0 |

最终交付前执行：

```bash
docker compose down -v --remove-orphans
```

## 放行依据关联与失效拦截（2026-09-23 追加）

需求：放行决定必须关联一条印刷批次和一份已接收校样，批次处于校样阶段且读数未超允许范围才能生成草稿；放行时重新读取两条记录；批次换版本、校样被拒绝或出现更新读数时标记依据失效并拦住放行，且复核与校样变化不能各成功一半；页面展示关联编号与失效原因。

在本机 SQLite 模式（无 Docker 环境）实测：

- `go test ./...`、`go vet ./...`、`go build ./...`、前端 `npm run typecheck`、`npm run build` 全部通过。
- 路由集成测试（`backend/internal/router/router_test.go`）覆盖：草稿必须绑定 proofing 批次 + accepted 校样（缺批次返回 422）；reviewer 放行成功后决定 v2、批次在同一事务内进入 released；校样被拒绝后放行返回 `409 basis_invalid`，决定持久化为依据失效（version +1、版本链含“依据失效”留痕、`basisValid=false`），批次仍停留在 proofing。
- 手工 API 冒烟（SQLite 实跑）验证四类拦截信息：
  - 读数 2.5 超过批次允许 2.0：草稿生成 422「校样读数 … 超出批次允许范围」。
  - 草稿后校样读数更新：放行 409「校样出现更新读数，放行依据与当前读数不一致」。
  - 草稿后批次配置 PUT 换版本：放行 409「批次配置已换版本，放行依据不再对应原批次配置」。
  - 校样被拒绝：放行 409「关联校样已被拒绝，不能作为放行依据」，批次未被部分放行。
- 原子性由 `ReleaseWithBasis` 保证：决定、批次、修订与审计在同一数据库事务内完成；MySQL/PostgreSQL 使用 `FOR UPDATE` 行锁，SQLite 以进程内互斥串行化等价处理。
- `/release` 页面展示关联批次/校样编号、依据快照版本与读数、依据有效/失效徽标及失效原因，并在详情版本链中保留失效留痕。

