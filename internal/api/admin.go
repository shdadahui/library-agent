package api


import (
	"net/http"
	"strconv"

	"github.com/shdadahui/library-agent/internal/store"
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

// handleAdminCheckout 馆员借出登记（管理员）：读者条码 + 图书条码。
func (s *Server) handleAdminCheckout(w http.ResponseWriter, r *http.Request) {
	var body struct {
		PatronBarcode string `json:"patron_barcode"`
		ItemBarcode   string `json:"item_barcode"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	loan, err := s.Svc.CheckoutByBarcode(body.PatronBarcode, body.ItemBarcode)
	if err != nil {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"loan": loan})
}

// handleAdminReturn 馆员归还登记（管理员）：图书条码。
func (s *Server) handleAdminReturn(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ItemBarcode string `json:"item_barcode"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	res, err := s.Svc.ReturnByItemBarcode(body.ItemBarcode)
	if err != nil {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// handleAdminAddBook 馆员新增书目（管理员）。
func (s *Server) handleAdminAddBook(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Title     string `json:"title"`
		Author    string `json:"author"`
		ISBN      string `json:"isbn"`
		Publisher string `json:"publisher"`
		Subjects  string `json:"subjects"`
		Year      int    `json:"year"`
		Copies    int    `json:"copies"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	b, err := s.Svc.AdminAddBook(body.Title, body.Author, body.ISBN, body.Publisher, body.Subjects, body.Year, body.Copies)
	if err != nil {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, b)
}


// ---- 管理端扩展：书目管理 / 借阅记录 / 读者管理 ----

// handleAdminBooksV2 书目分页 + 搜索（替代原 handleAdminBooks）。
func (s *Server) handleAdminBooksV2(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	size, _ := strconv.Atoi(r.URL.Query().Get("size"))
	if size < 1 || size > 100 {
		size = 20
	}
	res, err := s.Svc.AdminListBooks(q, page, size)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// handleAdminUpdateBook 编辑书目。
func (s *Server) handleAdminUpdateBook(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	var body store.Biblio
	if !decodeBody(w, r, &body) {
		return
	}
	if err := s.Svc.AdminUpdateBook(id, &body); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleAdminDeleteBook 删除书目。
func (s *Server) handleAdminDeleteBook(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.Svc.AdminDeleteBook(id); err != nil {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleAdminAddCopies 书目加副本。
func (s *Server) handleAdminAddCopies(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	var body struct {
		Copies int `json:"copies"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	barcodes, err := s.Svc.AdminAddCopies(id, body.Copies)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"barcodes": barcodes})
}

// handleAdminDeleteItem 删除单个副本。
func (s *Server) handleAdminDeleteItem(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.Svc.AdminDeleteItem(id); err != nil {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleAdminLoans 借阅记录分页 + 筛选。
func (s *Server) handleAdminLoans(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	q := r.URL.Query().Get("q")
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	size, _ := strconv.Atoi(r.URL.Query().Get("size"))
	if size < 1 || size > 100 {
		size = 20
	}
	res, err := s.Svc.AdminListLoans(status, q, page, size)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// handleAdminUpdatePatron 读者状态/角色。
func (s *Server) handleAdminUpdatePatron(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	var body struct {
		Status string `json:"status"`
		Role   string `json:"role"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	if err := s.Svc.AdminUpdatePatron(id, body.Status, body.Role); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
