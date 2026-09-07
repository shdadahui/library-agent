package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/shdadahui/library-agent/internal/service"
	"github.com/shdadahui/library-agent/internal/store"
)

// newAuthzTest 内存库 + 2 位读者各借 1 本书，返回编排器与两位读者。
func newAuthzTest(t *testing.T) (*Loop, *service.Service, *store.Store, *store.Patron, *store.Patron) {
	t.Helper()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("打开内存库失败: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	// 每位读者一本独立的书，避免借阅上限/预约规则干扰
	for i, name := range []string{"张三", "李四"} {
		bid, err := st.InsertBiblio(&store.Biblio{Title: fmt.Sprintf("书%d", i+1), Author: "作者", Lang: "zh"})
		if err != nil {
			t.Fatalf("插入书目失败: %v", err)
		}
		if _, err := st.InsertItem(&store.Item{
			BiblioID: bid, Barcode: fmt.Sprintf("B%d", i+1), Status: "available", LoanDurationDays: 14,
		}); err != nil {
			t.Fatalf("插入副本失败: %v", err)
		}
		if _, err := st.InsertPatron(&store.Patron{Name: name, Barcode: fmt.Sprintf("P%d", i+1)}); err != nil {
			t.Fatalf("插入读者失败: %v", err)
		}
	}
	svc := service.New(st)

	var zhangS, liS store.Patron
	ps, _ := st.ListPatrons()
	for _, p := range ps {
		switch p.Name {
		case "张三":
			zhangS = p
		case "李四":
			liS = p
		}
	}
	zhang, li := &zhangS, &liS
	// 各借一本书，产生各自的 loan
	for _, c := range []struct {
		patron  *store.Patron
		barcode string
	}{{zhang, "B1"}, {li, "B2"}} {
		it, err := st.GetItemByBarcode(c.barcode)
		if err != nil {
			t.Fatalf("未找到副本 %s: %v", c.barcode, err)
		}
		if _, err := svc.Borrow(c.patron.ID, it.ID); err != nil {
			t.Fatalf("%s 借书失败: %v", c.patron.Name, err)
		}
	}
	return &Loop{Svc: svc, Tools: AllTools()}, svc, st, zhang, li
}

// callTool 模拟 LLM 发起工具调用（可任意指定参数，包括伪造的 patron_id）。
// 返回原始 JSON 字符串，由各用例按需断言。
func callTool(t *testing.T, l *Loop, patron *store.Patron, name, argsJSON string) string {
	t.Helper()
	return l.executeTool(context.Background(), patron, ToolCall{
		ID: "t1", Function: Function{Name: name, Arguments: argsJSON},
	}, func(Event) {})
}

// callToolMap 同 callTool，但要求结果为 JSON 对象（错误契约用）。
func callToolMap(t *testing.T, l *Loop, patron *store.Patron, name, argsJSON string) map[string]any {
	t.Helper()
	out := callTool(t, l, patron, name, argsJSON)
	var m map[string]any
	if err := json.Unmarshal([]byte(out), &m); err != nil {
		t.Fatalf("工具结果不是 JSON 对象: %v (%s)", err, out)
	}
	return m
}

// TestAgentToolPatronIDOverride LLM 伪造他人 patron_id 应被会话身份覆盖：
// 张三会话里传 patron_id=李四 查借阅，返回的必须是张三的记录。
func TestAgentToolPatronIDOverride(t *testing.T) {
	l, _, _, zhang, li := newAuthzTest(t)
	b := callTool(t, l, zhang, "get_my_loans", fmt.Sprintf(`{"patron_id": %d}`, li.ID))
	if strings.Contains(b, "书2") {
		t.Fatalf("伪造 patron_id 应被覆盖，却返回了李四的借阅: %s", b)
	}
	if !strings.Contains(b, "书1") {
		t.Fatalf("应返回张三本人的借阅: %s", b)
	}
	if !strings.Contains(b, `"patron_id":1`) {
		t.Fatalf("借阅记录应属于张三（patron_id=1）: %s", b)
	}
}

// TestAgentReturnForeignLoan 模拟提示注入：张三会话归还李四的借阅记录应被拒绝。
func TestAgentReturnForeignLoan(t *testing.T) {
	l, svc, st, zhang, li := newAuthzTest(t)
	liLoan := firstLoanOf(t, svc, li.ID)
	m := callToolMap(t, l, zhang, "return_book", fmt.Sprintf(`{"loan_id": %d, "patron_id": %d}`, liLoan, li.ID))
	if err, ok := m["error"].(string); !ok || !strings.Contains(err, "无权") {
		t.Fatalf("归还他人借阅应被拒绝，实际: %v", m)
	}
	items, _ := st.ListItems(2)
	if len(items) == 0 || items[0].Status != "borrowed" {
		t.Fatalf("李四的书不应被张三归还")
	}
}

// TestAgentRenewForeignLoan 张三会话续借李四的记录应被拒绝。
func TestAgentRenewForeignLoan(t *testing.T) {
	l, svc, _, zhang, li := newAuthzTest(t)
	liLoan := firstLoanOf(t, svc, li.ID)
	m := callToolMap(t, l, zhang, "renew_loan", fmt.Sprintf(`{"loan_id": %d, "patron_id": %d}`, liLoan, li.ID))
	if err, ok := m["error"].(string); !ok || !strings.Contains(err, "无权") {
		t.Fatalf("续借他人借阅应被拒绝，实际: %v", m)
	}
}

// TestAgentReturnOwnLoan 本人归还（身份覆盖后归属匹配）不受影响。
func TestAgentReturnOwnLoan(t *testing.T) {
	l, svc, _, zhang, _ := newAuthzTest(t)
	zLoan := firstLoanOf(t, svc, zhang.ID)
	m := callToolMap(t, l, zhang, "return_book", fmt.Sprintf(`{"loan_id": %d, "patron_id": %d}`, zLoan, zhang.ID))
	if _, ok := m["error"]; ok {
		t.Fatalf("本人归还应成功，实际: %v", m)
	}
}

// TestAllPatronToolsDeclarePatronID 防回归：所有消费 patron_id 的工具
// 必须在 schema 声明该属性，否则不会被会话强制注入（越权面）。
func TestAllPatronToolsDeclarePatronID(t *testing.T) {
	need := map[string]bool{
		"get_my_loans": true, "recommend_books": true, "return_book": true,
		"renew_loan": true, "get_my_fines": true, "place_hold": true,
		"reserve_seat": true, "get_my_seat_reservations": true,
		"cancel_seat_reservation": true, "gate_scan": true,
	}
	for _, def := range AllTools() {
		if !need[def.Name] {
			continue
		}
		props, ok := def.Parameters["properties"].(map[string]any)
		if !ok {
			t.Fatalf("%s 缺少 properties", def.Name)
		}
		if _, has := props["patron_id"]; !has {
			t.Fatalf("%s 未在 schema 声明 patron_id，不会被会话强制注入", def.Name)
		}
	}
}

func firstLoanOf(t *testing.T, svc *service.Service, pid int64) int64 {
	t.Helper()
	loans, err := svc.PatronLoans(pid)
	if err != nil || len(loans) == 0 {
		t.Fatalf("读者 %d 无在借记录: %v", pid, err)
	}
	return loans[0].ID
}
