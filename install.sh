#!/usr/bin/env bash
set -e

# claude-meter installer — builds from source
# Review this script: https://github.com/abhishekray07/claude-meter/blob/main/install.sh

if ! command -v go &>/dev/null; then
  echo "Go is required. Install from https://go.dev/dl/"
  exit 1
fi

INSTALL_DIR="${HOME}/.local/bin"
mkdir -p "$INSTALL_DIR"

SRC_DIR="${CLAUDE_METER_SRC:-${HOME}/.claude-meter-src}"

if [ -n "$CLAUDE_METER_SRC" ]; then
  echo "Using existing source at $SRC_DIR"
elif [ -d "$SRC_DIR" ]; then
  echo "Updating existing source..."
  cd "$SRC_DIR" && git pull
else
  echo "Cloning claude-meter..."
  git clone https://github.com/abhishekray07/claude-meter.git "$SRC_DIR"
fi

echo "Building..."
cd "$SRC_DIR" && go build -o "$INSTALL_DIR/claude-meter" ./cmd/claude-meter

mkdir -p "${HOME}/.claude-meter"

echo ""
echo "claude-meter installed to $INSTALL_DIR/claude-meter"
echo ""
echo "Usage:"
echo "  claude-meter start --plan-tier max_20x"
echo ""
echo "Point Claude Code at it:"
echo "  ANTHROPIC_BASE_URL=http://127.0.0.1:7735 claude"
