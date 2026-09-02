import http from 'node:http';
import crypto from 'node:crypto';
import { pathToFileURL } from 'node:url';
import { chromium } from 'playwright';

const DOM_VERSION_SCRIPT = `(() => {
  let version = 0;
  Object.defineProperty(window, '__aiDomVersion', { get: () => version, configurable: true });
  const observer = new MutationObserver(() => { version += 1; });
  const start = () => observer.observe(document.documentElement || document, { subtree: true, childList: true, attributes: true, characterData: true });
  if (document.documentElement) start(); else document.addEventListener('DOMContentLoaded', start, { once: true });
})();`;

const DEFAULTS = {
  host: '127.0.0.1',
  port: 0,
  headless: true,
  navigationTimeout: 30000,
  actionTimeout: 10000,
  snapshotTimeout: 10000,
  idleTimeout: 30 * 60 * 1000
};

export async function startWorker(options = {}) {
  const config = { ...DEFAULTS, ...options };
  const token = crypto.randomBytes(32).toString('hex');
  const browser = await chromium.launch({ headless: config.headless });
  const sessions = new Map();
  const server = http.createServer(async (req, res) => {
    try {
      if (req.method === 'GET' && req.url === '/health') return json(res, 200, { ready: true });
      if (req.method !== 'POST' || req.url !== '/rpc') return json(res, 404, { ok: false, error: { code: 'not_found', message: 'not found' } });
      if (req.headers.authorization !== `Bearer ${token}`) return json(res, 401, { ok: false, error: { code: 'unauthorized', message: 'unauthorized' } });
      const request = await readJSON(req);
      if (!request || typeof request.id !== 'string' || typeof request.method !== 'string') return json(res, 400, { id: request?.id ?? null, ok: false, error: { code: 'invalid_request', message: 'id and method are required' } });
      const result = await dispatch(request.method, request.params ?? {}, { browser, sessions, config });
      return json(res, 200, { id: request.id, ok: true, result });
    } catch (error) {
      const code = error?.code || 'worker_error';
      const status = code === 'unauthorized' ? 401 : 200;
      return json(res, status, { id: null, ok: false, error: { code, message: error instanceof Error ? error.message : String(error) } });
    }
  });

  await new Promise((resolve, reject) => {
    server.once('error', reject);
    server.listen(config.port, config.host, resolve);
  });
  const address = server.address();
  const baseURL = `http://${address.address}:${address.port}`;

  const worker = {
    server,
    browser,
    token,
    baseURL,
    sessions,
    async rpc(method, params = {}) {
      const id = crypto.randomUUID();
      const response = await fetch(`${baseURL}/rpc`, {
        method: 'POST',
        headers: { authorization: `Bearer ${token}`, 'content-type': 'application/json' },
        body: JSON.stringify({ id, method, params })
      });
      const body = await response.json();
      if (!body.ok) {
        const error = new Error(body.error?.message || 'worker error');
        error.code = body.error?.code || 'worker_error';
        throw error;
      }
      return body.result;
    }
  };
  return worker;
}

export async function stopWorker(worker) {
  if (!worker) return;
  for (const state of worker.sessions?.values?.() ?? []) await state.context.close().catch(() => {});
  worker.sessions?.clear?.();
  await worker.browser?.close?.().catch(() => {});
  await new Promise(resolve => worker.server?.close(() => resolve()));
}

async function dispatch(method, params, runtime) {
  switch (method) {
    case 'browser.open': return open(params, runtime);
    case 'browser.close': return close(params, runtime);
    case 'browser.navigate': return navigate(params, runtime);
    case 'browser.snapshot': return snapshot(params, runtime);
    case 'browser.click': return actionRef(params, runtime, 'click');
    case 'browser.fill': return actionRef(params, runtime, 'fill');
    case 'browser.press': return actionRef(params, runtime, 'press');
    case 'browser.select': return actionRef(params, runtime, 'select');
    case 'browser.scroll': return scroll(params, runtime);
    case 'browser.get_text': return getText(params, runtime);
    case 'browser.screenshot': return screenshot(params, runtime);
    default: throw coded('unknown_method', `unknown method: ${method}`);
  }
}

async function open({ session_id }, { browser, sessions, config }) {
  requireSessionID(session_id);
  let state = sessions.get(session_id);
  if (!state) {
    const context = await browser.newContext();
    await context.addInitScript({ content: DOM_VERSION_SCRIPT });
    state = { context, pages: new Map(), current: null, lastUsed: Date.now() };
    sessions.set(session_id, state);
  }
  const page = await state.context.newPage();
  page.setDefaultTimeout(config.actionTimeout);
  page.setDefaultNavigationTimeout(config.navigationTimeout);
  const pageID = crypto.randomUUID();
  const pageState = { id: pageID, page, snapshot: null, lastUsed: Date.now() };
  state.pages.set(pageID, pageState);
  state.current = pageState;
  state.lastUsed = Date.now();
  return { session_id, context_id: session_id, page_id: pageID, url: page.url(), title: await page.title().catch(() => '') };
}

async function close({ session_id }, { sessions }) {
  requireSessionID(session_id);
  const state = sessions.get(session_id);
  if (!state) return { closed: false };
  await state.context.close();
  sessions.delete(session_id);
  return { closed: true };
}

async function navigate({ session_id, url }, runtime) {
  const pageState = currentPage(session_id, runtime.sessions);
  if (typeof url !== 'string' || !/^https?:\/\//i.test(url)) throw coded('invalid_url', 'only http/https URLs are allowed');
  const response = await pageState.page.goto(url, { waitUntil: 'domcontentloaded', timeout: runtime.config.navigationTimeout });
  pageState.snapshot = null;
  touch(runtime.sessions.get(session_id), pageState);
  return { page_id: pageState.id, url: pageState.page.url(), title: await pageState.page.title(), status: response?.status() ?? 0 };
}

async function snapshot({ session_id }, runtime) {
  const pageState = currentPage(session_id, runtime.sessions);
  const snapshot = await pageState.page.ariaSnapshot({ mode: 'ai', timeout: runtime.config.snapshotTimeout });
  const refs = parseRefs(snapshot);
  const domVersion = await pageState.page.evaluate(() => window.__aiDomVersion ?? 0).catch(() => -1);
  pageState.snapshot = { refs, domVersion };
  touch(runtime.sessions.get(session_id), pageState);
  return { page_id: pageState.id, url: pageState.page.url(), title: await pageState.page.title(), snapshot };
}

async function actionRef({ session_id, ref, text, key, value }, runtime, action) {
  const pageState = currentPage(session_id, runtime.sessions);
  const target = await resolveRef(pageState, ref);
  if (action === 'click') await target.click({ timeout: runtime.config.actionTimeout });
  else if (action === 'fill') await target.fill(String(text ?? ''), { timeout: runtime.config.actionTimeout });
  else if (action === 'press') await target.press(String(key ?? 'Enter'), { timeout: runtime.config.actionTimeout });
  else if (action === 'select') await target.selectOption(String(value ?? ''), { timeout: runtime.config.actionTimeout });
  pageState.snapshot = null;
  touch(runtime.sessions.get(session_id), pageState);
  return { page_id: pageState.id, ok: true };
}

async function scroll({ session_id, direction = 'down', amount = 700 }, runtime) {
  const pageState = currentPage(session_id, runtime.sessions);
  const delta = Math.max(0, Number(amount) || 0);
  const y = direction === 'up' ? -delta : delta;
  await pageState.page.mouse.wheel(0, y);
  pageState.snapshot = null;
  touch(runtime.sessions.get(session_id), pageState);
  return { page_id: pageState.id, direction, amount: delta };
}

async function getText({ session_id, ref }, runtime) {
  const pageState = currentPage(session_id, runtime.sessions);
  const text = ref
    ? await (await resolveRef(pageState, ref)).innerText({ timeout: runtime.config.actionTimeout })
    : await pageState.page.locator('body').innerText({ timeout: runtime.config.actionTimeout });
  touch(runtime.sessions.get(session_id), pageState);
  return { page_id: pageState.id, text };
}

async function screenshot({ session_id, full_page = false }, runtime) {
  const pageState = currentPage(session_id, runtime.sessions);
  const data = await pageState.page.screenshot({ type: 'png', fullPage: Boolean(full_page) });
  touch(runtime.sessions.get(session_id), pageState);
  return { page_id: pageState.id, content_type: 'image/png', base64: data.toString('base64') };
}

async function resolveRef(pageState, ref) {
  if (!pageState.snapshot || !pageState.snapshot.refs.has(ref)) throw coded('stale_reference', `reference ${ref} is not valid; take a new browser_snapshot`);
  const currentVersion = await pageState.page.evaluate(() => window.__aiDomVersion ?? 0).catch(() => -1);
  if (currentVersion !== pageState.snapshot.domVersion) {
    pageState.snapshot = null;
    throw coded('stale_reference', `reference ${ref} is stale; take a new browser_snapshot`);
  }
  const descriptor = pageState.snapshot.refs.get(ref);
  const locator = pageState.page.getByRole(descriptor.role, descriptor.name ? { name: descriptor.name, exact: true } : undefined).nth(descriptor.index);
  if (await locator.count() === 0) {
    pageState.snapshot = null;
    throw coded('stale_reference', `reference ${ref} no longer resolves; take a new browser_snapshot`);
  }
  return locator;
}

function parseRefs(snapshot) {
  const refs = new Map();
  const counts = new Map();
  for (const line of snapshot.split('\n')) {
    const match = line.match(/^\s*-\s+([a-zA-Z0-9_-]+)(?:\s+"((?:[^"\\]|\\.)*)")?.*\[ref=(e\d+)\]/);
    if (!match) continue;
    const role = match[1];
    const name = match[2] ? match[2].replace(/\\"/g, '"').replace(/\\\\/g, '\\') : '';
    const key = `${role}\u0000${name}`;
    const index = counts.get(key) ?? 0;
    counts.set(key, index + 1);
    refs.set(match[3], { role, name, index });
  }
  return refs;
}

function currentPage(sessionID, sessions) {
  requireSessionID(sessionID);
  const state = sessions.get(sessionID);
  if (!state?.current) throw coded('missing_page', `no browser page for session ${sessionID}`);
  if (state.current.page.isClosed()) throw coded('missing_page', 'current browser page is closed');
  return state.current;
}

function touch(state, pageState) {
  const now = Date.now();
  if (state) state.lastUsed = now;
  if (pageState) pageState.lastUsed = now;
}

function requireSessionID(sessionID) {
  if (typeof sessionID !== 'string' || sessionID.trim() === '') throw coded('invalid_session', 'session_id is required');
}

function coded(code, message) {
  const error = new Error(message);
  error.code = code;
  return error;
}

function json(res, status, body) {
  res.writeHead(status, { 'content-type': 'application/json; charset=utf-8' });
  res.end(JSON.stringify(body));
}

async function readJSON(req) {
  const chunks = [];
  for await (const chunk of req) chunks.push(chunk);
  const data = Buffer.concat(chunks);
  if (data.length > 1 << 20) throw coded('request_too_large', 'request body exceeds 1 MiB');
  try {
    return JSON.parse(data.toString('utf8'));
  } catch {
    throw coded('invalid_json', 'request body is not valid JSON');
  }
}

async function main() {
  const args = new Map(process.argv.slice(2).map(arg => {
    const [key, value = ''] = arg.replace(/^--/, '').split('=');
    return [key, value];
  }));
  const worker = await startWorker({
    host: process.env.AI_BROWSER_HOST || '127.0.0.1',
    port: Number(process.env.AI_BROWSER_PORT || 0),
    headless: (args.get('headless') || process.env.AI_BROWSER_HEADLESS || 'true') !== 'false'
  });
  process.stdout.write(JSON.stringify({ ready: true, host: new URL(worker.baseURL).hostname, port: Number(new URL(worker.baseURL).port), token: worker.token }) + '\n');
  const shutdown = async () => { await stopWorker(worker); process.exit(0); };
  process.once('SIGINT', shutdown);
  process.once('SIGTERM', shutdown);
}

if (process.argv[1] && pathToFileURL(process.argv[1]).href === import.meta.url) main().catch(error => { console.error(error?.message || error); process.exit(1); });
