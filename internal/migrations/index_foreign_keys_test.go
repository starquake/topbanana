package migrations_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	dbgen "github.com/starquake/topbanana/internal/db"
	"github.com/starquake/topbanana/internal/dbtest"
)

// TestIndexForeignKeys_EveryChildColumnIsIndexed pins that every foreign-key
// column leads some index, so FK checks and ON DELETE actions on the parent
// seek instead of scanning the child table (#1345).
func TestIndexForeignKeys_EveryChildColumnIsIndexed(t *testing.T) {
	t.Parallel()

	db := dbtest.Open(t)
	t.Cleanup(func() {
		if cerr := db.Close(); cerr != nil {
			t.Errorf("db.Close err = %v", cerr)
		}
	})

	rows, err := db.QueryContext(t.Context(), `
		SELECT m.name, fk."from"
		FROM sqlite_schema m, pragma_foreign_key_list(m.name) fk
		WHERE m.type = 'table'
		  AND NOT EXISTS (
		      SELECT 1
		      FROM pragma_index_list(m.name) il, pragma_index_info(il.name) ii
		      WHERE ii.seqno = 0 AND ii.name = fk."from"
		  )
		ORDER BY m.name, fk."from"`)
	if err != nil {
		t.Fatalf("query unindexed foreign keys err = %v", err)
	}
	t.Cleanup(func() {
		if cerr := rows.Close(); cerr != nil {
			t.Errorf("rows.Close err = %v", cerr)
		}
	})

	var missing []string
	for rows.Next() {
		var table, column string
		if err = rows.Scan(&table, &column); err != nil {
			t.Fatalf("scan err = %v", err)
		}
		missing = append(missing, table+"("+column+")")
	}
	if err = rows.Err(); err != nil {
		t.Fatalf("rows err = %v", err)
	}
	if got := missing; len(got) != 0 {
		t.Errorf("foreign-key columns with no leading index = %v, want none", got)
	}
}

// TestIndexForeignKeys_OptionLookupsUseIndex pins that the sqlc queries reading
// a question's options seek options_question_id_idx instead of scanning options
// (#1345).
func TestIndexForeignKeys_OptionLookupsUseIndex(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		call func(ctx context.Context, q *dbgen.Queries) error
	}{
		{
			name: "ListOptionsByQuestionID",
			call: func(ctx context.Context, q *dbgen.Queries) error {
				_, err := q.ListOptionsByQuestionID(ctx, 1)

				return err
			},
		},
		{
			name: "ListOptionIDsByQuestionID",
			call: func(ctx context.Context, q *dbgen.Queries) error {
				_, err := q.ListOptionIDsByQuestionID(ctx, 1)

				return err
			},
		},
		{
			name: "ListOptionsByQuizID",
			call: func(ctx context.Context, q *dbgen.Queries) error {
				_, err := q.ListOptionsByQuizID(ctx, 1)

				return err
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			db := dbtest.Open(t)
			t.Cleanup(func() {
				if cerr := db.Close(); cerr != nil {
					t.Errorf("db.Close err = %v", cerr)
				}
			})
			rec := &dbtest.QueryRecorder{DB: db}
			if got, want := tt.call(t.Context(), dbgen.New(rec)), errors.ErrUnsupported; !errors.Is(got, want) {
				t.Fatalf("%s err = %v, want %v", tt.name, got, want)
			}

			plan := dbtest.QueryPlan(t, db, rec.Query, rec.Args...)
			joined := strings.Join(plan, "\n")
			if got, want := joined, "INDEX options_question_id_idx"; !strings.Contains(got, want) {
				t.Errorf("plan = %q, should contain %q", got, want)
			}
			if slices.ContainsFunc(plan, isScan) {
				t.Errorf("plan = %q, should have no SCAN step", joined)
			}
		})
	}
}

func isScan(step string) bool { return strings.HasPrefix(step, "SCAN ") }
