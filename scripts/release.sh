#!/usr/bin/env bash
# Build image bot dari Dockerfile, tag dengan git short SHA (+ VERSION kalau
# diisi) dan "latest", lalu push semuanya ke GitHub Container Registry.
#
# Login sekali sebelum pakai script ini (token butuh scope write:packages):
#   echo <GITHUB_PAT> | docker login ghcr.io -u <github-username> --password-stdin
#
# Pemakaian:
#   ./scripts/release.sh                # tag: <git-sha> + latest
#   VERSION=v1.2.0 ./scripts/release.sh  # tag tambahan: v1.2.0
#
# Kalau DOKPLOY_WEBHOOK_URL diisi (lihat README bagian Deploy ke Dokploy),
# script ini otomatis memicu redeploy di Dokploy setelah push berhasil.
set -euo pipefail

IMAGE="ghcr.io/mik-sea/bot_discord_go"
SHA="$(git rev-parse --short HEAD)"

TAGS=("$SHA" "latest")
if [ -n "${VERSION:-}" ]; then
	TAGS+=("$VERSION")
fi

echo "==> Building $IMAGE (tags: ${TAGS[*]})"
docker build -t "$IMAGE:$SHA" .

for tag in "${TAGS[@]:1}"; do
	docker tag "$IMAGE:$SHA" "$IMAGE:$tag"
done

for tag in "${TAGS[@]}"; do
	echo "==> Pushing $IMAGE:$tag"
	docker push "$IMAGE:$tag"
done

echo "==> Done. Image tersedia sebagai:"
for tag in "${TAGS[@]}"; do
	echo "    $IMAGE:$tag"
done

if [ -n "${DOKPLOY_WEBHOOK_URL:-}" ]; then
	echo "==> Memicu redeploy Dokploy..."
	curl -fsS -X POST "$DOKPLOY_WEBHOOK_URL" && echo " -> redeploy triggered"
fi
