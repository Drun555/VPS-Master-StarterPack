#!/bin/sh

set -eu

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
BINARY="${SCRIPT_DIR}/vps-reality-master"
ENV_FILE="${SCRIPT_DIR}/.env"
SERVICE_NAME="vps-reality-master"

if [ "$(id -u)" -ne 0 ]; then
  printf 'Run register-initd.sh as root.\n' >&2
  exit 1
fi

if [ ! -x "$BINARY" ]; then
  printf 'Executable not found: %s\n' "$BINARY" >&2
  exit 1
fi

if [ ! -r "$ENV_FILE" ]; then
  printf 'Configuration not found: %s\n' "$ENV_FILE" >&2
  exit 1
fi

case "$SCRIPT_DIR" in
  *"'"*)
    printf 'Installation path must not contain a single quote.\n' >&2
    exit 1
    ;;
esac

if [ -d /opt/etc/init.d ] && [ -f /opt/etc/init.d/rc.unslung ]; then
  TARGET="/opt/etc/init.d/S90${SERVICE_NAME}"
  MODE="entware"
elif [ -d /etc/init.d ]; then
  TARGET="/etc/init.d/${SERVICE_NAME}"
  MODE="sysv"
else
  printf 'Neither Entware nor SysV init.d directory was found.\n' >&2
  exit 1
fi

cat >"$TARGET" <<EOF
#!/bin/sh

NAME='${SERVICE_NAME}'
WORK_DIR='${SCRIPT_DIR}'
BINARY='${BINARY}'
PID_FILE='${SCRIPT_DIR}/master.pid'
LOG_FILE='${SCRIPT_DIR}/master.log'

is_running() {
  [ -r "\$PID_FILE" ] || return 1
  PID=\$(sed -n '1p' "\$PID_FILE")
  [ -n "\$PID" ] && kill -0 "\$PID" 2>/dev/null
}

start() {
  if is_running; then
    printf '%s is already running.\n' "\$NAME"
    return 0
  fi
  cd "\$WORK_DIR"
  "\$BINARY" >>"\$LOG_FILE" 2>&1 &
  PID=\$!
  printf '%s\n' "\$PID" >"\$PID_FILE"
  sleep 1
  if ! kill -0 "\$PID" 2>/dev/null; then
    rm -f "\$PID_FILE"
    printf '%s failed to start; inspect %s.\n' "\$NAME" "\$LOG_FILE" >&2
    return 1
  fi
  printf 'Started %s (pid %s).\n' "\$NAME" "\$PID"
}

stop() {
  if ! is_running; then
    rm -f "\$PID_FILE"
    printf '%s is not running.\n' "\$NAME"
    return 0
  fi
  kill "\$PID"
  WAIT=0
  while kill -0 "\$PID" 2>/dev/null && [ "\$WAIT" -lt 20 ]; do
    sleep 1
    WAIT=\$((WAIT + 1))
  done
  if kill -0 "\$PID" 2>/dev/null; then
    kill -9 "\$PID" 2>/dev/null || true
  fi
  rm -f "\$PID_FILE"
  printf 'Stopped %s.\n' "\$NAME"
}

case "\${1:-}" in
  start) start ;;
  stop) stop ;;
  restart) stop; start ;;
  status) if is_running; then printf '%s is running.\n' "\$NAME"; else printf '%s is stopped.\n' "\$NAME"; exit 1; fi ;;
  *) printf 'Usage: %s {start|stop|restart|status}\n' "\$0" >&2; exit 1 ;;
esac
EOF

chmod 0755 "$TARGET"

if [ "$MODE" = "sysv" ] && command -v update-rc.d >/dev/null 2>&1; then
  update-rc.d "$SERVICE_NAME" defaults >/dev/null
fi

printf 'Registered %s startup script at %s.\n' "$MODE" "$TARGET"
printf 'The service was not started. Start it manually or reboot the device.\n'
