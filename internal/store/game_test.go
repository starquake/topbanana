package store_test

import (
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/starquake/topbanana/internal/dbtest"
	"github.com/starquake/topbanana/internal/game"
	. "github.com/starquake/topbanana/internal/store"
)

func TestGameStore_Ping(t *testing.T) {
	t.Parallel()

	t.Run("ping success", func(t *testing.T) {
		t.Parallel()
		db := dbtest.Open(t)
		s := NewGameStore(db, slog.Default())
		if err := s.Ping(t.Context()); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("ping failure", func(t *testing.T) {
		t.Parallel()
		db := dbtest.Open(t)
		s := NewGameStore(db, slog.Default())
		if err := db.Close(); err != nil {
			t.Fatalf("failed to close db: %v", err)
		}
		err := s.Ping(t.Context())
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if got, want := err.Error(), "failed to ping database"; !strings.Contains(got, want) {
			t.Errorf("err.Error() = %q, should contain %q", got, want)
		}
	})
}

func TestGameStore_CreateGame(t *testing.T) {
	t.Parallel()

	t.Run("populates ID and CreatedAt", func(t *testing.T) {
		t.Parallel()
		db := dbtest.Open(t)
		quizStore := NewQuizStore(db, slog.Default())
		testQuiz := newTestQuizzes()[0]
		if err := quizStore.CreateQuiz(t.Context(), testQuiz); err != nil {
			t.Fatalf("failed to create quiz: %v", err)
		}

		gameStore := NewGameStore(db, slog.Default())
		g := &game.Game{QuizID: testQuiz.ID}
		if err := gameStore.CreateGame(t.Context(), g); err != nil {
			t.Fatalf("failed to create game: %v", err)
		}
		if got := g.ID; got == "" {
			t.Error("g.ID is empty, want non-empty string")
		}
		if g.CreatedAt.IsZero() {
			t.Error("g.CreatedAt is zero, want non-zero time")
		}
	})
}

func TestGameStore_GetGame(t *testing.T) {
	t.Parallel()

	t.Run("returns existing game", func(t *testing.T) {
		t.Parallel()
		db := dbtest.Open(t)
		quizStore := NewQuizStore(db, slog.Default())
		testQuiz := newTestQuizzes()[0]
		if err := quizStore.CreateQuiz(t.Context(), testQuiz); err != nil {
			t.Fatalf("failed to create quiz: %v", err)
		}

		gameStore := NewGameStore(db, slog.Default())
		g := &game.Game{QuizID: testQuiz.ID}
		if err := gameStore.CreateGame(t.Context(), g); err != nil {
			t.Fatalf("failed to create game: %v", err)
		}

		fetched, err := gameStore.GetGame(t.Context(), g.ID)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got, want := fetched.ID, g.ID; got != want {
			t.Errorf("fetched.ID = %q, want %q", got, want)
		}
		if got, want := fetched.QuizID, testQuiz.ID; got != want {
			t.Errorf("fetched.QuizID = %d, want %d", got, want)
		}
	})

	t.Run("returns ErrGameNotFound for unknown ID", func(t *testing.T) {
		t.Parallel()
		db := dbtest.Open(t)
		gameStore := NewGameStore(db, slog.Default())
		_, err := gameStore.GetGame(t.Context(), "nonexistent")
		if got, want := err, game.ErrGameNotFound; !errors.Is(got, want) {
			t.Errorf("err = %v, want %v", got, want)
		}
	})
}

func TestGameStore_StartGame(t *testing.T) {
	t.Parallel()

	t.Run("sets started_at on the game", func(t *testing.T) {
		t.Parallel()
		db := dbtest.Open(t)
		quizStore := NewQuizStore(db, slog.Default())
		testQuiz := newTestQuizzes()[0]
		if err := quizStore.CreateQuiz(t.Context(), testQuiz); err != nil {
			t.Fatalf("failed to create quiz: %v", err)
		}

		gameStore := NewGameStore(db, slog.Default())
		g := &game.Game{QuizID: testQuiz.ID}
		if err := gameStore.CreateGame(t.Context(), g); err != nil {
			t.Fatalf("failed to create game: %v", err)
		}
		if err := gameStore.StartGame(t.Context(), g.ID); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		got, err := gameStore.GetGame(t.Context(), g.ID)
		if err != nil {
			t.Fatalf("failed to get game after start: %v", err)
		}
		if got.StartedAt == nil {
			t.Error("StartedAt is nil after starting game, want non-nil")
		}
	})

	t.Run("returns ErrStartingGameNoRowsAffected for unknown ID", func(t *testing.T) {
		t.Parallel()
		db := dbtest.Open(t)
		gameStore := NewGameStore(db, slog.Default())
		err := gameStore.StartGame(t.Context(), "nonexistent")
		if got, want := err, game.ErrStartingGameNoRowsAffected; !errors.Is(got, want) {
			t.Errorf("err = %v, want %v", got, want)
		}
	})
}

func TestGameStore_CreateParticipant(t *testing.T) {
	t.Parallel()

	t.Run("populates ID and JoinedAt", func(t *testing.T) {
		t.Parallel()
		db := dbtest.Open(t)
		quizStore := NewQuizStore(db, slog.Default())
		testQuiz := newTestQuizzes()[0]
		if err := quizStore.CreateQuiz(t.Context(), testQuiz); err != nil {
			t.Fatalf("failed to create quiz: %v", err)
		}

		gameStore := NewGameStore(db, slog.Default())
		g := &game.Game{QuizID: testQuiz.ID}
		if err := gameStore.CreateGame(t.Context(), g); err != nil {
			t.Fatalf("failed to create game: %v", err)
		}

		p := &game.Participant{GameID: g.ID, PlayerID: 1}
		if err := gameStore.CreateParticipant(t.Context(), p); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if p.ID == 0 {
			t.Error("p.ID is 0, want non-zero")
		}
		if p.JoinedAt.IsZero() {
			t.Error("p.JoinedAt is zero, want non-zero time")
		}
	})
}

func TestGameStore_CreateQuestion(t *testing.T) {
	t.Parallel()

	t.Run("populates ID, StartedAt, and ExpiredAt", func(t *testing.T) {
		t.Parallel()
		db := dbtest.Open(t)
		quizStore := NewQuizStore(db, slog.Default())
		testQuiz := newTestQuizzes()[0]
		if err := quizStore.CreateQuiz(t.Context(), testQuiz); err != nil {
			t.Fatalf("failed to create quiz: %v", err)
		}

		gameStore := NewGameStore(db, slog.Default())
		g := &game.Game{QuizID: testQuiz.ID}
		if err := gameStore.CreateGame(t.Context(), g); err != nil {
			t.Fatalf("failed to create game: %v", err)
		}

		now := time.Now()
		gq := &game.Question{
			GameID:     g.ID,
			QuestionID: testQuiz.Questions[0].ID,
			StartedAt:  now,
			ExpiredAt:  now.Add(10 * time.Second),
		}
		if err := gameStore.CreateQuestion(t.Context(), gq); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if gq.ID == 0 {
			t.Error("gq.ID is 0, want non-zero")
		}
	})
}

func TestGameStore_CreateAnswer(t *testing.T) {
	t.Parallel()

	t.Run("populates ID and AnsweredAt", func(t *testing.T) {
		t.Parallel()
		db := dbtest.Open(t)
		quizStore := NewQuizStore(db, slog.Default())
		testQuiz := newTestQuizzes()[0]
		if err := quizStore.CreateQuiz(t.Context(), testQuiz); err != nil {
			t.Fatalf("failed to create quiz: %v", err)
		}

		gameStore := NewGameStore(db, slog.Default())
		g := &game.Game{QuizID: testQuiz.ID}
		if err := gameStore.CreateGame(t.Context(), g); err != nil {
			t.Fatalf("failed to create game: %v", err)
		}

		now := time.Now()
		gq := &game.Question{
			GameID:     g.ID,
			QuestionID: testQuiz.Questions[0].ID,
			StartedAt:  now,
			ExpiredAt:  now.Add(10 * time.Second),
		}
		if err := gameStore.CreateQuestion(t.Context(), gq); err != nil {
			t.Fatalf("failed to create game question: %v", err)
		}

		a := &game.Answer{
			GameID:     g.ID,
			PlayerID:   1,
			QuestionID: gq.ID,
			OptionID:   testQuiz.Questions[0].Options[0].ID,
		}
		if err := gameStore.CreateAnswer(t.Context(), a); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if a.ID == 0 {
			t.Error("a.ID is 0, want non-zero")
		}
		if a.AnsweredAt.IsZero() {
			t.Error("a.AnsweredAt is zero, want non-zero time")
		}
	})
}

func TestGameStore_GetGameByPlayerAndQuiz(t *testing.T) {
	t.Parallel()

	t.Run("returns ErrGameNotFound when player has no game", func(t *testing.T) {
		t.Parallel()
		db := dbtest.Open(t)
		quizStore := NewQuizStore(db, slog.Default())
		testQuiz := newTestQuizzes()[0]
		if err := quizStore.CreateQuiz(t.Context(), testQuiz); err != nil {
			t.Fatalf("failed to create quiz: %v", err)
		}

		gameStore := NewGameStore(db, slog.Default())
		_, err := gameStore.GetGameByPlayerAndQuiz(t.Context(), 99, testQuiz.ID)
		if got, want := err, game.ErrGameNotFound; !errors.Is(got, want) {
			t.Errorf("err = %v, want %v", got, want)
		}
	})

	t.Run("returns the most-recent game for the player and quiz", func(t *testing.T) {
		t.Parallel()
		db := dbtest.Open(t)

		quizStore := NewQuizStore(db, slog.Default())
		testQuiz := newTestQuizzes()[0]
		if err := quizStore.CreateQuiz(t.Context(), testQuiz); err != nil {
			t.Fatalf("failed to create quiz: %v", err)
		}

		playerStore := NewPlayerStore(db, slog.Default())
		player, err := playerStore.CreateAnonymousPlayer(t.Context(), "anon-resume-1")
		if err != nil {
			t.Fatalf("failed to create player: %v", err)
		}

		gameStore := NewGameStore(db, slog.Default())
		g := &game.Game{QuizID: testQuiz.ID}
		if err = gameStore.CreateGame(t.Context(), g); err != nil {
			t.Fatalf("failed to create game: %v", err)
		}
		if err = gameStore.CreateParticipant(
			t.Context(), &game.Participant{GameID: g.ID, PlayerID: player.ID},
		); err != nil {
			t.Fatalf("failed to create participant: %v", err)
		}

		existing, err := gameStore.GetGameByPlayerAndQuiz(t.Context(), player.ID, testQuiz.ID)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got, want := existing.ID, g.ID; got != want {
			t.Errorf("existing.ID = %q, want %q", got, want)
		}
		if got, want := existing.QuizID, testQuiz.ID; got != want {
			t.Errorf("existing.QuizID = %d, want %d", got, want)
		}
	})
}

func TestGameStore_DeleteGamesForPlayerOnQuiz(t *testing.T) {
	t.Parallel()

	t.Run("removes games and dependent rows for the player", func(t *testing.T) {
		t.Parallel()
		db := dbtest.Open(t)

		quizStore := NewQuizStore(db, slog.Default())
		testQuiz := newTestQuizzes()[0]
		if err := quizStore.CreateQuiz(t.Context(), testQuiz); err != nil {
			t.Fatalf("failed to create quiz: %v", err)
		}

		playerStore := NewPlayerStore(db, slog.Default())
		player, err := playerStore.CreateAnonymousPlayer(t.Context(), "anon-reset-1")
		if err != nil {
			t.Fatalf("failed to create player: %v", err)
		}

		gameStore := NewGameStore(db, slog.Default())
		g := &game.Game{QuizID: testQuiz.ID}
		if err = gameStore.CreateGame(t.Context(), g); err != nil {
			t.Fatalf("failed to create game: %v", err)
		}
		if err = gameStore.CreateParticipant(
			t.Context(), &game.Participant{GameID: g.ID, PlayerID: player.ID},
		); err != nil {
			t.Fatalf("failed to create participant: %v", err)
		}

		now := time.Now().UTC().Truncate(time.Second)
		gq := &game.Question{
			GameID:     g.ID,
			QuestionID: testQuiz.Questions[0].ID,
			StartedAt:  now,
			ExpiredAt:  now.Add(10 * time.Second),
		}
		if err = gameStore.CreateQuestion(t.Context(), gq); err != nil {
			t.Fatalf("failed to create game question: %v", err)
		}
		if err = gameStore.CreateAnswer(t.Context(), &game.Answer{
			GameID:     g.ID,
			PlayerID:   player.ID,
			QuestionID: gq.ID,
			OptionID:   testQuiz.Questions[0].Options[0].ID,
		}); err != nil {
			t.Fatalf("failed to create answer: %v", err)
		}

		if err = gameStore.DeleteGamesForPlayerOnQuiz(t.Context(), player.ID, testQuiz.ID); err != nil {
			t.Fatalf("DeleteGamesForPlayerOnQuiz err = %v, want nil", err)
		}

		// All four tables for this game should be empty.
		assertCount := func(label, sqlStr string, want int) {
			t.Helper()
			row := db.QueryRowContext(t.Context(), sqlStr, g.ID)
			var got int
			if scanErr := row.Scan(&got); scanErr != nil {
				t.Fatalf("scan %s err = %v", label, scanErr)
			}
			if got != want {
				t.Errorf("%s count = %d, want %d", label, got, want)
			}
		}
		assertCount("game_answers", `SELECT COUNT(*) FROM game_answers WHERE game_id = ?`, 0)
		assertCount("game_questions", `SELECT COUNT(*) FROM game_questions WHERE game_id = ?`, 0)
		assertCount("game_participants", `SELECT COUNT(*) FROM game_participants WHERE game_id = ?`, 0)
		assertCount("games", `SELECT COUNT(*) FROM games WHERE id = ?`, 0)
	})

	t.Run("no-op when player has no games for the quiz", func(t *testing.T) {
		t.Parallel()
		db := dbtest.Open(t)

		quizStore := NewQuizStore(db, slog.Default())
		testQuiz := newTestQuizzes()[0]
		if err := quizStore.CreateQuiz(t.Context(), testQuiz); err != nil {
			t.Fatalf("failed to create quiz: %v", err)
		}

		gameStore := NewGameStore(db, slog.Default())
		if err := gameStore.DeleteGamesForPlayerOnQuiz(t.Context(), 999, testQuiz.ID); err != nil {
			t.Errorf("DeleteGamesForPlayerOnQuiz err = %v, want nil (no rows means no error)", err)
		}
	})

	t.Run("does not touch other players' games on the same quiz", func(t *testing.T) {
		t.Parallel()
		db := dbtest.Open(t)

		quizStore := NewQuizStore(db, slog.Default())
		testQuiz := newTestQuizzes()[0]
		if err := quizStore.CreateQuiz(t.Context(), testQuiz); err != nil {
			t.Fatalf("failed to create quiz: %v", err)
		}

		playerStore := NewPlayerStore(db, slog.Default())
		victim, err := playerStore.CreateAnonymousPlayer(t.Context(), "anon-reset-victim")
		if err != nil {
			t.Fatalf("failed to create victim player: %v", err)
		}
		bystander, err := playerStore.CreateAnonymousPlayer(t.Context(), "anon-reset-bystander")
		if err != nil {
			t.Fatalf("failed to create bystander player: %v", err)
		}

		gameStore := NewGameStore(db, slog.Default())
		victimGame := &game.Game{QuizID: testQuiz.ID}
		if err = gameStore.CreateGame(t.Context(), victimGame); err != nil {
			t.Fatalf("failed to create victim game: %v", err)
		}
		if err = gameStore.CreateParticipant(
			t.Context(), &game.Participant{GameID: victimGame.ID, PlayerID: victim.ID},
		); err != nil {
			t.Fatalf("failed to create victim participant: %v", err)
		}

		bystanderGame := &game.Game{QuizID: testQuiz.ID}
		if err = gameStore.CreateGame(t.Context(), bystanderGame); err != nil {
			t.Fatalf("failed to create bystander game: %v", err)
		}
		if err = gameStore.CreateParticipant(
			t.Context(), &game.Participant{GameID: bystanderGame.ID, PlayerID: bystander.ID},
		); err != nil {
			t.Fatalf("failed to create bystander participant: %v", err)
		}

		if err = gameStore.DeleteGamesForPlayerOnQuiz(t.Context(), victim.ID, testQuiz.ID); err != nil {
			t.Fatalf("DeleteGamesForPlayerOnQuiz err = %v, want nil", err)
		}

		// Bystander's game must still exist.
		if _, err = gameStore.GetGame(t.Context(), bystanderGame.ID); err != nil {
			t.Errorf("bystander game lookup err = %v, want nil", err)
		}
	})
}

func TestGameStore_ListAnswersForQuizLeaderboard(t *testing.T) {
	t.Parallel()

	t.Run("returns empty slice for quiz with no games", func(t *testing.T) {
		t.Parallel()
		db := dbtest.Open(t)
		quizStore := NewQuizStore(db, slog.Default())
		testQuiz := newTestQuizzes()[0]
		if err := quizStore.CreateQuiz(t.Context(), testQuiz); err != nil {
			t.Fatalf("failed to create quiz: %v", err)
		}

		gameStore := NewGameStore(db, slog.Default())
		got, err := gameStore.ListAnswersForQuizLeaderboard(t.Context(), testQuiz.ID)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got) != 0 {
			t.Errorf("len(answers) = %d, want 0", len(got))
		}
	})

	t.Run("returns one row per stored answer with joined fields", func(t *testing.T) {
		t.Parallel()
		db := dbtest.Open(t)
		quizStore := NewQuizStore(db, slog.Default())
		testQuiz := newTestQuizzes()[0]
		if err := quizStore.CreateQuiz(t.Context(), testQuiz); err != nil {
			t.Fatalf("failed to create quiz: %v", err)
		}

		playerStore := NewPlayerStore(db, slog.Default())
		player, err := playerStore.CreateAnonymousPlayer(
			t.Context(), "anon-leaderboard-1",
		)
		if err != nil {
			t.Fatalf("failed to create player: %v", err)
		}

		gameStore := NewGameStore(db, slog.Default())
		g := &game.Game{QuizID: testQuiz.ID}
		if err = gameStore.CreateGame(t.Context(), g); err != nil {
			t.Fatalf("failed to create game: %v", err)
		}
		if err = gameStore.CreateParticipant(
			t.Context(), &game.Participant{GameID: g.ID, PlayerID: player.ID},
		); err != nil {
			t.Fatalf("failed to create participant: %v", err)
		}

		// Issue every quiz question for the game so it counts as
		// complete; the leaderboard query now filters out partial
		// games. Only the first question gets an answer — the
		// second is "issued but timed out".
		now := time.Now().UTC().Truncate(time.Second)
		gameQuestions := make([]*game.Question, len(testQuiz.Questions))
		for i, q := range testQuiz.Questions {
			gq := &game.Question{
				GameID:     g.ID,
				QuestionID: q.ID,
				StartedAt:  now,
				ExpiredAt:  now.Add(10 * time.Second),
			}
			if err = gameStore.CreateQuestion(t.Context(), gq); err != nil {
				t.Fatalf("failed to create game question %d: %v", i, err)
			}
			gameQuestions[i] = gq
		}

		correctOption := testQuiz.Questions[0].Options[0]
		a := &game.Answer{
			GameID:     g.ID,
			PlayerID:   player.ID,
			QuestionID: gameQuestions[0].ID,
			OptionID:   correctOption.ID,
		}
		if err = gameStore.CreateAnswer(t.Context(), a); err != nil {
			t.Fatalf("failed to create answer: %v", err)
		}

		rows, err := gameStore.ListAnswersForQuizLeaderboard(t.Context(), testQuiz.ID)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got, want := len(rows), 1; got != want {
			t.Fatalf("len(rows) = %d, want %d", got, want)
		}
		if got, want := rows[0].PlayerID, player.ID; got != want {
			t.Errorf("rows[0].PlayerID = %d, want %d", got, want)
		}
		if got, want := rows[0].Username, player.Username; got != want {
			t.Errorf("rows[0].Username = %q, want %q", got, want)
		}
		if got, want := rows[0].Correct, correctOption.Correct; got != want {
			t.Errorf("rows[0].Correct = %v, want %v", got, want)
		}
	})

	t.Run("filters out partial games where not every question was issued", func(t *testing.T) {
		t.Parallel()
		db := dbtest.Open(t)
		quizStore := NewQuizStore(db, slog.Default())
		testQuiz := newTestQuizzes()[0]
		if err := quizStore.CreateQuiz(t.Context(), testQuiz); err != nil {
			t.Fatalf("failed to create quiz: %v", err)
		}

		playerStore := NewPlayerStore(db, slog.Default())
		player, err := playerStore.CreateAnonymousPlayer(
			t.Context(), "anon-leaderboard-partial",
		)
		if err != nil {
			t.Fatalf("failed to create player: %v", err)
		}

		gameStore := NewGameStore(db, slog.Default())
		g := &game.Game{QuizID: testQuiz.ID}
		if err = gameStore.CreateGame(t.Context(), g); err != nil {
			t.Fatalf("failed to create game: %v", err)
		}
		if err = gameStore.CreateParticipant(
			t.Context(), &game.Participant{GameID: g.ID, PlayerID: player.ID},
		); err != nil {
			t.Fatalf("failed to create participant: %v", err)
		}

		// Issue only the first of two questions — game is incomplete.
		now := time.Now().UTC().Truncate(time.Second)
		gq := &game.Question{
			GameID:     g.ID,
			QuestionID: testQuiz.Questions[0].ID,
			StartedAt:  now,
			ExpiredAt:  now.Add(10 * time.Second),
		}
		if err = gameStore.CreateQuestion(t.Context(), gq); err != nil {
			t.Fatalf("failed to create game question: %v", err)
		}
		if err = gameStore.CreateAnswer(t.Context(), &game.Answer{
			GameID:     g.ID,
			PlayerID:   player.ID,
			QuestionID: gq.ID,
			OptionID:   testQuiz.Questions[0].Options[0].ID,
		}); err != nil {
			t.Fatalf("failed to create answer: %v", err)
		}

		rows, err := gameStore.ListAnswersForQuizLeaderboard(t.Context(), testQuiz.ID)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got, want := len(rows), 0; got != want {
			t.Errorf("len(rows) = %d, want %d (partial games must be filtered out)", got, want)
		}
	})

	t.Run("only returns answers for the requested quiz", func(t *testing.T) {
		t.Parallel()
		db := dbtest.Open(t)
		quizStore := NewQuizStore(db, slog.Default())

		// Two distinct quizzes; the leaderboard query for quiz A must not
		// see answers recorded against quiz B.
		quizzes := newTestQuizzes()
		quizA := quizzes[0]
		quizB := quizzes[1]
		if err := quizStore.CreateQuiz(t.Context(), quizA); err != nil {
			t.Fatalf("failed to create quiz A: %v", err)
		}
		if err := quizStore.CreateQuiz(t.Context(), quizB); err != nil {
			t.Fatalf("failed to create quiz B: %v", err)
		}

		playerStore := NewPlayerStore(db, slog.Default())
		player, err := playerStore.CreateAnonymousPlayer(
			t.Context(), "anon-leaderboard-2",
		)
		if err != nil {
			t.Fatalf("failed to create player: %v", err)
		}

		gameStore := NewGameStore(db, slog.Default())
		now := time.Now().UTC().Truncate(time.Second)

		// Record a complete game on quizB (every quiz question issued)
		// so the new partial-game filter would normally let its rows
		// through. The cross-quiz filter must still keep them out of
		// quizA's leaderboard.
		gB := &game.Game{QuizID: quizB.ID}
		if err = gameStore.CreateGame(t.Context(), gB); err != nil {
			t.Fatalf("failed to create game B: %v", err)
		}
		if err = gameStore.CreateParticipant(
			t.Context(), &game.Participant{GameID: gB.ID, PlayerID: player.ID},
		); err != nil {
			t.Fatalf("failed to create participant for game B: %v", err)
		}
		gameQuestionsB := make([]*game.Question, len(quizB.Questions))
		for i, q := range quizB.Questions {
			gq := &game.Question{
				GameID:     gB.ID,
				QuestionID: q.ID,
				StartedAt:  now,
				ExpiredAt:  now.Add(10 * time.Second),
			}
			if err = gameStore.CreateQuestion(t.Context(), gq); err != nil {
				t.Fatalf("failed to create question %d for game B: %v", i, err)
			}
			gameQuestionsB[i] = gq
		}
		if err = gameStore.CreateAnswer(t.Context(), &game.Answer{
			GameID:     gB.ID,
			PlayerID:   player.ID,
			QuestionID: gameQuestionsB[0].ID,
			OptionID:   quizB.Questions[0].Options[0].ID,
		}); err != nil {
			t.Fatalf("failed to create answer for game B: %v", err)
		}

		rows, err := gameStore.ListAnswersForQuizLeaderboard(t.Context(), quizA.ID)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(rows) != 0 {
			t.Errorf("len(rows) = %d, want 0 (only quiz B has answers)", len(rows))
		}
	})
}
