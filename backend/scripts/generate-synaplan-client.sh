#!/usr/bin/env bash
# Generate the Synaplan API client from the OpenAPI spec published by the
# pinned synaplan Docker image.
#
# The spec is NOT committed to this repo — it is extracted fresh from the
# pinned image on every `go generate`. This keeps the generated client in
# lockstep with the synaplan version that CI exercises in E2E.
#
# Image pin MUST match the SYNAPLAN_IMAGE_DIGEST in .github/workflows/ci.yml.
# Override via env var for local experimentation:
#   SYNAPLAN_IMAGE=ghcr.io/metadist/synaplan:latest ./generate-synaplan-client.sh
set -euo pipefail

: "${SYNAPLAN_IMAGE:=ghcr.io/metadist/synaplan@sha256:71d343f439d0de8aa46dba95946a4b7948175d27b73599ebcfc6a6a8e4e1a66d}"

# Resolve paths relative to this script.
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BACKEND_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"
GEN_DIR="${BACKEND_DIR}/internal/synaplanapi"
SPEC_FILE="${GEN_DIR}/.cache/openapi.json"
CONFIG_FILE="${GEN_DIR}/oapi-codegen.yaml"
OUTPUT_FILE="${GEN_DIR}/client.gen.go"

mkdir -p "$(dirname "${SPEC_FILE}")"

echo ">>> Extracting OpenAPI spec from ${SYNAPLAN_IMAGE}"
# Symfony's dotenv insists on a .env file even when all vars are already set,
# so we copy .env.test to .env inside the container before dumping the spec.
# nelmio:apidoc:dump reads routes/attributes only — no DB or services needed.
docker run --rm --entrypoint=/bin/sh "${SYNAPLAN_IMAGE}" -c '
  cd /var/www/backend
  cp .env.test .env
  APP_ENV=test php bin/console nelmio:apidoc:dump --format=json
' > "${SPEC_FILE}"

# Sanity check — bail early if extraction silently produced something bogus.
if ! head -c 1 "${SPEC_FILE}" | grep -q '{'; then
  echo "!!! OpenAPI spec extraction produced unexpected output:" >&2
  head -c 500 "${SPEC_FILE}" >&2
  exit 1
fi

SPEC_SIZE=$(wc -c < "${SPEC_FILE}")
echo ">>> Extracted $(( SPEC_SIZE / 1024 )) KiB spec → ${SPEC_FILE}"

echo ">>> Running oapi-codegen → ${OUTPUT_FILE}"
# Run from the target directory so oapi-codegen resolves `output:` relative
# to the package dir, not the backend root. go tool still finds go.mod by
# walking upward from cwd.
cd "${GEN_DIR}"
go tool github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen \
  -config "${CONFIG_FILE}" \
  "${SPEC_FILE}"

echo ">>> Done"
