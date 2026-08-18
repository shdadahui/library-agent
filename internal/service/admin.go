package service

import (
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
