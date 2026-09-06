# InstaEdit CLI instructions for agents

Use the Go `instaedit` CLI for browserless automation. Do not use browser login flows for machine-to-machine work.

## Configuration

Set these environment variables before running commands:

```bash
export INSTAEDIT_URL="https://api.instaedit.org"
export INSTAEDIT_API_KEY="sk_live_xxxxxxxxx"
```

The API key must belong to the target workspace and include the permissions required by the command:

- `media` for media uploads;
- `write` for creating/reading editor sessions and attaching thumbnails;
- `publish` for publishing a YouTube editor session.

Never print, commit, or expose `INSTAEDIT_API_KEY`.

## Build

From the `InstaeditLogin` directory:

```bash
make instaedit-build
```

This writes `./bin/instaedit`. To run the focused CLI tests use:

```bash
make instaedit-test
```

To install the command into Go's standard binary directory (`GOBIN` or `GOPATH/bin`):

```bash
make instaedit-install
```

Then use either `./bin/instaedit` or the binary installed on `PATH` as `instaedit`. Override the local build path with `make instaedit-build CLI_BIN=/tmp/instaedit`.

## Commands

Upload a local media file. Supported types are JPEG, PNG, WebP, MP4, and QuickTime MOV:

```bash
instaedit media upload ./video.mp4
```

The command performs the complete media flow internally:

1. requests a presigned upload URL;
2. uploads directly to storage without forwarding the API key;
3. completes and verifies the media asset.

The JSON output contains the resulting `media_id`.

Create an editor session for a video that already exists on YouTube:

```bash
instaedit youtube editor-session create \
  --workspace 123 \
  --account 456 \
  --video YOUTUBE_VIDEO_ID
```

Save a generated cover as the session thumbnail:

```bash
instaedit youtube thumbnail set \
  --session SESSION_ID \
  --file ./cover.png
```

The CLI crops the source to 16:9, normalizes it to **1920x1080**, encodes it as JPEG, and compresses it below approximately **1.9 MB** before uploading it.

Publish an existing editor session:

```bash
instaedit youtube publish \
  --session SESSION_ID \
  --privacy public
```

Optional metadata overrides:

```bash
instaedit youtube publish \
  --session SESSION_ID \
  --privacy public \
  --title "Updated title" \
  --description "Updated description"
```

Run the cover upload, thumbnail attachment, and publish sequence in one command:

```bash
instaedit youtube cover-and-publish \
  --session SESSION_ID \
  --cover ./generated-cover.png \
  --privacy public
```

## Agent workflow

For a video already present on YouTube:

```text
generate or obtain cover image
→ instaedit youtube cover-and-publish --session SESSION_ID --cover COVER_PATH
```

For a new local video, use the complete workflow:

```bash
instaedit youtube upload \
  --workspace 123 \
  --account 456 \
  --file ./video.mp4 \
  --cover ./cover.png \
  --title "My video" \
  --description "Description" \
  --privacy public
```

This command uploads the video, creates a workspace post targeting the YouTube account, starts publication, polls until YouTube returns the real video ID, creates the editor session, optionally attaches the normalized cover, and performs the final editor-session publish. Use `--timeout` to change the maximum polling duration (default: 30 minutes). The initial posts worker publication uses the backend's configured YouTube default privacy; `--privacy` controls the final editor-session publish.

For a video that already exists on YouTube, use `editor-session create` followed by `cover-and-publish`.

Read an existing editor session without mutating staging:

```bash
INSTAEDIT_URL=https://api-staging.example.com \
INSTAEDIT_API_KEY="$STAGING_API_KEY" \
INSTAEDIT_SESSION_ID=ytedit_xxx \
make instaedit-staging-smoke
```

The staging smoke refuses non-staging HTTPS hostnames and is read-only by default. To intentionally exercise upload, thumbnail attachment, and unlisted publish, provide disposable staging inputs and explicitly set `APPLY_CLI_SMOKE=1`, `INSTAEDIT_SMOKE_MEDIA_FILE`, and `INSTAEDIT_SMOKE_COVER_FILE`. Never commit these values or print the API key.

All commands emit JSON on stdout. Errors are written to stderr and return a non-zero exit code. Preserve command output IDs for subsequent commands, but never preserve or log the API key.

## Git hygiene for agents

Never leave state behind that the next session cannot reconstruct:

- Do not create `git stash` entries. If you must temporarily set work aside, restore it before ending the session; if it must survive, export it with `git stash show -p > .stash-triage/<label>.patch` and delete the stash entry in the same session.
- Do not leave scratch worktrees (e.g. baseline checks under `/tmp`) — remove them with `git worktree remove` before finishing.
- Never drop or pop another session's stash: triage it into a patch file first and leave the decision to a human.
