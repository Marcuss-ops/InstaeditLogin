# InstaEditLogin — Provider Capabilities Matrix

| Provider    | OAuth | Publish | Media         | Refresh Token | Scopes |
|-------------|-------|---------|---------------|---------------|--------|
| Instagram   | ✅    | ✅      | image, video  | Long-lived token exchange | `instagram_basic`, `instagram_content_publish`, `pages_show_list` |
| Facebook    | ✅    | ✅      | image (text)  | Long-lived token exchange | `pages_manage_posts`, `pages_read_engagement`, `pages_show_list` |
| Threads     | ✅    | ✅      | text, image, video | Long-lived token exchange | `threads_basic`, `threads_content_publish` |
| TikTok      | ✅    | ✅      | video         | Yes | `user.info.basic`, `video.publish` |
| Twitter / X | ✅ OAuth 2.0 PKCE only | ✅ | text_only | Yes | `tweet.write`, `users.read` |
| YouTube     | ✅    | ✅      | video         | Yes | `youtube.upload`, `youtube.readonly` |
| LinkedIn    | ✅    | ✅      | text_only     | No (no offline_access scope) | `openid`, `profile`, `email`, `w_member_social` |

## Content Type Notes

- **text_only** (Twitter/X, LinkedIn): media upload is not yet implemented. These providers
  accept only text content. Images/videos will be added when native upload infrastructure
  (presigned S3 → platform media endpoint) is wired for these platforms.
- **image, video**: the provider accepts media via the presigned upload flow
  (`POST /media/presign` → PUT signed URL → `POST /media/{id}/complete`), and the
  handler resolves `asset_id` to a trusted internal S3 URL before the platform API
  sees it.

## Provider Registry

All providers are resolved through the common interfaces in `internal/services/provider.go`:

- `NameProvider` — platform identifier
- `OAuthProvider` — login flow, callback, token refresh
- `ContentValidator` — pre-publish content validation
- `Publisher` — synchronous publish
- `AsyncPublisher` — async publish state machine (TikTok, Threads)
- `ResourceDiscoverer` — sub-account discovery (Pages, IG Business Accounts)

No handler should contain `switch platform` logic; use the `CapabilityRouter` in
`internal/providers/registry.go`.
