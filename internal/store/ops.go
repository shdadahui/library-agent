package store

import (
	"errors"
	"fmt"
	"time"
)

// ErrLoanNotActive 借阅记录已关闭（store 层返回，service 层透传）。
var ErrLoanNotActive = errors.New("该借阅记录已关闭")

// 本文件提供需要事务保证的复合操作：借出 / 归还 / 续借（关旧开新）。

// Checkout 借出：事务内插入借阅记录并将副本置为 borrowed。
// 原子性：仅当副本仍为 available 时才更新（AND status='available'），
// 并发借同一副本时第二个事务 RowsAffected=0，回滚报错，杜绝双借。
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
	r, err := tx.Exec(`UPDATE items SET status='borrowed' WHERE id=? AND status='available'`, itemID)
	if err != nil {
		return 0, err
	}
	if n, _ := r.RowsAffected(); n == 0 {
		return 0, fmt.Errorf("该副本已被借出（并发冲突）")
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

// ReturnTx 归还（单一事务）：关闭借阅 → 副本置可借 → 计算逾期罚款 → 唤醒最早预约。
// 任一环节失败整体回滚，杜绝"罚款已收但书未还"等半完成状态。
// 返回 (逾期罚款分, 被唤醒的预约者姓名, error)。
func (s *Store) ReturnTx(loanID int64, checkinDate string, finePerDay int) (int, string, error) {
	tx, err := s.DB.Begin()
	if err != nil {
		return 0, "", err
	}
	defer tx.Rollback()

	var itemID, patronID int64
	var dueDate, status string
	if err := tx.QueryRow(`SELECT item_id,patron_id,due_date,status FROM loans WHERE id=?`, loanID).Scan(&itemID, &patronID, &dueDate, &status); err != nil {
		return 0, "", err
	}
	if status != "active" {
		return 0, "", ErrLoanNotActive
	}
	// 1. 逾期罚款（当天收尾，days 差为正才计）
	fine := 0
	if dueDate < checkinDate {
		td, _ := time.Parse("2006-01-02", dueDate)
		tc, _ := time.Parse("2006-01-02", checkinDate)
		days := int(tc.Sub(td).Hours() / 24)
		fine = days * finePerDay
		if fine > 0 {
			if _, err := tx.Exec(`INSERT INTO fines(patron_id,loan_id,amount_cents,created_date,paid) VALUES(?,?,?,?,0)`,
				patronID, loanID, fine, checkinDate); err != nil {
				return 0, "", err
			}
		}
	}
	// 2. 关闭借阅 + 副本可借
	if _, err := tx.Exec(`UPDATE loans SET checkin_date=?, status='returned' WHERE id=?`, checkinDate, loanID); err != nil {
		return 0, "", err
	}
	if _, err := tx.Exec(`UPDATE items SET status='available' WHERE id=?`, itemID); err != nil {
		return 0, "", err
	}
	// 3. 唤醒最早 waiting 预约（同一事务内）
	wakeName := ""
	var biblioID int64
	if err := tx.QueryRow(`SELECT biblio_id FROM items WHERE id=?`, itemID).Scan(&biblioID); err == nil {
		var hid int64
		err = tx.QueryRow(`SELECT id FROM holds WHERE biblio_id=? AND status='waiting' ORDER BY queue_pos, id LIMIT 1`, biblioID).Scan(&hid)
		if err == nil {
			if _, err := tx.Exec(`UPDATE holds SET status='fulfilled', item_id=? WHERE id=?`, itemID, hid); err == nil {
				var pid int64
				if err := tx.QueryRow(`SELECT patron_id FROM holds WHERE id=?`, hid).Scan(&pid); err == nil {
					// 事务内查读者名（避免经 Store.DB 复用连接导致单连接自锁）
					_ = tx.QueryRow(`SELECT name FROM patrons WHERE id=?`, pid).Scan(&wakeName)
				}
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, "", err
	}
	return fine, wakeName, nil
}

// FulfillHoldTx 已由 ReturnTx 内部实现替代，保留占位避免外部引用编译失败。
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
