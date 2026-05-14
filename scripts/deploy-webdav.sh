#!/usr/bin/env bash
# Build the static export and deploy it to a WebDAV server, pruning any
# remote files that no longer exist locally.
#
# Requires a webdav.env file in the repo root with:
#   WEBDAV_URL=https://dav.example.com
#   WEBDAV_PATH=/sites/wikitrivia
#   WEBDAV_USER=username
#   WEBDAV_PASS=password

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"

env_file="$repo_root/webdav.env"
if [[ ! -f "$env_file" ]]; then
  echo "error: $env_file not found" >&2
  echo "create it with WEBDAV_URL, WEBDAV_PATH, WEBDAV_USER, WEBDAV_PASS" >&2
  exit 1
fi

set -a
# shellcheck disable=SC1090
source "$env_file"
set +a

: "${WEBDAV_URL:?WEBDAV_URL not set in webdav.env}"
: "${WEBDAV_PATH:?WEBDAV_PATH not set in webdav.env}"
: "${WEBDAV_USER:?WEBDAV_USER not set in webdav.env}"
: "${WEBDAV_PASS:?WEBDAV_PASS not set in webdav.env}"

echo "==> building static export"
npm run build

if [[ ! -d "$repo_root/out" ]]; then
  echo "error: out/ directory not found after build" >&2
  exit 1
fi

base_url="${WEBDAV_URL%/}"
remote_path="/${WEBDAV_PATH#/}"
remote_path="${remote_path%/}"
remote_base="$base_url$remote_path"

auth=(--user "$WEBDAV_USER:$WEBDAV_PASS")
curl_common=(--fail-with-body --silent --show-error "${auth[@]}")

# Create a collection if it doesn't already exist. PROPFIND probe first because
# some servers (e.g. Fastmail) return 409 instead of the spec's 405 for MKCOL
# on an existing collection.
mkcol() {
  local url="$1"
  local code
  code="$(curl --silent --show-error --output /dev/null --write-out '%{http_code}' \
    "${auth[@]}" -X PROPFIND -H "Depth: 0" "$url" || true)"
  case "$code" in
    200|207) return 0 ;;
  esac
  code="$(curl --silent --show-error --output /dev/null --write-out '%{http_code}' \
    "${auth[@]}" -X MKCOL "$url" || true)"
  case "$code" in
    201|301) ;;
    *) echo "error: MKCOL $url returned $code" >&2; return 1 ;;
  esac
}

put_file() {
  local local_path="$1"
  local remote_url="$2"
  curl "${curl_common[@]}" -T "$local_path" "$remote_url" >/dev/null
}

url_decode() {
  local data="$1"
  printf '%b' "${data//%/\\x}"
}

echo "==> ensuring remote base exists: $remote_base"
mkcol "$remote_base/"

echo "==> uploading out/ to $remote_base"

while IFS= read -r dir; do
  rel="${dir#out}"
  [[ -z "$rel" ]] && continue
  echo "  mkdir $rel"
  mkcol "$remote_base$rel/"
done < <(find out -mindepth 1 -type d | sort)

while IFS= read -r file; do
  rel="${file#out}"
  echo "  put   $rel"
  put_file "$file" "$remote_base$rel"
done < <(find out -type f | sort)

echo "==> pruning remote entries not in build"

local_paths_file="$(mktemp)"
trap 'rm -f "$local_paths_file"' EXIT

find out -mindepth 1 | sed 's|^out||' | sort > "$local_paths_file"

propfind_body='<?xml version="1.0" encoding="utf-8"?>
<d:propfind xmlns:d="DAV:"><d:prop><d:resourcetype/></d:prop></d:propfind>'

remote_listing="$(curl --silent --show-error "${auth[@]}" \
  -X PROPFIND \
  -H "Depth: infinity" \
  -H "Content-Type: application/xml" \
  --data "$propfind_body" \
  "$remote_base/")"

declare -a to_delete=()

while IFS= read -r href; do
  [[ -z "$href" ]] && continue
  decoded="$(url_decode "$href")"
  # If the server returned an absolute URL, strip scheme://host to keep the path.
  case "$decoded" in
    http://*|https://*)
      decoded="${decoded#*://}"
      decoded="/${decoded#*/}"
      ;;
  esac
  rel="${decoded#"$remote_path"}"
  rel="${rel%/}"
  [[ -z "$rel" ]] && continue
  if ! grep -Fxq "$rel" "$local_paths_file"; then
    to_delete+=("$rel")
  fi
done < <(printf '%s' "$remote_listing" \
  | grep -oE '<[^>]*[Hh]ref[^>]*>[^<]*</[^>]*[Hh]ref>' \
  | sed -E 's/<[^>]+>//g')

if [[ "${#to_delete[@]}" -eq 0 ]]; then
  echo "  nothing to prune"
else
  # Delete deepest paths first so children go before their parents. A
  # cascaded DELETE on a collection may take children with it, so 404 is fine.
  while IFS= read -r rel; do
    echo "  del   $rel"
    code="$(curl --silent --show-error --output /dev/null --write-out '%{http_code}' \
      "${auth[@]}" -X DELETE "$remote_base$rel" || true)"
    case "$code" in
      200|204|404) ;;
      *) echo "warning: DELETE $rel returned $code" >&2 ;;
    esac
  done < <(printf '%s\n' "${to_delete[@]}" \
    | awk '{ print length, $0 }' \
    | sort -k1,1nr \
    | cut -d' ' -f2-)
fi

echo "==> done"
