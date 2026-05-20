//go:build integration

package integration_test

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/starquake/topbanana/internal/auth"
	"github.com/starquake/topbanana/internal/game"
	"github.com/starquake/topbanana/internal/quiz"
)

// TestHome_Integration covers #166: the public start page at GET /
// renders the popular quizzes, most active players, and a discreet
// admin link. The test exercises the real HTTP path, the real
// templates, and the real store queries — anything that breaks the
// rendering or routing surfaces here.
func TestHome_Integration(t *testing.T) {
	t.Parallel()

	ctx, setup := setupIntegration(t)
	baseURL := setup.BaseURL
	stores := setup.Stores

	// Seed two quizzes, two claimed players, and finished games that
	// make quiz1 the popular leader and alice the active leader.
	quiz1 := &quiz.Quiz{
		Title: "Bananas of the World", Slug: "bananas-of-the-world",
		Description: "Twenty rounds on cultivars.",
		Questions: []*quiz.Question{
			{Text: "Q1", Position: 1, Options: []*quiz.Option{{Text: "a"}, {Text: "b", Correct: true}}},
		},
	}
	if err := stores.Quizzes.CreateQuiz(ctx, quiz1); err != nil {
		t.Fatalf("CreateQuiz quiz1 err = %v, want nil", err)
	}
	quiz2 := &quiz.Quiz{
		Title: "Capital Cities", Slug: "capital-cities",
		Description: "Geography quickfire.",
		Questions: []*quiz.Question{
			{Text: "Q1", Position: 1, Options: []*quiz.Option{{Text: "c", Correct: true}}},
		},
	}
	if err := stores.Quizzes.CreateQuiz(ctx, quiz2); err != nil {
		t.Fatalf("CreateQuiz quiz2 err = %v, want nil", err)
	}

	alice, err := stores.Players.CreatePlayer(ctx, "alice-integration", "hash", auth.RolePlayer)
	if err != nil {
		t.Fatalf("CreatePlayer alice err = %v, want nil", err)
	}
	bob, err := stores.Players.CreatePlayer(ctx, "bob-integration", "hash", auth.RolePlayer)
	if err != nil {
		t.Fatalf("CreatePlayer bob err = %v, want nil", err)
	}

	// alice: 2 finished on quiz1 + 1 finished on quiz2; bob: 1 finished
	// on quiz1. Quiz1 = 3 plays, Quiz2 = 1 play.
	finishGameInt(t, stores.Games, alice.ID, quiz1)
	finishGameInt(t, stores.Games, alice.ID, quiz1)
	finishGameInt(t, stores.Games, alice.ID, quiz2)
	finishGameInt(t, stores.Games, bob.ID, quiz1)

	t.Run("GET / returns 200 and renders popular + players + admin link", func(t *testing.T) {
		t.Parallel()
		body := getBody(ctx, t, baseURL+"/")

		for _, want := range []string{
			"<title>Top Banana!</title>",
			"Popular quizzes",
			"Most active players",
			"Bananas of the World",
			"/play/bananas-of-the-world-",
			"Capital Cities",
			"/play/capital-cities-",
			"alice-integration",
			"bob-integration",
			`href="/admin"`,
		} {
			if !strings.Contains(body, want) {
				t.Errorf("body missing %q", want)
			}
		}
	})

	t.Run("GET / exposes sitewide Open Graph defaults", func(t *testing.T) {
		t.Parallel()
		assertSitewideOG(ctx, t, baseURL+"/")
	})

	t.Run("unknown path still 404s after start page is registered", func(t *testing.T) {
		t.Parallel()
		resp := httpGet(ctx, t, &http.Client{}, baseURL+"/does-not-exist")
		defer closeBody(t, resp.Body)

		if got, want := resp.StatusCode, http.StatusNotFound; got != want {
			t.Errorf("status = %d, want %d", got, want)
		}
	})
}

// finishGameInt creates a finished game for the (player, quiz) pair:
// game + participant + one game_question per quiz question. Mirrors
// store_test.finishGameFor but uses the integration store.Stores
// directly so the test does not duplicate the seed logic across
// packages.
func finishGameInt(t *testing.T, games game.Store, playerID int64, q *quiz.Quiz) {
	t.Helper()
	ctx := t.Context()
	g := &game.Game{QuizID: q.ID}
	if err := games.CreateGame(ctx, g); err != nil {
		t.Fatalf("CreateGame err = %v, want nil", err)
	}
	if err := games.CreateParticipant(ctx, &game.Participant{GameID: g.ID, PlayerID: playerID}); err != nil {
		t.Fatalf("CreateParticipant err = %v, want nil", err)
	}
	now := time.Now()
	for _, qs := range q.Questions {
		gq := &game.Question{
			GameID:     g.ID,
			QuestionID: qs.ID,
			StartedAt:  now,
			ExpiredAt:  now.Add(10 * time.Second),
		}
		if err := games.CreateQuestion(ctx, gq); err != nil {
			t.Fatalf("CreateQuestion err = %v, want nil", err)
		}
	}
}
