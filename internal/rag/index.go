// Package rag 提供轻量级 RAG（检索增强生成）能力：基于倒排索引 + 关键词打分的文档检索。
// 无向量库依赖，适合演示与小规模库（百万 token 以内），可平滑替换为 chromemego/Weaviate。
package rag

import (
	"sort"
	"strings"
)

// Document 知识库文档（一个文档 = 主题段）。
type Document struct {
	ID      string   `json:"id"`
	Title   string   `json:"title"`
	Content string   `json:"content"`
	Tags    []string `json:"tags,omitempty"`
}

// ScoredDoc 检索结果（含相似度）。
type ScoredDoc struct {
	Document
	Score float64 `json:"score"`
}

// Index 倒排索引（关键词 → 文档频次）。
type Index struct {
	docs   []Document
	tokens map[string]map[int]int // token → docIdx → 词频
}

// New 构建索引（会预分词）。
func New(documents []Document) *Index {
	idx := &Index{
		docs:   documents,
		tokens: map[string]map[int]int{},
	}
	for i, d := range documents {
		for _, t := range tokenize(d.Content + " " + d.Title + " " + strings.Join(d.Tags, " ")) {
			if idx.tokens[t] == nil {
				idx.tokens[t] = map[int]int{}
			}
			idx.tokens[t][i]++
		}
	}
	return idx
}

// Size 文档数。
func (idx *Index) Size() int { return len(idx.docs) }

// Search 检索 topK 文档（TF 归一化打分）。
func (idx *Index) Search(query string, topK int) []ScoredDoc {
	if topK <= 0 {
		topK = 3
	}
	q := tokenize(query)
	if len(q) == 0 {
		return nil
	}
	scores := map[int]float64{}
	for _, t := range q {
		for docIdx, freq := range idx.tokens[t] {
			// 词频对文档 token 总数归一化（避免长文档天然得分高）
			scores[docIdx] += float64(freq) / float64(len(idx.tokens[t]))
		}
	}
	if len(scores) == 0 {
		return nil
	}
	// 排序
	type sk struct {
		idx   int
		score float64
	}
	arr := make([]sk, 0, len(scores))
	for i, s := range scores {
		// 长度归一化（除以文档 token 总数）
		docLen := docTokenTotal(idx, i)
		if docLen > 0 {
			s /= float64(docLen)
		}
		arr = append(arr, sk{idx: i, score: s})
	}
	sort.SliceStable(arr, func(i, j int) bool { return arr[i].score > arr[j].score })
	if len(arr) > topK {
		arr = arr[:topK]
	}
	out := make([]ScoredDoc, len(arr))
	for i, s := range arr {
		out[i] = ScoredDoc{Document: idx.docs[s.idx], Score: s.score}
	}
	return out
}

// docTokenTotal 文档总 token 数（用于长度归一化）。
func docTokenTotal(idx *Index, docIdx int) int {
	n := 0
	for _, postings := range idx.tokens {
		if _, ok := postings[docIdx]; ok {
			n++
		}
	}
	return n
}

// tokenize 简单分词：标点/空格切分 + 小写 + 去重 + 中文单字（演示场景兼顾中英文）。
func tokenize(s string) []string {
	repl := strings.NewReplacer(",", " ", "，", " ", "、", " ", "·", " ", "；", " ", ";", " ",
		"（", " ", "）", " ", "\n", " ", "!", " ", "?", " ", "。", " ")
	s = strings.ToLower(repl.Replace(s))
	out := make([]string, 0)
	seen := map[string]bool{}
	add := func(t string) {
		t = strings.TrimSpace(t)
		if t == "" || seen[t] || len(t) > 40 {
			return
		}
		seen[t] = true
		out = append(out, t)
	}
	// 词
	for _, p := range strings.Fields(s) {
		add(p)
	}
	// 中文单字（按字符切分，便于模糊匹配）
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			continue
		}
		add(string(r))
	}
	return out
}
