#!/usr/bin/env bash
# Build the maze server and deploy it to the EC2 instance over SSH.
# Writes the systemd unit (with the admin password) and restarts the service,
# so it works for both first-time setup and redeploys.
#
# Usage:
#   MAZE_HOST=<public-ip> MAZE_KEY=~/path/to/key.pem ./deploy.sh
#
# Optional env:
#   MAZE_ADMIN_PASS   admin password (if unset, you'll be prompted)
#   MAZE_USER         SSH user (default: ec2-user)
#   MAZE_ROOMS        maze size (default: 20)
#   MAZE_TICK         tick rate ms (default: 300)
#   GOARCH            target arch: amd64 (default) or arm64 for Graviton

set -euo pipefail

: "${MAZE_HOST:?set MAZE_HOST to the instance public IP}"
: "${MAZE_KEY:?set MAZE_KEY to the path of the SSH .pem}"
MAZE_USER="${MAZE_USER:-ec2-user}"
MAZE_ROOMS="${MAZE_ROOMS:-20}"
MAZE_TICK="${MAZE_TICK:-300}"
GOARCH="${GOARCH:-amd64}"

# admin password: from env, else prompt (hidden input)
if [ -z "${MAZE_ADMIN_PASS:-}" ]; then
  read -rsp "admin password: " MAZE_ADMIN_PASS
  echo
  [ -n "$MAZE_ADMIN_PASS" ] || { echo "error: empty password" >&2; exit 1; }
fi

SSH_OPTS=(-i "$MAZE_KEY" -o StrictHostKeyChecking=accept-new)
TARGET="${MAZE_USER}@${MAZE_HOST}"

# build the unit locally so the password travels via scp (encrypted),
# never as a process argument
UNIT_TMP="$(mktemp)"
trap 'rm -f "$UNIT_TMP"' EXIT
chmod 600 "$UNIT_TMP"
cat > "$UNIT_TMP" <<UNIT
[Unit]
Description=maze-runner
After=network.target
[Service]
ExecStart=/usr/local/bin/maze-server -rooms ${MAZE_ROOMS} -tick ${MAZE_TICK}
Environment=MAZE_ADMIN_PASS=${MAZE_ADMIN_PASS}
Restart=always
User=root
[Install]
WantedBy=multi-user.target
UNIT

echo ">> building (linux/${GOARCH})"
GOOS=linux GOARCH="$GOARCH" CGO_ENABLED=0 go build -o /tmp/maze-server .

echo ">> copying binary + unit to ${TARGET}"
scp "${SSH_OPTS[@]}" /tmp/maze-server "${TARGET}:/tmp/maze-server"
scp "${SSH_OPTS[@]}" "$UNIT_TMP" "${TARGET}:/tmp/maze.service"

echo ">> installing + restarting"
ssh "${SSH_OPTS[@]}" "$TARGET" '
  set -e
  sudo mv /tmp/maze-server /usr/local/bin/maze-server
  sudo chmod +x /usr/local/bin/maze-server
  sudo mv /tmp/maze.service /etc/systemd/system/maze.service
  sudo chmod 600 /etc/systemd/system/maze.service
  sudo systemctl daemon-reload
  sudo systemctl enable --now maze
  sudo systemctl restart maze
  sleep 2
  systemctl is-active maze
'

echo ">> done — http://${MAZE_HOST}:8080/ (admin login uses the password you set)"
