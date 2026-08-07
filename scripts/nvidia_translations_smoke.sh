#!/usr/bin/env bash
# Smoke test traduzioni NVIDIA (10 lingue + titolo casuale + descrizione lunga).
#
# Uso:
#   scripts/nvidia_translations_smoke.sh
#
# La chiave viene presa da $NVIDIA_API_KEY (se già esportata) oppure
# estratta da .env.dev — MAI stampata a video.
set -euo pipefail
cd "$(dirname "$0")/.."

if [ -z "${NVIDIA_API_KEY:-}" ]; then
  if [ -f .env.dev ]; then
    NVIDIA_API_KEY="$(grep -E '^NVIDIA_API_KEY=' .env.dev | tail -1 | cut -d= -f2- | tr -d '"' | tr -d "'")"
    export NVIDIA_API_KEY
  fi
fi

if [ -z "${NVIDIA_MODEL:-}" ]; then
  if [ -f .env.dev ]; then
    NVIDIA_MODEL="$(grep -E '^NVIDIA_MODEL=' .env.dev | tail -1 | cut -d= -f2- | tr -d '"' | tr -d "'")"
    export NVIDIA_MODEL
  fi
fi

if [ -z "${NVIDIA_API_KEY:-}" ]; then
  echo "ERRORE: NVIDIA_API_KEY non trovata (né in shell né in .env.dev)" >&2
  exit 1
fi

echo "▶ Smoke test traduzioni NVIDIA (chiave ${#NVIDIA_API_KEY} caratteri)"
echo "▶ go test -tags=nvidiasmoke -v -run TestNVIDIA ./internal/services/"
echo
exec go test -tags=nvidiasmoke -v -count=1 -timeout 420s -run 'TestNVIDIA' ./internal/services/
