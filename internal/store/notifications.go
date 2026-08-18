package store

// Notification 系统通知（预约到书、逾期提醒等）。
type Notification struct {
	ID        int64  `json:"id"`
	PatronID  int64  `json:"patron_id"`
	Type      string `json:"type"`  // hold_ready / overdue / fine / system
	Title     string `json:"title"` // 短标题
	Body      string `json:"body"`
	Read      bool   `json:"read"`
	RefID     int64  `json:"ref_id"` // 业务引用（到期提醒=loan_id，去重用）
	CreatedAt string `json:"created_at"`
}

// CreateNotification 创建一条通知。
func (s *Store) CreateNotification(n *Notification) (int64, error) {
	res, err := s.DB.Exec(`INSERT INTO notifications(patron_id,type,title,body,is_read,ref_id,created_at)
		VALUES(?,?,?,?,0,?,?)`, n.PatronID, n.Type, n.Title, n.Body, n.RefID, n.CreatedAt)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// ListNotifications 读者通知列表（最新在前）。
func (s *Store) ListNotifications(patronID int64, limit int) ([]Notification, error) {
	rows, err := s.DB.Query(`SELECT id,patron_id,type,title,body,is_read,ref_id,created_at
		FROM notifications WHERE patron_id=? ORDER BY id DESC LIMIT ?`, patronID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Notification{}
	for rows.Next() {
		var n Notification
		if err := rows.Scan(&n.ID, &n.PatronID, &n.Type, &n.Title, &n.Body, &n.Read, &n.RefID, &n.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// UnreadNotificationCount 未读通知数。
func (s *Store) UnreadNotificationCount(patronID int64) (int, error) {
	var n int
	err := s.DB.QueryRow(`SELECT COUNT(*) FROM notifications WHERE patron_id=? AND is_read=0`, patronID).Scan(&n)
	return n, err
}

// MarkAllNotificationsRead 全部标记已读。
func (s *Store) MarkAllNotificationsRead(patronID int64) error {
	_, err := s.DB.Exec(`UPDATE notifications SET is_read=1 WHERE patron_id=? AND is_read=0`, patronID)
	return err
}
