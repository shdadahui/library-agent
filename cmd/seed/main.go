// 种子数据导入工具：初始化书目、馆藏、演示读者与预置借阅场景。
// 用法: go run ./cmd/seed [-reset] [-fetch] [-rows 200]
//   -reset  重建数据库（删除旧库）
//   -fetch  从 Open Library API 扩充书目（网络可用时）
//   -rows   扩充书目条数上限（默认 200）
package main

import (
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/shdadahui/library-agent/internal/store"
)

func main() {
	reset := flag.Bool("reset", false, "重建数据库")
	fetch := flag.Bool("fetch", false, "从 Open Library API 扩充书目")
	rows := flag.Int("rows", 200, "扩充书目条数上限")
	dbPath := flag.String("db", "data/library.db", "SQLite 数据库路径")
	flag.Parse()

	if *reset {
		if err := os.Remove(*dbPath); err == nil {
			log.Printf("已删除旧数据库 %s", *dbPath)
		}
	}
	if dir := filepath.Dir(*dbPath); dir != "." {
		_ = os.MkdirAll(dir, 0o755)
	}

	st, err := store.Open(*dbPath)
	if err != nil {
		log.Fatalf("打开数据库失败: %v", err)
	}
	defer st.Close()

	books := make([]SeedBook, 0, len(builtinBooks)+*rows)
	books = append(books, builtinBooks...)

	if *fetch {
		fetched := fetchOpenLibrary(*rows)
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
	log.Printf("书目 %d 本", len(idByTitle))

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
			log.Fatalf("插入读者失败: %v", err)
		}
		idByPatron[p.Name] = id
	}
	log.Printf("演示读者 %d 位", len(patrons))

	// 3. 预置借阅场景（跳过规则直接写入，方便演示各状态）
	now := time.Now()
	day := func(offset int) string { return now.AddDate(0, 0, offset).Format("2006-01-02") }
	itemOf := func(title string) int64 {
		id := idByTitle[title]
		items, err := st.ListItems(id)
		if err != nil || len(items) == 0 {
			return 0
		}
		return items[0].ID
	}
	_ = itemOf

	// 张三：三体（即将到期，可续借）/ 活着（已逾期）/ 围城（已续借1次）
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
	// 李四：百年孤独（正常在借），并预约三体（三体已被张三借走）
	lisi := idByPatron["李四"]
	seedLoan(st, itemOf("百年孤独"), lisi, day(-3), day(11), 0, "active")
	// 三体如果有第二个副本，借给钱七，确保全部借出，预约可演示
	if it2 := secondItemOf(st, idByTitle["三体"]); it2 != nil {
		seedLoan(st, it2.ID, idByPatron["钱七"], day(-5), day(9), 0, "active")
	}
	_, _ = st.CreateHold(&store.Hold{
		BiblioID: idByTitle["三体"], PatronID: lisi, CreatedAt: day(0),
	})
	// 赵六：两本英文书在借
	zhao := idByPatron["赵六"]
	seedLoan(st, itemOf("1984"), zhao, day(-7), day(7), 0, "active")
	seedLoan(st, itemOf("Dune"), zhao, day(-7), day(7), 0, "active")
	// 王五：历史记录（已还 + 已缴罚款），当前无在借
	wang := idByPatron["王五"]
	seedLoan(st, itemOf("呐喊"), wang, day(-40), day(-26), 0, "returned")
	seedLoan(st, itemOf("边城"), wang, day(-60), day(-46), 1, "returned")

	n, _ := st.CountBiblios()
	fmt.Printf("\n✅ 种子数据初始化完成：%d 本书、%d 位读者\n", n, len(patrons))
	fmt.Println("演示读者：张三(P0001) 李四(P0002) 王五(P0003) 赵六(P0004) 钱七(P0005) 孙八(P0006)")
	fmt.Println("启动服务：go run ./cmd/server，浏览器打开 http://localhost:8642")
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

// secondItemOf 取某本书的第二个副本（若存在）。
func secondItemOf(st *store.Store, biblioID int64) *store.Item {
	items, err := st.ListItems(biblioID)
	if err != nil || len(items) < 2 {
		return nil
	}
	return &items[1]
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
