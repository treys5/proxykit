#!/usr/bin/env bash
# One-click Mac build for Proxy Tester.
# Produces a .dmg in the dist/ folder.
# Usage:  chmod +x build.sh && ./build.sh
set -e

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
cd "$SCRIPT_DIR"

echo ""
echo "  PROXY TESTER -- BUILD SCRIPT (Mac)"
echo "  Building macOS DMG..."
echo ""

# 1. Check for Node.js
if ! command -v node &>/dev/null; then
  echo "  ERROR: Node.js is not installed."
  echo "  Install it from https://nodejs.org then re-run this script."
  exit 1
fi
echo "  Node: $(node --version)   npm: $(npm --version)"

# 2. Install dependencies
echo "  Installing dependencies (may take a minute on first run)..."
npm install
echo "  Dependencies ready."

# 3. Warn if icon is missing
if [ ! -f assets/icon.icns ]; then
  echo ""
  echo "  NOTE: No icon at assets/icon.icns -- DMG will use the default Electron icon."
  echo "  Replace assets/icon.icns (512x512 .icns) to customise."
  echo ""
fi

# 4. Build
echo "  Running electron-builder..."
npm run build:mac

# 5. Report
echo ""
echo "  BUILD COMPLETE!"
echo "  DMG saved to: $SCRIPT_DIR/dist/"
echo ""
ls -lh dist/*.dmg 2>/dev/null || true
echo ""
