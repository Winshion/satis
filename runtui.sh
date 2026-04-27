#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$ROOT_DIR"

detect_os() {
  case "$(uname -s)" in
    Darwin) echo "darwin" ;;
    Linux) echo "linux" ;;
    *) echo "" ;;
  esac
}

pick_config() {
  local os_name="$1"
  local config_path=""
  case "$os_name" in
    darwin) config_path="configs/vfs.darwin.json" ;;
    linux) config_path="configs/vfs.linux.json" ;;
    *) config_path="" ;;
  esac

  if [[ -n "$config_path" && -f "$config_path" ]]; then
    echo "$config_path"
    return
  fi

  if [[ -f "vfs.config.json" ]]; then
    echo "vfs.config.json"
    return
  fi

  echo ""
}

pick_invoke_config() {
  if [[ -f "invoke.config.json" ]]; then
    echo "invoke.config.json"
    return
  fi
  echo "invoke.config.example.json"
}

preflight() {
  local os_name="$1"
  local config_path="$2"
  local invoke_config="$3"

  if [[ -z "$os_name" ]]; then
    echo "[preflight] unsupported OS: $(uname -s)" >&2
    return 1
  fi

  if [[ ! -f "go.mod" ]]; then
    echo "[preflight] go.mod not found in $ROOT_DIR" >&2
    return 1
  fi

  if ! command -v go >/dev/null 2>&1; then
    echo "[preflight] go command not found in PATH" >&2
    return 1
  fi

  if [[ -z "$config_path" ]]; then
    echo "[preflight] no VFS config available for OS: $os_name" >&2
    return 1
  fi
  if [[ ! -f "$config_path" ]]; then
    echo "[preflight] VFS config missing: $config_path" >&2
    return 1
  fi

  if [[ ! -f "$invoke_config" ]]; then
    echo "[preflight] invoke config missing: $invoke_config" >&2
    return 1
  fi

  echo "[preflight] OS: $os_name"
  echo "[preflight] VFS config: $config_path"
  echo "[preflight] invoke config: $invoke_config"
  echo "[preflight] go version: $(go version)"
  echo "[preflight] running smoke tests..."
  go test ./sandbox ./appconfig ./vfs -run Test -count=1
}

OS_NAME="$(detect_os)"
CONFIG_PATH="$(pick_config "$OS_NAME")"
INVOKE_CONFIG="$(pick_invoke_config)"

preflight "$OS_NAME" "$CONFIG_PATH" "$INVOKE_CONFIG"
echo "[startup] preflight passed, launching Satis TUI..."
go run ./cmd/satis-tui --config "$CONFIG_PATH" --invoke-mode openai --invoke-config "$INVOKE_CONFIG"