package server

import (
	"net/http"

	"github.com/starquake/topbanana/internal/logging"
)

func handleAdmin(logger *logging.Logger) http.Handler {
	return http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			logger.Info(r.Context(), "Hello World", "handler", "handleAdmin")
			_, err := w.Write([]byte("Hello World"))
			if err != nil {
				logger.Error(r.Context(), "error writing response", err)

				return
			}
		})
}
