package agent

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/shdadahui/library-agent/internal/config"
	"github.com/shdadahui/library-agent/internal/service"
	"github.com/shdadahui/library-agent/internal/store"
)

// TestConcurrentRunNoSharedState 并发 Run 同一 Loop 单例（mock 模式）。
// 此前 Loop.Usage 是共享字段、Run 内写入，并发对话在 -race 下即触发数据竞争；
// 改为 Run 返回 usage 后应无任何共享写。-race 运行本用例即回归验证。
func TestConcurrentRunNoSharedState(t *testing.T) {
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("打开内存库失败: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	bid, _ := st.InsertBiblio(&store.Biblio{Title: "三体", Author: "刘慈欣", Lang: "zh"})
	_, _ = st.InsertItem(&store.Item{BiblioID: bid, Barcode: "B1", Status: "available", LoanDurationDays: 14})
	pid, _ := st.InsertPatron(&store.Patron{Name: "张三", Barcode: "P1"})
	patron, _ := st.GetPatron(pid)

	cfg := &config.Config{
		Providers:      map[string]config.Provider{"mock": {DefaultModel: "mock"}},
		ActiveProvider: "mock",
		MaxIterations:  2,
		Temperature:    0.7,
	}
	l := NewLoop(cfg, service.New(st))

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			msg := fmt.Sprintf("帮我查一下三体（并发 #%d）", n)
			if _, _, err := l.Run(context.Background(), patron, nil, msg, func(Event) {}); err != nil {
				t.Errorf("并发 Run 出错: %v", err)
			}
		}(i)
	}
	wg.Wait()
}
