package admin_test

import (
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	. "github.com/starquake/topbanana/internal/admin"
	"github.com/starquake/topbanana/internal/auth"
	"github.com/starquake/topbanana/internal/csrf"
	"github.com/starquake/topbanana/internal/quiz"
)

// nonOwnerID is a player id distinct from testAdminID; an actor carrying
// it with a non-admin role fails requireQuizOwner on a quiz owned by the
// seeded admin.
const nonOwnerID int64 = 4242

func newRoundsCSRF() *csrf.Manager {
	return csrf.New([]byte("test-key-test-key-test-key-32byt"), false)
}

// roundsFixture bundles the seeded quiz with the ids the round handler
// tests target, so the seed helper stays under revive's
// function-result-limit.
type roundsFixture struct {
	quiz        *quiz.Quiz
	secondRound int64
	questionID  int64
}

// seedRoundsQuiz persists a single-question quiz owned by the admin and
// returns it alongside the question's id and a freshly created second
// round so the move/delete handlers have real targets. The store stamps a
// default round on CreateQuiz; the seeded question lands in it.
func seedRoundsQuiz(t *testing.T, env *adminEnv) roundsFixture {
	t.Helper()

	qz := ownedQuiz("Rounds Quiz", "rounds-quiz")
	qz.Questions = []*quiz.Question{
		{
			Text:     "What is the capital of France?",
			Position: 1,
			Options: []*quiz.Option{
				{Text: "Paris", Correct: true},
				{Text: "London"},
			},
		},
	}
	env.seedQuiz(t, qz)

	second := &quiz.Round{QuizID: qz.ID, Position: 1, Title: "Round 2"}
	if err := env.quizzes.CreateRound(t.Context(), second); err != nil {
		t.Fatalf("CreateRound err = %v, want nil", err)
	}

	return roundsFixture{quiz: qz, secondRound: second.ID, questionID: qz.Questions[0].ID}
}

// adminActor returns the request with the seeded admin attached, passing
// the owner gate for an admin-owned quiz.
func adminActor(req *http.Request) *http.Request {
	return req.WithContext(auth.WithPlayer(req.Context(), &auth.Player{ID: testAdminID, Role: auth.RoleAdmin}))
}

// nonOwnerActor returns the request with a non-admin player whose id is
// not the quiz creator's, so requireQuizOwner renders a 403.
func nonOwnerActor(req *http.Request) *http.Request {
	return req.WithContext(auth.WithPlayer(req.Context(), &auth.Player{ID: nonOwnerID, Role: auth.RolePlayer}))
}

func postMoveToRound(
	t *testing.T, env *adminEnv, quizID, questionID string, form url.Values,
	actor func(*http.Request) *http.Request,
) *httptest.ResponseRecorder {
	t.Helper()
	handler := HandleQuestionMoveToRound(slog.New(slog.DiscardHandler), newRoundsCSRF(), env.quizzes)

	req := httptest.NewRequestWithContext(
		t.Context(), http.MethodPost,
		"/admin/quizzes/"+quizID+"/questions/"+questionID+"/round",
		strings.NewReader(form.Encode()),
	)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetPathValue("quizID", quizID)
	req.SetPathValue("questionID", questionID)
	req = actor(req)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	return rec
}

func TestHandleQuestionMoveToRound(t *testing.T) {
	t.Parallel()

	t.Run("moves the question to the target round", func(t *testing.T) {
		t.Parallel()

		env := newAdminEnv(t)
		f := seedRoundsQuiz(t, env)

		rec := postMoveToRound(
			t, env, strconv.FormatInt(f.quiz.ID, 10), strconv.FormatInt(f.questionID, 10),
			url.Values{"round_id": {strconv.FormatInt(f.secondRound, 10)}}, adminActor,
		)

		if got, want := rec.Code, http.StatusSeeOther; got != want {
			t.Errorf("status = %d, want %d", got, want)
		}
		moved, err := env.quizzes.GetQuestion(t.Context(), f.questionID)
		if err != nil {
			t.Fatalf("GetQuestion err = %v, want nil", err)
		}
		if got, want := moved.RoundID, f.secondRound; got != want {
			t.Errorf("moved.RoundID = %d, want %d", got, want)
		}
	})

	t.Run("unparseable quizID is a 400", func(t *testing.T) {
		t.Parallel()

		env := newAdminEnv(t)

		rec := postMoveToRound(
			t, env, "abc", "1", url.Values{"round_id": {"1"}}, adminActor,
		)
		if got, want := rec.Code, http.StatusBadRequest; got != want {
			t.Errorf("status = %d, want %d", got, want)
		}
	})

	t.Run("unparseable questionID is a 400", func(t *testing.T) {
		t.Parallel()

		env := newAdminEnv(t)
		f := seedRoundsQuiz(t, env)

		rec := postMoveToRound(
			t, env, strconv.FormatInt(f.quiz.ID, 10), "abc",
			url.Values{"round_id": {strconv.FormatInt(f.secondRound, 10)}}, adminActor,
		)
		if got, want := rec.Code, http.StatusBadRequest; got != want {
			t.Errorf("status = %d, want %d", got, want)
		}
	})

	t.Run("unparseable round_id is a 400", func(t *testing.T) {
		t.Parallel()

		env := newAdminEnv(t)
		f := seedRoundsQuiz(t, env)

		rec := postMoveToRound(
			t, env, strconv.FormatInt(f.quiz.ID, 10), strconv.FormatInt(f.questionID, 10),
			url.Values{"round_id": {"not-a-number"}}, adminActor,
		)
		if got, want := rec.Code, http.StatusBadRequest; got != want {
			t.Errorf("status = %d, want %d", got, want)
		}
	})

	t.Run("non-owner is forbidden", func(t *testing.T) {
		t.Parallel()

		env := newAdminEnv(t)
		f := seedRoundsQuiz(t, env)

		rec := postMoveToRound(
			t, env, strconv.FormatInt(f.quiz.ID, 10), strconv.FormatInt(f.questionID, 10),
			url.Values{"round_id": {strconv.FormatInt(f.secondRound, 10)}}, nonOwnerActor,
		)
		if got, want := rec.Code, http.StatusForbidden; got != want {
			t.Errorf("status = %d, want %d", got, want)
		}
	})

	t.Run("unknown question is a 404", func(t *testing.T) {
		t.Parallel()

		env := newAdminEnv(t)
		f := seedRoundsQuiz(t, env)

		rec := postMoveToRound(
			t, env, strconv.FormatInt(f.quiz.ID, 10), "999999",
			url.Values{"round_id": {strconv.FormatInt(f.secondRound, 10)}}, adminActor,
		)
		if got, want := rec.Code, http.StatusNotFound; got != want {
			t.Errorf("status = %d, want %d", got, want)
		}
	})

	t.Run("unknown round is a 404", func(t *testing.T) {
		t.Parallel()

		env := newAdminEnv(t)
		f := seedRoundsQuiz(t, env)

		rec := postMoveToRound(
			t, env, strconv.FormatInt(f.quiz.ID, 10), strconv.FormatInt(f.questionID, 10),
			url.Values{"round_id": {"999999"}}, adminActor,
		)
		if got, want := rec.Code, http.StatusNotFound; got != want {
			t.Errorf("status = %d, want %d", got, want)
		}
	})

	t.Run("store error renders a 500", func(t *testing.T) {
		t.Parallel()

		env := newAdminEnv(t)
		f := seedRoundsQuiz(t, env)
		env.closeStore(t)

		rec := postMoveToRound(
			t, env, strconv.FormatInt(f.quiz.ID, 10), strconv.FormatInt(f.questionID, 10),
			url.Values{"round_id": {strconv.FormatInt(f.secondRound, 10)}}, adminActor,
		)
		if got, want := rec.Code, http.StatusInternalServerError; got != want {
			t.Errorf("status = %d, want %d", got, want)
		}
	})
}

func deleteRound(
	t *testing.T, env *adminEnv, quizID, roundID string,
	actor func(*http.Request) *http.Request,
) *httptest.ResponseRecorder {
	t.Helper()
	handler := HandleRoundDelete(slog.New(slog.DiscardHandler), newRoundsCSRF(), env.quizzes)

	req := httptest.NewRequestWithContext(
		t.Context(), http.MethodPost,
		"/admin/quizzes/"+quizID+"/rounds/"+roundID+"/delete", nil,
	)
	req.SetPathValue("quizID", quizID)
	req.SetPathValue("roundID", roundID)
	req = actor(req)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	return rec
}

func TestHandleRoundDelete(t *testing.T) {
	t.Parallel()

	t.Run("deletes the round", func(t *testing.T) {
		t.Parallel()

		env := newAdminEnv(t)
		f := seedRoundsQuiz(t, env)

		rec := deleteRound(
			t, env, strconv.FormatInt(f.quiz.ID, 10), strconv.FormatInt(f.secondRound, 10), adminActor,
		)
		if got, want := rec.Code, http.StatusSeeOther; got != want {
			t.Errorf("status = %d, want %d", got, want)
		}
		_, err := env.quizzes.GetRound(t.Context(), f.secondRound)
		if got, want := err, quiz.ErrRoundNotFound; !errors.Is(got, want) {
			t.Errorf("GetRound err = %v, want %v (round should be gone)", got, want)
		}
	})

	t.Run("unparseable roundID is a 400", func(t *testing.T) {
		t.Parallel()

		env := newAdminEnv(t)
		f := seedRoundsQuiz(t, env)

		rec := deleteRound(t, env, strconv.FormatInt(f.quiz.ID, 10), "abc", adminActor)
		if got, want := rec.Code, http.StatusBadRequest; got != want {
			t.Errorf("status = %d, want %d", got, want)
		}
	})

	t.Run("non-owner is forbidden", func(t *testing.T) {
		t.Parallel()

		env := newAdminEnv(t)
		f := seedRoundsQuiz(t, env)

		rec := deleteRound(
			t, env, strconv.FormatInt(f.quiz.ID, 10), strconv.FormatInt(f.secondRound, 10), nonOwnerActor,
		)
		if got, want := rec.Code, http.StatusForbidden; got != want {
			t.Errorf("status = %d, want %d", got, want)
		}
	})

	t.Run("round not found is a 404", func(t *testing.T) {
		t.Parallel()

		env := newAdminEnv(t)
		f := seedRoundsQuiz(t, env)

		rec := deleteRound(t, env, strconv.FormatInt(f.quiz.ID, 10), "999999", adminActor)
		if got, want := rec.Code, http.StatusNotFound; got != want {
			t.Errorf("status = %d, want %d", got, want)
		}
	})

	t.Run("a round on another quiz is a 404", func(t *testing.T) {
		t.Parallel()

		env := newAdminEnv(t)
		f := seedRoundsQuiz(t, env)

		// A second owned quiz whose round id is real but foreign. Mounting
		// it on the first quiz's URL must surface as 404 (the IDOR gate in
		// roundByID), not delete the foreign round.
		other := ownedQuiz("Other Quiz", "other-quiz")
		env.seedQuiz(t, other)
		otherRounds, err := env.quizzes.ListRoundsByQuiz(t.Context(), other.ID)
		if err != nil {
			t.Fatalf("ListRoundsByQuiz err = %v, want nil", err)
		}
		foreignRound := otherRounds[0].ID

		rec := deleteRound(
			t, env, strconv.FormatInt(f.quiz.ID, 10), strconv.FormatInt(foreignRound, 10), adminActor,
		)
		if got, want := rec.Code, http.StatusNotFound; got != want {
			t.Errorf("status = %d, want %d", got, want)
		}
		// The foreign round must still exist.
		if _, err := env.quizzes.GetRound(t.Context(), foreignRound); err != nil {
			t.Errorf("GetRound(foreign) err = %v, want nil (must not be deleted)", err)
		}
	})

	t.Run("store error renders a 500", func(t *testing.T) {
		t.Parallel()

		env := newAdminEnv(t)
		f := seedRoundsQuiz(t, env)
		env.closeStore(t)

		rec := deleteRound(
			t, env, strconv.FormatInt(f.quiz.ID, 10), strconv.FormatInt(f.secondRound, 10), adminActor,
		)
		if got, want := rec.Code, http.StatusInternalServerError; got != want {
			t.Errorf("status = %d, want %d", got, want)
		}
	})
}

func postRoundSave(
	t *testing.T, env *adminEnv, quizID string, form url.Values,
	actor func(*http.Request) *http.Request,
) *httptest.ResponseRecorder {
	t.Helper()
	handler := HandleRoundSave(slog.New(slog.DiscardHandler), newRoundsCSRF(), env.quizzes)

	req := httptest.NewRequestWithContext(
		t.Context(), http.MethodPost,
		"/admin/quizzes/"+quizID+"/rounds", strings.NewReader(form.Encode()),
	)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetPathValue("quizID", quizID)
	req = actor(req)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	return rec
}

func TestHandleRoundSave(t *testing.T) {
	t.Parallel()

	t.Run("blank title re-renders the form as a 400", func(t *testing.T) {
		t.Parallel()

		env := newAdminEnv(t)
		f := seedRoundsQuiz(t, env)

		rec := postRoundSave(
			t, env, strconv.FormatInt(f.quiz.ID, 10), url.Values{"title": {""}}, adminActor,
		)
		if got, want := rec.Code, http.StatusBadRequest; got != want {
			t.Errorf("status = %d, want %d", got, want)
		}
	})

	t.Run("store error renders a 500", func(t *testing.T) {
		t.Parallel()

		env := newAdminEnv(t)
		f := seedRoundsQuiz(t, env)
		env.closeStore(t)

		rec := postRoundSave(
			t, env, strconv.FormatInt(f.quiz.ID, 10), url.Values{"title": {"New Round"}}, adminActor,
		)
		if got, want := rec.Code, http.StatusInternalServerError; got != want {
			t.Errorf("status = %d, want %d", got, want)
		}
	})
}
