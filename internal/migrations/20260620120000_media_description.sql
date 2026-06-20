-- +goose Up
-- Adds a host-supplied description to a media row (#1072) so an uploaded audio
-- clip carries a readable label in the library and the question picker instead
-- of the bare "Audio {id}". The column is on the shared media table (an image
-- row could reuse it later), but only the audio UI surfaces it for now.
--
-- NOT NULL DEFAULT '' so every existing row gets an empty description without a
-- backfill, and a caller that omits one stores the empty string rather than
-- NULL. ADD COLUMN is an in-place change in SQLite (no table rebuild), so even
-- though media is a parent table (questions references it) a plain ALTER inside
-- goose's default transaction is fine, exactly as 20260618120000 added
-- duration_ms.
-- +goose StatementBegin
ALTER TABLE media ADD COLUMN description TEXT NOT NULL DEFAULT '';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE media DROP COLUMN description;
-- +goose StatementEnd
