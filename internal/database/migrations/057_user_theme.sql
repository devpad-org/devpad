-- The colour theme each user chose (Settings > Appearance). The values are
-- the theme ids in frontend/src/theme/index.ts.
ALTER TABLE user_preferences ADD COLUMN theme TEXT NOT NULL DEFAULT 'gruvbox-dark';
