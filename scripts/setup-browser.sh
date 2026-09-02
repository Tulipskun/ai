#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BROWSER_DIR="$ROOT_DIR/browser"

if [[ "$(id -u)" -ne 0 ]]; then
  echo "setup-browser: run this script as root; sudo is not required" >&2
  exit 1
fi

command -v node >/dev/null 2>&1 || { echo "setup-browser: Node.js is required" >&2; exit 1; }
command -v npm >/dev/null 2>&1 || { echo "setup-browser: npm is required" >&2; exit 1; }

NODE_MAJOR="$(node -p 'process.versions.node.split(".")[0]')"
if (( NODE_MAJOR < 22 )); then
  echo "setup-browser: Node.js 22 or newer is required (found $(node --version))" >&2
  exit 1
fi

cd "$BROWSER_DIR"
npm ci
node node_modules/playwright/cli.js install --with-deps chromium

node --input-type=module <<'NODE'
import { chromium } from 'playwright';
const browser = await chromium.launch({ headless: true });
const page = await browser.newPage();
await page.goto('data:text/html,<title>ready</title><h1>Playwright ready</h1>');
if ((await page.title()) !== 'ready') process.exit(1);
await browser.close();
console.log('setup-browser: Chromium launch check passed');
NODE

echo "setup-browser: ready"
