package store

import "time"

// ---- 定时任务与审计支撑 ----

// timeParse 解析 YYYY-MM-DD。
func timeParse(d string) (time.Time, error) {
	return time.Parse("2006-01-02", d)
}

// DueLoan 到期/逾期借阅视图（提醒任务用）。
type DueLoan struct {
	LoanID   int64  `json:"loan_id"`
	PatronID int64  `json:"patron_id"`
	Title    string `json:"title"`
	DueDate  string `json:"due_date"`
	Overdue  bool   `json:"overdue"`
}

// DueLoans 扫描应还日期 <= 今天（含）的在借记录，标记是否逾期。
func (s *Store) DueLoans(today string) ([]DueLoan, error) {
	rows, err := s.DB.Query(`SELECT l.id, l.patron_id, b.title, l.due_date
		FROM loans l
		JOIN items i ON i.id = l.item_id
		JOIN biblios b ON b.id = i.biblio_id
		WHERE l.status='active' AND l.due_date <= ? ORDER BY l.due_date`, today)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DueLoan{}
	for rows.Next() {
		var d DueLoan
		if err := rows.Scan(&d.LoanID, &d.PatronID, &d.Title, &d.DueDate); err != nil {
			return nil, err
		}
		d.Overdue = d.DueDate < today
		out = append(out, d)
	}
	return out, rows.Err()
}

// NotificationExistsByRef 是否存在指定类型 + ref_id 的通知（去重）。
func (s *Store) NotificationExistsByRef(typ string, refID int64) (bool, error) {
	var n int
	err := s.DB.QueryRow(`SELECT COUNT(*) FROM notifications WHERE type=? AND ref_id=?`, typ, refID).Scan(&n)
	return n > 0, err
}

// InsertLoginLog 记录登录日志。
func (s *Store) InsertLoginLog(userID int64, username, ip string, success bool) error {
	succ := 0
	if success {
		succ = 1
	}
	_, err := s.DB.Exec(`INSERT INTO login_logs(user_id,username,ip,success,created_at) VALUES(?,?,?,?,?)`,
		userID, username, ip, succ, NowDateTime())
	return err
}

// LoginLog 登录日志视图。
type LoginLog struct {
	ID        int64  `json:"id"`
	Username  string `json:"username"`
	IP        string `json:"ip"`
	Success   bool   `json:"success"`
	CreatedAt string `json:"created_at"`
}

// ListLoginLogs 登录日志分页（最新在前）。
func (s *Store) ListLoginLogs(page, size int) ([]LoginLog, int, error) {
	var total int
	_ = s.DB.QueryRow(`SELECT COUNT(*) FROM login_logs`).Scan(&total)
	offset := (page - 1) * size
	rows, err := s.DB.Query(`SELECT id,username,ip,success,created_at FROM login_logs ORDER BY id DESC LIMIT ? OFFSET ?`, size, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []LoginLog{}
	for rows.Next() {
		var l LoginLog
		var succ int
		if err := rows.Scan(&l.ID, &l.Username, &l.IP, &succ, &l.CreatedAt); err != nil {
			return nil, 0, err
		}
		l.Success = succ == 1
		out = append(out, l)
	}
	return out, total, rows.Err()
}

// TrendPoint 借阅趋势点（某日借出/归还数）。
type TrendPoint struct {
	Date     string `json:"date"`
	Checkout int    `json:"checkout"`
	Return   int    `json:"return"`
}

// LoanTrend 近 N 日借出/归还趋势（按 checkout_date / checkin_date 统计）。
func (s *Store) LoanTrend(days int, today string) ([]TrendPoint, error) {
	from, _ := timeParse(today)
	from = from.AddDate(0, 0, -(days - 1))
	out := []TrendPoint{}
	for i := 0; i < days; i++ {
		d := from.AddDate(0, 0, i).Format("2006-01-02")
		var c, r int
		_ = s.DB.QueryRow(`SELECT COUNT(*) FROM loans WHERE checkout_date=?`, d).Scan(&c)
		_ = s.DB.QueryRow(`SELECT COUNT(*) FROM loans WHERE checkin_date=?`, d).Scan(&r)
		out = append(out, TrendPoint{Date: d, Checkout: c, Return: r})
	}
	return out, nil
}

// CategoryStat 热门分类统计。
type CategoryStat struct {
	Category string `json:"category"`
	Count    int    `json:"count"`
}

// TopCategories 按分类统计借出次数 TopN（subjects 首词）。
func (s *Store) TopCategories(limit int) ([]CategoryStat, error) {
	rows, err := s.DB.Query(`SELECT CASE WHEN instr(b.subjects, ',') > 0 THEN substr(b.subjects, 1, instr(b.subjects, ',') - 1) ELSE b.subjects END AS cat, COUNT(*) AS cnt
		FROM loans l
		JOIN items i ON i.id = l.item_id
		JOIN biblios b ON b.id = i.biblio_id
		WHERE b.subjects != ''
		GROUP BY cat ORDER BY cnt DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []CategoryStat{}
	for rows.Next() {
		var c CategoryStat
		if err := rows.Scan(&c.Category, &c.Count); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
