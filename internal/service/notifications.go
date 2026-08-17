package service

import (
	"github.com/shdadahui/library-agent/internal/store"
)

// NotificationView 通知视图（含未读数）。
type NotificationView struct {
	Items    []store.Notification `json:"items"`
	Unread   int                  `json:"unread"`
}

// Notifications 读者通知列表 + 未读数。
func (s *Service) Notifications(patronID int64) (*NotificationView, error) {
	items, err := s.st.ListNotifications(patronID, 50)
	if err != nil {
		return nil, err
	}
	unread, err := s.st.UnreadNotificationCount(patronID)
	if err != nil {
		return nil, err
	}
	return &NotificationView{Items: items, Unread: unread}, nil
}

// MarkNotificationsRead 全部标记已读。
func (s *Service) MarkNotificationsRead(patronID int64) error {
	return s.st.MarkAllNotificationsRead(patronID)
}

// notify 创建通知（内部助手）。
func (s *Service) notify(patronID int64, typ, title, body string) {
	_, _ = s.st.CreateNotification(&store.Notification{
		PatronID: patronID, Type: typ, Title: title, Body: body, CreatedAt: store.NowDateTime(),
	})
}

// NotifyHoldReady 预约到书通知（还书唤醒预约时调用）。
func (s *Service) NotifyHoldReady(patronID int64, biblioTitle string) {
	s.notify(patronID, "hold_ready", "预约到书", "您预约的《"+biblioTitle+"》已到馆，请凭读者证到馆取书（保留 7 天）。")
}

// NotifyFine 产生罚款通知（还书结算时调用）。
func (s *Service) NotifyFine(patronID int64, biblioTitle string, fineCents int) {
	if fineCents <= 0 {
		return
	}
	s.notify(patronID, "fine", "逾期罚款", "归还《"+biblioTitle+"》产生逾期罚款 "+centsToYuan(fineCents)+" 元，请尽快在「我的罚款」中查看。")
}

// NotifyOverdue 逾期提醒（seed 预置/定期调用）。
func (s *Service) NotifyOverdue(patronID int64, title string) {
	s.notify(patronID, "overdue", "逾期提醒", "您借阅的《"+title+"》已逾期，请尽快归还以免影响借阅权限。")
}

// CancelHold 取消预约（本人或管理员；等待中可取消，已唤醒不可取消）。
func (s *Service) CancelHold(patronID, holdID int64) error {
	return s.st.CancelHoldByID(patronID, holdID)
}
