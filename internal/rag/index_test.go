package rag
import "testing"
func TestRAGSearchBasic(t *testing.T) {
    idx := New(DefaultDocs)
    if idx.Size() < 5 { t.Fatal("内置文档不足") }
    docs := idx.Search("能借几本书", 3)
    if len(docs) == 0 { t.Fatal("应命中借阅规则") }
    t.Logf("top: %s (%.3f)", docs[0].Title, docs[0].Score)
}
func TestRAGSearchRank(t *testing.T) {
    idx := New(DefaultDocs)
    docs := idx.Search("续借", 3)
    if len(docs) == 0 || docs[0].Title != "续借规则" {
        t.Fatalf("续借应排第一，实际: %+v", docs)
    }
}
