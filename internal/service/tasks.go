package service

import (
	"log"
	"time"

	"github.com/shdadahui/library-agent/internal/store"
)

// RunDueLoansTask 启动到期/逾期提醒任务：每小时检查一次，每天仅对每个借阅生成一次通知（ref_id 去重）。
// 规则：今天到期 → 提醒；已逾期 → 逾期提醒。
func (s *Service) RunDueLoansTask(stop <-chan struct{}) {
	go func() {
		s.CheckDueLoansNow()
		ticker := time.NewTicker(time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				s.CheckDueLoansNow()
			}
		}
	}()
	log.Println("到期提醒任务已启动（每小时检查）")
}

// CheckDueLoansNow 立即执行一次到期/逾期提醒扫描（供任务循环与测试调用）。
func (s *Service) CheckDueLoansNow() {
	today := store.Now()
	due, err := s.st.DueLoans(today)
	if err != nil {
		log.Printf("到期提醒扫描失败: %v", err)
		return
	}
	for _, d := range due {
		typ := "due"
		title := "到期提醒"
		if d.Overdue {
			typ = "overdue"
			title = "逾期提醒"
		}
		ok, _ := s.st.NotificationExistsByRef(typ, d.LoanID)
		if ok {
			continue // 已提醒过
		}
		if _, err := s.st.CreateNotification(&store.Notification{
			PatronID: d.PatronID, Type: typ, Title: title,
			Body:      "您借阅的《" + d.Title + "》应还日期为 " + d.DueDate + "，" + dueAction(d.Overdue),
			RefID:     d.LoanID,
			CreatedAt: store.NowDateTime(),
		}); err != nil {
			// 不吞错：提醒写入失败必须外显（曾因静默失败导致通知长期未生成）
			log.Printf("到期提醒写入失败 patron=%d loan=%d: %v", d.PatronID, d.LoanID, err)
		}
	}
}

func dueAction(overdue bool) string {
	if overdue {
		return "请尽快归还以免影响借阅权限"
	}
	return "请按时归还或及时续借"
}

// RecordLoginLog 登录成功/失败审计（审计日志失败必须外显，不静默吞错）。
func (s *Service) RecordLoginLog(userID int64, username, ip string, success bool) {
	if err := s.st.InsertLoginLog(userID, username, ip, success); err != nil {
		log.Printf("登录审计写入失败 user=%s success=%v: %v", username, success, err)
	}
}

// AdminLoginLogs 登录日志分页。
func (s *Service) AdminLoginLogs(page, size int) (*PageResult, error) {
	items, total, err := s.st.ListLoginLogs(page, size)
	if err != nil {
		return nil, err
	}
	pages := (total + size - 1) / size
	return &PageResult{Items: items, Total: total, Page: page, Pages: pages}, nil
}

// AdminDashboard 仪表盘数据（近 14 日趋势 + 热门分类）。
func (s *Service) AdminDashboard() (map[string]any, error) {
	today := store.Now()
	trend, err := s.st.LoanTrend(14, today)
	if err != nil {
		return nil, err
	}
	cats, err := s.st.TopCategories(6)
	if err != nil {
		return nil, err
	}
	return map[string]any{"trend": trend, "top_categories": cats}, nil
}
