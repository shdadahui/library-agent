package store

// ---- 用户与会话实体 ----

// User 登录用户（绑定读者）。
type User struct {
	ID           int64  `json:"id"`
	Username     string `json:"username"`
	PasswordHash string `json:"-"`
	PatronID     int64  `json:"patron_id"`
	CreatedAt    string `json:"created_at"`
}

// Conversation 历史会话。
type Conversation struct {
	ID        int64  `json:"id"`
	UserID    int64  `json:"user_id"`
	Title     string `json:"title"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

// Message 会话消息（仅 user/assistant 文本，工具中间过程不入库）。
type Message struct {
	ID             int64  `json:"id"`
	ConversationID int64  `json:"conversation_id"`
	Role           string `json:"role"`
	Content        string `json:"content"`
	CreatedAt      string `json:"created_at"`
}

// ---- 用户 ----

// CreateUser 创建用户。
func (s *Store) CreateUser(u *User) (int64, error) {
	res, err := s.DB.Exec(`INSERT INTO users(username,password_hash,patron_id,created_at) VALUES(?,?,?,?)`,
		u.Username, u.PasswordHash, u.PatronID, u.CreatedAt)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// GetUserByUsername 按用户名取用户。
func (s *Store) GetUserByUsername(username string) (*User, error) {
	row := s.DB.QueryRow(`SELECT id,username,password_hash,patron_id,created_at FROM users WHERE username=?`, username)
	return scanUser(row)
}

// GetUserByID 按 ID 取用户。
func (s *Store) GetUserByID(id int64) (*User, error) {
	row := s.DB.QueryRow(`SELECT id,username,password_hash,patron_id,created_at FROM users WHERE id=?`, id)
	return scanUser(row)
}

func scanUser(row interface{ Scan(...any) error }) (*User, error) {
	var u User
	if err := row.Scan(&u.ID, &u.Username, &u.PasswordHash, &u.PatronID, &u.CreatedAt); err != nil {
		return nil, err
	}
	return &u, nil
}

// ---- 会话 ----

// CreateConversation 创建会话。
func (s *Store) CreateConversation(c *Conversation) (int64, error) {
	res, err := s.DB.Exec(`INSERT INTO conversations(user_id,title,created_at,updated_at) VALUES(?,?,?,?)`,
		c.UserID, c.Title, c.CreatedAt, c.UpdatedAt)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// ListConversations 用户的会话列表（最近更新在前）。
func (s *Store) ListConversations(userID int64) ([]Conversation, error) {
	rows, err := s.DB.Query(`SELECT id,user_id,title,created_at,updated_at FROM conversations WHERE user_id=? ORDER BY updated_at DESC, id DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Conversation{}
	for rows.Next() {
		var c Conversation
		if err := rows.Scan(&c.ID, &c.UserID, &c.Title, &c.CreatedAt, &c.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// GetConversation 按 ID 取会话（含归属）。
func (s *Store) GetConversation(id int64) (*Conversation, error) {
	row := s.DB.QueryRow(`SELECT id,user_id,title,created_at,updated_at FROM conversations WHERE id=?`, id)
	var c Conversation
	if err := row.Scan(&c.ID, &c.UserID, &c.Title, &c.CreatedAt, &c.UpdatedAt); err != nil {
		return nil, err
	}
	return &c, nil
}

// TouchConversation 更新会话 updated_at（活跃会话置顶）。
func (s *Store) TouchConversation(id int64) error {
	_, err := s.DB.Exec(`UPDATE conversations SET updated_at=? WHERE id=?`, NowDateTime(), id)
	return err
}

// UpdateConversationTitle 更新会话标题（首条用户消息生成）。
func (s *Store) UpdateConversationTitle(id int64, title string) error {
	_, err := s.DB.Exec(`UPDATE conversations SET title=? WHERE id=?`, title, id)
	return err
}

// DeleteConversation 删除会话及其消息（事务）。
func (s *Store) DeleteConversation(id int64) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM messages WHERE conversation_id=?`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM conversations WHERE id=?`, id); err != nil {
		return err
	}
	return tx.Commit()
}

// ---- 消息 ----

// AddMessage 追加消息。
func (s *Store) AddMessage(m *Message) (int64, error) {
	res, err := s.DB.Exec(`INSERT INTO messages(conversation_id,role,content,created_at) VALUES(?,?,?,?)`,
		m.ConversationID, m.Role, m.Content, m.CreatedAt)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// ListMessages 会话全部消息（按时间序）。
func (s *Store) ListMessages(conversationID int64) ([]Message, error) {
	rows, err := s.DB.Query(`SELECT id,conversation_id,role,content,created_at FROM messages WHERE conversation_id=? ORDER BY id`, conversationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Message{}
	for rows.Next() {
		var m Message
		if err := rows.Scan(&m.ID, &m.ConversationID, &m.Role, &m.Content, &m.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
