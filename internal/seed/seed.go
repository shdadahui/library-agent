// Package seed 提供演示种子数据：书目、馆藏、读者与预置借阅场景。
// 供 cmd/seed（数据导入）与 cmd/eval（评测隔离库）复用。
package seed

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"

	"github.com/shdadahui/library-agent/internal/store"
	"golang.org/x/crypto/bcrypt"
)

// SeedBook 种子书目。
type SeedBook struct {
	Title     string
	Author    string
	ISBN      string
	Publisher string
	Year      int
	Subjects  string
	Lang      string
	CoverID   int64
}

// Result 种子初始化统计。
type Result struct {
	Books   int
	Patrons int
}

// Seed 在给定 Store 上初始化种子数据（书目/副本/读者/预置借阅场景）。
// fetch=true 时尝试从 Open Library API 扩充书目（网络不可用时静默跳过）。
func Seed(st *store.Store, fetch bool, fetchRows int) (*Result, error) {
	books := make([]SeedBook, 0, len(builtinBooks)+fetchRows)
	books = append(books, builtinBooks...)

	if fetch {
		fetched := fetchOpenLibrary(fetchRows)
		if len(fetched) > 0 {
			log.Printf("Open Library 扩充 %d 本", len(fetched))
			books = append(books, fetched...)
		} else {
			log.Printf("Open Library 扩充失败或为空，仅使用内置书单")
		}
	}

	// 1. 书目 + 馆藏副本
	idByTitle := map[string]int64{}
	for _, b := range books {
		id, err := st.InsertBiblio(&store.Biblio{
			Title: b.Title, Author: b.Author, ISBN: b.ISBN, Publisher: b.Publisher,
			PublishYear: b.Year, Subjects: b.Subjects, Lang: b.Lang, CoverID: b.CoverID,
		})
		if err != nil {
			log.Printf("跳过 %s: %v", b.Title, err)
			continue
		}
		idByTitle[b.Title] = id
		// 每本书 1~3 个副本
		n := 1 + (len(b.Title)+len(b.Author))%3
		for i := 0; i < n; i++ {
			_, _ = st.InsertItem(&store.Item{
				BiblioID: id, Barcode: fmt.Sprintf("LIB-%05d-%d", id, i+1),
				Status: "available", Location: "总馆", LoanDurationDays: 14,
			})
		}
	}

	// 2. 演示读者
	patrons := []store.Patron{
		{Name: "张三", Barcode: "P0001", Phone: "13800000001"},
		{Name: "李四", Barcode: "P0002", Phone: "13800000002"},
		{Name: "王五", Barcode: "P0003", Phone: "13800000003"},
		{Name: "赵六", Barcode: "P0004", Phone: "13800000004"},
		{Name: "钱七", Barcode: "P0005", Phone: "13800000005"},
		{Name: "孙八", Barcode: "P0006", Phone: "13800000006"},
	}
	idByPatron := map[string]int64{}
	for _, p := range patrons {
		id, err := st.InsertPatron(&p)
		if err != nil {
			return nil, fmt.Errorf("插入读者失败: %w", err)
		}
		idByPatron[p.Name] = id
	}

	// 3. 预置借阅场景（跳过规则直接写入，方便演示各状态）
	now := time.Now()
	day := func(offset int) string { return now.AddDate(0, 0, offset).Format("2006-01-02") }
	itemOf := func(title string) int64 {
		items, err := st.ListItems(idByTitle[title])
		if err != nil || len(items) == 0 {
			return 0
		}
		return items[0].ID
	}

	// 张三：三体（即将到期，可续借）/ 活着（已逾期）/ 围城（已续借1次且逾期）
	zhang := idByPatron["张三"]
	seedLoan(st, itemOf("三体"), zhang, day(-10), day(4), 0, "active")
	seedLoan(st, itemOf("活着"), zhang, day(-20), day(-6), 0, "active")
	seedLoan(st, itemOf("围城"), zhang, day(-30), day(-2), 1, "active")
	// 张三预置一笔未缴罚款（2.4 元，逾期 24 天）
	if loan, err := st.ActiveLoanByItem(itemOf("活着")); err == nil {
		_, _ = st.CreateFine(&store.Fine{
			PatronID: zhang, LoanID: loan.ID, AmountCents: 240, CreatedDate: day(-6),
		})
	}
	// 李四：百年孤独（正常在借）
	lisi := idByPatron["李四"]
	seedLoan(st, itemOf("百年孤独"), lisi, day(-3), day(11), 0, "active")
	// 三体其余副本全部借出（副本1 已被张三借），保证「全部借出→可预约」场景成立
	if items, err := st.ListItems(idByTitle["三体"]); err == nil {
		for _, it := range items {
			if it.Status == "available" {
				seedLoan(st, it.ID, idByPatron["钱七"], day(-5), day(9), 0, "active")
			}
		}
	}
	// 赵六：两本英文书在借
	zhao := idByPatron["赵六"]
	seedLoan(st, itemOf("1984"), zhao, day(-7), day(7), 0, "active")
	seedLoan(st, itemOf("Dune"), zhao, day(-7), day(7), 0, "active")
	// 王五：历史记录（已还 + 已缴罚款），当前无在借
	wang := idByPatron["王五"]
	seedLoan(st, itemOf("呐喊"), wang, day(-40), day(-26), 0, "returned")
	seedLoan(st, itemOf("边城"), wang, day(-60), day(-46), 1, "returned")
	// 孙八：蛙（该书面仅 1 副本，借出后可供预约场景演示）
	seedLoan(st, itemOf("蛙"), idByPatron["孙八"], day(-8), day(6), 0, "active")

	// 4. 演示登录账号（bcrypt 哈希，绑定演示读者）
	demoAccounts := []struct {
		username, password, patronName string
	}{
		{"alice", "alice123", "张三"},
		{"bob", "bob123", "李四"},
	}
	for _, a := range demoAccounts {
		if _, err := st.GetUserByUsername(a.username); err == nil {
			continue
		}
		hash, err := bcrypt.GenerateFromPassword([]byte(a.password), bcrypt.DefaultCost)
		if err != nil {
			return nil, fmt.Errorf("生成密码哈希失败: %w", err)
		}
		pid, ok := idByPatron[a.patronName]
		if !ok {
			continue
		}
		if _, err := st.CreateUser(&store.User{
			Username: a.username, PasswordHash: string(hash), PatronID: pid, CreatedAt: store.NowDateTime(),
		}); err != nil {
			return nil, fmt.Errorf("创建演示账号失败: %w", err)
		}
	}

	return &Result{Books: len(idByTitle), Patrons: len(patrons)}, nil
}

// seedLoan 直接写入借阅记录并置副本为 borrowed（用于预置场景，绕过业务规则）。
func seedLoan(st *store.Store, itemID, patronID int64, checkout, due string, renewals int, status string) {
	if itemID == 0 {
		return
	}
	q := `INSERT INTO loans(item_id,patron_id,checkout_date,due_date,renewals,status) VALUES(?,?,?,?,?,?)`
	_, _ = st.DB.Exec(q, itemID, patronID, checkout, due, renewals, status)
	if status == "active" {
		_ = st.UpdateItemStatus(itemID, "borrowed")
	}
}

// fetchOpenLibrary 从 Open Library Search API 拉取书目。
func fetchOpenLibrary(limit int) []SeedBook {
	type doc struct {
		Title            string   `json:"title"`
		AuthorName       []string `json:"author_name"`
		FirstPublishYear int      `json:"first_publish_year"`
		Subject          []string `json:"subject"`
		CoverI           int64    `json:"cover_i"`
		Language         []string `json:"language"`
		ISBN             []string `json:"isbn"`
	}
	queries := []string{
		"subject:fiction&lang=eng",
		"subject:science&lang=eng",
		"subject:history&lang=eng",
	}
	seen := map[string]bool{}
	var out []SeedBook
	for _, q := range queries {
		if len(out) >= limit {
			break
		}
		url := fmt.Sprintf("https://openlibrary.org/search.json?q=%s&limit=100&fields=title,author_name,first_publish_year,subject,cover_i,language,isbn", q)
		resp, err := http.Get(url)
		if err != nil {
			log.Printf("Open Library 请求失败: %v", err)
			continue
		}
		var payload struct {
			Docs []doc `json:"docs"`
		}
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
		resp.Body.Close()
		if err := json.Unmarshal(body, &payload); err != nil {
			log.Printf("Open Library 解析失败: %v", err)
			continue
		}
		for _, d := range payload.Docs {
			if len(out) >= limit {
				break
			}
			if d.Title == "" || len(d.AuthorName) == 0 || seen[d.Title] {
				continue
			}
			seen[d.Title] = true
			isbn := ""
			if len(d.ISBN) > 0 {
				isbn = d.ISBN[0]
			}
			subj := ""
			if len(d.Subject) > 0 {
				subj = d.Subject[0]
			}
			lang := "en"
			if len(d.Language) > 0 {
				lang = d.Language[0]
			}
			out = append(out, SeedBook{
				Title: d.Title, Author: d.AuthorName[0], ISBN: isbn,
				Publisher: "", Year: d.FirstPublishYear, Subjects: subj,
				Lang: lang, CoverID: d.CoverI,
			})
		}
		time.Sleep(300 * time.Millisecond) // 温和限速
	}
	return out
}

var _ = sql.ErrNoRows
