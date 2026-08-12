# 📚 图书馆智能助手（Library Agent）

一个类似「千问点外卖」的 Agent 项目：**Go 后端 + Web 聊天界面**，用户用自然语言对话（"帮我查一下《三体》"、"续借我借的那本书"、"图书馆有多少藏书"），AI Agent 自主调用图书馆系统工具完成查书、预约、续借、还书、罚款查询等操作，工具调用过程以卡片实时展示，回复支持 **Markdown 渲染**（表格/列表），并支持**多轮上下文对话**与**历史会话持久化**。

**完整登录系统**（bcrypt 密码哈希 + Redis 会话），**MySQL 8 + Redis 7** 生产存储（SQLite 供开发/评测），**意图预过滤器**拦截无关问题省 token，内置**智能推荐书系统**（基于借阅历史的兴趣画像 + 主题匹配 + 热门兜底）。

数据模型参照真实开源图书馆系统（Koha / Evergreen）：书目 → 馆藏副本 → 读者 → 流通记录，续借采用「关旧开新」做法，逾期按 0.1 元/天计罚，预约队列 FIFO。**借书遵循真实图书馆规则：须到馆办理，线上仅提供到馆引导与预约。**

## 快速开始（Docker 模式）

```bash
# 0. 前置：启动 Docker Desktop

# 1. 启动 MySQL 8 + Redis 7
docker compose up -d

# 2. 初始化种子数据（连接 config.local.json 中的 MySQL，可扩充 2000+ 本）
go run ./cmd/seed -fetch -rows 2000

# 3. 启动服务（自动加载 config.json + config.local.json 合并）
go run ./cmd/server
# 浏览器打开 http://localhost:8642
```

**演示账号**：`alice / alice123`（张三，有在借/逾期/罚款）、`bob / bob123`（李四）。也可直接注册新账号（自动创建读者）。

> 无 Docker 时：改 `config.local.json` 中 `db.driver` 为 `sqlite` 即可单文件运行（开发/评测模式）。
> 无 `DEEPSEEK_API_KEY` 时自动进入 **mock 演示模式**（本地规则模拟 Agent，不耗 token）。

## 演示场景

| 提问 | Agent 行为 |
|---|---|
| 帮我查一下《三体》 | search_books → 展示书目与可借副本 |
| 我借了什么书？ | get_my_loans → 列出在借清单与应还日期 |
| 帮我续借《三体》 | get_my_loans → renew_loan（限 2 次、逾期/被预约拒绝） |
| 帮我预约《三体》 | 全部借出时 place_hold 排队 |
| 我要借《三体Ⅱ》 | 有可借副本 → 引导到馆借阅（线上不借出） |
| 图书馆有多少藏书 | get_library_stats → 藏书/在借/预约/读者统计 |
| 有什么好书推荐 | recommend_books → 基于借阅历史个性化推荐 |
| 我喜欢科幻，推荐几本 | recommend_books → 按兴趣主题匹配 |
| 今天天气怎么样 | **PreFilter 本地拦截**，不消耗 LLM token |
| 续借我借的那本书（接上一轮） | 历史会话：结合上下文理解"那本书" |

## 架构

```
浏览器 ──登录/会话──► API 层 (REST + SSE)
   │   login/register/me   │
   ▼                      ▼
Redis（会话令牌）    internal/auth（bcrypt + session）
                        │
web/ 聊天+工具卡片 ◄── POST /api/chat（SSE 流式）
                        │ internal/agent
                        ├─ PreFilter   意图预过滤（省 token）
                        ├─ tools.go    9 工具注册表
                        └─ loop.go     tool calling 循环
                        │ internal/service（流通业务规则）
                        ▼
              internal/store ──► MySQL 8 / SQLite（双驱动）
              users / conversations / messages / biblios / items / patrons / loans / fines / holds
```

- **数据库**：MySQL 8（生产，docker compose）或 SQLite（开发/评测，纯 Go 免 CGO），`database/sql` 双驱动切换
- **Redis 7**：会话令牌存储（`session:{token}`，7 天 TTL），不可用时自动降级内存（日志提示）
- **认证**：bcrypt 密码哈希 + Bearer 令牌中间件；公开端点仅 auth/health/metrics/books
- **历史会话**：conversations/messages 表持久化，登录用户可新建/续聊/删除会话
- **Token 节省**：PreFilter 本地判定意图，闲聊/无关主题直接回复不调 LLM

## 配置

`config.json`（提交）+ `config.local.json`（本机私密配置，字段级覆盖，已 gitignore）：

```jsonc
// config.json（默认值）
{
  "providers": { "deepseek": { "baseURL": "https://api.deepseek.com", "apiKeyEnv": "DEEPSEEK_API_KEY", "defaultModel": "deepseek-chat" } },
  "activeProvider": "deepseek",
  "temperature": 0.7,
  "maxIterations": 8,
  "db":     { "driver": "sqlite", "dsn": "data/library.db" },
  "redis":  { "addr": "127.0.0.1:6379", "password": "", "db": 0 },
  "auth":   { "session_ttl_hours": 168 }
}

// config.local.json（本机 MySQL/Redis 覆盖示例，不提交）
{
  "db": { "driver": "mysql", "dsn": "library:libpass@tcp(127.0.0.1:3307)/library?parseTime=true&charset=utf8mb4&loc=Local" },
  "redis": { "addr": "127.0.0.1:6379", "password": "", "db": 0 }
}
```

## 评测集（Agent Eval）

`data/eval/cases.json` 定义了 **22 个评测用例**，覆盖：检索、借阅、续借、罚款、预约、统计、多轮上下文、边界（闲聊/超范围/信息不足/查无此书）。判定机制三级：
1. **期望工具序列**（exact=严格相等 / contains=子序列）
2. **工具白名单**（accept_tools：路径无关断言，如"引导到馆"可用工具或文字完成）
3. **回复文本断言**（expect_text：验证行为结果，如多轮用例须出现"百年孤独"）

```bash
go run ./cmd/eval -mock          # mock 模式（确定性）
go run ./cmd/eval                # 真实 LLM（低温度 0.2 提升确定性，执行错误自动重试）
go run ./cmd/eval -only multi-01 # 只跑单个用例
```

- 每个用例**独立内存数据库**（SQLite），互不污染、可重复执行
- 当前基线：**mock 21/21、真实 DeepSeek 22/22（100%）**

## 智能推荐

`recommend_books` 工具（或 `GET /api/recommend?taste=&limit=`）三级推荐策略：

1. **个性化**（无 taste）：从读者借阅历史提取主题/作者兴趣画像，全库匹配相似书（关键词重叠评分）
2. **主题推荐**（taste="科幻"等）：按输入关键词匹配书目 subjects/作者
3. **热门兜底**：借阅次数榜补充（"被借 N 次"）

排序叠加：兴趣匹配分 + 可借副本加分，候选排除已借过的书。种子数据预置每位演示读者 10~17 条模拟历史，推荐开箱可演示。

## 监控与日志

- `GET /api/metrics`：运行时统计（对话数、各工具调用次数、LLM/工具错误数、平均延迟、运行时长）
- 请求日志：中间件输出 `[http] METHOD /path -> status (耗时)` 到 stdout
- 对话日志：每次对话追加 JSON 行到 `data/logs/chat-YYYYMMDD.jsonl`（用户、会话 ID、输入、工具序列、回复、耗时、错误）

## 常用命令

```bash
go test ./...                      # 单元测试（流通规则/PreFilter 等）
go run ./cmd/seed -reset           # SQLite 重建种子
go run ./cmd/seed -fetch -rows 2000 # MySQL 扩充 2000+ 本（Open Library）
go run ./cmd/server -addr :8080    # 启动
docker compose down -v             # 重置容器数据
```

## API 一览

| 端点 | 说明 |
|---|---|
| `POST /api/auth/register · /login · /logout` | 注册（自动建读者）/登录/注销 |
| `GET /api/auth/me` | 当前用户 + 绑定读者 |
| `GET /api/books?q=&lang=` · `GET /api/books/{id}` | 书目检索/详情（公开） |
| `GET /api/conversations` · `POST` · `GET .../{id}/messages` · `DELETE .../{id}` | 历史会话管理 |
| `GET /api/patrons/{id}/loans · /history · /fines · /holds` | 借阅/历史/罚款/预约 |
| `POST /api/loans` · `/{id}/return` · `/{id}/renew` | 借书（线下通道）/还书/续借 |
| `POST /api/holds` | 预约 |
| `POST /api/chat` | SSE 流式对话（conversation_id 续聊；事件：message/tool_call/tool_result/done/error/conversation_id） |
| `GET /api/metrics` | 运行时统计 |

## 目录结构

```
cmd/server    服务入口              internal/config  LLM/DB/Redis/Auth 配置（支持 config.local.json 合并）
cmd/seed      种子数据工具           internal/store   双驱动（SQLite/MySQL）+ 用户/会话/消息表
cmd/eval      Agent 评测执行器       internal/seed     种子数据（供 seed/eval 复用）
web/          前端单页（登录/会话列表） internal/service 流通业务规则 + 会话服务
data/eval/    评测集与报告           internal/agent    PreFilter + LLM 客户端 + tool calling
data/logs/    对话日志               internal/auth     bcrypt + 会话（Redis/内存）
                                   internal/api      REST + SSE + 认证中间件
docker-compose.yml   MySQL 8 + Redis 7
```

## 数据来源

- 内置书单：37 本演示书目（中文经典/数学/英文经典，含《三体》《活着》《百年孤独》）
- `-fetch` 扩充（双源容错，seed 幂等可重复执行）：
  - Open Library Search API（CC0 公共领域元数据）
  - **Gutendex**（Project Gutenberg API，Open Library 不可用时自动兜底，已实测扩至 **2000+ 本**）
- 每位演示读者预置 10~17 条模拟借阅历史（供推荐系统与借阅历史展示）

## License

MIT
