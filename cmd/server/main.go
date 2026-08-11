// 图书馆 Agent 服务入口。
// 用法: go run ./cmd/server [-addr :8642] [-db data/library.db] [-config config.json]
package main

import (
	"flag"
	"log"
	"net/http"
	"os"
	"path/filepath"

	"github.com/shdadahui/library-agent/internal/agent"
	"github.com/shdadahui/library-agent/internal/api"
	"github.com/shdadahui/library-agent/internal/config"
	"github.com/shdadahui/library-agent/internal/service"
	"github.com/shdadahui/library-agent/internal/store"
)

func main() {
	addr := flag.String("addr", ":8642", "监听地址")
	dbPath := flag.String("db", "data/library.db", "SQLite 数据库路径")
	cfgPath := flag.String("config", "config.json", "LLM 配置文件路径")
	flag.Parse()

	if dir := filepath.Dir(*dbPath); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			log.Fatalf("创建数据目录失败: %v", err)
		}
	}

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		log.Fatalf("加载配置失败: %v（可先运行 go run ./cmd/seed 初始化数据）", err)
	}

	st, err := store.Open(*dbPath)
	if err != nil {
		log.Fatalf("打开数据库失败: %v", err)
	}
	defer st.Close()

	svc := service.New(st)
	loop := agent.NewLoop(cfg, svc)
	srv := api.NewServer(cfg, svc, loop)

	log.Printf("图书馆 Agent 已启动: http://localhost%s", *addr)
	log.Printf("LLM 供应商: %s（模型 %s）", cfg.ActiveProvider, cfg.Active().DefaultModel)
	if cfg.Active().IsMock() {
		log.Printf("提示: 当前为 mock 模式，未调用真实 LLM；配置 DEEPSEEK_API_KEY 并设置 activeProvider=deepseek 可启用真智能体")
	}
	if err := http.ListenAndServe(*addr, srv.Handler()); err != nil {
		log.Fatal(err)
	}
}
