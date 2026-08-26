# 安全加固与性能优化技术文档

- **日期**：2026-08-23
- **范围**：全库审查（安全 / 效率 / 消耗三大类），共 20 项修复，分三批实施
- **验证**：全量单测绿（含 7 个新增回归测试）、`go vet` 干净、离线评测 39/39（100%）、端到端冒烟通过
- **变更文件**：24 个（生产代码 18、配置 2、前端 1、README 1、新增测试 2、新增聚合层 1）

---

## 一、背景与审查方法

对图书馆 Agent 系统（Go 后端 + 单文件前端 + LLM 工具调用编排）做了三方面静态审查：

1. **安全**：认证/授权完整性（重点比对 REST 路径与 Agent 工具路径的防护差异）、XSS 注入面、传输与服务器超时、限流可绕过性；
2. **效率**：逐端点追踪 SQL 查询次数（N+1 模式）、索引覆盖、连接池配置、静态资源 I/O；
3. **消耗**：LLM 请求参数（max_tokens）、上下文构造、token 统计的正确性（并发安全）。

审查结论：项目基础较好（SQL 全参数化、借还 CAS 事务、意图预过滤、上下文压缩、聊天限流均已存在），但存在一个高危授权缺口、一处存储型 XSS、多处放大式 N+1 查询和一个数据竞争。

---

## 二、安全加固

### 2.1 【高危】Agent 工具层越权（IDOR）

**现象**：任何登录用户通过 `POST /api/chat` 即可以他人身份查询/操作。

**根因**（两处叠加）：

1. `internal/agent/loop.go` 的 `executeTool` 只在 LLM **未传** `patron_id` 时注入会话读者身份。LLM 被 prompt injection 或用户诱导显式传他人 ID 时，注入被跳过，工具按伪造身份执行；
2. `internal/agent/tools.go` 的 `return_book` / `renew_loan` 仅凭 `loan_id` 直接调用 `Service.Return/Renew`，无归属校验——而 REST 路径（`api.go` 的 `handleReturn/handleRenew`）一直有 `requirePatronScope` 保护，Agent 路径是防护盲区。

**修复**：

| 位置 | 修改 |
|---|---|
| `loop.go` `executeTool` | patron_id 从"缺失才注入"改为**声明即覆盖**：工具 schema 声明了 `patron_id` 属性时，无条件以会话读者覆盖参数值，LLM 提供的值一律忽略 |
| `tools.go` `return_book`/`renew_loan` | schema 补声明 `patron_id`（required），handler 执行前经 `Service.LoanOwner` 校验 `loan.PatronID == 会话读者`，不匹配返回"无权操作他人借阅记录" |
| `tools.go` `recommend_books` | 原实现 handler 读取 `patron_id` 但 schema 未声明（不会被注入），补声明 |

受此覆盖保护的工具（共 10 个）：`get_my_loans`、`recommend_books`、`return_book`、`renew_loan`、`get_my_fines`、`place_hold`、`reserve_seat`、`get_my_seat_reservations`、`cancel_seat_reservation`、`gate_scan`。

**回归测试**（`internal/agent/authz_test.go`，5 个用例）：

- 伪造他人 `patron_id` 查借阅 → 返回本人记录（覆盖生效）；
- 归还/续借他人 loan → 拒绝且书的状态未被改动；
- 本人操作不受影响；
- 防回归清单：所有消费 `patron_id` 的工具必须在 schema 声明该属性，否则不会被注入。

### 2.2 前端存储型 XSS

**根因**：`web/index.html` 三处 `innerHTML` 拼接未转义，且数据源可控：

| 位置 | 数据源 |
|---|---|
| 738-739（管理面板读者表） | 注册时自由输入的 name/username/phone |
| 759（热门图书榜） | 书名/作者——**种子数据来自外部 Open Library/Gutendex API** |
| 791（阅读报告条形图） | 书目作者/主题名 |

**修复**：三处统一改用已有的 `escapeHtml()`（函数声明提升，前向可用）；顺带给 `escapeHtml` 补引号转义（`"`/`'`），因为既有代码已将其用于 HTML 属性上下文（`data-title="..."`）。

### 2.3 HTTP 服务器超时（slowloris 防护）

**根因**：`cmd/server/main.go` 的 `http.Server` 无任何超时，公网直连暴露时慢速请求可耗尽连接。

**修复**：`ReadHeaderTimeout: 10s`、`ReadTimeout: 30s`、`IdleTimeout: 120s`。**不设 `WriteTimeout`**——SSE 长连接的写阶段不能被全局超时切断（注释已说明）。

### 2.4 /api/metrics 收权

**根因**：metrics 端点在 `isPublicPath` 白名单中，匿名可读取 token 花费、调用量、工具名、延迟。

**修复**：移出白名单，改为需登录；`/api/health` 保持公开供监控探针。冒烟验证：匿名 401、带 token 200。

### 2.5 请求体与参数上限

| 项 | 修复 |
|---|---|
| 请求体 | `decodeBody` 加 `http.MaxBytesReader`（1MB），防超大 payload 打满内存或直达 LLM |
| 聊天消息 | `maxMessageRunes = 8000`，超长返回 400（冒烟验证 9000 字 → 400） |
| 列表 limit | `maxListLimit = 100`，应用于 `/api/books`、`/api/books/hot`、`/api/books/new`、`/api/recommend` |

### 2.6 XFF 解析与登录 per-IP 限流

**根因**：`clientIP` 取 `X-Forwarded-For` **第一个**元素——该元素完全由客户端伪造；nginx（`$proxy_add_x_forwarded_for`）在链尾追加真实来源。原实现导致注册限流（5/h/IP）可伪造头绕过、审计日志 IP 可被污染。另外登录仅有 per-username 锁定（5 次失败锁 15 分钟），存在定向锁号 DoS 与分布式撞库空间。

**修复**：

- `clientIP` 改取 XFF **最后一个**元素（代理追加的真实直连 IP）；
- `handleLogin` 增加 per-IP 尝试限流：20 次 / 15 分钟（复用 `Auth.CheckRate`），与按用户名锁定互补。

> 前提说明：该解析在"服务部署于可信反向代理之后"时准确；直连场景下 XFF 本身不可信（通用问题，需 trusted-proxy 配置解决，超出本次范围）。

---

## 三、性能优化（消灭 N+1）

### 3.1 聚合查询层（新增 `internal/store/aggregate.go`）

将"逐行循环再查关联表"的模式收敛为单条 `GROUP BY` / 三表 `JOIN`。新增方法：

| 方法 | 用途 |
|---|---|
| `ItemCountsByBiblio(ids)` | 批量书目副本数/可借数（单条 GROUP BY + IN） |
| `AllItemCounts()` | 全库副本计数（推荐候选用） |
| `ActiveLoansWithBook` / `LoanHistoryWithBook` | 借阅 + 条码 + 书名三表 JOIN |
| `BorrowedBiblios(patronID)` | 读者读过的书目（DISTINCT，含主题/作者，画像用） |
| `ActiveLoanCountsByPatron()` | 每读者在借数（GROUP BY） |
| `AllUsersByPatron()` | 读者→登录账号映射（一次取齐） |
| `LoanTrendRange(from, to)` | 区间借出/归还按日聚合（替代逐日 COUNT） |

SQL 兼容性：`SUM(CASE WHEN ...)`、VARCHAR 日期（'YYYY-MM-DD'）字符串比较与 GROUP BY 在 SQLite/MySQL 行为一致，无需方言分支。

### 3.2 各端点查询数变化

| 端点/路径 | 修复前 | 修复后 | 改动位置 |
|---|---|---|---|
| `GET /api/books`（公共，limit=50） | 51 | 2 | `service.go` SearchBooks |
| 我的借阅 / 借阅历史 | 21（在借 10 本） | 1 | `service.go` PatronLoans/LoanHistory |
| `GET /api/admin/users`（~156 读者） | 300-500 | 3 | `service/admin.go` AdminUserRows（新增） |
| `GET /api/admin/stats` | 全表加载 patrons 只为 len() | 用已有 COUNT(*) | `api/admin.go` |
| `GET /api/recommend` | ~1000+ | ~5 | `recommend.go`（画像 JOIN 化 + 全量聚合） |
| `GET /api/books/hot` | 1 + N | 2 | `report.go` HotBooks |
| `GET /api/me/report` | 1 + 2N | 3 | `report.go` ReadingReport |
| 仪表盘 14 日趋势 | 28 | 2 | `store/tasks.go` LoanTrend |

SQLite 部署为 `SetMaxOpenConns(1)`（modernc 驱动写锁安全），所有查询本就串行排队，上述削减会被完整放大为延迟收益。顺带修复：`PatronLoans` 中 `renewErr` 对每条记录被调用两次的冗余。

### 3.3 索引迁移（v21–v23）

```
v21: CREATE INDEX idx_loans_due     ON loans(status, due_date)   -- 每小时到期扫描原为全表扫
v22: CREATE INDEX idx_loans_checkout ON loans(checkout_date)     -- 趋势/报表按日聚合
v23: CREATE INDEX idx_loans_checkin  ON loans(checkin_date)
```

沿用既有迁移框架（版本化、幂等、双库容错）；内存库测试即验证迁移可执行。

### 3.4 静态资源与连接池

- `web/` 静态资源改内存缓存（`sync.Map`），免去每请求磁盘 I/O；响应加 `Cache-Control: public, max-age=300`（冒烟验证响应头存在）；
- MySQL 池补 `SetConnMaxLifetime(30min)`，避免长命连接被 `wait_timeout` 静默掐断。

---

## 四、LLM 消耗控制与正确性

### 4.1 max_tokens 可配置上限

**根因**：`ChatRequest` 从不设置 `max_tokens`，输出长度只受供应商默认限制，失控长回复直接转化为费用。

**修复**：`config.json` 新增 `"maxTokens": 2048`（默认），`Config.MaxTokens` 经 `Loop.Run` 注入每次请求；`<0` 表示不限制。LLM 客户端 `ChatRequest` 相应增加 `max_tokens,omitempty` 字段。

### 4.2 Loop.Usage 数据竞争修复

**根因**：`Loop` 是全请求共享的单例，`Run` 写共享字段 `l.Usage`（清零 + 累计），`handleChat` 读取——并发对话时既违反 `-race`，又导致 token 统计串台（监控数据不可信）。

**修复**：`Run` 签名改为 `(text string, usage Usage, err error)`，usage 为局部变量随请求走；删除共享字段。调用点同步更新：`api/chat.go`、`cmd/eval/main.go`。新增并发回归测试 `TestConcurrentRunNoSharedState`（20 goroutine 并发 Run 同一单例；注意本机无 gcc 时 `-race` 不可用，建议在 CGO 环境跑一次 `go test -race ./...` 做最终确认）。

既有节流机制（意图预过滤、20 条上下文窗口、规则式历史摘要、2000 字符工具结果截断、搜索 10 条上限、30 req/min 聊天限流）保持不变，仍为 token 成本的第一道防线。

---

## 五、验证记录

| 验证项 | 结果 |
|---|---|
| `gofmt -l` / `go vet ./...` | 干净 |
| 全量单测 `go test ./...` | 全绿（agent / api / config / rag / service） |
| 新增回归测试 | 越权 5 例 + 并发 1 例 + schema 声明防回归 1 例 |
| 离线评测 `go run ./cmd/eval -mock` | 39/39 通过（1 例跳过，通过率 100%） |
| 端到端冒烟（隔离目录启动真实服务） | metrics 匿名 401 / 带 token 200；health 200；9000 字消息 400；SSE 聊天 200；Cache-Control 头存在 |

## 六、遗留事项与运维提示

1. **默认管理员凭据保留**（`admin/admin123` 等演示账号，`seed.go` 自动种子）：按决策保留演示便利性，README 部署章节已加"公网部署务必修改默认管理员密码"警示；
2. **`-race` 终验**：本机（Windows 无 gcc）无法启用，建议在 CI/Linux 执行 `go test -race ./...`；
3. **数据库密码硬编码**于 `docker-compose.yml` / `config.docker.json`（dev 级），生产部署应改为环境变量注入；
4. **座位端点的惰性过期扫描**（每次读请求触发 `ExpireStaleSeatReservations`）为读放大，但座位量级小，本次未动，可作为后续优化点；
5. **内存会话后端**（Redis 不可用时的降级）的过期条目仅在被再次访问时清理，长跑进程建议配 Redis。

## 七、文件变更清单

```
新增:
  internal/store/aggregate.go        聚合查询层（7 个方法）
  internal/agent/authz_test.go       越权回归测试（5 用例）
  internal/agent/concurrency_test.go 并发 Run 竞态回归测试
  docs/2026-08-security-performance-hardening.md  本文档

修改:
  internal/agent/loop.go             patron_id 强制覆盖；Run 返回 usage；max_tokens 注入
  internal/agent/tools.go            return/renew 归属校验；recommend/return/renew schema 补 patron_id
  internal/agent/llm.go              ChatRequest 增加 max_tokens
  internal/api/api.go                metrics 收权；MaxBytesReader；limit cap；静态缓存+Cache-Control
  internal/api/chat.go               消息长度上限；适配 Run 新签名
  internal/api/auth_handlers.go      XFF 取最后一跳；登录 per-IP 限流
  internal/api/admin.go              用户列表/统计批量化
  internal/api/report.go / recommend.go    limit cap
  internal/service/service.go        SearchBooks 聚合；借阅视图 JOIN 化
  internal/service/admin.go          AdminUserRows（3 查询）
  internal/service/recommend.go      画像 JOIN 化 + 全量聚合
  internal/service/report.go         阅读报告 JOIN 化；HotBooks 聚合
  internal/store/migrate.go          迁移 v21–v23（loans 三索引）
  internal/store/tasks.go            LoanTrend 单查询化
  internal/store/mysql.go            SetConnMaxLifetime
  internal/config/config.go          MaxTokens 字段与默认值
  cmd/server/main.go                 http.Server 超时
  cmd/eval/main.go                   适配 Run 新签名
  config.json                        maxTokens: 2048
  web/index.html                     三处 XSS 转义；escapeHtml 补引号
  README.md                          默认密码公网部署警示
```
