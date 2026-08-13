#!/bin/bash
set -Eeuo pipefail

APP_DIR="${RUNNING_APP_DIR:-/www/wwwroot/running-tools}"
COMPOSE_FILE="$APP_DIR/compose.server.yml"
CHECK_REQUEST_FILE="$APP_DIR/data/system/check-request.json"
UPDATE_REQUEST_FILE="$APP_DIR/data/system/update-request.json"
STATUS_FILE="$APP_DIR/data/system/update-status.json"
ENV_FILE="$APP_DIR/.env"
LOCK_FILE="/run/lock/running-tools-update.lock"

request_id=""
request_action="update"
current_revision=""
target_revision=""
old_image=""
image=""

compose_bin=()
if docker compose version >/dev/null 2>&1; then
  compose_bin=(docker compose)
elif command -v docker-compose >/dev/null 2>&1; then
  compose_bin=(docker-compose)
else
  echo "Docker Compose is not installed" >&2
  exit 1
fi

compose() { "${compose_bin[@]}" --env-file "$ENV_FILE" -f "$COMPOSE_FILE" "$@"; }

write_status() {
  local state="$1" message="$2" error="${3:-}"
  python3 - "$STATUS_FILE" "$state" "$message" "$current_revision" "$target_revision" "$request_id" "$error" "$request_action" <<'PY'
import json, os, sys, time
from pathlib import Path
path = Path(sys.argv[1])
state, message, current, latest, request_id, error, action = sys.argv[2:]
existing = {}
try:
    value = json.loads(path.read_text(encoding="utf-8"))
    if isinstance(value, dict): existing = value
except (OSError, json.JSONDecodeError):
    pass
now = time.time()
payload = {
    "state": state, "action": action or existing.get("action"), "message": message, "currentRevision": current or None,
    "latestRevision": latest or None, "updateAvailable": bool(current and latest and current != latest),
    "requestId": request_id or existing.get("requestId"), "requestedAt": existing.get("requestedAt"),
    "startedAt": existing.get("startedAt"), "finishedAt": existing.get("finishedAt"),
    "updatedAt": now, "error": error or None,
}
if state in {"checking", "updating"}: payload["startedAt"], payload["finishedAt"] = now, None
if state in {"update_available", "up_to_date", "success", "error"}: payload["finishedAt"] = now
path.parent.mkdir(parents=True, exist_ok=True)
temporary = path.with_name(f".{path.name}.{os.getpid()}.tmp")
temporary.write_text(json.dumps(payload, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
os.replace(temporary, path)
os.chmod(path, 0o644)
PY
}

fail() {
  local code="$?"
  trap - ERR
  local message="更新失败"
  [ "$request_action" = "check" ] && message="检查更新失败"
  write_status error "$message" "宿主机更新脚本退出，状态码 $code" || true
  exit "$code"
}
trap fail ERR

exec 9>"$LOCK_FILE"
flock -n 9 || exit 0
test -f "$ENV_FILE"
test -f "$COMPOSE_FILE"
mkdir -p "$(dirname "$STATUS_FILE")"

request_file=""
if [ -f "$CHECK_REQUEST_FILE" ]; then
  request_file="$CHECK_REQUEST_FILE"
elif [ -f "$UPDATE_REQUEST_FILE" ]; then
  request_file="$UPDATE_REQUEST_FILE"
fi
if [ -n "$request_file" ]; then
  processing="${request_file}.processing.$$"
  mv "$request_file" "$processing"
  request_id="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1])).get("requestId", ""))' "$processing" 2>/dev/null || true)"
  request_action="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1])).get("action", "update"))' "$processing" 2>/dev/null || true)"
  rm -f -- "$processing"
fi
case "$request_action" in check|update) ;; *) echo "unexpected request action: $request_action" >&2; exit 1 ;; esac

image="$(compose config --images | head -n 1)"
case "$image" in ghcr.io/bringbasket/running-tools:*) ;; *) echo "unexpected image: $image" >&2; exit 1 ;; esac
container="$(compose ps -q app 2>/dev/null || true)"
if [ -n "$container" ]; then
  old_image="$(docker inspect --format '{{.Image}}' "$container")"
  current_revision="$(docker inspect --format '{{index .Config.Labels "org.opencontainers.image.revision"}}' "$container" 2>/dev/null || true)"
fi

if [ "$request_action" = "check" ]; then
  write_status checking "正在检查可用版本"
else
  write_status updating "正在拉取已构建的新版本"
fi
docker pull "$image"
target_revision="$(docker image inspect --format '{{index .Config.Labels "org.opencontainers.image.revision"}}' "$image" 2>/dev/null || true)"
if [ -z "$target_revision" ]; then echo "image has no revision label" >&2; exit 1; fi

if [ "$request_action" = "check" ]; then
  if [ "$current_revision" = "$target_revision" ]; then
    write_status up_to_date "当前已经是最新版本"
  else
    write_status update_available "发现可用的新版本"
  fi
  exit 0
fi

if [ "$current_revision" = "$target_revision" ] && [ -n "$container" ] && [ "$(docker inspect --format '{{.State.Health.Status}}' "$container" 2>/dev/null || true)" = healthy ]; then
  write_status up_to_date "已是最新版本"
  exit 0
fi

write_status restarting "正在重启服务"
compose up -d --no-build --force-recreate --no-deps app
new_container="$(compose ps -q app)"
for _ in $(seq 1 30); do
  [ "$(docker inspect --format '{{.State.Health.Status}}' "$new_container" 2>/dev/null || true)" = healthy ] && {
    current_revision="$target_revision"
    write_status success "更新完成"
    exit 0
  }
  sleep 3
done

if [ -n "$old_image" ] && docker image inspect "$old_image" >/dev/null 2>&1; then
  docker tag "$old_image" "$image"
  compose up -d --no-build --force-recreate --no-deps app || true
fi
echo "new container did not become healthy" >&2
exit 1
