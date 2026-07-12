# InstaEditLogin — Provider Capabilities Matrix

| Provider | OAuth | Publish | Refresh Token | Scopes |
|----------|-------|---------|---------------|--------|
| Meta (Facebook/Instagram) | ✅ | ✅ | Long-lived token exchange | `pages_manage_posts`, `instagram_content_publish` |
| TikTok | ✅ | ✅ | Yes | `user.info.basic`, `video.publish` |
| Twitter / X | ✅ OAuth 2.0 PKCE only | ✅ | Yes | `tweet.write`, `users.read` |
| YouTube | ✅ | ✅ | Yes | `youtube.upload`, `youtube.readonly` |
| LinkedIn | ✅ | ✅ | No (no offline_access scope) | `openid`, `profile`, `email`, `w_member_social` |

## Provider Registry

All providers are resolved through the common interfaces in `internal/services/provider.go`:

- `OAuthProvider`
- `ContentPublisher`
- `TokenManager`

No handler should contain `switch platform` logic; use the registry map in `cmd/server/main.go`.
