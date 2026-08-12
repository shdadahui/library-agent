// Package store 封装数据访问层，支持 SQLite（默认，纯 Go 免 CGO）与 MySQL 双驱动。
package store

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// Store 持有数据库连接。
type Store struct {
	DB     *sql.DB
	Driver string // sqlite / mysql
}

// Open 打开 SQLite 数据库（兼容旧调用，等价 OpenDriver("sqlite", path)）。
func Open(path string) (*Store, error) { return OpenDriver("sqlite", path) }

// OpenDriver 按驱动打开数据库并建表。
func OpenDriver(driver, dsn string) (*Store, error) {
	switch driver {
	case "mysql":
		return openMySQL(dsn)
	case "", "sqlite":
		return openSQLite(dsn)
	default:
		return nil, fmt.Errorf("不支持的数据库驱动: %s", driver)
	}
}

// openSQLite 打开（必要时创建）SQLite 数据库并建表。
// 内存库（":memory:" 或 file: 开头）不附加 WAL pragma，避免文件系统要求。
func openSQLite(path string) (*Store, error) {
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
	s := &Store{DB: db, Driver: "sqlite"}
	if err := s.createSchema(sqliteTokens); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// Close 关闭数据库。
func (s *Store) Close() error { return s.DB.Close() }

// schemaTokens 方言差异标记。
type schemaTokens struct {
	pk       string // 主键定义
	isbnIdx  string // isbn 索引（SQLite 部分唯一索引 / MySQL 普通索引）
}

var (
	sqliteTokens = schemaTokens{
		pk:      "INTEGER PRIMARY KEY AUTOINCREMENT",
		isbnIdx: "CREATE UNIQUE INDEX IF NOT EXISTS idx_biblio_isbn ON biblios(isbn) WHERE isbn IS NOT NULL AND isbn != '';",
	}
	mysqlTokens = schemaTokens{
		pk:      "BIGINT PRIMARY KEY AUTO_INCREMENT",
		isbnIdx: "CREATE INDEX idx_biblio_isbn ON biblios(isbn);",
	}
)

// schemaTemplate 建表与索引（幂等）。{pk} / {isbnIdx} 按方言替换。
const schemaTemplate = `
CREATE TABLE IF NOT EXISTS biblios(
  id {pk},
  title VARCHAR(512) NOT NULL,
  author VARCHAR(256) NOT NULL DEFAULT '',
  isbn VARCHAR(32) NOT NULL DEFAULT '',
  publisher VARCHAR(256) NOT NULL DEFAULT '',
  publish_year INTEGER NOT NULL DEFAULT 0,
  subjects VARCHAR(1024) NOT NULL DEFAULT '',
  lang VARCHAR(16) NOT NULL DEFAULT 'zh',
  cover_id INTEGER NOT NULL DEFAULT 0,
  online_url VARCHAR(512) NOT NULL DEFAULT ''
);
{isbnIdx}

CREATE TABLE IF NOT EXISTS items(
  id {pk},
  biblio_id INTEGER NOT NULL,
  barcode VARCHAR(64) NOT NULL,
  status VARCHAR(16) NOT NULL DEFAULT 'available',
  location VARCHAR(64) NOT NULL DEFAULT '总馆',
  loan_duration_days INTEGER NOT NULL DEFAULT 14
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_items_barcode ON items(barcode);

CREATE TABLE IF NOT EXISTS patrons(
  id {pk},
  name VARCHAR(128) NOT NULL,
  barcode VARCHAR(64) NOT NULL,
  phone VARCHAR(32) NOT NULL DEFAULT ''
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_patrons_barcode ON patrons(barcode);

CREATE TABLE IF NOT EXISTS loans(
  id {pk},
  item_id INTEGER NOT NULL,
  patron_id INTEGER NOT NULL,
  checkout_date VARCHAR(32) NOT NULL,
  due_date VARCHAR(32) NOT NULL,
  checkin_date VARCHAR(32),
  renewals INTEGER NOT NULL DEFAULT 0,
  status VARCHAR(16) NOT NULL DEFAULT 'active'
);
CREATE INDEX IF NOT EXISTS idx_loans_patron_active ON loans(patron_id, status);
CREATE INDEX IF NOT EXISTS idx_loans_item_active ON loans(item_id, status);

CREATE TABLE IF NOT EXISTS fines(
  id {pk},
  patron_id INTEGER NOT NULL,
  loan_id INTEGER NOT NULL,
  amount_cents INTEGER NOT NULL,
  created_date VARCHAR(32) NOT NULL,
  paid INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_fines_patron ON fines(patron_id, paid);

CREATE TABLE IF NOT EXISTS holds(
  id {pk},
  biblio_id INTEGER NOT NULL,
  patron_id INTEGER NOT NULL,
  item_id INTEGER,
  queue_pos INTEGER NOT NULL,
  status VARCHAR(16) NOT NULL DEFAULT 'waiting',
  created_at VARCHAR(32) NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_holds_biblio ON holds(biblio_id, status);
CREATE INDEX IF NOT EXISTS idx_holds_patron ON holds(patron_id, status);

CREATE TABLE IF NOT EXISTS users(
  id {pk},
  username VARCHAR(64) NOT NULL,
  password_hash VARCHAR(128) NOT NULL,
  patron_id INTEGER NOT NULL,
  role VARCHAR(16) NOT NULL DEFAULT 'user',
  created_at VARCHAR(32) NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_users_username ON users(username);

CREATE TABLE IF NOT EXISTS conversations(
  id {pk},
  user_id INTEGER NOT NULL,
  title VARCHAR(255) NOT NULL DEFAULT '新会话',
  created_at VARCHAR(32) NOT NULL,
  updated_at VARCHAR(32) NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_conversations_user ON conversations(user_id);

CREATE TABLE IF NOT EXISTS messages(
  id {pk},
  conversation_id INTEGER NOT NULL,
  role VARCHAR(16) NOT NULL,
  content TEXT NOT NULL,
  created_at VARCHAR(32) NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_messages_conv ON messages(conversation_id);

CREATE TABLE IF NOT EXISTS seats(
  id {pk},
  seat_no VARCHAR(16) NOT NULL,
  area VARCHAR(64) NOT NULL,
  seat_type VARCHAR(32) NOT NULL DEFAULT '普通',
  status VARCHAR(16) NOT NULL DEFAULT 'available',
  row_pos INTEGER NOT NULL DEFAULT 0,
  col_pos INTEGER NOT NULL DEFAULT 0
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_seats_no ON seats(seat_no);
CREATE INDEX IF NOT EXISTS idx_seats_area ON seats(area);

CREATE TABLE IF NOT EXISTS seat_reservations(
  id {pk},
  seat_id INTEGER NOT NULL,
  patron_id INTEGER NOT NULL,
  reserve_date VARCHAR(32) NOT NULL,
  slot VARCHAR(16) NOT NULL,
  status VARCHAR(16) NOT NULL DEFAULT 'active',
  created_at VARCHAR(32) NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_seat_res_seat ON seat_reservations(seat_id, reserve_date, slot);
CREATE INDEX IF NOT EXISTS idx_seat_res_patron ON seat_reservations(patron_id, reserve_date, status);

CREATE TABLE IF NOT EXISTS gate_logs(
  id {pk},
  patron_id INTEGER NOT NULL,
  direction VARCHAR(8) NOT NULL,
  gate VARCHAR(32) NOT NULL DEFAULT '东门',
  verified_by VARCHAR(16) NOT NULL DEFAULT 'card',
  created_at VARCHAR(32) NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_gate_logs_patron ON gate_logs(patron_id);
CREATE INDEX IF NOT EXISTS idx_gate_logs_time ON gate_logs(created_at);
`

// createSchema 建表与索引（幂等，逐语句执行以兼容 SQLite/MySQL 双驱动）。
func (s *Store) createSchema(t schemaTokens) error {
	schema := strings.ReplaceAll(schemaTemplate, "{pk}", t.pk)
	schema = strings.ReplaceAll(schema, "{isbnIdx}", t.isbnIdx)
	if s.Driver == "mysql" {
		// MySQL 不支持 CREATE INDEX IF NOT EXISTS；靠忽略"索引已存在"错误保证幂等
		schema = strings.ReplaceAll(schema, "INDEX IF NOT EXISTS", "INDEX")
	}
	for _, stmt := range strings.Split(schema, ";") {
		stmt = strings.TrimSpace(stmt)
		if stmt == "" {
			continue
		}
		if _, err := s.DB.Exec(stmt); err != nil {
			if s.Driver == "mysql" && (strings.Contains(err.Error(), "Duplicate key name") || strings.Contains(err.Error(), "already exists")) {
				continue // 索引已存在（重复启动）
			}
			head := stmt
			if len(head) > 80 {
				head = head[:80] + "…"
			}
			return fmt.Errorf("建表失败: %w（语句: %s）", err, head)
		}
	}
	// 兼容旧库：补充新增列（忽略 duplicate column）
	if _, err := s.DB.Exec(`ALTER TABLE biblios ADD COLUMN online_url VARCHAR(512) NOT NULL DEFAULT ''`); err != nil {
		if !strings.Contains(strings.ToLower(err.Error()), "duplicate column") {
			return fmt.Errorf("迁移列失败: %w", err)
		}
	}
	if _, err := s.DB.Exec(`ALTER TABLE users ADD COLUMN role VARCHAR(16) NOT NULL DEFAULT 'user'`); err != nil {
		if !strings.Contains(strings.ToLower(err.Error()), "duplicate column") {
			return fmt.Errorf("迁移列失败: %w", err)
		}
	}
	return nil
}

// Now 返回本地时区日期（YYYY-MM-DD），全库统一使用。
func Now() string { return time.Now().Format("2006-01-02") }

// NowDateTime 返回本地时间（YYYY-MM-DD HH:MM:SS）。
func NowDateTime() string { return time.Now().Format("2006-01-02 15:04:05") }
