package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// ---- 实体定义 ----

// Biblio 书目记录（一部作品的元信息）。
type Biblio struct {
	ID          int64  `json:"id"`
	Title       string `json:"title"`
	Author      string `json:"author"`
	ISBN        string `json:"isbn,omitempty"`
	Publisher   string `json:"publisher,omitempty"`
	PublishYear int    `json:"publish_year"`
	Subjects    string `json:"subjects,omitempty"`
	Lang        string `json:"lang"`
	CoverID     int64  `json:"cover_id,omitempty"`
	OnlineURL   string `json:"online_url,omitempty"` // 在线阅读地址（Gutendex 全文）
}

// Item 馆藏副本（实体书/条码）。
type Item struct {
	ID               int64  `json:"id"`
	BiblioID         int64  `json:"biblio_id"`
	Barcode          string `json:"barcode"`
	Status           string `json:"status"` // available / borrowed / on_hold / lost
	Location         string `json:"location"`
	LoanDurationDays int    `json:"loan_duration_days"`
}

// Patron 读者。
type Patron struct {
	ID      int64  `json:"id"`
	Name    string `json:"name"`
	Barcode string `json:"barcode"`
	Phone   string `json:"phone,omitempty"`
	Status  string `json:"status"` // active / disabled
	Vip     int    `json:"vip"`    // 0/1 VIP 会员
}

// Loan 流通记录（借阅）。
type Loan struct {
	ID           int64   `json:"id"`
	ItemID       int64   `json:"item_id"`
	PatronID     int64   `json:"patron_id"`
	CheckoutDate string  `json:"checkout_date"`
	DueDate      string  `json:"due_date"`
	CheckinDate  *string `json:"checkin_date,omitempty"`
	Renewals     int     `json:"renewals"`
	Status       string  `json:"status"` // active / returned
}

// Fine 罚款。
type Fine struct {
	ID          int64  `json:"id"`
	PatronID    int64  `json:"patron_id"`
	LoanID      int64  `json:"loan_id"`
	AmountCents int    `json:"amount_cents"`
	CreatedDate string `json:"created_date"`
	Paid        bool   `json:"paid"`
}

// Hold 预约。
type Hold struct {
	ID        int64  `json:"id"`
	BiblioID  int64  `json:"biblio_id"`
	PatronID  int64  `json:"patron_id"`
	ItemID    *int64 `json:"item_id,omitempty"`
	QueuePos  int    `json:"queue_pos"`
	Status    string `json:"status"` // waiting / fulfilled / cancelled
	CreatedAt string `json:"created_at"`
}

// ---- 书目 ----

// SearchBooks 按书名/作者/主题模糊检索；lang 非空时过滤语种。
func (s *Store) SearchBooks(q, lang string, limit int) ([]Biblio, error) {
	if limit <= 0 {
		limit = 50
	}
	clauses := []string{"1=1"}
	args := []any{}
	if q != "" {
		like := "%" + normalizeQuery(q) + "%"
		clauses = append(clauses, "(title LIKE ? OR author LIKE ? OR subjects LIKE ?)")
		args = append(args, like, like, like)
	}
	if lang != "" {
		clauses = append(clauses, "lang = ?")
		args = append(args, lang)
	}
	args = append(args, limit)
	rows, err := s.DB.Query(`
		SELECT id,title,author,isbn,publisher,publish_year,subjects,lang,cover_id,online_url
		FROM biblios WHERE `+strings.Join(clauses, " AND ")+`
		ORDER BY id LIMIT ?`, args...)
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

// GetBiblio 按 ID 取书目。
func (s *Store) GetBiblio(id int64) (*Biblio, error) {
	row := s.DB.QueryRow(`SELECT id,title,author,isbn,publisher,publish_year,subjects,lang,cover_id,online_url FROM biblios WHERE id=?`, id)
	var b Biblio
	if err := row.Scan(&b.ID, &b.Title, &b.Author, &b.ISBN, &b.Publisher, &b.PublishYear, &b.Subjects, &b.Lang, &b.CoverID, &b.OnlineURL); err != nil {
		return nil, err
	}
	return &b, nil
}

// GetBiblioByTitle 按书名取书目（seed 幂等用，LIMIT 1 兼容 MySQL 严格模式）。
func (s *Store) GetBiblioByTitle(title string) (*Biblio, error) {
	row := s.DB.QueryRow(`SELECT id,title,author,isbn,publisher,publish_year,subjects,lang,cover_id,online_url FROM biblios WHERE title=? LIMIT 1`, title)
	var b Biblio
	if err := row.Scan(&b.ID, &b.Title, &b.Author, &b.ISBN, &b.Publisher, &b.PublishYear, &b.Subjects, &b.Lang, &b.CoverID, &b.OnlineURL); err != nil {
		return nil, err
	}
	return &b, nil
}

// UpdateBiblioOnlineURL 回填在线阅读地址（seed 幂等更新用）。
func (s *Store) UpdateBiblioOnlineURL(id int64, url string) error {
	_, err := s.DB.Exec(`UPDATE biblios SET online_url=? WHERE id=?`, url, id)
	return err
}

// InsertBiblio 插入书目，返回新 ID。
func (s *Store) InsertBiblio(b *Biblio) (int64, error) {
	res, err := s.DB.Exec(`
		INSERT INTO biblios(title,author,isbn,publisher,publish_year,subjects,lang,cover_id,online_url)
		VALUES(?,?,?,?,?,?,?,?,?)`,
		b.Title, b.Author, b.ISBN, b.Publisher, b.PublishYear, b.Subjects, b.Lang, b.CoverID, b.OnlineURL)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// CountBiblios 书目总数。
func (s *Store) CountBiblios() (int, error) {
	var n int
	err := s.DB.QueryRow(`SELECT COUNT(*) FROM biblios`).Scan(&n)
	return n, err
}

// ListBooks 全部书目（分页，管理员用）。
func (s *Store) ListBooks(limit, offset int) ([]Biblio, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.DB.Query(`SELECT id,title,author,isbn,publisher,publish_year,subjects,lang,cover_id,online_url FROM biblios ORDER BY id LIMIT ? OFFSET ?`, limit, offset)
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

// ---- 馆藏副本 ----

// ListItems 列出某书目的全部副本。
func (s *Store) ListItems(biblioID int64) ([]Item, error) {
	rows, err := s.DB.Query(`SELECT id,biblio_id,barcode,status,location,loan_duration_days FROM items WHERE biblio_id=? ORDER BY id`, biblioID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Item{}
	for rows.Next() {
		var it Item
		if err := rows.Scan(&it.ID, &it.BiblioID, &it.Barcode, &it.Status, &it.Location, &it.LoanDurationDays); err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// GetItem 按 ID 取副本。
func (s *Store) GetItem(id int64) (*Item, error) {
	row := s.DB.QueryRow(`SELECT id,biblio_id,barcode,status,location,loan_duration_days FROM items WHERE id=?`, id)
	var it Item
	if err := row.Scan(&it.ID, &it.BiblioID, &it.Barcode, &it.Status, &it.Location, &it.LoanDurationDays); err != nil {
		return nil, err
	}
	return &it, nil
}

// UpdateItemStatus 更新副本状态。
func (s *Store) UpdateItemStatus(id int64, status string) error {
	_, err := s.DB.Exec(`UPDATE items SET status=? WHERE id=?`, status, id)
	return err
}

// InsertItem 插入副本，返回新 ID。
func (s *Store) InsertItem(it *Item) (int64, error) {
	res, err := s.DB.Exec(`INSERT INTO items(biblio_id,barcode,status,location,loan_duration_days) VALUES(?,?,?,?,?)`,
		it.BiblioID, it.Barcode, it.Status, it.Location, it.LoanDurationDays)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// ---- 读者 ----

// ListPatrons 全部读者。
func (s *Store) ListPatrons() ([]Patron, error) {
	rows, err := s.DB.Query(`SELECT id,name,barcode,phone,status,vip FROM patrons ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Patron{}
	for rows.Next() {
		var p Patron
		if err := rows.Scan(&p.ID, &p.Name, &p.Barcode, &p.Phone, &p.Status, &p.Vip); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// GetPatron 按 ID 取读者。
func (s *Store) GetPatron(id int64) (*Patron, error) {
	row := s.DB.QueryRow(`SELECT id,name,barcode,phone,status,vip FROM patrons WHERE id=?`, id)
	var p Patron
	if err := row.Scan(&p.ID, &p.Name, &p.Barcode, &p.Phone, &p.Status, &p.Vip); err != nil {
		return nil, err
	}
	return &p, nil
}

// InsertPatron 插入读者。
func (s *Store) InsertPatron(p *Patron) (int64, error) {
	res, err := s.DB.Exec(`INSERT INTO patrons(name,barcode,phone) VALUES(?,?,?)`, p.Name, p.Barcode, p.Phone)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// GetPatronByBarcode 按读者证号取读者（seed 幂等用）。
func (s *Store) GetPatronByBarcode(barcode string) (*Patron, error) {
	row := s.DB.QueryRow(`SELECT id,name,barcode,phone,status,vip FROM patrons WHERE barcode=?`, barcode)
	var p Patron
	if err := row.Scan(&p.ID, &p.Name, &p.Barcode, &p.Phone, &p.Status, &p.Vip); err != nil {
		return nil, err
	}
	return &p, nil
}

// ---- 流通记录 ----

// CreateLoan 创建借阅记录。
func (s *Store) CreateLoan(l *Loan) (int64, error) {
	res, err := s.DB.Exec(`INSERT INTO loans(item_id,patron_id,checkout_date,due_date,renewals,status) VALUES(?,?,?,?,?,?)`,
		l.ItemID, l.PatronID, l.CheckoutDate, l.DueDate, l.Renewals, "active")
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// GetLoan 按 ID 取流通记录。
func (s *Store) GetLoan(id int64) (*Loan, error) {
	row := s.DB.QueryRow(`SELECT id,item_id,patron_id,checkout_date,due_date,checkin_date,renewals,status FROM loans WHERE id=?`, id)
	var l Loan
	var ci sql.NullString
	if err := row.Scan(&l.ID, &l.ItemID, &l.PatronID, &l.CheckoutDate, &l.DueDate, &ci, &l.Renewals, &l.Status); err != nil {
		return nil, err
	}
	if ci.Valid {
		l.CheckinDate = &ci.String
	}
	return &l, nil
}

// ActiveLoanByItem 取某副本当前生效的借阅记录。
func (s *Store) ActiveLoanByItem(itemID int64) (*Loan, error) {
	row := s.DB.QueryRow(`SELECT id,item_id,patron_id,checkout_date,due_date,checkin_date,renewals,status FROM loans WHERE item_id=? AND status='active'`, itemID)
	var l Loan
	var ci sql.NullString
	if err := row.Scan(&l.ID, &l.ItemID, &l.PatronID, &l.CheckoutDate, &l.DueDate, &ci, &l.Renewals, &l.Status); err != nil {
		return nil, err
	}
	if ci.Valid {
		l.CheckinDate = &ci.String
	}
	return &l, nil
}

// ActiveLoans 读者的全部在借记录。
func (s *Store) ActiveLoans(patronID int64) ([]Loan, error) {
	return s.queryLoans(`WHERE patron_id=? AND status='active' ORDER BY due_date`, patronID)
}

// LoanHistory 读者全部借阅历史。
func (s *Store) LoanHistory(patronID int64) ([]Loan, error) {
	return s.queryLoans(`WHERE patron_id=? ORDER BY checkout_date DESC`, patronID)
}

func (s *Store) queryLoans(where string, args ...any) ([]Loan, error) {
	rows, err := s.DB.Query(`SELECT id,item_id,patron_id,checkout_date,due_date,checkin_date,renewals,status FROM loans `+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Loan{}
	for rows.Next() {
		var l Loan
		var ci sql.NullString
		if err := rows.Scan(&l.ID, &l.ItemID, &l.PatronID, &l.CheckoutDate, &l.DueDate, &ci, &l.Renewals, &l.Status); err != nil {
			return nil, err
		}
		if ci.Valid {
			l.CheckinDate = &ci.String
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// CloseLoan 关闭借阅记录（归还或续借时的旧记录）。
func (s *Store) CloseLoan(id int64, checkinDate string) error {
	_, err := s.DB.Exec(`UPDATE loans SET checkin_date=?, status='returned' WHERE id=?`, checkinDate, id)
	return err
}

// ---- 罚款 ----

// CreateFine 创建罚款。
func (s *Store) CreateFine(f *Fine) (int64, error) {
	paid := 0
	if f.Paid {
		paid = 1
	}
	res, err := s.DB.Exec(`INSERT INTO fines(patron_id,loan_id,amount_cents,created_date,paid) VALUES(?,?,?,?,?)`,
		f.PatronID, f.LoanID, f.AmountCents, f.CreatedDate, paid)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// Fines 读者罚款；unpaidOnly 时只取未缴。
func (s *Store) Fines(patronID int64, unpaidOnly bool) ([]Fine, error) {
	q := `SELECT id,patron_id,loan_id,amount_cents,created_date,paid FROM fines WHERE patron_id=?`
	args := []any{patronID}
	if unpaidOnly {
		q += ` AND paid=0`
	}
	q += ` ORDER BY created_date DESC`
	rows, err := s.DB.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Fine{}
	for rows.Next() {
		var f Fine
		var paid int
		if err := rows.Scan(&f.ID, &f.PatronID, &f.LoanID, &f.AmountCents, &f.CreatedDate, &paid); err != nil {
			return nil, err
		}
		f.Paid = paid == 1
		out = append(out, f)
	}
	return out, rows.Err()
}

// SumUnpaidFines 未缴罚款总额（分）。
func (s *Store) SumUnpaidFines(patronID int64) (int, error) {
	var n int
	err := s.DB.QueryRow(`SELECT COALESCE(SUM(amount_cents),0) FROM fines WHERE patron_id=? AND paid=0`, patronID).Scan(&n)
	return n, err
}

// ---- 预约 ----

// CreateHold 创建预约，queue_pos 自动取该书目下一个位置。
func (s *Store) CreateHold(h *Hold) (int64, error) {
	var pos int
	if err := s.DB.QueryRow(`SELECT COALESCE(MAX(queue_pos),0)+1 FROM holds WHERE biblio_id=?`, h.BiblioID).Scan(&pos); err != nil {
		return 0, err
	}
	h.QueuePos = pos
	res, err := s.DB.Exec(`INSERT INTO holds(biblio_id,patron_id,item_id,queue_pos,status,created_at) VALUES(?,?,?,?,?,?)`,
		h.BiblioID, h.PatronID, h.ItemID, h.QueuePos, "waiting", h.CreatedAt)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// WaitingHolds 某书目等待中的预约队列（FIFO）。
func (s *Store) WaitingHolds(biblioID int64) ([]Hold, error) {
	rows, err := s.DB.Query(`SELECT id,biblio_id,patron_id,item_id,queue_pos,status,created_at FROM holds WHERE biblio_id=? AND status='waiting' ORDER BY queue_pos`, biblioID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanHolds(rows)
}

// PatronHolds 读者全部预约。
func (s *Store) PatronHolds(patronID int64) ([]Hold, error) {
	rows, err := s.DB.Query(`SELECT id,biblio_id,patron_id,item_id,queue_pos,status,created_at FROM holds WHERE patron_id=? ORDER BY created_at DESC`, patronID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanHolds(rows)
}

// FulfillHold 将预约置为 fulfilled 并绑定归还的副本。
func (s *Store) FulfillHold(holdID int64, itemID int64) error {
	_, err := s.DB.Exec(`UPDATE holds SET status='fulfilled', item_id=? WHERE id=?`, itemID, holdID)
	return err
}

func scanHolds(rows interface {
	Next() bool
	Scan(...any) error
}) ([]Hold, error) {
	out := []Hold{}
	for rows.Next() {
		var h Hold
		var itemID sql.NullInt64
		if err := rows.Scan(&h.ID, &h.BiblioID, &h.PatronID, &itemID, &h.QueuePos, &h.Status, &h.CreatedAt); err != nil {
			return nil, err
		}
		if itemID.Valid {
			h.ItemID = &itemID.Int64
		}
		out = append(out, h)
	}
	return out, nil
}

var _ = fmt.Sprintf // 保留 fmt 导入以备扩展

// normalizeQuery 检索词归一化：半角罗马数字→全角、半角点/间隔符→·、去书名号。
// 解决 LLM 将「三体Ⅱ」写成「三体II」导致的检索失败。
func normalizeQuery(q string) string {
	r := strings.NewReplacer(
		"III", "Ⅲ", "II", "Ⅱ", "IV", "Ⅳ", "I", "Ⅰ",
		".", "·", "・", "·", "《", "", "》", "",
	)
	return strings.TrimSpace(r.Replace(q))
}

// UpdateItemsLocation 回填某书目全部副本的馆藏位置（seed 幂等迁移用）。
func (s *Store) UpdateItemsLocation(biblioID int64, loc string) error {
	_, err := s.DB.Exec(`UPDATE items SET location=? WHERE biblio_id=?`, loc, biblioID)
	return err
}

// CancelHoldByID 取消预约：仅等待中的本人预约可取消（fulfilled 已唤醒不可取消）。
func (s *Store) CancelHoldByID(patronID, holdID int64) error {
	res, err := s.DB.Exec(`UPDATE holds SET status='cancelled' WHERE id=? AND patron_id=? AND status='waiting'`, holdID, patronID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrHoldNotCancelable
	}
	return nil
}

// ErrHoldNotCancelable 预约不可取消（不存在/非本人/已唤醒）。
var ErrHoldNotCancelable = errors.New("预约不存在或不可取消")

// GetItemByBarcode 按条码查副本。
func (s *Store) GetItemByBarcode(barcode string) (*Item, error) {
	var it Item
	var loc, status string
	err := s.DB.QueryRow(`SELECT id,biblio_id,barcode,status,location,loan_duration_days FROM items WHERE barcode=?`, barcode).
		Scan(&it.ID, &it.BiblioID, &it.Barcode, &status, &loc, &it.LoanDurationDays)
	if err != nil {
		return nil, err
	}
	it.Status = status
	it.Location = loc
	return &it, nil
}

// ErrItemNotAvailable 副本不可删除（在借或状态异常）。
var ErrItemNotAvailable = errors.New("副本不可删除（存在在借或状态异常）")
