export default {
  async fetch(request, env) {
    const url = new URL(request.url);
    const path = url.pathname.replace(/\/+$/, '') || '/';
    if (request.method === 'OPTIONS') return new Response(null, { status: 204, headers: cors() });
    if (path === '/health' || path === '/v1/health') return json({ ok: true, service: 'ai-control-plane' });
    try {
      if (path === '/v1/sessions' && request.method === 'GET') {
        requireToken(request, env, 'client');
        const limit = clampInt(url.searchParams.get('limit'), 1, 100, 20);
        const rows = await env.DB.prepare(`SELECT id, config_json, history_json, created_at, updated_at FROM sessions ORDER BY updated_at DESC LIMIT ?`).bind(limit).all();
        return json({ sessions: rows.results.map(sessionInfo) });
      }
      if (path === '/v1/sessions' && request.method === 'POST') {
        requireToken(request, env, 'client');
        const body = await readJson(request);
        const id = String(body.id || crypto.randomUUID());
        const config = normalizeConfig(id, body);
        await upsertSession(env, id, config, [], zeroUsage());
        return json({ id, config, history: [], usage: zeroUsage() }, 201);
      }
      let m = path.match(/^\/v1\/sessions\/([^/]+)$/);
      if (m && request.method === 'GET') {
        requireToken(request, env, 'client');
        const row = await getSession(env, decodeURIComponent(m[1]));
        if (!row) return json({ error: 'session not found' }, 404);
        return json(sessionEnvelope(row));
      }
      m = path.match(/^\/v1\/sessions\/([^/]+)\/messages$/);
      if (m && request.method === 'POST') {
        requireToken(request, env, 'client');
        const id = decodeURIComponent(m[1]);
        const row = await getSession(env, id);
        if (!row) return json({ error: 'session not found' }, 404);
        const body = await readJson(request);
        const message = String(body.message || '').trim();
        if (!message) return json({ error: 'message is required' }, 400);
        const jobId = crypto.randomUUID();
        const model = String(body.model || sessionEnvelope(row).config.model || '');
        const now = new Date().toISOString();
        await env.DB.prepare(`INSERT INTO jobs (id, session_id, message, model, status, worker_id, cancel_requested, lease_expires_at, error, created_at, updated_at) VALUES (?, ?, ?, ?, 'queued', NULL, 0, NULL, NULL, ?, ?)`).bind(jobId, id, message, model, now, now).run();
        await addEvent(env, jobId, 'queued', { session_id: id, model });
        return json({ job_id: jobId, session_id: id, status: 'queued' }, 202);
      }
      m = path.match(/^\/v1\/jobs\/([^/]+)$/);
      if (m && request.method === 'GET') {
        requireToken(request, env, 'client');
        const row = await getJob(env, decodeURIComponent(m[1]));
        if (!row) return json({ error: 'job not found' }, 404);
        return json(jobInfo(row));
      }
      m = path.match(/^\/v1\/jobs\/([^/]+)\/events$/);
      if (m && request.method === 'GET') {
        requireToken(request, env, 'client');
        const id = decodeURIComponent(m[1]);
        if (!await getJob(env, id)) return json({ error: 'job not found' }, 404);
        const after = Math.max(0, Number(url.searchParams.get('after') || 0));
        const rows = await env.DB.prepare(`SELECT id, type, payload_json, at_ms FROM job_events WHERE job_id = ? AND id > ? ORDER BY id LIMIT 500`).bind(id, after).all();
        return json({ events: rows.results.map(row => ({ id: row.id, type: row.type, payload: parseJson(row.payload_json, {}), at_ms: row.at_ms })) });
      }
      m = path.match(/^\/v1\/jobs\/([^/]+)\/cancel$/);
      if (m && request.method === 'POST') {
        requireToken(request, env, 'client');
        const id = decodeURIComponent(m[1]);
        if (!await getJob(env, id)) return json({ error: 'job not found' }, 404);
        await env.DB.prepare(`UPDATE jobs SET cancel_requested=1, status=CASE WHEN status='queued' THEN 'cancelled' ELSE status END, updated_at=? WHERE id=?`).bind(new Date().toISOString(), id).run();
        await addEvent(env, id, 'cancel_requested', {});
        return json({ ok: true });
      }
      if (path === '/v1/compute/claim' && request.method === 'GET') {
        requireToken(request, env, 'compute');
        const workerId = String(url.searchParams.get('worker_id') || '').trim();
        if (!workerId) return json({ error: 'worker_id is required' }, 400);
        const now = new Date().toISOString();
        await env.DB.prepare(`UPDATE jobs SET status='queued', worker_id=NULL, lease_expires_at=NULL, updated_at=? WHERE status='running' AND lease_expires_at IS NOT NULL AND lease_expires_at < ?`).bind(now, now).run();
        const row = await env.DB.prepare(`SELECT * FROM jobs WHERE status='queued' AND cancel_requested=0 ORDER BY created_at LIMIT 1`).first();
        if (!row) return json({ job: null });
        const lease = new Date(Date.now()+30000).toISOString();
        const updated = await env.DB.prepare(`UPDATE jobs SET status='running', worker_id=?, lease_expires_at=?, updated_at=? WHERE id=? AND status='queued'`).bind(workerId, lease, now, row.id).run();
        if (!updated.meta.changes) return json({ job: null });
        await addEvent(env, row.id, 'claimed', { worker_id: workerId });
        return json({ job: { id: row.id, session_id: row.session_id, message: row.message, model: row.model } });
      }
      m = path.match(/^\/v1\/compute\/jobs\/([^/]+)\/control$/);
      if (m && request.method === 'GET') {
        requireToken(request, env, 'compute');
        const row = await getJob(env, decodeURIComponent(m[1]));
        if (!row) return json({ error: 'job not found' }, 404);
        return json({ status: row.status, cancel_requested: Boolean(row.cancel_requested), lease_expires_at: row.lease_expires_at });
      }
      m = path.match(/^\/v1\/compute\/jobs\/([^/]+)\/heartbeat$/);
      if (m && request.method === 'POST') {
        requireToken(request, env, 'compute');
        const body = await readJson(request);
        const id = decodeURIComponent(m[1]);
        const row = await getJob(env, id);
        if (!row) return json({ error: 'job not found' }, 404);
        if (row.worker_id !== String(body.worker_id || '')) return json({ error: 'worker mismatch' }, 409);
        const lease = new Date(Date.now()+30000).toISOString();
        await env.DB.prepare(`UPDATE jobs SET lease_expires_at=?, updated_at=? WHERE id=? AND status='running'`).bind(lease, new Date().toISOString(), id).run();
        return json({ ok: true, lease_expires_at: lease });
      }
      m = path.match(/^\/v1\/compute\/jobs\/([^/]+)\/events$/);
      if (m && request.method === 'POST') {
        requireToken(request, env, 'compute');
        const id = decodeURIComponent(m[1]);
        if (!await getJob(env, id)) return json({ error: 'job not found' }, 404);
        const body = await readJson(request);
        await addEvent(env, id, String(body.type || 'event'), body.payload ?? {}, Number(body.at_ms || Date.now()));
        return json({ ok: true });
      }
      m = path.match(/^\/v1\/compute\/jobs\/([^/]+)\/complete$/);
      if (m && request.method === 'POST') {
        requireToken(request, env, 'compute');
        const id = decodeURIComponent(m[1]);
        const row = await getJob(env, id);
        if (!row) return json({ error: 'job not found' }, 404);
        const body = await readJson(request);
        if (row.worker_id !== String(body.worker_id || '')) return json({ error: 'worker mismatch' }, 409);
        const status = ['succeeded','failed','cancelled'].includes(body.status) ? body.status : 'failed';
        const errorText = String(body.error || '');
        await env.DB.prepare(`UPDATE jobs SET status=?, error=?, lease_expires_at=NULL, updated_at=? WHERE id=?`).bind(status, errorText, new Date().toISOString(), id).run();
        await addEvent(env, id, 'completed', { status, error: errorText });
        return json({ ok:true, status });
      }
      if (path === '/v1/compute/storage/sessions' && request.method === 'GET') {
        requireToken(request, env, 'compute');
        const limit = clampInt(url.searchParams.get('limit'), 1, 100, 20);
        const rows = await env.DB.prepare(`SELECT id, config_json, history_json, usage_json, created_at, updated_at FROM sessions ORDER BY updated_at DESC LIMIT ?`).bind(limit).all();
        return json({ sessions: rows.results.map(sessionInfo) });
      }
      m = path.match(/^\/v1\/compute\/storage\/sessions\/([^/]+)$/);
      if (m) {
        requireToken(request, env, 'compute');
        const id = decodeURIComponent(m[1]);
        if (request.method === 'GET') {
          const row = await getSession(env, id);
          if (!row) return json({ error:'not found' },404);
          return json(sessionEnvelope(row));
        }
        if (request.method === 'PUT') {
          const body = await readJson(request);
          await upsertSession(env, id, body.config || body, Array.isArray(body.history) ? body.history : [], body.usage || zeroUsage());
          return json({ ok:true });
        }
      }
      return json({ error:'not found' },404);
    } catch (error) {
      const status = error && typeof error.status === 'number' ? error.status : 500;
      return json({ error: error instanceof Error ? error.message : String(error) }, status);
    }
  }
};

class HttpError extends Error {
  constructor(status, message) { super(message); this.status = status; }
}
function requireToken(request, env, kind) {
  const expected = kind === 'compute' ? env.COMPUTE_TOKEN : env.CLIENT_TOKEN;
  if (!expected) throw new HttpError(500, kind + ' token is not configured');
  if ((request.headers.get('Authorization') || '') !== 'Bearer ' + expected) throw new HttpError(401, 'unauthorized');
}
function normalizeConfig(id, body) { return { id, provider:String(body.provider||''), model:String(body.model||''), key_index:Number(body.key_index||0), thinking_level:String(body.thinking_level||''), temperature:body.temperature===null||body.temperature===undefined?null:Number(body.temperature), agent_mode:String(body.agent_mode||'main'), workspace:String(body.workspace||'') }; }
function zeroUsage(){return {input_tokens:0,output_tokens:0,total_tokens:0,cache_read_tokens:0,cache_write_tokens:0};}
async function getSession(env,id){return env.DB.prepare(`SELECT id, config_json, history_json, usage_json, created_at, updated_at FROM sessions WHERE id=?`).bind(id).first();}
async function getJob(env,id){return env.DB.prepare(`SELECT * FROM jobs WHERE id=?`).bind(id).first();}
function sessionEnvelope(row){return {config:parseJson(row.config_json,{id:row.id}),history:parseJson(row.history_json,[]),usage:parseJson(row.usage_json,zeroUsage())};}
function sessionInfo(row){const v=sessionEnvelope(row);return {id:row.id,provider:v.config.provider||'',model:v.config.model||'',updated_at:row.updated_at,turn_count:Array.isArray(v.history)?v.history.length:0};}
function jobInfo(row){return {id:row.id,session_id:row.session_id,message:row.message,model:row.model,status:row.status,cancel_requested:Boolean(row.cancel_requested),worker_id:row.worker_id,lease_expires_at:row.lease_expires_at,error:row.error,created_at:row.created_at,updated_at:row.updated_at};}
async function upsertSession(env,id,config,history,usage){const now=new Date().toISOString();const old=await getSession(env,id);const created=old?.created_at||now;await env.DB.prepare(`INSERT INTO sessions (id,config_json,history_json,usage_json,created_at,updated_at) VALUES (?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET config_json=excluded.config_json,history_json=excluded.history_json,usage_json=excluded.usage_json,updated_at=excluded.updated_at`).bind(id,JSON.stringify({...config,id}),JSON.stringify(history||[]),JSON.stringify(usage||zeroUsage()),created,now).run();}
async function addEvent(env,jobId,type,payload,atMs=Date.now()){await env.DB.prepare(`INSERT INTO job_events(job_id,type,payload_json,at_ms) VALUES (?,?,?,?)`).bind(jobId,type,JSON.stringify(payload??{}),atMs).run();}
async function readJson(request){const body=await request.json();return body&&typeof body==='object'?body:{};}
function parseJson(value,fallback){try{return JSON.parse(value);}catch{return fallback;}}
function clampInt(value,min,max,fallback){const n=Number(value);if(!Number.isFinite(n))return fallback;return Math.max(min,Math.min(max,Math.floor(n)));}
function cors(){return {'Access-Control-Allow-Origin':'*','Access-Control-Allow-Headers':'Authorization, Content-Type','Access-Control-Allow-Methods':'GET, POST, PUT, OPTIONS'};}
function json(value,status=200){return new Response(JSON.stringify(value),{status,headers:{'Content-Type':'application/json; charset=utf-8',...cors()}});}
