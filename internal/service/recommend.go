package service

import (
	"sort"
	"strings"

	"github.com/shdadahui/library-agent/internal/store"
)

// Recommendation 推荐结果。
type Recommendation struct {
	store.Biblio
	Available int      `json:"available"`
	Score     int      `json:"score"`
	Reasons   []string `json:"reasons"`
}

// tokenize 将逗号/空格/间隔号分隔的文本切成关键词集合。
func tokenize(s string) []string {
	repl := strings.NewReplacer(",", " ", "，", " ", "、", " ", "·", " ", "；", " ", ";", " ")
	parts := strings.Fields(repl.Replace(s))
	out := make([]string, 0, len(parts))
	seen := map[string]bool{}
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	return out
}

// RecommendForPatron 智能推荐：
//   - taste 非空：按兴趣主题/关键词匹配（如"科幻""数学"）
//   - taste 为空且读者有借阅历史：基于历史书目主题/作者构建兴趣画像，找相似书（个性化）
//   - 无历史：热门榜兜底
//
// 候选排除读者已借过的书，排序：兴趣匹配分 + 热门加分 + 可借加分。
func (s *Service) RecommendForPatron(patronID int64, taste string, limit int) ([]Recommendation, error) {
	if limit <= 0 {
		limit = 5
	}
	if limit > 20 {
		limit = 20
	}
	interest := map[string]int{} // 关键词 → 权重
	borrowedSet := map[int64]bool{}

	// 1. 借阅历史构建兴趣画像
	if patronID > 0 {
		history, _ := s.st.LoanHistory(patronID)
		for _, l := range history {
			it, err := s.st.GetItem(l.ItemID)
			if err != nil {
				continue
			}
			borrowedSet[it.BiblioID] = true
			b, err := s.st.GetBiblio(it.BiblioID)
			if err != nil {
				continue
			}
			for _, k := range tokenize(b.Subjects) {
				interest[k]++
			}
			if b.Author != "" {
				interest[b.Author]++ // 喜欢的作者权重
			}
		}
	}
	// 2. 兴趣主题输入
	for _, k := range tokenize(taste) {
		interest[k] += 3
	}

	// 3. 候选书目（全库，排除已借）
	books, err := s.st.SearchBooks("", "", 1000)
	if err != nil {
		return nil, err
	}
	// 4. 评分
	var ranked []Recommendation
	for _, b := range books {
		if borrowedSet[b.ID] {
			continue
		}
		rec := Recommendation{Biblio: b}
		score := 0
		var why []string
		for _, k := range tokenize(b.Subjects) {
			if w := interest[k]; w > 0 {
				score += w
				why = append(why, k)
			}
		}
		if b.Author != "" && interest[b.Author] > 0 {
			score += interest[b.Author] * 2
			why = append(why, "作者 "+b.Author)
		}
		// 可借加分（推荐可借的书）
		items, _ := s.st.ListItems(b.ID)
		avail := 0
		for _, it := range items {
			if it.Status == "available" {
				avail++
			}
		}
		rec.Available = avail
		if avail > 0 {
			score += 2
			why = append(why, "有可借副本")
		}
		rec.Score = score
		rec.Reasons = dedupStrings(why)
		if score > 0 {
			ranked = append(ranked, rec)
		}
	}
	sort.SliceStable(ranked, func(i, j int) bool { return ranked[i].Score > ranked[j].Score })

	// 5. 结果不足时用热门榜补充
	if len(ranked) < limit {
		hot, err := s.st.TopBorrowed(limit * 2)
		if err == nil {
			for _, h := range hot {
				if len(ranked) >= limit {
					break
				}
				if borrowedSet[h.Biblio.ID] {
					continue
				}
				// 已在 ranked 中则跳过
				exists := false
				for _, r := range ranked {
					if r.ID == h.Biblio.ID {
						exists = true
						break
					}
				}
				if exists {
					continue
				}
				avail := 0
				if items, err := s.st.ListItems(h.Biblio.ID); err == nil {
					for _, it := range items {
						if it.Status == "available" {
							avail++
						}
					}
				}
				ranked = append(ranked, Recommendation{
					Biblio: h.Biblio, Available: avail, Score: h.BorrowCount,
					Reasons: []string{"热门图书（被借 " + itoa(h.BorrowCount) + " 次）"},
				})
			}
		}
	}
	if len(ranked) > limit {
		ranked = ranked[:limit]
	}
	return ranked, nil
}

func dedupStrings(list []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, v := range list {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	if neg {
		b = append([]byte{'-'}, b...)
	}
	return string(b)
}
