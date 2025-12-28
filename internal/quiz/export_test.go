package quiz

import (
	"database/sql"
)

var (
	ExportSQLiteStoreWithTx              = (*SQLiteStore).withTx
	ExportSQLiteStoreUpsertQuestionInTx  = (*SQLiteStore).upsertQuestionInTx
	ExportSQLiteStoreDeleteQuestionsInTx = (*SQLiteStore).deleteQuestionsInTx
	ExportSQLiteStoreGetOptionIDsInTx    = (*SQLiteStore).getOptionIDsInTx
	ExportSQLiteStoreUpsertOptionInTx    = (*SQLiteStore).upsertOptionInTx
	ExportSQLiteStoreUpdateOptionInTx    = (*SQLiteStore).updateOptionInTx
)

func (s *SQLiteStore) DB() *sql.DB {
	return s.db
}
