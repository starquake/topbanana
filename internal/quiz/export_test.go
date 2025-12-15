package quiz

import (
	"context"
	"database/sql"
)

func (s *SQLiteStore) DB() *sql.DB {
	return s.db
}

func (s *SQLiteStore) WithTx(ctx context.Context, fn func(tx *sql.Tx) error) error {
	return s.withTx(ctx, fn)
}

func (s *SQLiteStore) UpsertQuestionInTx(ctx context.Context, tx *sql.Tx, qs *Question) error {
	return s.upsertQuestionInTx(ctx, tx, qs)
}

func (s *SQLiteStore) DeleteQuestionsInTx(ctx context.Context, tx *sql.Tx, deleteIDs []int64) error {
	return s.deleteQuestionsInTx(ctx, tx, deleteIDs)
}

func (s *SQLiteStore) GetOptionIDsInTx(ctx context.Context, tx *sql.Tx, questionID int64) ([]int64, error) {
	return s.getOptionIDsInTx(ctx, tx, questionID)
}

func (s *SQLiteStore) UpsertOptionInTx(ctx context.Context, tx *sql.Tx, o *Option) error {
	return s.upsertOptionInTx(ctx, tx, o)
}

func (s *SQLiteStore) UpdateOptionInTx(ctx context.Context, tx *sql.Tx, o *Option) error {
	return s.updateOptionInTx(ctx, tx, o)
}
