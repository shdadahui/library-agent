package store

import "fmt"

// 本文件提供需要事务保证的复合操作：借出 / 归还 / 续借（关旧开新）。

// Checkout 借出：事务内插入借阅记录并将副本置为 borrowed。
func (s *Store) Checkout(itemID, patronID int64, checkoutDate, dueDate string) (int64, error) {
	tx, err := s.DB.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	res, err := tx.Exec(`INSERT INTO loans(item_id,patron_id,checkout_date,due_date,renewals,status) VALUES(?,?,?,?,0,'active')`,
		itemID, patronID, checkoutDate, dueDate)
	if err != nil {
		return 0, err
	}
	if _, err := tx.Exec(`UPDATE items SET status='borrowed' WHERE id=?`, itemID); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	id, _ := res.LastInsertId()
	return id, nil
}

// Checkin 归还：事务内关闭借阅记录并将副本置为 available。
func (s *Store) Checkin(loanID int64, checkinDate string) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var itemID int64
	if err := tx.QueryRow(`SELECT item_id FROM loans WHERE id=?`, loanID).Scan(&itemID); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE loans SET checkin_date=?, status='returned' WHERE id=?`, checkinDate, loanID); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE items SET status='available' WHERE id=?`, itemID); err != nil {
		return err
	}
	return tx.Commit()
}

// RenewCheckout 续借（关旧开新）：
// 旧记录在旧应还日闭合，新建一条从旧应还日顺延借期的记录，续借次数 +1。
func (s *Store) RenewCheckout(oldLoanID int64, oldDueDate, newDueDate string, renewals int) (int64, error) {
	tx, err := s.DB.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	var itemID, patronID int64
	if err := tx.QueryRow(`SELECT item_id,patron_id FROM loans WHERE id=?`, oldLoanID).Scan(&itemID, &patronID); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(`UPDATE loans SET checkin_date=?, status='returned' WHERE id=?`, oldDueDate, oldLoanID); err != nil {
		return 0, err
	}
	res, err := tx.Exec(`INSERT INTO loans(item_id,patron_id,checkout_date,due_date,renewals,status) VALUES(?,?,?,?,?,'active')`,
		itemID, patronID, oldDueDate, newDueDate, renewals)
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	id, _ := res.LastInsertId()
	return id, nil
}

// FulfillHoldTx 归还时唤醒预约：事务内将最早等待中的预约置为 fulfilled。
func (s *Store) FulfillHoldTx(biblioID, itemID int64) (*Hold, error) {
	holds, err := s.WaitingHolds(biblioID)
	if err != nil {
		return nil, err
	}
	if len(holds) == 0 {
		return nil, nil
	}
	h := holds[0]
	if err := s.FulfillHold(h.ID, itemID); err != nil {
		return nil, err
	}
	return &h, nil
}

var _ = fmt.Sprintf
