#!/bin/bash
set -e

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"

echo "Starting Cosmos backend..."
cd "$SCRIPT_DIR/backend"
source /Users/thomas/.local/bin/env
uv run uvicorn main:app --host 0.0.0.0 --port 8002 --reload &
BACKEND_PID=$!

echo "Starting Cosmos frontend..."
cd "$SCRIPT_DIR/frontend"
npm run dev &
FRONTEND_PID=$!

echo ""
echo "Cosmos is running."
echo "  vmlx:     http://localhost:8080"
echo "  Backend:  http://localhost:8002"
echo "  Frontend: http://localhost:5173"
echo ""
echo "Press Ctrl+C to stop."

trap "kill $BACKEND_PID $FRONTEND_PID 2>/dev/null; echo 'Cosmos stopped.'" INT TERM
wait
