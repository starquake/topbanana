package integration_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/starquake/topbanana/internal/media"
	"github.com/starquake/topbanana/internal/store"
)

// TestMediaDelete_Integration covers the image-delete endpoint (#936 slice 4):
// an owner deletes their own image; the IDOR guard refuses deleting another
// quiz's image through this quiz's path; an admin moderates (deletes) another
// host's image; a non-owner non-admin host gets the same opaque 404 an unknown
// media id gives (#1207).
func TestMediaDelete_Integration(t *testing.T) {
	t.Parallel()

	ctx, setup := setupMedia(t, map[string]string{
		"ADMIN_EMAILS": "delete-boss@example.test",
	})
	baseURL := setup.BaseURL

	admin := registerAdminClient(ctx, t, baseURL, setup.DBURI, "delete-boss")
	owner := registerAdminClient(ctx, t, baseURL, setup.DBURI, "delete-owner")
	other := registerAdminClient(ctx, t, baseURL, setup.DBURI, "delete-other")
	makeHost(ctx, t, setup.DBURI, "delete-owner")
	makeHost(ctx, t, setup.DBURI, "delete-other")

	t.Run("owner deletes own image", func(t *testing.T) {
		t.Parallel()
		quizID := createQuizAs(ctx, t, owner, baseURL, "Owner Delete Quiz")
		uploadImage(ctx, t, owner, baseURL, quizID, "pic.png", pngBytes(t, 200, 120))
		mediaID := latestMediaID(ctx, t, setup.Stores, quizID)

		deleteMedia(ctx, t, owner, baseURL, quizID, mediaID, http.StatusSeeOther)

		assertMediaGone(ctx, t, setup.Stores, mediaID)
		resp := httpGet(ctx, t, owner, baseURL+fmt.Sprintf("/media/%d", mediaID))
		defer closeBody(t, resp.Body)
		if got, want := resp.StatusCode, http.StatusNotFound; got != want {
			t.Errorf("serve after delete status = %d, want %d", got, want)
		}
	})

	t.Run("IDOR guard refuses foreign quiz's media id", func(t *testing.T) {
		t.Parallel()
		quizA := createQuizAs(ctx, t, owner, baseURL, "IDOR Quiz A")
		quizB := createQuizAs(ctx, t, other, baseURL, "IDOR Quiz B")
		uploadImage(ctx, t, other, baseURL, quizB, "b.png", pngBytes(t, 200, 120))
		bMedia := latestMediaID(ctx, t, setup.Stores, quizB)

		// owner of quiz A posts a delete for quiz B's media id under A's path.
		deleteMedia(ctx, t, owner, baseURL, quizA, bMedia, http.StatusNotFound)

		// B's media still exists and still serves to its owner.
		if _, err := setup.Stores.Media.GetMedia(ctx, bMedia); err != nil {
			t.Errorf("GetMedia after blocked IDOR delete err = %v, want nil (B's media must survive)", err)
		}
		resp := httpGet(ctx, t, other, baseURL+fmt.Sprintf("/media/%d", bMedia))
		defer closeBody(t, resp.Body)
		if got, want := resp.StatusCode, http.StatusOK; got != want {
			t.Errorf("B's media serve after blocked IDOR delete status = %d, want %d", got, want)
		}
	})

	t.Run("admin moderates another host's image", func(t *testing.T) {
		t.Parallel()
		quizID := createQuizAs(ctx, t, owner, baseURL, "Admin Moderation Quiz")
		uploadImage(ctx, t, owner, baseURL, quizID, "pic.png", pngBytes(t, 200, 120))
		mediaID := latestMediaID(ctx, t, setup.Stores, quizID)

		// The admin does not own this quiz but passes the creator-or-admin gate.
		deleteMedia(ctx, t, admin, baseURL, quizID, mediaID, http.StatusSeeOther)
		assertMediaGone(ctx, t, setup.Stores, mediaID)
	})

	t.Run("non-owner non-admin host gets an opaque 404", func(t *testing.T) {
		t.Parallel()
		quizID := createQuizAs(ctx, t, owner, baseURL, "Forbidden Delete Quiz")
		uploadImage(ctx, t, owner, baseURL, quizID, "pic.png", pngBytes(t, 200, 120))
		mediaID := latestMediaID(ctx, t, setup.Stores, quizID)

		deleteMedia(ctx, t, other, baseURL, quizID, mediaID, http.StatusNotFound)

		if _, err := setup.Stores.Media.GetMedia(ctx, mediaID); err != nil {
			t.Errorf("GetMedia after refused delete err = %v, want nil (image must survive)", err)
		}
	})

	t.Run("unknown media id is 404", func(t *testing.T) {
		t.Parallel()
		quizID := createQuizAs(ctx, t, owner, baseURL, "Unknown Media Quiz")
		deleteMedia(ctx, t, owner, baseURL, quizID, 999999, http.StatusNotFound)
	})

	t.Run("HX-Request returns an empty 200 instead of the 303", func(t *testing.T) {
		t.Parallel()
		quizID := createQuizAs(ctx, t, owner, baseURL, "HX Delete Quiz")
		uploadImage(ctx, t, owner, baseURL, quizID, "pic.png", pngBytes(t, 200, 120))
		mediaID := latestMediaID(ctx, t, setup.Stores, quizID)

		token := fetchCSRFToken(ctx, t, owner, baseURL+"/admin/quizzes")
		form := url.Values{"csrf_token": {token}}
		target := baseURL + fmt.Sprintf("/admin/quizzes/%d/media/%d/delete", quizID, mediaID)
		req := newFormReq(ctx, t, target, form)
		req.Header.Set("Hx-Request", "true")
		resp, err := owner.Do(req)
		if err != nil {
			t.Fatalf("HX delete Do err = %v, want nil", err)
		}
		defer closeBody(t, resp.Body)
		if got, want := resp.StatusCode, http.StatusOK; got != want {
			t.Fatalf("HX delete status = %d, want %d", got, want)
		}
		if got := resp.Header.Get("Location"); got != "" {
			t.Errorf("HX delete Location = %q, want empty", got)
		}
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatalf("HX delete body read err = %v, want nil", err)
		}
		if len(body) != 0 {
			t.Errorf("HX delete body = %q, want empty", body)
		}
		assertMediaGone(ctx, t, setup.Stores, mediaID)
	})
}

// TestMediaDeleteView_Integration covers the delete affordance in the per-quiz
// image library (#936 slice 4): the owner's quiz view renders a delete control
// and a delete form posting to the media delete route for each image, while a
// non-owner host cannot open the quiz view at all - it 404s under the owner-or-
// admin view gate (#1207).
func TestMediaDeleteView_Integration(t *testing.T) {
	t.Parallel()

	ctx, setup := setupMedia(t, map[string]string{
		"ADMIN_EMAILS": "deleteview-boss@example.test",
	})
	baseURL := setup.BaseURL

	registerAdminClient(ctx, t, baseURL, setup.DBURI, "deleteview-boss")
	owner := registerAdminClient(ctx, t, baseURL, setup.DBURI, "deleteview-owner")
	viewer := registerAdminClient(ctx, t, baseURL, setup.DBURI, "deleteview-viewer")
	makeHost(ctx, t, setup.DBURI, "deleteview-owner")
	makeHost(ctx, t, setup.DBURI, "deleteview-viewer")

	quizID := createQuizAs(ctx, t, owner, baseURL, "Delete View Quiz")
	uploadImage(ctx, t, owner, baseURL, quizID, "pic.png", pngBytes(t, 200, 120))
	mediaID := latestMediaID(ctx, t, setup.Stores, quizID)

	t.Run("owner sees the delete control and form", func(t *testing.T) {
		t.Parallel()
		page := getQuizViewBody(ctx, t, owner, baseURL, quizID)
		for _, want := range []string{
			fmt.Sprintf(`action="/admin/quizzes/%d/media/%d/delete"`, quizID, mediaID),
			fmt.Sprintf(`openModal('modal-delete-media-%d')`, mediaID),
		} {
			if !strings.Contains(page, want) {
				t.Errorf("owner quiz view missing %q", want)
			}
		}
	})

	t.Run("non-owner host cannot open the quiz view", func(t *testing.T) {
		t.Parallel()
		resp := httpGet(ctx, t, viewer, baseURL+fmt.Sprintf("/admin/quizzes/%d", quizID))
		defer closeBody(t, resp.Body)
		if got, want := resp.StatusCode, http.StatusNotFound; got != want {
			t.Errorf("non-owner quiz view status = %d, want %d", got, want)
		}
	})
}

// deleteMedia posts the urlencoded delete form to the media delete endpoint and
// asserts the response status. The client's CheckRedirect keeps the 303 as the
// last response, so a wantStatus of http.StatusSeeOther asserts the redirect
// fired rather than following it.
func deleteMedia(
	ctx context.Context, t *testing.T, client *http.Client,
	baseURL string, quizID, mediaID int64, wantStatus int,
) {
	t.Helper()
	token := fetchCSRFToken(ctx, t, client, baseURL+"/admin/quizzes")
	form := url.Values{"csrf_token": {token}}
	target := baseURL + fmt.Sprintf("/admin/quizzes/%d/media/%d/delete", quizID, mediaID)
	req := newFormReq(ctx, t, target, form)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("delete Do err = %v, want nil", err)
	}
	defer closeBody(t, resp.Body)
	if got, want := resp.StatusCode, wantStatus; got != want {
		rb, _ := io.ReadAll(resp.Body)
		t.Fatalf("delete status = %d, want %d; body=%q", got, want, rb)
	}
	if wantStatus == http.StatusSeeOther {
		if got, want := resp.Header.Get("Location"), fmt.Sprintf("/admin/quizzes/%d#images", quizID); got != want {
			t.Errorf("delete redirect Location = %q, want %q", got, want)
		}
	}
}

// assertMediaGone fails the test unless the media id no longer names a row.
func assertMediaGone(ctx context.Context, t *testing.T, stores *store.Stores, mediaID int64) {
	t.Helper()
	if _, err := stores.Media.GetMedia(ctx, mediaID); !errors.Is(err, media.ErrMediaNotFound) {
		t.Errorf("GetMedia after delete err = %v, want %v", err, media.ErrMediaNotFound)
	}
}
