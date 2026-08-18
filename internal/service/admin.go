package service

import (
	"errors"
	"fmt"

	"github.com/shdadahui/library-agent/internal/store"
)

// CheckoutByBarcode 馆员借出登记：读者条码 + 图书条码 → 办理借出。
func (s *Service) CheckoutByBarcode(patronBarcode, itemBarcode string) (*store.Loan, error) {
	patron, err := s.st.GetPatronByBarcode(patronBarcode)
	if err != nil {
		return nil, ErrPatronNotFound
	}
	item, err := s.st.GetItemByBarcode(itemBarcode)
	if err != nil {
		return nil, ErrItemNotFound
	}
	return s.Borrow(patron.ID, item.ID)
}

// ReturnByItemBarcode 馆员归还登记：图书条码 → 找到在借记录 → 归还。
func (s *Service) ReturnByItemBarcode(itemBarcode string) (*ReturnResult, error) {
	item, err := s.st.GetItemByBarcode(itemBarcode)
	if err != nil {
		return nil, ErrItemNotFound
	}
	loan, err := s.st.ActiveLoanByItem(item.ID)
	if err != nil {
		return nil, ErrLoanNotActive
	}
	return s.Return(loan.ID)
}

// AdminAddBook 馆员新增书目 + 指定数量副本（admin）。
func (s *Service) AdminAddBook(title, author, isbn, publisher, subjects string, year int, copies int) (*store.Biblio, error) {
	if title == "" || copies < 1 {
		return nil, ErrInvalidInput
	}
	id, err := s.st.InsertBiblio(&store.Biblio{
		Title: title, Author: author, ISBN: isbn, Publisher: publisher,
		PublishYear: year, Subjects: subjects, Lang: "zh",
	})
	if err != nil {
		return nil, err
	}
	loc := locationForBook(subjects)
	for i := 0; i < copies; i++ {
		_, _ = s.st.InsertItem(&store.Item{
			BiblioID: id, Barcode: fmt.Sprintf("LIB-%05d-%d", id, i+1),
			Status: "available", Location: loc, LoanDurationDays: 14,
		})
	}
	return &store.Biblio{ID: id, Title: title, Author: author, Subjects: subjects, Lang: "zh"}, nil
}

// ---- 管理端服务（书目管理 / 借阅记录 / 读者管理） ----

// PageResult 分页结果。
type PageResult struct {
	Items any `json:"items"`
	Total int `json:"total"`
	Page  int `json:"page"`
	Pages int `json:"pages"`
}

// AdminListBooks 书目分页 + 搜索。
func (s *Service) AdminListBooks(q string, page, size int) (*PageResult, error) {
	items, total, err := s.st.ListBooksPaged(q, page, size)
	if err != nil {
		return nil, err
	}
	pages := (total + size - 1) / size
	return &PageResult{Items: items, Total: total, Page: page, Pages: pages}, nil
}

// AdminUpdateBook 编辑书目。
func (s *Service) AdminUpdateBook(id int64, b *store.Biblio) error {
	if b.Title == "" {
		return ErrInvalidInput
	}
	return s.st.UpdateBiblioFields(id, b)
}

// AdminDeleteBook 删除书目（有在借或等待预约时拒绝）。
func (s *Service) AdminDeleteBook(id int64) error {
	if n, err := s.st.BiblioActiveLoanCount(id); err != nil {
		return err
	} else if n > 0 {
		return errors.New("该书有副本在借，无法删除")
	}
	if n, err := s.st.BiblioWaitingHoldCount(id); err != nil {
		return err
	} else if n > 0 {
		return errors.New("该书有读者预约排队，无法删除")
	}
	return s.st.DeleteBiblioAndItems(id)
}

// AdminAddCopies 书目加副本。
func (s *Service) AdminAddCopies(biblioID int64, n int) ([]string, error) {
	if n < 1 || n > 50 {
		return nil, ErrInvalidInput
	}
	return s.st.AddCopies(biblioID, n)
}

// AdminDeleteItem 删除单个副本（在借拒绝）。
func (s *Service) AdminDeleteItem(itemID int64) error {
	it, err := s.st.GetItem(itemID)
	if err != nil {
		return err
	}
	loan, err := s.st.ActiveLoanByItem(it.ID)
	if err == nil && loan != nil {
		return errors.New("该副本在借中，无法删除")
	}
	return s.st.DeleteItemSafe(it.ID)
}

// AdminListLoans 借阅记录分页 + 筛选（status=active/returned）。
func (s *Service) AdminListLoans(status, q string, page, size int) (*PageResult, error) {
	items, total, err := s.st.ListLoansPaged(status, q, page, size)
	if err != nil {
		return nil, err
	}
	pages := (total + size - 1) / size
	return &PageResult{Items: items, Total: total, Page: page, Pages: pages}, nil
}

// AdminUpdatePatron 读者状态（active/disabled）与角色（user/admin）。
func (s *Service) AdminUpdatePatron(patronID int64, status, role string) error {
	if status != "" && status != "active" && status != "disabled" {
		return ErrInvalidInput
	}
	if role != "" && role != "user" && role != "admin" {
		return ErrInvalidInput
	}
	return s.st.UpdatePatronStatusRole(patronID, status, role)
}

// PatronDisabled 判断读者是否被禁用。
func (s *Service) PatronDisabled(patronID int64) bool {
	p, err := s.st.GetPatron(patronID)
	return err == nil && p.Status == "disabled"
}
