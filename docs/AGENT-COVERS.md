# Agent cover workflow

The agent never receives browser cookies. Use a workspace-scoped API key with
`read`, `write`, and `media` permissions. Create it once from the authenticated
API-key settings page or `POST /api/v1/api-keys`, then store the plaintext only
in the agent/Vercel secret `INSTAEDIT_AGENT_API_KEY`.

The helper uploads a generated PNG/JPEG/WebP through the existing presign and
complete flow, creates a workspace thumbnail project tagged with the group,
and links the media as its preview asset:

```bash
export INSTAEDIT_AGENT_API_KEY='sk_live_...'
export INSTAEDIT_WORKSPACE_ID=7
export INSTAEDIT_GROUP_ID=7
./scripts/agent-create-thumbnail-draft.sh cover.png 'Comedy Codex Draft'
```

The API key is accepted by the thumbnail-project routes without changing the
browser cookie flow. It remains workspace-scoped and every request is audited
through the normal API-key identity.
