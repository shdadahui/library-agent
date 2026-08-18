package store

import (
	"fmt"
)

// ---- 管理端查询/操作（书目管理、借阅记录、读者管理） ----

// BiblioWithCopies 书目 + 副本统计（管理表格用）。
type BiblioWithCopies struct {
	Biblio
	TotalCopies  int `json:"total_copies"`
	AvailCopies  int `json:"avail_copies"`
	ActiveLoans  int `json:"active_loans"`
	WaitingHolds int `json:"waiting_holds"`
}

// ListBooksPaged 书目分页 + 关键词搜索（书名/作者/ISBN）。
func (s *Store) ListBooksPaged(q string, page, size int) ([]BiblioWithCopies, int, error) {
	where, args := "", []any{}
	if q != "" {
		where = " WHERE b.title LIKE ? OR b.author LIKE ? OR b.isbn LIKE ?"
		like := "%" + q + "%"
		args = []any{like, like, like}
	}
	var total int
	if err := s.DB.QueryRow(`SELECT COUNT(*) FROM biblios b`+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	offset := (page - 1) * size
	sql := `SELECT b.id,b.title,b.author,b.isbn,b.publisher,b.publish_year,b.subjects,b.lang,COALESCE(b.cover_id,0),b.online_url,
		(SELECT COUNT(*) FROM items i WHERE i.biblio_id=b.id),
		(SELECT COUNT(*) FROM items i WHERE i.biblio_id=b.id AND i.status='available'),
		(SELECT COUNT(*) FROM loans l JOIN items i ON i.id=l.item_id WHERE i.biblio_id=b.id AND l.status='active'),
		(SELECT COUNT(*) FROM holds h WHERE h.biblio_id=b.id AND h.status='waiting')
		FROM biblios b` + where + ` ORDER BY b.id DESC LIMIT ? OFFSET ?`
	allArgs := append(args, size, offset)
	rows, err := s.DB.Query(sql, allArgs...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []BiblioWithCopies{}
	for rows.Next() {
		var b BiblioWithCopies
		if err := rows.Scan(&b.ID, &b.Title, &b.Author, &b.ISBN, &b.Publisher, &b.PublishYear, &b.Subjects, &b.Lang, &b.CoverID, &b.OnlineURL,
			&b.TotalCopies, &b.AvailCopies, &b.ActiveLoans, &b.WaitingHolds); err != nil {
			return nil, 0, err
		}
		out = append(out, b)
	}
	return out, total, rows.Err()
}

// UpdateBiblioFields 编辑书目。
func (s *Store) UpdateBiblioFields(id int64, b *Biblio) error {
	_, err := s.DB.Exec(`UPDATE biblios SET title=?,author=?,isbn=?,publisher=?,publish_year=?,subjects=? WHERE id=?`,
		b.Title, b.Author, b.ISBN, b.Publisher, b.PublishYear, b.Subjects, id)
	return err
}

// BiblioActiveLoanCount 某书目在借副本数（删除校验用）。
func (s *Store) BiblioActiveLoanCount(biblioID int64) (int, error) {
	var n int
	err := s.DB.QueryRow(`SELECT COUNT(*) FROM loans l JOIN items i ON i.id=l.item_id WHERE i.biblio_id=? AND l.status='active'`, biblioID).Scan(&n)
	return n, err
}

// BiblioWaitingHoldCount 某书目等待预约数。
func (s *Store) BiblioWaitingHoldCount(biblioID int64) (int, error) {
	var n int
	err := s.DB.QueryRow(`SELECT COUNT(*) FROM holds WHERE biblio_id=? AND status='waiting'`, biblioID).Scan(&n)
	return n, err
}

// DeleteBiblioAndItems 删除书目及其全部副本（调用前须校验无在借/预约）。
func (s *Store) DeleteBiblioAndItems(biblioID int64) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM items WHERE biblio_id=?`, biblioID); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM holds WHERE biblio_id=?`, biblioID); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM biblios WHERE id=?`, biblioID); err != nil {
		return err
	}
	return tx.Commit()
}

// AddCopies 为书目新增 n 个副本，返回新增副本条码。
func (s *Store) AddCopies(biblioID int64, n int) ([]string, error) {
	// 取当前最大副本序号（条码 LIB-%05d-%d 的 %d 部分）
	var maxSeq int
	_ = s.DB.QueryRow(`SELECT COALESCE(MAX(CAST(SUBSTRING_INDEX(barcode,'-',-1) AS UNSIGNED)),0) FROM items WHERE biblio_id=?`, biblioID).Scan(&maxSeq)
	out := []string{}
	for i := 1; i <= n; i++ {
		seq := maxSeq + i
		barcode := fmt.Sprintf("LIB-%05d-%d", biblioID, seq)
		if _, err := s.DB.Exec(`INSERT INTO items(biblio_id,barcode,status,location,loan_duration_days) VALUES(?,?,'available',?,14)`,
			biblioID, barcode, "总馆"); err != nil {
			return out, err
		}
		out = append(out, barcode)
	}
	return out, nil
}

// DeleteItemSafe 删除单个副本（调用前须校验无在借）。
func (s *Store) DeleteItemSafe(itemID int64) error {
	res, err := s.DB.Exec(`DELETE FROM items WHERE id=? AND status='available'`, itemID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrItemNotAvailable
	}
	return nil
}

// LoanRecord 借阅记录视图（管理表格用）。
type LoanRecord struct {
	ID            int64  `json:"id"`
	BookTitle     string `json:"title"`
	Barcode       string `json:"barcode"`
	PatronName    string `json:"patron_name"`
	PatronBarcode string `json:"patron_barcode"`
	CheckoutDate  string `json:"checkout_date"`
	DueDate       string `json:"due_date"`
	CheckinDate   string `json:"checkin_date,omitempty"`
	Renewals      int    `json:"renewals"`
	Status        string `json:"status"`
}

// ListLoansPaged 借阅记录分页 + 筛选（status=active/returned，q=书名/读者）。
func (s *Store) ListLoansPaged(status, q string, page, size int) ([]LoanRecord, int, error) {
	where, args := " WHERE 1=1", []any{}
	if status == "active" {
		where += " AND l.status='active'"
	} else if status == "returned" {
		where += " AND l.status='returned'"
	}
	if q != "" {
		where += " AND (b.title LIKE ? OR p.name LIKE ?)"
		like := "%" + q + "%"
		args = append(args, like, like)
	}
	var total int
	if err := s.DB.QueryRow(`SELECT COUNT(*) FROM loans l JOIN biblios b ON b.id=(SELECT biblio_id FROM items i WHERE i.id=l.item_id) JOIN patrons p ON p.id=l.patron_id`+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	offset := (page - 1) * size
	sql := `SELECT l.id, b.title, i.barcode, p.name, p.barcode, l.checkout_date, l.due_date,
		COALESCE(l.checkin_date,''), l.renewals, l.status
		FROM loans l
		JOIN items i ON i.id=l.item_id
		JOIN biblios b ON b.id=i.biblio_id
		JOIN patrons p ON p.id=l.patron_id` + where + ` ORDER BY l.id DESC LIMIT ? OFFSET ?`
	allArgs := append(args, size, offset)
	rows, err := s.DB.Query(sql, allArgs...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []LoanRecord{}
	for rows.Next() {
		var r LoanRecord
		if err := rows.Scan(&r.ID, &r.BookTitle, &r.Barcode, &r.PatronName, &r.PatronBarcode, &r.CheckoutDate, &r.DueDate, &r.CheckinDate, &r.Renewals, &r.Status); err != nil {
			return nil, 0, err
		}
		out = append(out, r)
	}
	return out, total, rows.Err()
}

// UpdatePatronStatusRole 读者状态/角色管理。
func (s *Store) UpdatePatronStatusRole(patronID int64, status, role string) error {
	if status != "" {
		if _, err := s.DB.Exec(`UPDATE patrons SET status=? WHERE id=?`, status, patronID); err != nil {
			return err
		}
	}
	if role != "" {
		if _, err := s.DB.Exec(`UPDATE users SET role=? WHERE patron_id=?`, role, patronID); err != nil {
			return err
		}
	}
	return nil
}
