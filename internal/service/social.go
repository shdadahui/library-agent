package service

import "github.com/shdadahui/library-agent/internal/store"

// ---- 读者社交：收藏 / 评分 / VIP ----

// ToggleFavorite 收藏/取消收藏（返回收藏后的状态）。
func (s *Service) ToggleFavorite(patronID, biblioID int64) (bool, error) {
	if _, err := s.st.GetBiblio(biblioID); err != nil {
		return false, ErrBiblioNotFound
	}
	ok, _ := s.st.IsFavorite(patronID, biblioID)
	if ok {
		return false, s.st.RemoveFavorite(patronID, biblioID)
	}
	return true, s.st.AddFavorite(patronID, biblioID)
}

// MyFavorites 我的收藏列表。
func (s *Service) MyFavorites(patronID int64) ([]store.FavoriteBiblio, error) {
	return s.st.ListFavorites(patronID)
}

// RateBook 评分（1-5）。
func (s *Service) RateBook(patronID, biblioID int64, score int) (float64, int, error) {
	if score < 1 || score > 5 {
		return 0, 0, ErrInvalidInput
	}
	if _, err := s.st.GetBiblio(biblioID); err != nil {
		return 0, 0, ErrBiblioNotFound
	}
	if err := s.st.RateBook(patronID, biblioID, score); err != nil {
		return 0, 0, err
	}
	avg, count, _ := s.st.BiblioRating(biblioID)
	return avg, count, nil
}

// BiblioRating 书目均分。
func (s *Service) BiblioRating(biblioID int64) (float64, int, error) {
	return s.st.BiblioRating(biblioID)
}

// SetVip 管理端设置 VIP。
func (s *Service) SetVip(patronID int64, vip bool) error {
	return s.st.SetVip(patronID, vip)
}

// IsVip 是否 VIP（借阅上限扩展用）。
func (s *Service) IsVip(patronID int64) bool {
	return s.st.IsVip(patronID)
}
