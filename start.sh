#!/bin/bash
# Starts Cosmos: builds Nova's core, then opens the app.
#   ./start.sh        normal start
#   ./start.sh dev    start with live reload, for working on the app
set -e

cd "$(dirname "$0")/app"

if ! command -v go >/dev/null 2>&1; then
  echo "Go is needed to build the core. Install it with: brew install go" >&2
  exit 1
fi
if ! command -v npm >/dev/null 2>&1; then
  echo "Node is needed to run the app. Install it with: brew install node" >&2
  exit 1
fi

if [ ! -d "node_modules" ]; then
  echo "Installing dependencies..."
  npm install
fi

# Electron's postinstall sometimes skips the binary download without saying so.
# Run it again if path.txt is missing (safe to run when already installed).
if [ ! -f "node_modules/electron/path.txt" ]; then
  echo "Downloading Electron..."
  node node_modules/electron/install.js
fi

if [ "$1" = "dev" ]; then
  npm run dev
else
  npm start
fi
