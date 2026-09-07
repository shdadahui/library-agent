package store

import (
	"database/sql"
	"strings"
)

// ---- 聚合查询（消灭 N+1）----
// 以下查询将此前"逐行循环再查关联表"的模式收敛为单条 GROUP BY / JOIN，
// 对 SQLite 单连接部署（所有查询串行）收益尤其明显。

// ItemCount 书目副本计数。
type ItemCount struct {
	Total     int
	Available int
}

// ItemCountsByBiblio 批量统计一组书目的副本总数与可借数（单条 GROUP BY）。
func (s *Store) ItemCountsByBiblio(ids []int64) (map[int64]ItemCount, error) {
	out := map[int64]ItemCount{}
	if len(ids) == 0 {
		return out, nil
	}
	ph := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	rows, err := s.DB.Query(`SELECT biblio_id, COUNT(*),
		SUM(CASE WHEN status='available' THEN 1 ELSE 0 END)
		FROM items WHERE biblio_id IN (`+ph+`) GROUP BY biblio_id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var c ItemCount
		if err := rows.Scan(&id, &c.Total, &c.Available); err != nil {
			return nil, err
		}
		out[id] = c
	}
	return out, rows.Err()
}

// AllItemCounts 全库书目副本计数（推荐候选等需要全量可借状态的场景）。
func (s *Store) AllItemCounts() (map[int64]ItemCount, error) {
	out := map[int64]ItemCount{}
	rows, err := s.DB.Query(`SELECT biblio_id, COUNT(*),
		SUM(CASE WHEN status='available' THEN 1 ELSE 0 END)
		FROM items GROUP BY biblio_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var c ItemCount
		if err := rows.Scan(&id, &c.Total, &c.Available); err != nil {
			return nil, err
		}
		out[id] = c
	}
	return out, rows.Err()
}

// LoanBookRow 借阅记录 + 副本条码 + 书名（三表 JOIN 一次取齐）。
type LoanBookRow struct {
	Loan
	Barcode string
	Title   string
}

// ActiveLoansWithBook 读者当前在借（含条码/书名）。
func (s *Store) ActiveLoansWithBook(patronID int64) ([]LoanBookRow, error) {
	return s.loansWithBook(`WHERE l.patron_id=? AND l.status='active' ORDER BY l.due_date`, patronID)
}

// LoanHistoryWithBook 读者借阅历史（含条码/书名）。
func (s *Store) LoanHistoryWithBook(patronID int64) ([]LoanBookRow, error) {
	return s.loansWithBook(`WHERE l.patron_id=? ORDER BY l.checkout_date DESC`, patronID)
}

func (s *Store) loansWithBook(where string, args ...any) ([]LoanBookRow, error) {
	rows, err := s.DB.Query(`SELECT l.id,l.item_id,l.patron_id,l.checkout_date,l.due_date,l.checkin_date,l.renewals,l.status,
		i.barcode,b.title
		FROM loans l
		JOIN items i ON i.id = l.item_id
		JOIN biblios b ON b.id = i.biblio_id `+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []LoanBookRow{}
	for rows.Next() {
		var r LoanBookRow
		var ci sql.NullString
		if err := rows.Scan(&r.ID, &r.ItemID, &r.PatronID, &r.CheckoutDate, &r.DueDate, &ci, &r.Renewals, &r.Status, &r.Barcode, &r.Title); err != nil {
			return nil, err
		}
		if ci.Valid {
			r.CheckinDate = &ci.String
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// BorrowedBiblios 读者借过的书目（去重，含主题/作者，兴趣画像用）。
func (s *Store) BorrowedBiblios(patronID int64) ([]Biblio, error) {
	rows, err := s.DB.Query(`
		SELECT DISTINCT b.id,b.title,b.author,b.isbn,b.publisher,b.publish_year,b.subjects,b.lang,b.cover_id,b.online_url
		FROM loans l
		JOIN items i ON i.id = l.item_id
		JOIN biblios b ON b.id = i.biblio_id
		WHERE l.patron_id=? ORDER BY b.id`, patronID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Biblio{}
	for rows.Next() {
		var b Biblio
		if err := rows.Scan(&b.ID, &b.Title, &b.Author, &b.ISBN, &b.Publisher, &b.PublishYear, &b.Subjects, &b.Lang, &b.CoverID, &b.OnlineURL); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// ActiveLoanCountsByPatron 每位读者当前在借数（单条 GROUP BY，管理端列表用）。
func (s *Store) ActiveLoanCountsByPatron() (map[int64]int, error) {
	rows, err := s.DB.Query(`SELECT patron_id, COUNT(*) FROM loans WHERE status='active' GROUP BY patron_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]int{}
	for rows.Next() {
		var pid, n int64
		if err := rows.Scan(&pid, &n); err != nil {
			return nil, err
		}
		out[pid] = int(n)
	}
	return out, rows.Err()
}

// AllUsersByPatron 读者 ID → 登录账号（管理端一次取齐，替代逐读者查询）。
func (s *Store) AllUsersByPatron() (map[int64]*User, error) {
	rows, err := s.DB.Query(`SELECT id,username,password_hash,patron_id,role,created_at FROM users`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]*User{}
	for rows.Next() {
		var u User
		if err := rows.Scan(&u.ID, &u.Username, &u.PasswordHash, &u.PatronID, &u.Role, &u.CreatedAt); err != nil {
			return nil, err
		}
		out[u.PatronID] = &u
	}
	return out, rows.Err()
}

// LoanTrendRange 区间内借出/归还按日聚合（单条 GROUP BY，替代逐日 COUNT）。
// 日期为 VARCHAR 'YYYY-MM-DD'，字符串比较与 GROUP BY 在 SQLite/MySQL 下行为一致。
func (s *Store) LoanTrendRange(from, to string) (map[string]TrendPoint, error) {
	out := map[string]TrendPoint{}
	rows, err := s.DB.Query(`SELECT checkout_date, COUNT(*) FROM loans
		WHERE checkout_date BETWEEN ? AND ? GROUP BY checkout_date`, from, to)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var d string
		var n int
		if err := rows.Scan(&d, &n); err != nil {
			rows.Close()
			return nil, err
		}
		p := out[d]
		p.Date = d
		p.Checkout = n
		out[d] = p
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows, err = s.DB.Query(`SELECT checkin_date, COUNT(*) FROM loans
		WHERE checkin_date BETWEEN ? AND ? GROUP BY checkin_date`, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var d string
		var n int
		if err := rows.Scan(&d, &n); err != nil {
			return nil, err
		}
		p := out[d]
		p.Date = d
		p.Return = n
		out[d] = p
	}
	return out, rows.Err()
}
