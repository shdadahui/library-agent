# 📚 图书馆智能助手（Library Agent）

一个类似「千问点外卖」的 Agent 演示项目：**Go 后端 + Web 聊天界面**，用户用自然语言对话（"帮我查一下《三体》"、"续借我借的那本书"、"图书馆有多少藏书"），AI Agent 自主调用图书馆系统工具完成查书、预约、续借、还书、罚款查询等操作，工具调用过程以卡片实时展示，回复支持 **Markdown 渲染**（表格/列表），并支持**多轮上下文对话**。

数据模型参照真实开源图书馆系统（Koha / Evergreen）：书目 → 馆藏副本 → 读者 → 流通记录，续借采用「关旧开新」做法，逾期按 0.1 元/天计罚，预约队列 FIFO。**借书遵循真实图书馆规则：须到馆办理，线上仅提供到馆引导与预约。**

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
| 帮我续借《三体》 | get_my_loans → renew_loan（限 2 次、逾期/被预约拒绝） |
| 帮我预约《三体》 | 全部借出时 place_hold 排队 |
| 我要借《三体Ⅱ》 | 有可借副本 → guide_borrow 引导到馆借阅（线上不借出） |
| 图书馆有多少藏书 | get_library_stats → 藏书/在借/预约/读者统计 |
| 我有罚款吗？ | get_my_fines → 未缴罚款 |
| 续借我借的那本书（接上一轮） | 多轮上下文：结合历史理解"那本书" |

预置演示读者：张三(P0001) 有快到期/逾期/续借过/罚款；李四(P0002) 正常在借；王五(P0003) 有借阅历史无在借；孙八(P0006) 借走《蛙》供预约演示。

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

## 评测集（Agent Eval）

`data/eval/cases.json` 定义了 **22 个评测用例**，覆盖 7 类场景：检索、借阅、续借、罚款、预约、统计、多轮上下文、边界（闲聊/超范围/信息不足/查无此书）。判定依据：**期望工具调用序列**（exact=严格相等 / contains=子序列）+ 关键参数断言（如 `search_books.q` 需包含书名）。多轮用例标记 `real_only`（mock 无状态不适用）。

```bash
go run ./cmd/eval -mock          # mock 模式（确定性，校验评测集与引擎对齐）
go run ./cmd/eval                # 真实 LLM 评测（config.json 指定供应商）
go run ./cmd/eval -only renew-01 # 只跑单个用例
```

- 每个用例使用**独立内存数据库**（种子数据预置），互不污染、可重复执行
- 输出逐用例 PASS/FAIL + 工具序列对比，报告保存到 `data/eval/report-<时间戳>.json`
- 退出码：0 全部通过；1 存在失败用例（可接入 CI）
- 评测驱动开发：新增能力 → 先补用例 → 再迭代实现
- 当前基线：mock 21/21、真实 DeepSeek 22/22

## 监控与日志

- `GET /api/metrics`：运行时统计（对话数、各工具调用次数、LLM/工具错误数、平均延迟、运行时长）
- 请求日志：中间件输出 `[http] METHOD /path -> status (耗时)` 到 stdout
- 对话日志：每次对话追加一行 JSON 到 `data/logs/chat-YYYYMMDD.jsonl`（读者、输入、工具序列、回复、耗时、错误）

## API 一览

| 端点 | 说明 |
|---|---|
| `GET /api/health` | 健康检查 |
| `GET /api/metrics` | 运行时统计（对话/工具调用/错误/延迟） |
| `GET /api/books?q=&lang=` | 书目检索 |
| `GET /api/books/{id}` | 书目详情 + 馆藏可用性 |
| `GET /api/patrons` | 读者列表 |
| `GET /api/patrons/{id}/loans · /history · /fines · /holds` | 借阅/历史/罚款/预约 |
| `POST /api/loans` | 借书（线下/后台通道） |
| `POST /api/loans/{id}/return` | 还书（自动算罚款、唤醒预约） |
| `POST /api/loans/{id}/renew` | 续借 |
| `POST /api/holds` | 预约 |
| `POST /api/chat` | SSE 流式对话（history 支持多轮，事件：message/tool_call/tool_result/done/error） |

## 目录结构

```
cmd/server    服务入口            internal/config  LLM 多供应商配置
cmd/seed      种子数据工具         internal/store   SQLite 数据访问
cmd/eval      Agent 评测执行器     internal/seed    种子数据（供 seed/eval 复用）
web/          前端单页             internal/service 流通业务规则
data/eval/    评测集与报告         internal/agent    LLM 客户端 + tool calling
                                 internal/api      REST + SSE
```

## 数据来源

- 内置书单：36 本演示书目（中文经典/数学/英文经典，含《三体》《活着》《百年孤独》）
- `-fetch` 扩充：Open Library Search API（CC0 公共领域元数据，Kaggle "Open Library Books 50K" 同源）

## License

MIT
