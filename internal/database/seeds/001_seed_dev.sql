-- InstaEditLogin — Development Seed Data
-- Run only in local/dev environments. Never run in production.

-- Seed users (idempotent by email; users table has no unique constraint on email)
INSERT INTO users (email, name)
SELECT v.email, v.name
FROM (VALUES
  ('dev@example.com', 'Dev User'),
  ('test@example.com', 'Test User')
) AS v(email, name)
WHERE NOT EXISTS (
  SELECT 1 FROM users u WHERE u.email = v.email
);

-- Seed one workspace per dev user (idempotent via helper CTE)
WITH dev_user AS (
    SELECT id FROM users WHERE email = 'dev@example.com'
)
INSERT INTO workspaces (name, owner_id)
SELECT 'Personal', id FROM dev_user
WHERE NOT EXISTS (
    SELECT 1 FROM workspaces w WHERE w.owner_id = dev_user.id AND w.name = 'Personal'
);

-- Seed platform accounts for the dev user (idempotent by platform + platform_user_id)
INSERT INTO platform_accounts (user_id, platform, platform_user_id, username, workspace_id)
SELECT
  u.id,
  p.platform,
  p.platform_user_id,
  p.username,
  w.id
FROM users u
CROSS JOIN (VALUES
  ('meta', 'meta_dev_123', 'dev_meta'),
  ('tiktok', 'tiktok_dev_456', 'dev_tiktok'),
  ('youtube', 'youtube_dev_789', 'dev_youtube')
) AS p(platform, platform_user_id, username)
JOIN workspaces w ON w.owner_id = u.id
WHERE u.email = 'dev@example.com'
ON CONFLICT (platform, platform_user_id) DO NOTHING;
