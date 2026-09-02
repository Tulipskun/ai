import test from 'node:test';
import assert from 'node:assert/strict';
import http from 'node:http';
import { startWorker, stopWorker } from './server.mjs';

test('worker exposes authenticated health and RPC', async () => {
  const worker = await startWorker({ host: '127.0.0.1', port: 0, headless: true });
  try {
    const health = await fetch(`${worker.baseURL}/health`);
    assert.equal(health.status, 200);
    const body = await health.json();
    assert.equal(body.ready, true);
    assert.equal(body.token, undefined);

    const denied = await fetch(`${worker.baseURL}/rpc`, {
      method: 'POST',
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify({ id: '1', method: 'browser.open', params: { session_id: 'a' } })
    });
    assert.equal(denied.status, 401);

    const opened = await worker.rpc('browser.open', { session_id: 'a' });
    assert.equal(opened.session_id, 'a');
    assert.ok(opened.page_id);
  } finally {
    await stopWorker(worker);
  }
});

test('sessions isolate contexts and pages can coexist', async () => {
  const worker = await startWorker({ host: '127.0.0.1', port: 0, headless: true });
  try {
    const a1 = await worker.rpc('browser.open', { session_id: 'a' });
    const a2 = await worker.rpc('browser.open', { session_id: 'a' });
    const b1 = await worker.rpc('browser.open', { session_id: 'b' });
    assert.notEqual(a1.page_id, a2.page_id);
    assert.notEqual(a1.context_id, b1.context_id);
  } finally {
    await stopWorker(worker);
  }
});

test('browser navigation and snapshot expose refs', async () => {
  const pageServer = http.createServer((req, res) => {
    res.setHeader('content-type', 'text/html');
    res.end('<!doctype html><html><body><h1>Hello</h1><label for="q">Search</label><input id="q"><button id="go">Go</button></body></html>');
  });
  await new Promise(resolve => pageServer.listen(0, '127.0.0.1', resolve));
  const port = pageServer.address().port;
  const worker = await startWorker({ host: '127.0.0.1', port: 0, headless: true });
  try {
    await worker.rpc('browser.open', { session_id: 'a' });
    const nav = await worker.rpc('browser.navigate', { session_id: 'a', url: `http://127.0.0.1:${port}` });
    assert.equal(nav.status, 200);
    const snap = await worker.rpc('browser.snapshot', { session_id: 'a' });
    assert.match(snap.snapshot, /button .*ref=e\d+/);
    assert.match(snap.snapshot, /textbox .*ref=e\d+/);
  } finally {
    await stopWorker(worker);
    await new Promise(resolve => pageServer.close(resolve));
  }
});
