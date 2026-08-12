package store

// LibraryStats 全馆统计（用于 get_library_stats 工具）。
type LibraryStats struct {
	Books            int `json:"books"`              // 书目种数
	Copies           int `json:"copies"`             // 馆藏副本总数
	Available        int `json:"available"`          // 可借副本数
	Borrowed         int `json:"borrowed"`           // 在借副本数
	HoldsWaiting     int `json:"holds_waiting"`      // 等待中的预约数
	Patrons          int `json:"patrons"`            // 读者数
	UnpaidFinesCents int `json:"unpaid_fines_cents"` // 全馆未缴罚款总额（分）
}

// TopBorrowed 借阅次数最多的书目（热门榜）。
func (s *Store) TopBorrowed(limit int) ([]struct {
	Biblio
	BorrowCount int `json:"borrow_count"`
}, error) {
	rows, err := s.DB.Query(`
		SELECT b.id, b.title, b.author, b.isbn, b.publisher, b.publish_year, b.subjects, b.lang, b.cover_id, COUNT(l.id) AS cnt
		FROM loans l
		JOIN items i ON l.item_id = i.id
		JOIN biblios b ON i.biblio_id = b.id
		GROUP BY b.id
		ORDER BY cnt DESC, b.id
		LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []struct {
		Biblio
		BorrowCount int `json:"borrow_count"`
	}{}
	for rows.Next() {
		var b Biblio
		var cnt int
		if err := rows.Scan(&b.ID, &b.Title, &b.Author, &b.ISBN, &b.Publisher, &b.PublishYear, &b.Subjects, &b.Lang, &b.CoverID, &cnt); err != nil {
			return nil, err
		}
		out = append(out, struct {
			Biblio
			BorrowCount int `json:"borrow_count"`
		}{Biblio: b, BorrowCount: cnt})
	}
	return out, rows.Err()
}

// LibraryStats 聚合统计。
func (s *Store) LibraryStats() (*LibraryStats, error) {
	st := &LibraryStats{}
	queries := []struct {
		sql string
		dst *int
	}{
		{`SELECT COUNT(*) FROM biblios`, &st.Books},
		{`SELECT COUNT(*) FROM items`, &st.Copies},
		{`SELECT COUNT(*) FROM items WHERE status='available'`, &st.Available},
		{`SELECT COUNT(*) FROM items WHERE status='borrowed'`, &st.Borrowed},
		{`SELECT COUNT(*) FROM holds WHERE status='waiting'`, &st.HoldsWaiting},
		{`SELECT COUNT(*) FROM patrons`, &st.Patrons},
		{`SELECT COALESCE(SUM(amount_cents),0) FROM fines WHERE paid=0`, &st.UnpaidFinesCents},
	}
	for _, q := range queries {
		if err := s.DB.QueryRow(q.sql).Scan(q.dst); err != nil {
			return nil, err
		}
	}
	return st, nil
}
