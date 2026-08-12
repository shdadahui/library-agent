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
