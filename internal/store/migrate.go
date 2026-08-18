package store

import (
	"fmt"
	"strings"
)

// 版本化迁移：新增表/列等结构性变更统一走这里，由 schema_migrations 表记录执行进度。
// 已发布的迁移不可修改；新变更追加在列表末尾。
// 说明：初始建表（createSchema，幂等 DDL）在迁移之前执行，此处只处理"建表之后的增量变更"。
var migrations = []migration{
	{Version: 1, SQL: "ALTER TABLE biblios ADD COLUMN online_url VARCHAR(512) NOT NULL DEFAULT ''", Desc: "书目加在线阅读 URL"},
	{Version: 2, SQL: "ALTER TABLE users ADD COLUMN role VARCHAR(16) NOT NULL DEFAULT 'user'", Desc: "用户加角色字段（admin/user）"},
	{Version: 3, SQL: `CREATE TABLE IF NOT EXISTS notifications(
		id INTEGER PRIMARY KEY,
		patron_id INTEGER NOT NULL,
		type VARCHAR(16) NOT NULL DEFAULT 'system',
		title VARCHAR(128) NOT NULL,
		body VARCHAR(512) NOT NULL DEFAULT '',
		is_read INTEGER NOT NULL DEFAULT 0,
		created_at VARCHAR(32) NOT NULL
	)`, Desc: "系统通知表（预约到书/逾期提醒）"},
	{Version: 4, SQL: `CREATE INDEX idx_notif_patron ON notifications(patron_id, is_read)`, Desc: "通知查询索引（MySQL 不支持 IF NOT EXISTS；重复索引由容错逻辑视为已完成）"},
	{Version: 6, SQL: `ALTER TABLE patrons ADD COLUMN status VARCHAR(16) NOT NULL DEFAULT 'active'`, Desc: "读者状态（active/disabled 禁用）"},
	{Version: 7, SQL: `ALTER TABLE notifications ADD COLUMN ref_id INTEGER NOT NULL DEFAULT 0`, Desc: "通知业务引用（到期提醒=loan_id，去重用）"},
	{Version: 8, SQL: `CREATE TABLE IF NOT EXISTS login_logs(
		id INTEGER PRIMARY KEY,
		user_id INTEGER NOT NULL DEFAULT 0,
		username VARCHAR(64) NOT NULL DEFAULT '',
		ip VARCHAR(64) NOT NULL DEFAULT '',
		success INTEGER NOT NULL DEFAULT 0,
		created_at VARCHAR(32) NOT NULL
	)`, Desc: "登录审计日志"},
	{Version: 9, SQL: `CREATE INDEX idx_loginlog_time ON login_logs(created_at)`, Desc: "登录日志时间索引"},
	{Version: 10, SQL: `ALTER TABLE notifications MODIFY COLUMN id BIGINT NOT NULL AUTO_INCREMENT`, Driver: "mysql", Desc: "MySQL 自增主键（迁移建表缺 AUTO_INCREMENT，插入静默失败）"},
	{Version: 11, SQL: `ALTER TABLE login_logs MODIFY COLUMN id BIGINT NOT NULL AUTO_INCREMENT`, Driver: "mysql", Desc: "MySQL 自增主键（同上）"},
	{Version: 12, SQL: `CREATE TABLE IF NOT EXISTS favorites(
		id INTEGER PRIMARY KEY,
		patron_id INTEGER NOT NULL,
		biblio_id INTEGER NOT NULL,
		created_at VARCHAR(32) NOT NULL
	)`, Desc: "读者收藏书目"},
	{Version: 13, SQL: `CREATE TABLE IF NOT EXISTS ratings(
		id INTEGER PRIMARY KEY,
		patron_id INTEGER NOT NULL,
		biblio_id INTEGER NOT NULL,
		score INTEGER NOT NULL,
		created_at VARCHAR(32) NOT NULL
	)`, Desc: "读者评分（1-5 星）"},
	{Version: 14, SQL: `ALTER TABLE patrons ADD COLUMN vip INTEGER NOT NULL DEFAULT 0`, Desc: "VIP 会员标记（借阅上限 10）"},
	{Version: 15, SQL: `CREATE INDEX idx_fav_patron ON favorites(patron_id)`, Desc: "收藏按读者查询索引"},
	{Version: 16, SQL: `CREATE INDEX idx_rate_biblio ON ratings(biblio_id)`, Desc: "评分按书目聚合索引"},
	{Version: 17, SQL: `ALTER TABLE favorites MODIFY COLUMN id BIGINT NOT NULL AUTO_INCREMENT`, Driver: "mysql", Desc: "MySQL 自增主键"},
	{Version: 18, SQL: `ALTER TABLE ratings MODIFY COLUMN id BIGINT NOT NULL AUTO_INCREMENT`, Driver: "mysql", Desc: "MySQL 自增主键"},
}

type migration struct {
	Version int
	SQL     string
	Desc    string
	// Driver 限定执行的数据库驱动（""=两者都执行；"mysql"/"sqlite"=仅该驱动）
	Driver string
}

// runMigrations 执行未记录的迁移；已存在的列/索引视为已完成（兼容历史库）。
func (s *Store) runMigrations() error {
	// 迁移记录表（幂等）
	if _, err := s.DB.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations(
		version INTEGER PRIMARY KEY,
		applied_at VARCHAR(32) NOT NULL
	)`); err != nil {
		return fmt.Errorf("建迁移表失败: %w", err)
	}
	for _, m := range migrations {
		// 驱动限定：仅目标驱动的迁移才执行（如 MySQL 补自增主键）
		if m.Driver != "" && m.Driver != s.Driver {
			continue
		}
		var n int
		if err := s.DB.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE version=?`, m.Version).Scan(&n); err != nil {
			return err
		}
		if n > 0 {
			continue // 已执行
		}
		if _, err := s.DB.Exec(m.SQL); err != nil {
			// MySQL 重复列名 / 重复索引：说明库中已存在该结构（历史库），视为完成
			lower := strings.ToLower(err.Error())
			if strings.Contains(lower, "duplicate column") || strings.Contains(lower, "duplicate key name") ||
				strings.Contains(lower, "already exists") || strings.Contains(lower, "duplicate column name") {
				_ = s.recordMigration(m.Version)
				continue
			}
			return fmt.Errorf("迁移 v%d（%s）失败: %w", m.Version, m.Desc, err)
		}
		if err := s.recordMigration(m.Version); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) recordMigration(version int) error {
	_, err := s.DB.Exec(`INSERT INTO schema_migrations(version, applied_at) VALUES(?, ?)`, version, NowDateTime())
	return err
}
