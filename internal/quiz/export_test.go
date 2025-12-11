package quiz

import (
	"context"
	"database/sql"
)

const (
	ListQuizzesSQL = listQuizzesSQL
)

func (s *SQLiteStore) DB() *sql.DB {
	return s.db
}

func (s *SQLiteStore) WithTx(ctx context.Context, fn func(tx *sql.Tx) error) error {
	return s.withTx(ctx, fn)
}
