// Package host serves the TV / presentation surface for a hosted live
// session (MP-3 / #680): a full-screen big screen that shows the join QR and
// room code, the live player roster with ready states, and the host start
// control. The page is host-gated (RequireGameHost wraps the route) and
// reads the authoritative lobby state through the same service the JSON API
// uses; the live updates run off the SSE tick -> GET /api/sessions/{code}/state
// contract, driven by host-bigscreen.js.
package host

import (
	"errors"
	"html/template"
	"log/slog"
	"net/http"
	"strings"

	"github.com/starquake/topbanana/internal/absurl"
	"github.com/starquake/topbanana/internal/auth"
	"github.com/starquake/topbanana/internal/csrf"
	"github.com/starquake/topbanana/internal/envtag"
	"github.com/starquake/topbanana/internal/livesession"
	"github.com/starquake/topbanana/internal/qrcode"
	"github.com/starquake/topbanana/internal/quiz"
	"github.com/starquake/topbanana/internal/reltime"
	"github.com/starquake/topbanana/internal/render"
	"github.com/starquake/topbanana/internal/web/tmpl"
)

// msgInternalError is the body returned to the client on an unexpected
// server-side failure; the detail is logged, never leaked.
const msgInternalError = "internal error"

// joinPathPrefix is the player-facing join path the QR encodes. The player
// join UI (MP-4 / #681) owns the route itself; the QR points a phone there
// with the room code in the path. If MP-4 lands on a different path this is
// the single place to change.
const joinPathPrefix = "/join/"

// joinEntryPath is the player-facing path that serves the enter-code form.
// A phone that cannot scan the QR goes here and types the room code.
const joinEntryPath = "/join"

// hostScreenPathPrefix is the host big-screen path prefix the host POST handlers
// redirect back to after their action; the code (server-minted, not user input)
// is appended to form a same-origin destination.
const hostScreenPathPrefix = "/host/"

// BigScreenData feeds the host big-screen template.
type BigScreenData struct {
	Title    string
	JoinCode string
	// JoinURL is the absolute URL the QR encodes for one-tap scanning; it is
	// the deep link that carries the room code in the path.
	JoinURL string
	// JoinEntryDisplay is the bare host+path of the enter-code page (no
	// scheme), e.g. "topbanana.app/join". It is what the typed-code guidance
	// tells a player to visit before typing the code shown on the TV.
	JoinEntryDisplay string
	// QRSVG is the server-rendered QR of JoinURL, injected as trusted markup.
	QRSVG template.HTML
	// HasQuiz reports whether a quiz is armed in the room (#836): false for an
	// empty room opened with no game picked yet. It seeds the lobby component's
	// initial hasQuiz so a preselected-quiz lobby renders its Start controls
	// straight away rather than flashing the staging picker until the first
	// state read lands (the page's no-flash hydration).
	HasQuiz bool
	// QuizTitle is the quiz being hosted.
	QuizTitle string
	// QuestionCount is shown as lobby metadata; the lobby never leaks
	// question text (the no-spoiler guarantee).
	QuestionCount int
}

// Handlers serves the host big-screen page and the host start control.
type Handlers struct {
	logger    *slog.Logger
	service   *livesession.Service
	quizzes   quiz.Store
	bigScreen *render.Renderer
	quizList  *render.Renderer
}

// NewHandlers wires the host surface over the live-session service.
func NewHandlers(
	logger *slog.Logger,
	csrfMgr *csrf.Manager,
	service *livesession.Service,
	quizStore quiz.Store,
) *Handlers {
	return &Handlers{
		logger:  logger,
		service: service,
		quizzes: quizStore,
		// The host surfaces render none of admin's top-bar / nav chrome, so
		// they bind nothing beyond render.Renderer's own csrfToken (nil funcs).
		bigScreen: render.New(logger, csrfMgr, parseTemplate("host/pages/bigscreen.gohtml"), "base.gohtml", nil),
		quizList:  render.New(logger, csrfMgr, parseQuizListTemplate("host/pages/quizlist.gohtml"), "page.gohtml", nil),
	}
}

// BigScreen handles GET /host/{code}: it renders the host big screen for a
// session the caller hosts. The route is host-gated; this handler additionally
// enforces that the caller may view the session (GetLobbyState returns
// ErrNotParticipant for a host who does not own it), so one host cannot open
// another host's room by guessing a code. An unknown code or a foreign
// session both 404 so the code stays opaque.
func (h *Handlers) BigScreen(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	player, ok := auth.PlayerFromContext(ctx)
	if !ok {
		h.logger.ErrorContext(ctx, "missing player on context for host big screen")
		http.Error(w, msgInternalError, http.StatusInternalServerError)

		return
	}

	code := r.PathValue("code")
	state, err := h.service.GetLobbyState(ctx, code, player.ID)
	if err != nil {
		if errors.Is(err, livesession.ErrSessionNotFound) || errors.Is(err, livesession.ErrNotParticipant) {
			http.NotFound(w, r)

			return
		}
		h.logger.ErrorContext(ctx, "error loading host big-screen state", slog.Any("err", err))
		http.Error(w, msgInternalError, http.StatusInternalServerError)

		return
	}

	baseURL := absurl.BaseURL(r)
	joinURL := baseURL + joinPathPrefix + state.Session.JoinCode
	joinEntry := strings.TrimPrefix(strings.TrimPrefix(baseURL+joinEntryPath, "https://"), "http://")
	svg, err := qrcode.SVG([]byte(joinURL))
	if err != nil {
		h.logger.ErrorContext(ctx, "error rendering join QR", slog.Any("err", err))
		http.Error(w, msgInternalError, http.StatusInternalServerError)

		return
	}

	// The QR markup is generated by internal/qrcode from a server-built URL,
	// never from user input, so it is safe to inject as trusted HTML.
	qrMarkup := template.HTML(svg) //nolint:gosec // server-generated SVG, no user markup.

	data := BigScreenData{
		Title:            "Live lobby",
		JoinCode:         state.Session.JoinCode,
		JoinURL:          joinURL,
		JoinEntryDisplay: joinEntry,
		QRSVG:            qrMarkup,
	}
	// An empty room (#836) has no quiz yet: leave the quiz metadata zero-valued so
	// the lobby renders the staging state rather than naming a quiz.
	if state.Quiz != nil {
		data.HasQuiz = true
		data.QuizTitle = state.Quiz.Title
		data.QuestionCount = len(state.Quiz.Questions)
	}

	h.bigScreen.Render(w, r, http.StatusOK, data)
}

// parseTemplate parses the host layout plus the named page. Placeholder
// funcs are registered before parse so the layout's {{envTitleTag}} and the
// page's {{csrfToken}} resolve at parse time; render rebinds csrfToken per
// request.
//
// The host/layouts/*.gohtml glob pulls in every host layout (base.gohtml and
// page.gohtml), so this FuncMap must register every func any host layout uses
// and stay in sync with parseQuizListTemplate's - else adding a func to one
// layout panics the other tree at parse. humanizeTime is registered here for
// that reason even though bigscreen does not call it.
func parseTemplate(path string) *template.Template {
	funcs := template.FuncMap{
		"envTitleTag":  envtag.Get,
		"csrfToken":    func() string { return "" },
		"humanizeTime": reltime.Humanize,
	}
	base := template.Must(template.New("").Funcs(funcs).ParseFS(tmpl.FS, "host/layouts/*.gohtml"))

	return template.Must(template.Must(base.Clone()).ParseFS(tmpl.FS, path))
}

// parseQuizListTemplate parses the host layouts plus the shared quiz-card
// partial and the named page. It registers the same funcs as parseTemplate
// (the shared quiz_card partial calls humanizeTime). Only the quiz_card partial
// is parsed (not the whole components/ glob): the footer and topbar partials
// reference funcs (isSignedIn, isAdmin) this page does not provide.
func parseQuizListTemplate(path string) *template.Template {
	funcs := template.FuncMap{
		"envTitleTag":  envtag.Get,
		"csrfToken":    func() string { return "" },
		"humanizeTime": reltime.Humanize,
	}
	base := template.Must(template.New("").Funcs(funcs).ParseFS(tmpl.FS, "host/layouts/*.gohtml"))
	base = template.Must(base.ParseFS(tmpl.FS, "components/quiz_card.gohtml"))

	return template.Must(template.Must(base.Clone()).ParseFS(tmpl.FS, path))
}
