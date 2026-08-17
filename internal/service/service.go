// Package service 实现图书馆流通业务规则（借/还/续借/罚款/预约队列）。
// 本层只做规则校验与编排，数据原子性由 store 层的事务保证。
package service

import (
	"errors"
	"fmt"
	"time"

	"github.com/shdadahui/library-agent/internal/store"
)

// 业务规则常量。
const (
	MaxActiveLoans  = 5  // 读者同时最多在借 5 本
	MaxRenewals     = 2  // 每本书最多续借 2 次
	FinePerDayCents = 10 // 逾期罚款 0.1 元/天（10 分）
	DefaultLoanDays = 14 // 默认借期 14 天
	DateLayout      = "2006-01-02"
)

// 业务错误（对外直接展示中文消息）。
var (
	ErrPatronNotFound   = errors.New("读者不存在")
	ErrItemNotFound     = errors.New("馆藏副本不存在")
	ErrBiblioNotFound   = errors.New("书目不存在")
	ErrLoanNotFound     = errors.New("借阅记录不存在")
	ErrItemUnavailable  = errors.New("该副本当前不可借出")
	ErrLoanLimitReached = errors.New("同时最多借 5 本，请先归还部分图书")
	ErrOverdue          = errors.New("您有逾期未还的图书，请先归还")
	ErrMaxRenewals      = errors.New("每本书最多续借 2 次")
	ErrHoldPending      = errors.New("该书有读者预约排队，无法续借")
	ErrLoanNotActive    = errors.New("该借阅记录已关闭")
	ErrAlreadyHeld      = errors.New("您已预约过这本书")
	ErrNoAvailableItem  = errors.New("该书全部副本已借出，无法直接借阅，可为您预约")
)

// Service 图书馆业务服务。
type Service struct {
	st *store.Store
}

// New 创建业务服务。
func New(st *store.Store) *Service { return &Service{st: st} }

// Patrons 全部读者（演示身份切换）。
func (s *Service) Patrons() ([]store.Patron, error) { return s.st.ListPatrons() }

// ListAllBooks 全库书目（分页，管理员用）。
func (s *Service) ListAllBooks(limit, offset int) ([]store.Biblio, error) {
	if limit <= 0 {
		limit = 50
	}
	return s.st.ListBooks(limit, offset)
}

// FindUserByPatronID 按读者 ID 找关联登录用户。
func (s *Service) FindUserByPatronID(patronID int64) (*store.User, error) {
	return s.st.GetUserByPatronID(patronID)
}

// Patron 按 ID 取单个读者。
func (s *Service) Patron(id int64) (*store.Patron, error) { return s.st.GetPatron(id) }

// LoanOwner 返回借阅记录所属读者（越权校验用；不存在返回 ErrLoanNotFound）。
func (s *Service) LoanOwner(loanID int64) (int64, error) {
	loan, err := s.st.GetLoan(loanID)
	if err != nil {
		return 0, ErrLoanNotFound
	}
	return loan.PatronID, nil
}

// CountBooks 书目总数。
func (s *Service) CountBooks() (int, error) { return s.st.CountBiblios() }

// LibraryStats 全馆统计。
func (s *Service) LibraryStats() (*store.LibraryStats, error) { return s.st.LibraryStats() }

// ---- 历史会话（conversations / messages） ----

// CreateConversation 新建会话。
func (s *Service) CreateConversation(userID int64) (*store.Conversation, error) {
	now := store.NowDateTime()
	c := &store.Conversation{UserID: userID, Title: "新会话", CreatedAt: now, UpdatedAt: now}
	id, err := s.st.CreateConversation(c)
	if err != nil {
		return nil, err
	}
	return s.st.GetConversation(id)
}

// ListConversations 会话列表。
func (s *Service) ListConversations(userID int64) ([]store.Conversation, error) {
	return s.st.ListConversations(userID)
}

// GetConversation 单个会话。
func (s *Service) GetConversation(id int64) (*store.Conversation, error) {
	return s.st.GetConversation(id)
}

// DeleteConversation 删除会话。
func (s *Service) DeleteConversation(id int64) error { return s.st.DeleteConversation(id) }

// ListMessages 会话消息。
func (s *Service) ListMessages(conversationID int64) ([]store.Message, error) {
	return s.st.ListMessages(conversationID)
}

// AddMessage 追加消息并刷新会话时间。
func (s *Service) AddMessage(conversationID int64, role, content string) error {
	if _, err := s.st.AddMessage(&store.Message{
		ConversationID: conversationID, Role: role, Content: content, CreatedAt: store.NowDateTime(),
	}); err != nil {
		return err
	}
	return s.st.TouchConversation(conversationID)
}

// RenameConversation 以首条用户消息生成会话标题。
func (s *Service) RenameConversation(id int64, firstUserMsg string) error {
	title := firstUserMsg
	if runes := []rune(title); len(runes) > 18 {
		title = string(runes[:18]) + "…"
	}
	return s.st.UpdateConversationTitle(id, title)
}

// SearchBooks 检索书目并附带可借副本数。
type BookSearchResult struct {
	store.Biblio
	Available int `json:"available"`
	Total     int `json:"total"`
}

func (s *Service) SearchBooks(q, lang string, limit int) ([]BookSearchResult, error) {
	books, err := s.st.SearchBooks(q, lang, limit)
	if err != nil {
		return nil, err
	}
	out := make([]BookSearchResult, 0, len(books))
	for _, b := range books {
		items, err := s.st.ListItems(b.ID)
		if err != nil {
			return nil, err
		}
		r := BookSearchResult{Biblio: b}
		for _, it := range items {
			r.Total++
			if it.Status == "available" {
				r.Available++
			}
		}
		out = append(out, r)
	}
	return out, nil
}

// ItemView 副本视图（含当前借阅人/应还日）。
type ItemView struct {
	store.Item
	Borrower     string `json:"borrower,omitempty"`
	DueDate      string `json:"due_date,omitempty"`
	QueueCount   int    `json:"queue_count"`
	HasAvailable bool   `json:"has_available"`
}

// BookAvailability 书目详情 + 各副本可用性。
func (s *Service) BookAvailability(biblioID int64) (*store.Biblio, []ItemView, error) {
	b, err := s.st.GetBiblio(biblioID)
	if err != nil {
		return nil, nil, ErrBiblioNotFound
	}
	items, err := s.st.ListItems(biblioID)
	if err != nil {
		return nil, nil, err
	}
	holds, _ := s.st.WaitingHolds(biblioID)
	hasAvailable := false
	for _, it := range items {
		if it.Status == "available" {
			hasAvailable = true
			break
		}
	}
	views := make([]ItemView, 0, len(items))
	for _, it := range items {
		v := ItemView{Item: it, QueueCount: len(holds), HasAvailable: hasAvailable}
		if it.Status == "borrowed" {
			if loan, err := s.st.ActiveLoanByItem(it.ID); err == nil {
				v.DueDate = loan.DueDate
				if p, err := s.st.GetPatron(loan.PatronID); err == nil {
					v.Borrower = p.Name
				}
			}
		}
		views = append(views, v)
	}
	return b, views, nil
}

// LoanView 借阅视图（含书名与可续借标志）。
type LoanView struct {
	store.Loan
	Title     string `json:"title"`
	Barcode   string `json:"barcode"`
	Renewable bool   `json:"renewable"`
	RenewMsg  string `json:"renew_msg,omitempty"`
}

// PatronLoans 读者当前在借图书。
func (s *Service) PatronLoans(patronID int64) ([]LoanView, error) {
	loans, err := s.st.ActiveLoans(patronID)
	if err != nil {
		return nil, err
	}
	out := make([]LoanView, 0, len(loans))
	today := store.Now()
	for _, l := range loans {
		v := LoanView{Loan: l}
		if it, err := s.st.GetItem(l.ItemID); err == nil {
			v.Barcode = it.Barcode
			if b, err := s.st.GetBiblio(it.BiblioID); err == nil {
				v.Title = b.Title
			}
		}
		v.Renewable = s.renewErr(l, today) == nil
		v.RenewMsg = ""
		if err := s.renewErr(l, today); err != nil {
			v.RenewMsg = err.Error()
		}
		out = append(out, v)
	}
	return out, nil
}

// LoanHistory 读者借阅历史。
func (s *Service) LoanHistory(patronID int64) ([]LoanView, error) {
	loans, err := s.st.LoanHistory(patronID)
	if err != nil {
		return nil, err
	}
	out := make([]LoanView, 0, len(loans))
	for _, l := range loans {
		v := LoanView{Loan: l}
		if it, err := s.st.GetItem(l.ItemID); err == nil {
			v.Barcode = it.Barcode
			if b, err := s.st.GetBiblio(it.BiblioID); err == nil {
				v.Title = b.Title
			}
		}
		out = append(out, v)
	}
	return out, nil
}

// renewErr 返回该借阅不可续借的原因；nil 表示可续借。
func (s *Service) renewErr(l store.Loan, today string) error {
	if l.Status != "active" {
		return ErrLoanNotActive
	}
	if l.Renewals >= MaxRenewals {
		return ErrMaxRenewals
	}
	if l.DueDate < today {
		return ErrOverdue
	}
	it, err := s.st.GetItem(l.ItemID)
	if err != nil {
		return err
	}
	if holds, err := s.st.WaitingHolds(it.BiblioID); err == nil && len(holds) > 0 {
		return ErrHoldPending
	}
	return nil
}

// Borrow 借书。
func (s *Service) Borrow(patronID, itemID int64) (*store.Loan, error) {
	if _, err := s.st.GetPatron(patronID); err != nil {
		return nil, ErrPatronNotFound
	}
	it, err := s.st.GetItem(itemID)
	if err != nil {
		return nil, ErrItemNotFound
	}
	if it.Status != "available" {
		return nil, ErrItemUnavailable
	}
	active, err := s.st.ActiveLoans(patronID)
	if err != nil {
		return nil, err
	}
	if len(active) >= MaxActiveLoans {
		return nil, ErrLoanLimitReached
	}
	days := it.LoanDurationDays
	if days <= 0 {
		days = DefaultLoanDays
	}
	today := store.Now()
	due := addDays(today, days)
	loanID, err := s.st.Checkout(itemID, patronID, today, due)
	if err != nil {
		return nil, err
	}
	return s.st.GetLoan(loanID)
}

// ReturnResult 还书结果。
type ReturnResult struct {
	LoanID     int64  `json:"loan_id"`
	FineCents  int    `json:"fine_cents"`
	HoldWakeUp string `json:"hold_wake_up,omitempty"`
}

// Return 还书：单一事务内完成"关借阅 + 副本可借 + 逾期罚款 + 唤醒预约"，
// 任何一步失败整体回滚，杜绝半完成状态。
func (s *Service) Return(loanID int64) (*ReturnResult, error) {
	today := store.Now()
	txRes, err := s.st.ReturnTx(loanID, today, FinePerDayCents)
	if err != nil {
		if errors.Is(err, store.ErrLoanNotActive) {
			return nil, ErrLoanNotActive
		}
		return nil, err
	}
	res := &ReturnResult{LoanID: loanID, FineCents: txRes.Fine}
	if txRes.WakeName != "" {
		res.HoldWakeUp = fmt.Sprintf("已通知预约者 %s 到馆取书", txRes.WakeName)
		// 通知：预约到书（还书唤醒预约者）
		s.NotifyHoldReady(txRes.WakePatronID, txRes.WakeBiblioTitle)
	}
	// 通知：逾期罚款
	if txRes.Fine > 0 {
		s.NotifyFine(txRes.LoanPatronID, txRes.LoanBiblioTitle, txRes.Fine)
	}
	return res, nil
}

// Renew 续借（关旧开新）。
func (s *Service) Renew(loanID int64) (*store.Loan, error) {
	loan, err := s.st.GetLoan(loanID)
	if err != nil {
		return nil, ErrLoanNotFound
	}
	if loan.Status != "active" {
		return nil, ErrLoanNotActive
	}
	today := store.Now()
	if err := s.renewErr(*loan, today); err != nil {
		return nil, err
	}
	it, err := s.st.GetItem(loan.ItemID)
	if err != nil {
		return nil, err
	}
	days := it.LoanDurationDays
	if days <= 0 {
		days = DefaultLoanDays
	}
	newDue := addDays(loan.DueDate, days)
	newID, err := s.st.RenewCheckout(loanID, loan.DueDate, newDue, loan.Renewals+1)
	if err != nil {
		return nil, err
	}
	return s.st.GetLoan(newID)
}

// FinesView 罚款视图。
type FinesView struct {
	UnpaidCents int          `json:"unpaid_cents"`
	Items       []store.Fine `json:"items"`
}

// Fines 读者未缴罚款。
func (s *Service) Fines(patronID int64) (*FinesView, error) {
	if _, err := s.st.GetPatron(patronID); err != nil {
		return nil, ErrPatronNotFound
	}
	fines, err := s.st.Fines(patronID, true)
	if err != nil {
		return nil, err
	}
	sum, err := s.st.SumUnpaidFines(patronID)
	if err != nil {
		return nil, err
	}
	return &FinesView{UnpaidCents: sum, Items: fines}, nil
}

// PlaceHold 预约（书全部借出时排队）。
func (s *Service) PlaceHold(patronID, biblioID int64) (*store.Hold, error) {
	if _, err := s.st.GetPatron(patronID); err != nil {
		return nil, ErrPatronNotFound
	}
	b, err := s.st.GetBiblio(biblioID)
	if err != nil {
		return nil, ErrBiblioNotFound
	}
	_ = b
	// 已有副本可借则提示直接借阅
	items, err := s.st.ListItems(biblioID)
	if err != nil {
		return nil, err
	}
	for _, it := range items {
		if it.Status == "available" {
			return nil, ErrNoAvailableItem
		}
	}
	// 检查是否已预约
	holds, err := s.st.PatronHolds(patronID)
	if err != nil {
		return nil, err
	}
	for _, h := range holds {
		if h.BiblioID == biblioID && h.Status == "waiting" {
			return nil, ErrAlreadyHeld
		}
	}
	h := &store.Hold{BiblioID: biblioID, PatronID: patronID, CreatedAt: store.Now()}
	id, err := s.st.CreateHold(h)
	if err != nil {
		return nil, err
	}
	h.ID = id
	return h, nil
}

// HoldView 预约视图（含书名，供前端"我的预约"面板）。
type HoldView struct {
	store.Hold
	Title string `json:"title"`
}

// PatronHolds 读者预约列表（含书名）。
func (s *Service) PatronHolds(patronID int64) ([]HoldView, error) {
	holds, err := s.st.PatronHolds(patronID)
	if err != nil {
		return nil, err
	}
	out := make([]HoldView, 0, len(holds))
	for _, h := range holds {
		v := HoldView{Hold: h}
		if b, err := s.st.GetBiblio(h.BiblioID); err == nil {
			v.Title = b.Title
		}
		out = append(out, v)
	}
	return out, nil
}

// ---- 日期工具 ----

// addDays 日期加 n 天（YYYY-MM-DD）。
func addDays(date string, n int) string {
	t, err := time.Parse(DateLayout, date)
	if err != nil {
		return date
	}
	return t.AddDate(0, 0, n).Format(DateLayout)
}

// daysBetween 两个日期相差的天数（b 晚于 a 时为正）。
func daysBetween(a, b string) int {
	ta, _ := time.Parse(DateLayout, a)
	tb, _ := time.Parse(DateLayout, b)
	return int(tb.Sub(ta).Hours() / 24)
}

// centsToYuan 分转元字符串（保留 1 位小数，如 240→"2.4"）。
func centsToYuan(cents int) string {
	whole := cents / 100
	frac := cents % 100 / 10
	if frac == 0 {
		return itoa(whole)
	}
	return itoa(whole) + "." + itoa(frac)
}

