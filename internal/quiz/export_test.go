package quiz

import "database/sql"

func (s *SQLiteStore) DB() *sql.DB {
	return s.db
}

const (
	ListQuizzesSQL = listQuizzesSQL
)
