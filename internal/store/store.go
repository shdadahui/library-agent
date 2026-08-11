// Package store 封装 SQLite 数据访问层。
// 使用纯 Go 实现的 modernc.org/sqlite，避免 Windows 下 CGO 编译问题。
package store

import (
	"database/sql"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

// Store 持有数据库连接。
type Store struct {
	DB *sql.DB
}

// Open 打开（必要时创建）SQLite 数据库并建表。
// 内存库（":memory:" 或 file: 开头）不附加 WAL pragma，避免文件系统要求。
func Open(path string) (*Store, error) {
	var dsn string
	if path == ":memory:" || path == "" {
		// 每次 Open 使用唯一内存库名，避免测试间共享污染
		dsn = fmt.Sprintf("file:memdb%d?mode=memory&cache=shared", time.Now().UnixNano())
	} else if len(path) >= 5 && path[:5] == "file:" {
		dsn = path
	} else {
		dsn = fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)", path)
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("打开数据库失败: %w", err)
	}
	// 单连接避免 modernc 驱动多连接写锁问题；内存库必须单连接
	db.SetMaxOpenConns(1)
	s := &Store{DB: db}
	if err := s.createSchema(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// Close 关闭数据库。
func (s *Store) Close() error { return s.DB.Close() }

// createSchema 建表与索引（幂等）。
func (s *Store) createSchema() error {
	schema := `
CREATE TABLE IF NOT EXISTS biblios(
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  title TEXT NOT NULL,
  author TEXT NOT NULL DEFAULT '',
  isbn TEXT NOT NULL DEFAULT '',
  publisher TEXT NOT NULL DEFAULT '',
  publish_year INTEGER NOT NULL DEFAULT 0,
  subjects TEXT NOT NULL DEFAULT '',
  lang TEXT NOT NULL DEFAULT 'zh',
  cover_id INTEGER NOT NULL DEFAULT 0
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_biblio_isbn
  ON biblios(isbn) WHERE isbn IS NOT NULL AND isbn != '';

CREATE TABLE IF NOT EXISTS items(
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  biblio_id INTEGER NOT NULL REFERENCES biblios(id),
  barcode TEXT NOT NULL UNIQUE,
  status TEXT NOT NULL DEFAULT 'available',
  location TEXT NOT NULL DEFAULT '总馆',
  loan_duration_days INTEGER NOT NULL DEFAULT 14
);

CREATE TABLE IF NOT EXISTS patrons(
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  name TEXT NOT NULL,
  barcode TEXT NOT NULL UNIQUE,
  phone TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS loans(
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  item_id INTEGER NOT NULL REFERENCES items(id),
  patron_id INTEGER NOT NULL REFERENCES patrons(id),
  checkout_date TEXT NOT NULL,
  due_date TEXT NOT NULL,
  checkin_date TEXT,
  renewals INTEGER NOT NULL DEFAULT 0,
  status TEXT NOT NULL DEFAULT 'active'
);
CREATE INDEX IF NOT EXISTS idx_loans_patron_active ON loans(patron_id, status);
CREATE INDEX IF NOT EXISTS idx_loans_item_active ON loans(item_id) WHERE status = 'active';

CREATE TABLE IF NOT EXISTS fines(
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  patron_id INTEGER NOT NULL REFERENCES patrons(id),
  loan_id INTEGER NOT NULL REFERENCES loans(id),
  amount_cents INTEGER NOT NULL,
  created_date TEXT NOT NULL,
  paid INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_fines_patron ON fines(patron_id, paid);

CREATE TABLE IF NOT EXISTS holds(
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  biblio_id INTEGER NOT NULL REFERENCES biblios(id),
  patron_id INTEGER NOT NULL REFERENCES patrons(id),
  item_id INTEGER REFERENCES items(id),
  queue_pos INTEGER NOT NULL,
  status TEXT NOT NULL DEFAULT 'waiting',
  created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_holds_biblio ON holds(biblio_id, status);
CREATE INDEX IF NOT EXISTS idx_holds_patron ON holds(patron_id, status);
`
	if _, err := s.DB.Exec(schema); err != nil {
		return fmt.Errorf("建表失败: %w", err)
	}
	return nil
}

// Now 返回本地时区日期（YYYY-MM-DD），全库统一使用。
func Now() string { return time.Now().Format("2006-01-02") }
