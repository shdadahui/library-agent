package service

import (
	"sort"
	"time"

	"github.com/shdadahui/library-agent/internal/store"
)

// CountItem 统计条目（作者/主题）。
type CountItem struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

// MonthlyStat 月度借阅。
type MonthlyStat struct {
	Month string `json:"month"` // YYYY-MM
	Count int    `json:"count"`
}

// ReadingReport 个人阅读报告。
type ReadingReport struct {
	PatronName      string        `json:"patron_name"`
	TotalBorrows    int           `json:"total_borrows"`     // 累计借阅
	UniqueBooks     int           `json:"unique_books"`      // 读过不同书
	ActiveLoans     int           `json:"active_loans"`      // 当前在借
	OverdueCount    int           `json:"overdue_count"`     // 历史逾期次数
	UnpaidFinesYuan float64       `json:"unpaid_fines_yuan"` // 未缴罚款
	TopAuthors      []CountItem   `json:"top_authors"`
	TopSubjects     []CountItem   `json:"top_subjects"`
	MonthlyTrend    []MonthlyStat `json:"monthly_trend"` // 最近 6 个月
}

// ReadingReport 基于借阅历史生成个人阅读报告。
func (s *Service) ReadingReport(patronID int64) (*ReadingReport, error) {
	patron, err := s.st.GetPatron(patronID)
	if err != nil {
		return nil, ErrPatronNotFound
	}
	history, err := s.st.LoanHistory(patronID)
	if err != nil {
		return nil, err
	}
	rep := &ReadingReport{PatronName: patron.Name}
	authors := map[string]int{}
	subjects := map[string]int{}
	books := map[int64]bool{}
	monthly := map[string]int{}

	for _, l := range history {
		rep.TotalBorrows++
		if l.Status == "active" {
			rep.ActiveLoans++
		}
		// 逾期：已还的看 due<checkin，在借的看 due<today
		if l.CheckinDate != nil {
			if l.DueDate < *l.CheckinDate {
				rep.OverdueCount++
			}
		} else if l.DueDate < store.Now() {
			rep.OverdueCount++
		}
		it, err := s.st.GetItem(l.ItemID)
		if err != nil {
			continue
		}
		b, err := s.st.GetBiblio(it.BiblioID)
		if err != nil {
			continue
		}
		if !books[b.ID] {
			books[b.ID] = true
			rep.UniqueBooks++
		}
		if b.Author != "" {
			authors[b.Author]++
		}
		for _, k := range tokenize(b.Subjects) {
			subjects[k]++
		}
		if len(l.CheckoutDate) >= 7 {
			monthly[l.CheckoutDate[:7]]++
		}
	}
	// 最近 6 个月趋势
	now := time.Now()
	for i := 5; i >= 0; i-- {
		m := now.AddDate(0, -i, 0).Format("2006-01")
		rep.MonthlyTrend = append(rep.MonthlyTrend, MonthlyStat{Month: m, Count: monthly[m]})
	}
	rep.TopAuthors = topN(authors, 5)
	rep.TopSubjects = topN(subjects, 5)
	if fines, err := s.st.SumUnpaidFines(patronID); err == nil {
		rep.UnpaidFinesYuan = float64(fines) / 100
	}
	return rep, nil
}

// topN 按出现次数取前 N。
func topN(m map[string]int, n int) []CountItem {
	out := make([]CountItem, 0, len(m))
	for k, v := range m {
		out = append(out, CountItem{Name: k, Count: v})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Count > out[j].Count })
	if len(out) > n {
		out = out[:n]
	}
	return out
}

// HotBooks 借阅热门榜（带可借数与借阅次数）。
func (s *Service) HotBooks(limit int) ([]Recommendation, error) {
	hot, err := s.st.TopBorrowed(limit)
	if err != nil {
		return nil, err
	}
	out := make([]Recommendation, 0, len(hot))
	for _, h := range hot {
		avail := 0
		if items, err := s.st.ListItems(h.Biblio.ID); err == nil {
			for _, it := range items {
				if it.Status == "available" {
					avail++
				}
			}
		}
		out = append(out, Recommendation{
			Biblio: h.Biblio, Available: avail, Score: h.BorrowCount,
			Reasons: []string{"被借 " + itoa(h.BorrowCount) + " 次"},
		})
	}
	return out, nil
}

// NewBooks 新书上架。
func (s *Service) NewBooks(limit int) ([]store.Biblio, error) { return s.st.NewBooks(limit) }
