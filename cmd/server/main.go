// 图书馆 Agent 服务入口。
// 用法: go run ./cmd/server [-addr :8642] [-config config.json]
package main

import (
	"flag"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/shdadahui/library-agent/internal/agent"
	"github.com/shdadahui/library-agent/internal/api"
	"github.com/shdadahui/library-agent/internal/auth"
	"github.com/shdadahui/library-agent/internal/config"
	"github.com/shdadahui/library-agent/internal/rag"
	"github.com/shdadahui/library-agent/internal/service"
	"github.com/shdadahui/library-agent/internal/store"
)

func main() {
	addr := flag.String("addr", ":8642", "监听地址")
	cfgPath := flag.String("config", "config.json", "配置文件路径")
	flag.Parse()

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		slog.Error("加载配置失败", "err", err)
		os.Exit(1)
	}

	// 数据库（sqlite 文件 / mysql 由 config.db 决定）
	if cfg.DB.Driver == "sqlite" {
		if dir := filepath.Dir(cfg.DB.DSN); dir != "." && dir != "" {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				slog.Error("创建数据目录失败", "err", err)
			os.Exit(1)
			}
		}
	}
	st, err := store.OpenDriver(cfg.DB.Driver, cfg.DB.DSN)
	if err != nil {
		slog.Error("打开数据库失败", "err", err)
		os.Exit(1)
	}
	defer st.Close()

	// 会话存储：优先 Redis，失败降级内存（日志提示）
	var sess auth.SessionStore = auth.NewMemorySessionStore()
	if rs, err := auth.NewRedisSessionStore(cfg.Redis.Addr, cfg.Redis.Password, cfg.Redis.DB); err == nil {
		sess = rs
		slog.Info("会话存储", "backend", "redis", "addr", cfg.Redis.Addr)
	} else {
		slog.Warn("Redis 不可用，会话降级为内存存储", "err", err)
	}
	am := auth.NewManager(st, sess, time.Duration(cfg.Auth.SessionTTLHours)*time.Hour)

	svc := service.New(st)
	// 初始化 RAG 知识库（Agent 工具 rag_search 用）
	agent.RagIndex = rag.New(rag.DefaultDocs)
	loop := agent.NewLoop(cfg, svc)
	srv := api.NewServer(cfg, svc, loop, am)

	slog.Info("图书馆 Agent 已启动", "url", "http://localhost"+*addr)
	slog.Info("运行配置", "db", cfg.DB.Driver, "llm", cfg.ActiveProvider, "model", cfg.Active().DefaultModel)
	if cfg.Active().IsMock() {
		slog.Warn("当前为 mock 模式")
	}
	if err := http.ListenAndServe(*addr, srv.Handler()); err != nil {
		slog.Error("服务退出", "err", err)
		os.Exit(1)
	}
}
