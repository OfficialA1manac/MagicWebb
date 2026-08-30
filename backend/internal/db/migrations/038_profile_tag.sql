-- 038_profile_tag.sql — user-editable profile tag.
--
--   tag  a short free-form label the user pins to their profile (e.g. a
--        gamertag or crew name), shown next to the display name. NULL =
--        unset. The API layer enforces the format (max 32 chars; letters,
--        digits, spaces, dash, underscore; trimmed, empty clears it), so
--        the column carries no CHECK — validation rules can evolve without
--        a migration.
--
-- The 011 RLS grant for profiles UPDATE is column-scoped (verified stays
-- server-owned), so the new column needs its own UPDATE grant or the
-- self-update policy path could never write it.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE profiles
  ADD COLUMN IF NOT EXISTS tag TEXT;
GRANT UPDATE (tag) ON profiles TO authenticated;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE profiles DROP COLUMN IF EXISTS tag;
-- +goose StatementEnd
