package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
)

// mediaDirPerm is the permission for the media root created at startup.
const mediaDirPerm os.FileMode = 0o755

// errEmptyMediaDir is returned by ensureMediaDir when MediaDir resolves to the
// empty string, which is a misconfiguration: uploaded media would have nowhere
// to land.
var errEmptyMediaDir = errors.New("media directory must not be empty")

// ensureMediaDir creates the configured media directory (and any missing
// parents) so the first upload does not race a missing root (#936). An empty
// dir is a misconfiguration: media has nowhere to land, so fail fast rather
// than writing into the working directory. Errors are logged here so the
// caller stays within its function-length budget.
func ensureMediaDir(ctx context.Context, dir string, logger *slog.Logger) error {
	err := mkMediaDir(dir)
	if err != nil {
		logger.ErrorContext(ctx, "error preparing media directory", slog.Any("err", err))
	}

	return err
}

// mkMediaDir is the pure half of ensureMediaDir: it validates and creates the
// directory without logging, so the helper's behaviour is testable without a
// logger and the empty-dir guard stays matchable via [errors.Is].
func mkMediaDir(dir string) error {
	if dir == "" {
		return errEmptyMediaDir
	}
	if err := os.MkdirAll(dir, mediaDirPerm); err != nil {
		return fmt.Errorf("creating media directory %q: %w", dir, err)
	}

	return nil
}
