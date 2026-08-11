# 📚 图书馆智能助手（Library Agent）

一个类似「千问点外卖」的 Agent 演示项目：**Go 后端 + Web 聊天界面**，用户用自然语言对话（"帮我查一下《三体》"、"续借我借的那本书"），AI Agent 自主调用图书馆系统工具完成查书、借书、还书、续借、预约、查询罚款等操作，工具调用过程以卡片实时展示。

数据模型参照真实开源图书馆系统（Koha / Evergreen）：书目 → 馆藏副本 → 读者 → 流通记录，续借采用「关旧开新」做法，逾期按 0.1 元/天计罚，预约队列 FIFO。

## 快速开始

```bash
# 1. 初始化种子数据（内置 36 本演示书目 + 6 位读者 + 预置借阅场景）
go run ./cmd/seed

# 2. 启动服务
go run ./cmd/server
# 浏览器打开 http://localhost:8642
```

> 无 `DEEPSEEK_API_KEY` 时自动进入 **mock 演示模式**（本地规则模拟 Agent 行为）；
> 配置密钥后修改 `config.json` 的 `activeProvider` 即可启用真实 LLM 智能体。

## 演示场景

| 提问 | Agent 行为 |
|---|---|
| 帮我查一下《三体》 | search_books → 展示书目与可借副本 |
| 我借了什么书？ | get_my_loans → 列出在借清单与应还日期 |
| 帮我续借《活着》 | get_my_loans → renew_loan（限 2 次、逾期/被预约拒绝） |
| 帮我预约《三体》 | 全部借出时 place_hold 排队 |
| 我有罚款吗？ | get_my_fines → 未缴罚款 |
| 我要借《1984》 | search → availability → borrow_book |

预置演示读者：张三(P0001) 有快到期/逾期/续借过/罚款；李四(P0002) 正常在借 + 预约排队中；王五(P0003) 有借阅历史无在借。

## 架构

```
web/index.html ── POST /api/chat (SSE) ──► internal/api ──► internal/agent (Loop)
  (聊天 + 工具卡片)                       (REST + SSE)      ├─ llm.go   OpenAI 兼容客户端（零依赖）
                                                            ├─ tools.go 8 个工具注册表
                                                            └─ loop.go  tool calling 循环
                                                                      │
                                          internal/service (业务规则：续借/罚款/预约队列)
                                                                      │
                                          internal/store   (SQLite, modernc.org/sqlite 纯 Go)
```

- **数据库**：SQLite（`data/library.db`），`modernc.org/sqlite` 纯 Go 实现，免 CGO
- **LLM**：OpenAI 兼容 `/chat/completions`，零第三方依赖（标准库 `net/http` 实现）
- **多供应商**：`config.json` 可配置 DeepSeek / OpenAI / mock（仿 tutorialsmith 模式）

## 配置

```jsonc
{
  "providers": {
    "deepseek": { "baseURL": "https://api.deepseek.com", "apiKeyEnv": "DEEPSEEK_API_KEY", "defaultModel": "deepseek-chat" },
    "openai":   { "baseURL": "https://api.openai.com/v1", "apiKeyEnv": "OPENAI_API_KEY", "defaultModel": "gpt-4o-mini" },
    "mock":     { "baseURL": "", "apiKeyEnv": "", "defaultModel": "mock" }
  },
  "activeProvider": "deepseek",
  "temperature": 0.7,
  "maxIterations": 8
}
```

## 常用命令

```bash
go test ./...            # 单元测试（续借上限/逾期罚款/预约队列等）
go run ./cmd/seed -fetch # 从 Open Library API 扩充书目（最多 200 本）
go run ./cmd/seed -reset # 重建数据库
go run ./cmd/server -addr :8080
```

## API 一览

| 端点 | 说明 |
|---|---|
| `GET /api/books?q=&lang=` | 书目检索 |
| `GET /api/books/{id}` | 书目详情 + 馆藏可用性 |
| `GET /api/patrons` | 读者列表 |
| `GET /api/patrons/{id}/loans · /history · /fines · /holds` | 借阅/历史/罚款/预约 |
| `POST /api/loans` | 借书 |
| `POST /api/loans/{id}/return` | 还书（自动算罚款、唤醒预约） |
| `POST /api/loans/{id}/renew` | 续借 |
| `POST /api/holds` | 预约 |
| `POST /api/chat` | SSE 流式对话（message/tool_call/tool_result/done/error） |

## 目录结构

```
cmd/server    服务入口            internal/config  LLM 多供应商配置
cmd/seed      种子数据工具         internal/store   SQLite 数据访问
web/          前端单页             internal/service 流通业务规则
                                 internal/agent    LLM 客户端 + tool calling
                                 internal/api      REST + SSE
```

## 数据来源

- 内置书单：36 本演示书目（中文经典/数学/英文经典，含《三体》《活着》《百年孤独》）
- `-fetch` 扩充：Open Library Search API（CC0 公共领域元数据，Kaggle "Open Library Books 50K" 同源）

## License

MIT
