#!/usr/bin/env bash
set -euo pipefail

# Secure machine workflow: the API key is read only from the environment and
# is never persisted or printed. Required permissions: read, write, media.
# Usage:
# INSTAEDIT_AGENT_API_KEY=sk_live_... INSTAEDIT_WORKSPACE_ID=7 \
# INSTAEDIT_GROUP_ID=7 ./scripts/agent-create-thumbnail-draft.sh image.png "Comedy draft"

image_path=${1:?image path is required}
name=${2:-Codex Comedy Draft}
base_url=${INSTAEDIT_API_BASE_URL:-https://api.instaedit.org}
api_key=${INSTAEDIT_AGENT_API_KEY:?INSTAEDIT_AGENT_API_KEY is required}
workspace_id=${INSTAEDIT_WORKSPACE_ID:?INSTAEDIT_WORKSPACE_ID is required}
group_id=${INSTAEDIT_GROUP_ID:?INSTAEDIT_GROUP_ID is required}

command -v jq >/dev/null || { echo "jq is required" >&2; exit 1; }
command -v file >/dev/null || { echo "file is required" >&2; exit 1; }
content_type=$(file --brief --mime-type "$image_path")
case "$content_type" in image/png|image/jpeg|image/webp) ;; *) echo "only PNG/JPEG/WebP is supported" >&2; exit 1 ;; esac
size_bytes=$(wc -c < "$image_path")
sha256=$(sha256sum "$image_path" | awk '{print $1}')
auth=( -H "Authorization: Bearer $api_key" -H "Content-Type: application/json" )

presign=$(curl --fail-with-body -sS "${auth[@]}" -X POST "$base_url/api/v1/media/presign" \
  --data "$(jq -cn --arg f "$(basename "$image_path")" --arg c "$content_type" --argjson s "$size_bytes" --arg h "$sha256" '{filename:$f,content_type:$c,size_bytes:$s,sha256:$h}')")")
upload_url=$(jq -r '.upload_url' <<<"$presign")
asset_id=$(jq -r '.asset_id' <<<"$presign")
curl --fail-with-body -sS -X PUT -H "Content-Type: $content_type" --data-binary "@$image_path" "$upload_url" >/dev/null
curl --fail-with-body -sS "${auth[@]}" -X POST "$base_url/api/v1/media/$asset_id/complete" >/dev/null

project=$(curl --fail-with-body -sS "${auth[@]}" -X POST "$base_url/api/v1/thumbnail-projects" \
  --data "$(jq -cn --argjson w "$workspace_id" --arg n "$name" --arg d "[instaedit-group:$group_id] Bozza Codex senza video" '{workspace_id:$w,name:$n,description:$d,canvas_width:1280,canvas_height:720}')")")
project_id=$(jq -r '.id' <<<"$project")
curl --fail-with-body -sS "${auth[@]}" -X POST "$base_url/api/v1/thumbnail-projects/$project_id/assets?workspace_id=$workspace_id" \
  --data "$(jq -cn --arg m "$asset_id" '{media_id:$m,role:"preview"}')" >/dev/null
printf '%s\n' "$project"
