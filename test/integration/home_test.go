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
		Description:       "Twenty rounds on cultivars.",
		CreatedByPlayerID: seededAdminID,
		Questions: []*quiz.Question{
			{Text: "Q1", Position: 1, Options: []*quiz.Option{{Text: "a"}, {Text: "b", Correct: true}}},
		},
	}
	if err := stores.Quizzes.CreateQuiz(ctx, quiz1); err != nil {
		t.Fatalf("CreateQuiz quiz1 err = %v, want nil", err)
	}
	quiz2 := &quiz.Quiz{
		Title: "Capital Cities", Slug: "capital-cities",
		Description:       "Geography quickfire.",
		CreatedByPlayerID: seededAdminID,
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

	// alice plays quiz1 + quiz2; bob plays quiz1. The one-attempt-per-
	// (player, quiz) rule (#273) means each pair shows up at most once,
	// so popular ranking is driven by distinct-player counts: quiz1 = 2
	// plays (alice + bob), quiz2 = 1 play (alice).
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

	t.Run("GET / renders Browse all link below the popular quizzes", func(t *testing.T) {
		t.Parallel()
		body := getBody(ctx, t, baseURL+"/")

		const browse = `href="/quizzes"`
		// invariant pinned by #315: the Browse all link sits AFTER the
		// last popular-quiz card, not in the section header. We assert
		// position by index so a future move back to the header surfaces
		// the regression.
		browseIdx := strings.Index(body, browse)
		if browseIdx == -1 {
			t.Fatalf("body missing %q", browse)
		}
		lastCardIdx := strings.LastIndex(body, "/play/")
		if lastCardIdx == -1 {
			t.Fatal("body missing a /play/ link")
		}
		if got, want := browseIdx > lastCardIdx, true; got != want {
			t.Errorf(
				"Browse all link at %d, last /play/ at %d — want Browse all after the cards",
				browseIdx,
				lastCardIdx,
			)
		}
	})

	t.Run("GET / renders a share trigger per popular quiz", func(t *testing.T) {
		t.Parallel()
		body := getBody(ctx, t, baseURL+"/")

		// Each popular-quiz card must carry the data-* attrs share.js
		// reads to drive the dialog. We assert the per-quiz invitation
		// text is composed correctly so a recipient sees the title
		// the host actually shared.
		for _, want := range []string{
			`data-share-trigger`,
			`data-share-path="/play/bananas-of-the-world-`,
			`data-share-title="Bananas of the World"`,
			`data-share-text="Play this quiz: Bananas of the World"`,
			`data-share-path="/play/capital-cities-`,
			`data-share-title="Capital Cities"`,
			`<script type="module" src="/assets/js/share.js"></script>`,
		} {
			if !strings.Contains(body, want) {
				t.Errorf("body missing share-trigger marker %q", want)
			}
		}
	})

	t.Run("share.js asset is served", func(t *testing.T) {
		t.Parallel()
		resp := httpGet(ctx, t, &http.Client{}, baseURL+"/assets/js/share.js")
		defer closeBody(t, resp.Body)

		if got, want := resp.StatusCode, http.StatusOK; got != want {
			t.Errorf("status = %d, want %d", got, want)
		}
		if got, want := resp.Header.Get("Content-Type"), "text/javascript"; !strings.HasPrefix(got, want) {
			// Static file server uses application/javascript on
			// some Go versions; accept either.
			if !strings.HasPrefix(got, "application/javascript") {
				t.Errorf("Content-Type = %q, want text/javascript or application/javascript", got)
			}
		}
	})

	t.Run("GET / exposes sitewide Open Graph defaults", func(t *testing.T) {
		t.Parallel()
		assertSitewideOG(ctx, t, baseURL+"/", baseURL)
	})

	t.Run("unknown path still 404s after start page is registered", func(t *testing.T) {
		t.Parallel()
		resp := httpGet(ctx, t, &http.Client{}, baseURL+"/does-not-exist")
		defer closeBody(t, resp.Body)

		if got, want := resp.StatusCode, http.StatusNotFound; got != want {
			t.Errorf("status = %d, want %d", got, want)
		}
	})

	// #284 — /quizzes is the public list of every visible quiz. The
	// headline AC is "includes a quiz that has never been played": the
	// home page's popular list filters by play count over the last 30
	// days, so a brand-new quiz wouldn't surface there. Seeding a
	// never-played quiz here pins that the /quizzes path does NOT
	// inherit the same filter.
	neverPlayed := &quiz.Quiz{
		Title:             "Newly Authored",
		Slug:              "newly-authored",
		Description:       "Just published — no one has played yet.",
		CreatedByPlayerID: seededAdminID,
		Questions: []*quiz.Question{
			{Text: "Q1", Position: 1, Options: []*quiz.Option{{Text: "ok", Correct: true}}},
		},
	}
	if err := stores.Quizzes.CreateQuiz(ctx, neverPlayed); err != nil {
		t.Fatalf("CreateQuiz neverPlayed err = %v, want nil", err)
	}

	t.Run("GET /quizzes lists every quiz including ones with zero plays", func(t *testing.T) {
		t.Parallel()
		body := getBody(ctx, t, baseURL+"/quizzes")

		for _, want := range []string{
			`<title>All quizzes — Top Banana!</title>`,
			"Bananas of the World",
			"Capital Cities",
			// The never-played quiz must appear — the home page would
			// hide it (0 plays in the popular-30-day window) but
			// /quizzes is the discoverable home for it.
			"Newly Authored",
			"Just published — no one has played yet.",
			`href="/play/newly-authored-`,
			`1 question`,
		} {
			if !strings.Contains(body, want) {
				t.Errorf("body missing %q", want)
			}
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
	if err := games.CreateParticipant(ctx, &game.Participant{
		GameID: g.ID, PlayerID: playerID, QuizID: q.ID,
	}); err != nil {
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
