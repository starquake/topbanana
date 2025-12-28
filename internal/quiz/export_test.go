package quiz

import (
	"database/sql"
)

var (
	ExportWithTx              = withTx
	ExportUpsertQuestionInTx  = upsertQuestionInTx
	ExportDeleteQuestionsInTx = deleteQuestionsInTx
	ExportGetOptionIDsInTx    = getOptionIDsInTx
	ExportUpsertOptionInTx    = upsertOptionInTx
	ExportUpdateOptionInTx    = updateOptionInTx
)

func (s *SQLiteStore) DB() *sql.DB {
	return s.db
}
