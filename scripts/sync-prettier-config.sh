#!/usr/bin/env sh
set -e

# Resolve root from script location
ROOT_DIR="$(CDPATH= cd -- "$(dirname "$0")/.." && pwd)"
TARGET_CONFIG="$ROOT_DIR/prettier.config.json"
TARGET_IGNORE="$ROOT_DIR/.prettierignore"

# Remote prettier root
BASE_URL="https://raw.githubusercontent.com/project-init/.github/refs/heads/main/prettier"

# Check if curl is available
if ! command -v curl >/dev/null 2>&1; then
  echo "Error: curl is required to sync prettier config from remote source." >&2
  exit 1
fi

# Sync prettier config and ignore files
echo "Syncing Prettier config from remote source: $BASE_URL"
curl -fsSL "$BASE_URL/prettier.config.json" -o "$TARGET_CONFIG"

if [ ! -f "$TARGET_IGNORE" ]; then
  curl -fsSL "$BASE_URL/.prettierignore" -o "$TARGET_IGNORE"
else
  echo "Skipping .prettierignore sync because one already exists at $TARGET_IGNORE"
fi

echo "Prettier config synced to $ROOT_DIR"