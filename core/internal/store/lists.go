package store

import "time"

// ListItem is one line on one of the user's lists: shopping, to-do, or
// any list they name.
type ListItem struct {
	ID        int64     `json:"id"`
	List      string    `json:"list"`
	Text      string    `json:"text"`
	Done      bool      `json:"done"`
	CreatedAt time.Time `json:"created_at"`
	DoneAt    time.Time `json:"done_at"`
}

func (s *Store) AddListItem(list, text string, at time.Time) (ListItem, error) {
	res, err := s.db.Exec(`INSERT INTO list_items(list,text,created_at) VALUES(?,?,?)`, list, text, ms(at))
	if err != nil {
		return ListItem{}, err
	}
	id, _ := res.LastInsertId()
	return ListItem{ID: id, List: list, Text: text, CreatedAt: at}, nil
}

// ListItems returns the items of one list, or of every list when list is
// empty: open items first, each group oldest first. Items ticked off
// before doneSince are left out.
func (s *Store) ListItems(list string, doneSince time.Time) ([]ListItem, error) {
	rows, err := s.db.Query(`SELECT id,list,text,done,created_at,done_at FROM list_items
		WHERE (?='' OR list=?) AND (done=0 OR done_at>=?) ORDER BY list, done, id`, list, list, ms(doneSince))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ListItem{}
	for rows.Next() {
		var it ListItem
		var created, done int64
		if err := rows.Scan(&it.ID, &it.List, &it.Text, &it.Done, &created, &done); err != nil {
			return nil, err
		}
		it.CreatedAt = time.UnixMilli(created)
		if done > 0 {
			it.DoneAt = time.UnixMilli(done)
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

func (s *Store) SetListItemDone(id int64, done bool, at time.Time) error {
	stamp := int64(0)
	if done {
		stamp = ms(at)
	}
	_, err := s.db.Exec(`UPDATE list_items SET done=?, done_at=? WHERE id=?`, done, stamp, id)
	return err
}

func (s *Store) DeleteListItem(id int64) error {
	_, err := s.db.Exec(`DELETE FROM list_items WHERE id=?`, id)
	return err
}
