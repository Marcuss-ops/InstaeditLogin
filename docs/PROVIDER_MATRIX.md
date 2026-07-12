# InstaEditLogin — Provider Capabilities Matrix

This matrix records the **minimum supported feature set** per platform. No platform exposes analytics, inbox, calendar, templates, or advanced editing — every provider is reduced to its essential content type.

| Provider    | OAuth | Publish | Content                          | Scopes |
|-------------|-------|---------|----------------------------------|--------|
| Instagram   | ✅    | ✅      | image, Reel                      | `instagram_basic`, `instagram_content_publish`, `pages_show_list` |
| Facebook    | ✅    | ✅      | text, image (Page)               | `pages_manage_posts`, `pages_read_engagement`, `pages_show_list` |
| Threads     | ✅    | ✅      | text, image                      | `threads_basic`, `threads_content_publish` |
| TikTok      | ✅    | ✅      | video, privacy, comment/duet/stitch | `user.info.basic`, `video.publish` |
| Twitter / X | ✅    | ✅      | text, single image               | `tweet.write`, `users.read` |
| YouTube     | ✅    | ✅      | video, title, description, privacy | `youtube.upload` |
| LinkedIn    | ✅    | ✅      | text, single image (personal profile) | `openid`, `profile`, `email`, `w_member_social` |

## Provider interfaces

All providers are resolved through the common interfaces in `internal/services/provider.go`:

- `NameProvider` — platform identifier
- `OAuthProvider` — login flow, callback, token refresh
- `ContentValidator` — pre-publish content validation
- `Publisher` — synchronous publish
- `AsyncPublisher` — async publish state machine (TikTok, Threads)
- `ResourceDiscoverer` — sub-account discovery (Pages, IG Business Accounts)

No handler should contain `switch platform` logic; use the `CapabilityRouter` in `internal/providers/registry.go`.
