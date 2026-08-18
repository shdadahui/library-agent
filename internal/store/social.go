package store

// ---- 读者社交：收藏 / 评分 / VIP ----

// Favorite 收藏。
type Favorite struct {
	ID        int64  `json:"id"`
	PatronID  int64  `json:"patron_id"`
	BiblioID  int64  `json:"biblio_id"`
	CreatedAt string `json:"created_at"`
}

// AddFavorite 收藏（已存在则忽略）。
func (s *Store) AddFavorite(patronID, biblioID int64) error {
	_, err := s.DB.Exec(`INSERT OR IGNORE INTO favorites(patron_id,biblio_id,created_at) VALUES(?,?,?)`,
		patronID, biblioID, NowDateTime())
	if err != nil {
		// MySQL 兼容（无 INSERT OR IGNORE 的冲突处理）
		_, err2 := s.DB.Exec(`INSERT IGNORE INTO favorites(patron_id,biblio_id,created_at) VALUES(?,?,?)`,
			patronID, biblioID, NowDateTime())
		return err2
	}
	return nil
}

// RemoveFavorite 取消收藏。
func (s *Store) RemoveFavorite(patronID, biblioID int64) error {
	_, err := s.DB.Exec(`DELETE FROM favorites WHERE patron_id=? AND biblio_id=?`, patronID, biblioID)
	return err
}

// IsFavorite 是否已收藏。
func (s *Store) IsFavorite(patronID, biblioID int64) (bool, error) {
	var n int
	err := s.DB.QueryRow(`SELECT COUNT(*) FROM favorites WHERE patron_id=? AND biblio_id=?`, patronID, biblioID).Scan(&n)
	return n > 0, err
}

// FavoriteBiblio 收藏列表视图（含书目信息）。
type FavoriteBiblio struct {
	BiblioID int64  `json:"biblio_id"`
	Title    string `json:"title"`
	Author   string `json:"author"`
	Subjects string `json:"subjects"`
	Avail    int    `json:"avail_copies"`
}

// ListFavorites 我的收藏列表。
func (s *Store) ListFavorites(patronID int64) ([]FavoriteBiblio, error) {
	rows, err := s.DB.Query(`SELECT b.id,b.title,b.author,b.subjects,
		(SELECT COUNT(*) FROM items i WHERE i.biblio_id=b.id AND i.status='available')
		FROM favorites f JOIN biblios b ON b.id=f.biblio_id
		WHERE f.patron_id=? ORDER BY f.id DESC`, patronID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []FavoriteBiblio{}
	for rows.Next() {
		var f FavoriteBiblio
		if err := rows.Scan(&f.BiblioID, &f.Title, &f.Author, &f.Subjects, &f.Avail); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// RateBook 评分（upsert：同读者同书只保留最新评分）。
func (s *Store) RateBook(patronID, biblioID int64, score int) error {
	_, err := s.DB.Exec(`DELETE FROM ratings WHERE patron_id=? AND biblio_id=?`, patronID, biblioID)
	if err != nil {
		return err
	}
	_, err = s.DB.Exec(`INSERT INTO ratings(patron_id,biblio_id,score,created_at) VALUES(?,?,?,?)`,
		patronID, biblioID, score, NowDateTime())
	return err
}

// BiblioRating 书目均分与评分人数。
func (s *Store) BiblioRating(biblioID int64) (avg float64, count int, err error) {
	err = s.DB.QueryRow(`SELECT COALESCE(AVG(score),0), COUNT(*) FROM ratings WHERE biblio_id=?`, biblioID).Scan(&avg, &count)
	return
}

// SetVip 设置/取消 VIP。
func (s *Store) SetVip(patronID int64, vip bool) error {
	v := 0
	if vip {
		v = 1
	}
	_, err := s.DB.Exec(`UPDATE patrons SET vip=? WHERE id=?`, v, patronID)
	return err
}

// IsVip 是否 VIP 会员。
func (s *Store) IsVip(patronID int64) bool {
	var v int
	if err := s.DB.QueryRow(`SELECT vip FROM patrons WHERE id=?`, patronID).Scan(&v); err != nil {
		return false
	}
	return v == 1
}
