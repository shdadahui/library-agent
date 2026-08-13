package api

import (
	"net/http"
	"strconv"
)

// handleAdminUsers 全部用户（管理员）。
func (s *Server) handleAdminUsers(w http.ResponseWriter, r *http.Request) {
	patrons, err := s.Svc.Patrons()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]map[string]any, 0, len(patrons))
	for _, p := range patrons {
		// 关联该读者的登录账号（若有）
		row := map[string]any{
			"id": p.ID, "name": p.Name, "barcode": p.Barcode, "phone": p.Phone,
		}
		if user, err := s.Svc.FindUserByPatronID(p.ID); err == nil {
			row["username"] = user.Username
			row["user_id"] = user.ID
			row["role"] = user.Role
		}
		// 当前在借数
		if loans, err := s.Svc.PatronLoans(p.ID); err == nil {
			row["active_loans"] = len(loans)
		}
		out = append(out, row)
	}
	writeJSON(w, http.StatusOK, out)
}

// handleAdminBooks 全部书目（分页，管理员）。
func (s *Server) handleAdminBooks(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	books, err := s.Svc.ListAllBooks(limit, offset)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, books)
}

// handleAdminStats 运营统计（管理员）。
func (s *Server) handleAdminStats(w http.ResponseWriter, r *http.Request) {
	stats, err := s.Svc.LibraryStats()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	totalPatrons, _ := s.Svc.Patrons()
	out := map[string]any{
		"books":             stats.Books,
		"copies":            stats.Copies,
		"available":         stats.Available,
		"borrowed":          stats.Borrowed,
		"holds_waiting":     stats.HoldsWaiting,
		"patrons":           len(totalPatrons),
		"unpaid_fines_yuan": float64(stats.UnpaidFinesCents) / 100,
	}
	writeJSON(w, http.StatusOK, out)
}
