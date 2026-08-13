// 种子数据导入工具：初始化书目、馆藏、演示读者与预置借阅场景。
// 用法: go run ./cmd/seed [-reset] [-fetch] [-rows 200] [-driver sqlite|mysql] [-dsn ...]
//
//	-reset  重建数据库（删除旧库，仅 sqlite）
//	-fetch  从 Open Library API 扩充书目（网络可用时）
//	-rows   扩充书目条数上限（默认 200）
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/shdadahui/library-agent/internal/seed"
	"github.com/shdadahui/library-agent/internal/store"
)

func main() {
	reset := flag.Bool("reset", false, "重建数据库（仅 sqlite 生效）")
	fetch := flag.Bool("fetch", false, "从 Open Library API 扩充书目")
	rows := flag.Int("rows", 200, "扩充书目条数上限")
	driver := flag.String("driver", "sqlite", "数据库驱动: sqlite / mysql")
	dsn := flag.String("dsn", "data/library.db", "SQLite 文件路径 或 MySQL DSN(user:pass@tcp(host:port)/db?...)")
	flag.Parse()

	if *reset && *driver == "sqlite" {
		if err := os.Remove(*dsn); err == nil {
			log.Printf("已删除旧数据库 %s", *dsn)
		}
	}
	if *driver == "sqlite" {
		if dir := filepath.Dir(*dsn); dir != "." && dir != "" {
			_ = os.MkdirAll(dir, 0o755)
		}
	}

	st, err := store.OpenDriver(*driver, *dsn)
	if err != nil {
		log.Fatalf("打开数据库失败: %v", err)
	}
	defer st.Close()

	res, err := seed.Seed(st, *fetch, *rows)
	if err != nil {
		log.Fatalf("种子初始化失败: %v", err)
	}

	fmt.Printf("\n✅ 种子数据初始化完成：%d 本书、%d 位读者（%s）\n", res.Books, res.Patrons, *driver)
	fmt.Println("演示读者：张三(P0001) 李四(P0002) 王五(P0003) 赵六(P0004) 钱七(P0005) 孙八(P0006)")
	fmt.Println("演示账号：alice / alice123（张三）、bob / bob123（李四）")
	fmt.Println("启动服务：go run ./cmd/server，浏览器打开 http://localhost:8642")
}
