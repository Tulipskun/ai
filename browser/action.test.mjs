import test from 'node:test';
import assert from 'node:assert/strict';
import http from 'node:http';
import { startWorker, stopWorker } from './server.mjs';

test('snapshot refs support fill, press, click and reject stale refs', async () => {
  const pageServer = http.createServer((req, res) => {
    res.setHeader('content-type', 'text/html');
    res.end(`<!doctype html><html><body>
      <form onsubmit="event.preventDefault(); document.body.insertAdjacentHTML('beforeend','<p id=done>done</p>')">
        <label for="q">Search</label><input id="q">
        <button id="go" type="submit">Go</button>
      </form>
    </body></html>`);
  });
  await new Promise(resolve => pageServer.listen(0, '127.0.0.1', resolve));
  const port = pageServer.address().port;
  const worker = await startWorker({ host: '127.0.0.1', port: 0, headless: true, allowPrivate: true });
  try {
    await worker.rpc('browser.open', { session_id: 'a' });
    await worker.rpc('browser.navigate', { session_id: 'a', url: `http://127.0.0.1:${port}` });
    const snap = await worker.rpc('browser.snapshot', { session_id: 'a' });
    const textbox = snap.snapshot.match(/textbox[^\n]*\[ref=(e\d+)\]/)?.[1];
    const button = snap.snapshot.match(/button[^\n]*\[ref=(e\d+)\]/)?.[1];
    assert.ok(textbox);
    assert.ok(button);

    await worker.rpc('browser.fill', { session_id: 'a', ref: textbox, text: 'hello' });
    await assert.rejects(() => worker.rpc('browser.click', { session_id: 'a', ref: button }), error => error.code === 'stale_reference');

    const fresh = await worker.rpc('browser.snapshot', { session_id: 'a' });
    const freshButton = fresh.snapshot.match(/button[^\n]*\[ref=(e\d+)\]/)?.[1];
    await worker.rpc('browser.click', { session_id: 'a', ref: freshButton });
    const text = await worker.rpc('browser.get_text', { session_id: 'a' });
    assert.match(text.text, /done/);
  } finally {
    await stopWorker(worker);
    await new Promise(resolve => pageServer.close(resolve));
  }
});
