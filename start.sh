#!/bin/bash
set -e

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"

# Kill any leftover processes from a previous run
echo "Cleaning up any existing processes..."
lsof -ti :8002 | xargs kill -9 2>/dev/null || true
lsof -ti :5173 | xargs kill -9 2>/dev/null || true
sleep 1

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

cleanup() {
    echo "Stopping Cosmos..."
    kill $BACKEND_PID $FRONTEND_PID 2>/dev/null
    lsof -ti :8002 | xargs kill -9 2>/dev/null || true
    lsof -ti :5173 | xargs kill -9 2>/dev/null || true
    echo "Cosmos stopped."
}
trap cleanup INT TERM
wait
