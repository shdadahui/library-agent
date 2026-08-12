package store

import (
	"database/sql"
	"fmt"

	_ "github.com/go-sql-driver/mysql"
)

// openMySQL 打开 MySQL 数据库并建表。
// dsn 形如: user:pass@tcp(127.0.0.1:3306)/library?parseTime=true&charset=utf8mb4&loc=Local
func openMySQL(dsn string) (*Store, error) {
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, fmt.Errorf("打开 MySQL 失败: %w", err)
	}
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(5)
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("连接 MySQL 失败（请确认 docker compose 已启动）: %w", err)
	}
	s := &Store{DB: db, Driver: "mysql"}
	if err := s.createSchema(mysqlTokens); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}
