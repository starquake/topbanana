package integration_test

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/starquake/topbanana/cmd/server/app"
	"github.com/starquake/topbanana/internal/media"
	"github.com/starquake/topbanana/internal/quiz"
)

// Large enough to overrun the loopback socket buffers, so the server's writes
// block on the slow reader and the write deadline decides the outcome.
const slowDownloadSize = 32 << 20

// Short enough for the slow reads below to outlast it, long enough that the
// sign-in requests the export needs do not trip it.
const slowDownloadWriteTimeout = 3 * time.Second

// TestSlowDownloads_Integration pins #1352: a media file or quiz export read
// slower than the server's WriteTimeout still arrives whole, because both
// handlers size the write deadline to the body.
func TestSlowDownloads_Integration(t *testing.T) {
	t.Parallel()

	mediaDir := t.TempDir()
	ctx, setup := setupIntegrationWithEnv(t, map[string]string{
		"MEDIA_DIR":    mediaDir,
		"ADMIN_EMAILS": "slow-dl-admin@example.test",
	}, app.WithWriteTimeout(slowDownloadWriteTimeout))

	qz := &quiz.Quiz{
		Title:             "Slow Download Quiz",
		Slug:              "slow-download-quiz",
		Description:       "seed for the slow download test",
		CreatedByPlayerID: seededAdminID,
		Visibility:        quiz.VisibilityPublic,
		Published:         true,
		Questions: []*quiz.Question{
			{Text: "Q", Position: 1, Options: []*quiz.Option{{Text: "A", Correct: true}, {Text: "B"}}},
		},
	}
	if err := setup.Stores.Quizzes.CreateQuiz(ctx, qz); err != nil {
		t.Fatalf("CreateQuiz err = %v, want nil", err)
	}
	mediaID := seedLargeAudio(ctx, t, setup, mediaDir, qz)

	t.Run("media file", func(t *testing.T) {
		t.Parallel()
		got := slowGet(ctx, t, newAnonClient(t), fmt.Sprintf("%s/media/%d", setup.BaseURL, mediaID))
		if want := int64(slowDownloadSize); got != want {
			t.Errorf("media bytes read = %d, want %d", got, want)
		}
	})

	t.Run("quiz export", func(t *testing.T) {
		t.Parallel()
		admin := registerAdminClient(ctx, t, setup.BaseURL, setup.DBURI, "slow-dl-admin")
		got := slowGet(ctx, t, admin, fmt.Sprintf("%s/admin/quizzes/%d/export", setup.BaseURL, qz.ID))
		if want := int64(slowDownloadSize); got <= want {
			t.Errorf("export bytes read = %d, want more than the %d-byte clip it bundles", got, want)
		}
	})
}

// seedLargeAudio writes a random slowDownloadSize-byte clip into mediaDir,
// records it as a ready audio row, and attaches it to qz's first question so
// the export bundles it.
func seedLargeAudio(
	ctx context.Context, t *testing.T, setup integrationSetup, mediaDir string, qz *quiz.Quiz,
) int64 {
	t.Helper()

	data := make([]byte, slowDownloadSize)
	if _, err := rand.Read(data); err != nil {
		t.Fatalf("rand.Read err = %v, want nil", err)
	}
	const relPath = "slow-download.mp3"
	if err := os.WriteFile(filepath.Join(mediaDir, relPath), data, 0o600); err != nil {
		t.Fatalf("WriteFile err = %v, want nil", err)
	}

	m, err := setup.Stores.Media.CreateMedia(ctx, &media.Media{
		QuizID:            qz.ID,
		Type:              media.TypeAudio,
		MIME:              "audio/mpeg",
		SizeBytes:         slowDownloadSize,
		SHA256:            "slow-download",
		CreatedByPlayerID: seededAdminID,
	})
	if err != nil {
		t.Fatalf("CreateMedia err = %v, want nil", err)
	}
	if err = setup.Stores.Media.UpdateMediaPaths(ctx, m.ID, relPath, ""); err != nil {
		t.Fatalf("UpdateMediaPaths err = %v, want nil", err)
	}
	if err = setup.Stores.Media.MarkMediaReady(ctx, m.ID); err != nil {
		t.Fatalf("MarkMediaReady err = %v, want nil", err)
	}

	question := qz.Questions[0]
	question.AudioMediaID = &m.ID
	if err = setup.Stores.Quizzes.UpdateQuestion(ctx, question); err != nil {
		t.Fatalf("UpdateQuestion err = %v, want nil", err)
	}

	return m.ID
}

// slowGet GETs url and reads the body in small chunks across twice the
// server's WriteTimeout, returning how many bytes arrived before EOF or a
// reset.
func slowGet(ctx context.Context, t *testing.T, client *http.Client, url string) int64 {
	t.Helper()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("NewRequest err = %v, want nil", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("GET %s err = %v, want nil", url, err)
	}
	defer closeBody(t, resp.Body)
	if got, want := resp.StatusCode, http.StatusOK; got != want {
		t.Fatalf("GET %s status = %d, want %d", url, got, want)
	}

	const chunks = 64
	delay := 2 * slowDownloadWriteTimeout / chunks
	buf := make([]byte, slowDownloadSize/chunks)
	var total int64
	for {
		time.Sleep(delay)
		n, rerr := io.ReadFull(resp.Body, buf)
		total += int64(n)
		if rerr != nil {
			if !errors.Is(rerr, io.EOF) && !errors.Is(rerr, io.ErrUnexpectedEOF) {
				t.Logf("GET %s read stopped after %d bytes: %v", url, total, rerr)
			}

			return total
		}
	}
}
