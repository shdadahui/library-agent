// 图书馆 Agent 服务入口。
// 用法: go run ./cmd/server [-addr :8642] [-config config.json]
package main

import (
	"flag"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/shdadahui/library-agent/internal/agent"
	"github.com/shdadahui/library-agent/internal/api"
	"github.com/shdadahui/library-agent/internal/auth"
	"github.com/shdadahui/library-agent/internal/config"
	"github.com/shdadahui/library-agent/internal/service"
	"github.com/shdadahui/library-agent/internal/store"
)

func main() {
	addr := flag.String("addr", ":8642", "监听地址")
	cfgPath := flag.String("config", "config.json", "配置文件路径")
	flag.Parse()

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		log.Fatalf("加载配置失败: %v", err)
	}

	// 数据库（sqlite 文件 / mysql 由 config.db 决定）
	if cfg.DB.Driver == "sqlite" {
		if dir := filepath.Dir(cfg.DB.DSN); dir != "." && dir != "" {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				log.Fatalf("创建数据目录失败: %v", err)
			}
		}
	}
	st, err := store.OpenDriver(cfg.DB.Driver, cfg.DB.DSN)
	if err != nil {
		log.Fatalf("打开数据库失败: %v", err)
	}
	defer st.Close()

	// 会话存储：优先 Redis，失败降级内存（日志提示）
	var sess auth.SessionStore = auth.NewMemorySessionStore()
	if rs, err := auth.NewRedisSessionStore(cfg.Redis.Addr, cfg.Redis.Password, cfg.Redis.DB); err == nil {
		sess = rs
		log.Printf("会话存储: Redis（%s）", cfg.Redis.Addr)
	} else {
		log.Printf("警告: Redis 不可用（%v），会话降级为内存存储，重启后登录失效", err)
	}
	am := auth.NewManager(st, sess, time.Duration(cfg.Auth.SessionTTLHours)*time.Hour)

	svc := service.New(st)
	loop := agent.NewLoop(cfg, svc)
	srv := api.NewServer(cfg, svc, loop, am)

	log.Printf("图书馆 Agent 已启动: http://localhost%s", *addr)
	log.Printf("数据库: %s | LLM: %s（模型 %s）", cfg.DB.Driver, cfg.ActiveProvider, cfg.Active().DefaultModel)
	if cfg.Active().IsMock() {
		log.Printf("提示: 当前为 mock 模式，未调用真实 LLM")
	}
	if err := http.ListenAndServe(*addr, srv.Handler()); err != nil {
		log.Fatal(err)
	}
}
