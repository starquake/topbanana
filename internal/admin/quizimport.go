package admin

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/gosimple/slug"

	"github.com/starquake/topbanana/internal/auth"
	"github.com/starquake/topbanana/internal/csrf"
	"github.com/starquake/topbanana/internal/quiz"
)

// quizImportPayload mirrors the JSON shape an admin pastes into the import
// textarea. Decoupled from quiz.Quiz so the wire shape stays small and
// LLM-friendly (no IDs, timestamps, position fields, or slugs - the slug
// is derived server-side from the title). The handler translates this
// into the full domain model before validation.
type quizImportPayload struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	// TimeLimitSeconds is the per-quiz default answer window (#99).
	// Optional in the payload - omitted maps to
	// [quiz.DefaultTimeLimitSeconds], matching the admin form's
	// new-quiz default.
	TimeLimitSeconds *int `json:"timeLimitSeconds,omitempty"`
	// Questions and Rounds are mutually exclusive (#546). Supply
	// Questions for a flat quiz (every question lands in the default
	// round, the original behaviour) or Rounds to author named rounds
	// with their own questions - never both, never neither.
	Questions []quizImportQuestionPayload `json:"questions,omitempty"`
	Rounds    []quizImportRoundPayload    `json:"rounds,omitempty"`
}

type quizImportRoundPayload struct {
	Title   string `json:"title"`
	Summary string `json:"summary,omitempty"`
	// BoundaryDurationSeconds overrides the quiz default for this round's
	// boundary auto-advance window (#554). Optional - omitted means
	// "inherit the quiz value at game time", same as leaving the admin
	// form's field blank.
	BoundaryDurationSeconds *int `json:"boundaryDurationSeconds,omitempty"`
	// Questions for this round, in play order. Required and non-empty;
	// quiz-wide positions are assigned 1..N across all rounds (#546).
	Questions []quizImportQuestionPayload `json:"questions"`
}

type quizImportQuestionPayload struct {
	Text string `json:"text"`
	// TimeLimitSeconds overrides the quiz default for this question
	// (#99). Optional - omitted means "inherit the quiz value at
	// game time", same as leaving the admin form's field blank.
	TimeLimitSeconds *int                      `json:"timeLimitSeconds,omitempty"`
	Options          []quizImportOptionPayload `json:"options"`
}

type quizImportOptionPayload struct {
	Text    string `json:"text"`
	Correct bool   `json:"correct"`
}

// quizImportExample is the JSON block rendered on the import page so the
// admin can copy it into a chat with Claude (or any LLM), have it generate
// a quiz, and paste the result back. Kept here as a const string rather
// than in the template so the rendered example stays byte-identical to
// what the handler will actually accept.
const quizImportExample = `{
  "title": "European Capitals",
  "description": "A quick tour of EU capitals.",
  "timeLimitSeconds": 10,
  "rounds": [
    {
      "title": "Warm-up",
      "summary": "An easy start before things speed up.",
      "questions": [
        {
          "text": "Which city sits on the river Vltava?",
          "options": [
            { "text": "Bratislava", "correct": false },
            { "text": "Budapest",   "correct": false },
            { "text": "Prague",     "correct": true  },
            { "text": "Warsaw",     "correct": false }
          ]
        },
        {
          "text": "Which of these is a capital city?",
          "options": [
            { "text": "Lisbon",   "correct": true  },
            { "text": "Porto",    "correct": false },
            { "text": "Helsinki", "correct": true  },
            { "text": "Tampere",  "correct": false }
          ]
        }
      ]
    },
    {
      "title": "Final stretch",
      "summary": "One harder question to finish.",
      "boundaryDurationSeconds": 15,
      "questions": [
        {
          "text": "Which capital is furthest north?",
          "timeLimitSeconds": 20,
          "options": [
            { "text": "Reykjavik",  "correct": true  },
            { "text": "Oslo",       "correct": false },
            { "text": "Stockholm",  "correct": false },
            { "text": "Copenhagen", "correct": false }
          ]
        }
      ]
    }
  ]
}`

// quizImportPageData is the render-time data for quizimport.gohtml. Both
// the form (GET) and save (POST) handlers populate it, so the type is
// declared once at package scope rather than re-declared per handler.
//
// Mode holds the play mode the admin picked; it has no default so the
// selector forces an explicit choice (#752). ModeOptions feeds the
// selector with the recognised play modes.
type quizImportPageData struct {
	Title       string
	JSON        string
	Example     string
	Error       string
	Mode        string
	ModeOptions []string
	// VisibilityOptions feeds the archive-import form's visibility override
	// selector (#1113). It is empty on the paste-JSON render, which has no
	// visibility selector; the template only iterates it inside the archive
	// form. The archive form's default option ("use the archive's visibility")
	// is rendered statically, so this is just the explicit overrides.
	VisibilityOptions []string
}

// HandleQuizImportForm renders the JSON-import page. The textarea is empty
// on a fresh GET; the POST handler re-renders this template with the
// submitted JSON intact when validation fails.
func HandleQuizImportForm(logger *slog.Logger, csrfMgr *csrf.Manager) http.Handler {
	renderer := NewTemplateRenderer(logger, csrfMgr, "admin/pages/quizimport.gohtml")

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		renderer.Render(w, r, http.StatusOK, quizImportPageData{
			Title:             "Admin Dashboard - Import Quiz",
			Example:           quizImportExample,
			ModeOptions:       quiz.ModeValues(),
			VisibilityOptions: quiz.VisibilityValues(),
		})
	})
}

// HandleQuizImportSave parses the JSON pasted into the import form, builds
// a fresh quiz.Quiz from it, and persists via the existing store path so
// the resulting row is indistinguishable from one created via the regular
// quiz form. Validation errors re-render the form with the submitted JSON
// preserved so the admin can fix the payload without re-pasting.
func HandleQuizImportSave(logger *slog.Logger, csrfMgr *csrf.Manager, quizStore quiz.Store) http.Handler {
	renderer := NewTemplateRenderer(logger, csrfMgr, "admin/pages/quizimport.gohtml")

	renderStatus := func(w http.ResponseWriter, r *http.Request, status int, jsonText, mode, msg string) {
		renderer.Render(w, r, status, quizImportPageData{
			Title:             "Admin Dashboard - Import Quiz",
			JSON:              jsonText,
			Example:           quizImportExample,
			Error:             msg,
			Mode:              mode,
			ModeOptions:       quiz.ModeValues(),
			VisibilityOptions: quiz.VisibilityValues(),
		})
	}
	renderErr := func(w http.ResponseWriter, r *http.Request, jsonText, mode, msg string) {
		renderStatus(w, r, http.StatusBadRequest, jsonText, mode, msg)
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		parsed, ok := parseImportPayload(w, r, logger, renderErr)
		if !ok {
			return
		}

		// Stamp the session admin as the creator so the downstream
		// owner-gated mutating routes can match (#281).
		if p, present := auth.PlayerFromContext(r.Context()); present {
			parsed.Quiz.CreatedByPlayerID = p.ID
		}

		if err := storeQuiz(r.Context(), quizStore, parsed.Quiz); err != nil {
			if errors.Is(err, quiz.ErrSlugTaken) {
				// Same slug-derivation rule applies on the import path
				// (#293): re-render at 409 with the JSON intact so the
				// admin can rename and resubmit without re-pasting.
				renderStatus(
					w, r, http.StatusConflict, parsed.JSONText, parsed.Quiz.Mode,
					"A quiz with this title already exists - change the title in the JSON and resubmit.",
				)

				return
			}
			logger.ErrorContext(r.Context(), "error storing imported quiz", slog.Any("err", err))
			render500(w, r, logger, csrfMgr)

			return
		}

		http.Redirect(w, r, fmt.Sprintf("/admin/quizzes/%d", parsed.Quiz.ID), http.StatusSeeOther)
	})
}

// parsedImport holds the decoded + validated payload [parseImportPayload]
// returns to [HandleQuizImportSave]. Bundled so the parser can return a
// single struct (plus an ok flag) and stay under revive's
// function-result-limit while still surfacing the JSON text (for
// re-render on later failures) alongside the parsed quiz.
type parsedImport struct {
	JSONText string
	Quiz     *quiz.Quiz
}

// parseImportPayload reads + decodes + validates the request body for
// [HandleQuizImportSave]. On any failure it writes the form-rendered
// error response via renderErr and returns ok=false; the caller
// early-returns. Split out so [HandleQuizImportSave] stays under
// revive's function-length and gocognit limits.
func parseImportPayload(
	w http.ResponseWriter, r *http.Request, logger *slog.Logger,
	renderErr func(http.ResponseWriter, *http.Request, string, string, string),
) (parsedImport, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, maxFormSize)
	if err := r.ParseForm(); err != nil {
		logger.ErrorContext(r.Context(), "error parsing import form", slog.Any("err", err))
		renderErr(w, r, "", "", "request body too large or malformed")

		return parsedImport{}, false
	}

	// Play mode has no default on the import form (#752): the admin must
	// pick solo or live before the import proceeds. Reject a missing or
	// unrecognised value here rather than silently defaulting to solo the
	// way the regular quiz form does.
	mode := r.PostFormValue("mode")
	jsonText := r.PostFormValue("json")
	if !quiz.IsValidMode(mode) {
		renderErr(w, r, stripCodeFences(jsonText), mode, "choose a play mode (solo or live) before importing")

		return parsedImport{}, false
	}

	if jsonText == "" {
		renderErr(w, r, "", mode, "json field is required")

		return parsedImport{}, false
	}
	// The prompt asks the LLM to return the JSON in a ```json code block, so
	// tolerate a pasted block by stripping the surrounding fences before decode.
	jsonText = stripCodeFences(jsonText)

	var payload quizImportPayload
	dec := json.NewDecoder(strings.NewReader(jsonText))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&payload); err != nil {
		renderErr(w, r, jsonText, mode, fmt.Sprintf("invalid JSON: %v", err))

		return parsedImport{}, false
	}

	qz, err := quizFromImportPayload(payload)
	if err != nil {
		renderErr(w, r, jsonText, mode, fmt.Sprintf("validation errors: %v", err))

		return parsedImport{}, false
	}
	qz.Mode = mode
	if problems := (&quizForm{quiz: qz}).Valid(r.Context()); len(problems) > 0 {
		renderErr(w, r, jsonText, mode, fmt.Sprintf("validation errors: %v", problems))

		return parsedImport{}, false
	}

	return parsedImport{JSONText: jsonText, Quiz: qz}, true
}

// stripCodeFences removes a single surrounding Markdown fenced code block
// (```...``` or ```json...```) from s, so JSON pasted straight from an LLM's
// code block imports cleanly. It returns s unchanged when it is not fenced.
func stripCodeFences(s string) string {
	t := strings.TrimSpace(s)
	if !strings.HasPrefix(t, "```") {
		return s
	}
	nl := strings.IndexByte(t, '\n')
	if nl < 0 {
		return s
	}
	t = t[nl+1:]
	if i := strings.LastIndex(t, "```"); i >= 0 {
		t = t[:i]
	}

	return strings.TrimSpace(t)
}

var (
	// errImportQuestionsOrRounds is returned when the payload supplies
	// both a top-level questions[] and rounds[], or neither. The two are
	// mutually exclusive (#546): one flat list or named rounds, not a mix.
	errImportQuestionsOrRounds = errors.New(
		"provide either a top-level questions array or a rounds array, not both and not neither",
	)
	// errImportRoundTitleRequired is returned when an imported round
	// carries no title (#546).
	errImportRoundTitleRequired = errors.New("title is required")
	// errImportRoundNoQuestions is returned when an imported round
	// carries no questions (#546).
	errImportRoundNoQuestions = errors.New("at least one question is required")
)

// quizFromImportPayload converts the wire-shape payload into the domain
// model. The slug is always derived from the title - the payload doesn't
// carry one because LLMs are bad at picking a stable slug and the admin
// form does the same derivation. Question positions are assigned 1..N in
// payload order across all rounds.
//
// When the payload carries rounds[], the rounds are mapped onto
// Quiz.Rounds (each with its own questions) and the same questions are
// also flattened onto Quiz.Questions so the shared quizForm.Valid runs
// every per-question rule. With a top-level questions[] instead, the
// store drops everything in the quiz's default round, the original
// behaviour (#546).
func quizFromImportPayload(p quizImportPayload) (*quiz.Quiz, error) {
	if (len(p.Questions) == 0) == (len(p.Rounds) == 0) {
		return nil, errImportQuestionsOrRounds
	}

	// #99: honour the payload's per-quiz default when present; fall
	// back to the project value so authors who don't care can omit
	// the field entirely and still pass Quiz.Valid's range check.
	timeLimit := quiz.DefaultTimeLimitSeconds
	if p.TimeLimitSeconds != nil {
		timeLimit = *p.TimeLimitSeconds
	}
	qz := &quiz.Quiz{
		Title:            p.Title,
		Slug:             slug.Make(p.Title),
		Description:      p.Description,
		TimeLimitSeconds: timeLimit,
	}

	if len(p.Rounds) > 0 {
		if err := fillQuizFromRounds(qz, p.Rounds); err != nil {
			return nil, err
		}

		return qz, nil
	}

	qz.Questions = make([]*quiz.Question, 0, len(p.Questions))
	pos := 0
	for _, qIn := range p.Questions {
		pos++
		qz.Questions = append(qz.Questions, questionFromImportPayload(qIn, pos))
	}

	return qz, nil
}

// fillQuizFromRounds maps the authored rounds onto qz.Rounds and mirrors
// every question onto qz.Questions with a quiz-wide 1..N position in
// payload order, so the shared quizForm.Valid sees the full question set
// (#546). A round must carry a non-empty title and at least one question.
func fillQuizFromRounds(qz *quiz.Quiz, rounds []quizImportRoundPayload) error {
	qz.Rounds = make([]*quiz.Round, 0, len(rounds))
	pos := 0
	for i, rIn := range rounds {
		if rIn.Title == "" {
			return fmt.Errorf("round %d: %w", i+1, errImportRoundTitleRequired)
		}
		if len(rIn.Questions) == 0 {
			return fmt.Errorf("round %q: %w", rIn.Title, errImportRoundNoQuestions)
		}

		round := &quiz.Round{
			Position: i,
			Title:    rIn.Title,
			Summary:  rIn.Summary,
			// nil -> "inherit the quiz default", the same semantics the
			// admin form's blank input carries (#554).
			BoundaryDurationSeconds: rIn.BoundaryDurationSeconds,
			Questions:               make([]*quiz.Question, 0, len(rIn.Questions)),
		}
		for _, qIn := range rIn.Questions {
			pos++
			qs := questionFromImportPayload(qIn, pos)
			round.Questions = append(round.Questions, qs)
			qz.Questions = append(qz.Questions, qs)
		}
		qz.Rounds = append(qz.Rounds, round)
	}

	return nil
}

// questionFromImportPayload maps one import question onto the domain
// type at the given quiz-wide position. Imported questions never carry an
// image: the JSON import cannot reference uploaded media (which is keyed by a
// per-quiz media id, not a URL), so ImageMediaID stays nil (#937).
func questionFromImportPayload(qIn quizImportQuestionPayload, position int) *quiz.Question {
	qs := &quiz.Question{
		Text:     qIn.Text,
		Position: position,
		// nil -> "inherit the quiz default", the same semantics
		// the admin form's blank input carries (#99).
		TimeLimitSeconds: qIn.TimeLimitSeconds,
	}
	qs.Options = make([]*quiz.Option, 0, len(qIn.Options))
	for _, oIn := range qIn.Options {
		qs.Options = append(qs.Options, &quiz.Option{
			Text:    oIn.Text,
			Correct: oIn.Correct,
		})
	}

	return qs
}
