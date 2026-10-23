#!/bin/sh
set -eu

if ! git rev-parse --is-inside-work-tree >/dev/null 2>&1; then
  echo "error: initialize or clone the Git repository before running this check" >&2
  exit 1
fi

required_files="
LICENSE
README.md
CONTRIBUTING.md
CODE_OF_CONDUCT.md
SECURITY.md
.env.example
.github/workflows/ci.yml
"

for file in $required_files; do
  if [ ! -f "$file" ]; then
    echo "error: required public file is missing: $file" >&2
    exit 1
  fi
done

python3 scripts/check-source-tree.py
python3 scripts/check-release-docs.py

# The UI is built from its own Docker context; keep its distributed project
# notices identical to the canonical root copies.
for notice in LICENSE NOTICE; do
  if ! cmp -s "$notice" "web/$notice"; then
    echo "error: web/$notice must match the canonical $notice" >&2
    exit 1
  fi
done

forbidden_paths="$(git ls-files | grep -E '(^|/)(\.env($|\.)|data|bridge|slides-build|trusted-read-[^/]*|diagrams)(/|$)|\.(pdf|pptx|docx|inspect\.ndjson)$|deploy/macos/[^/]*\.plist$' | grep -Ev '(^|/)\.env\.example$' || true)"
if [ -n "$forbidden_paths" ]; then
  echo "error: local/internal artifacts are tracked:" >&2
  echo "$forbidden_paths" >&2
  exit 1
fi

if [ -f .env ] && ! git check-ignore --quiet .env; then
  echo "error: .env exists but is not ignored" >&2
  exit 1
fi

personal_paths="$(git grep -I -n -E '/Users/[^/]+/|/home/[^/]+/|[A-Za-z]:[/\\]Users[/\\]' -- . ':(exclude)scripts/public-release-check.sh' || true)"
if [ -n "$personal_paths" ]; then
  echo "error: machine-specific absolute path found:" >&2
  echo "$personal_paths" >&2
  exit 1
fi

secret_candidates="$(git grep -I -l -E 'AKIA[0-9A-Z]{16}|-----BEGIN [A-Z ]*PRIVATE KEY-----|sk-[A-Za-z0-9_-]{20,}' -- . ':(exclude)scripts/public-release-check.sh' || true)"
if [ -n "$secret_candidates" ]; then
  echo "error: possible embedded credential found in:" >&2
  echo "$secret_candidates" >&2
  exit 1
fi

if ! git diff --check; then
  echo "error: Git whitespace check failed" >&2
  exit 1
fi

echo "Public release preflight passed for the current Git tree."
echo "Before publishing, also scan the complete Git history with a dedicated secret scanner."
