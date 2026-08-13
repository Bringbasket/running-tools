#!/bin/sh
set -eu

SOURCE_DIR="${1:-}"
TARGET_DIR="${2:-}"
if [ -z "$SOURCE_DIR" ] || [ -z "$TARGET_DIR" ]; then
  echo "usage: migrate-hme-data.sh OLD_DATA_DIR NEW_DATA_DIR" >&2
  exit 2
fi
if [ ! -d "$SOURCE_DIR" ]; then
  echo "source directory does not exist: $SOURCE_DIR" >&2
  exit 1
fi

mkdir -p "$TARGET_DIR/mail/state" "$TARGET_DIR/system"
copy_if_present() {
  source_file="$1"
  target_file="$2"
  if [ -f "$source_file" ]; then
    cp -p "$source_file" "$target_file"
    chmod 600 "$target_file"
    echo "copied $source_file"
  fi
}

copy_if_present "$SOURCE_DIR/hme-config.json" "$TARGET_DIR/mail/hme-config.json"
copy_if_present "$SOURCE_DIR/state/hme-session.json" "$TARGET_DIR/mail/state/hme-session.json"
copy_if_present "$SOURCE_DIR/state/session-state.json" "$TARGET_DIR/mail/state/session-state.json"
copy_if_present "$SOURCE_DIR/state/auto-refresh.json" "$TARGET_DIR/mail/state/auto-refresh.json"
copy_if_present "$SOURCE_DIR/state/update-status.json" "$TARGET_DIR/system/update-status.json"

echo "migration copy complete; source files were not changed"
