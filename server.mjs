import { createServer } from 'node:http';
import { readFile } from 'node:fs/promises';
import { randomUUID } from 'node:crypto';
import { Readable } from 'node:stream';
import { pipeline } from 'node:stream/promises';
import { mediaUrl, resolveTrack, search, sourceNames, validTrackId } from './sources.mjs';

const web = new URL('./web/', import.meta.url);
const host = process.env.HOST || '127.0.0.1';
const port = Number(process.env.PORT || 8787);
const staticFiles = {
  '/': ['index.html', 'text/html; charset=utf-8'],
  '/player.css': ['player.css', 'text/css; charset=utf-8'],
  '/player.js': ['player.js', 'application/javascript; charset=utf-8'],
  '/icon.png': ['icon.png', 'image/png'],
  '/itunes-badge.gif': ['itunes-badge.gif', 'image/gif'],
};
const audioTypes = { mp3: 'audio/mpeg', m4a: 'audio/mp4', aac: 'audio/aac' };
const streams = new Map();
const mediaLifetime = 20 * 60 * 1000;

function json(res, status, data) {
  res.writeHead(status, { 'Content-Type': 'application/json; charset=utf-8' });
  res.end(JSON.stringify(data));
}

function streamPath(source, media) {
  const now = Date.now();
  for (const [key, entry] of streams) {
    if (entry.expires < now) streams.delete(key);
  }
  if (streams.size >= 256) streams.delete(streams.keys().next().value);
  const key = randomUUID() + '.' + media.extension;
  streams.set(key, { ...media, source, expires: now + mediaLifetime });
  return '/media/' + key;
}

async function fetchMedia(entry, req, signal) {
  let url = entry.url;
  const headers = { 'Accept-Encoding': 'identity' };
  if (req.headers.range) headers.Range = req.headers.range;
  for (let hop = 0; hop < 4; hop++) {
    const response = await fetch(url, { headers, method: req.method, redirect: 'manual', signal });
    if (![301, 302, 303, 307, 308].includes(response.status)) return response;
    const next = response.headers.get('location');
    await response.body?.cancel();
    url = next && mediaUrl(entry.source, new URL(next, url));
    if (!url) break;
  }
  throw new Error('Media redirect unavailable');
}

async function sendMedia(req, res, key) {
  const entry = streams.get(key);
  if (!entry || entry.expires < Date.now()) {
    json(res, 404, { message: '播放地址已过期，请重新选择曲目。' });
    return;
  }
  if (req.headers.range && !/^bytes=\d*-\d*$/.test(req.headers.range)) {
    res.writeHead(416).end();
    return;
  }
  const controller = new AbortController();
  const timeout = setTimeout(() => controller.abort(), 12000);
  res.once('close', () => controller.abort());
  let upstream;
  try {
    upstream = await fetchMedia(entry, req, controller.signal);
  } finally {
    clearTimeout(timeout);
  }
  if (upstream.status === 416) {
    await upstream.body?.cancel();
    res.writeHead(416, { 'Content-Range': upstream.headers.get('content-range') || 'bytes */*' }).end();
    return;
  }
  const type = upstream.headers.get('content-type') || '';
  if (!upstream.ok || /(?:text\/|json|html)/i.test(type)) {
    await upstream.body?.cancel();
    json(res, 502, { message: '原平台暂时无法提供音频。' });
    return;
  }
  const headers = { 'Content-Type': audioTypes[entry.extension] };
  for (const name of ['content-range', 'accept-ranges', 'content-length']) {
    const value = upstream.headers.get(name);
    if (value && !upstream.headers.get('content-encoding')) headers[name] = value;
  }
  res.writeHead(upstream.status, headers);
  if (!upstream.body) { res.end(); return; }
  await pipeline(Readable.fromWeb(upstream.body), res);
}

async function handle(req, res) {
  res.setHeader('Access-Control-Allow-Origin', '*');
  res.setHeader('Cache-Control', 'no-store');
  res.setHeader('X-Content-Type-Options', 'nosniff');
  const url = new URL(req.url, 'http://localhost');
  const { pathname, searchParams } = url;
  if (req.method !== 'GET' && !(req.method === 'HEAD' && pathname.startsWith('/media/'))) {
    json(res, 405, { message: '请求方式不受支持。' });
    return;
  }
  if (Object.hasOwn(staticFiles, pathname)) {
    const [name, type] = staticFiles[pathname];
    const content = await readFile(new URL(name, web));
    res.writeHead(200, { 'Content-Type': type, 'Content-Length': content.length });
    res.end(content);
    return;
  }
  if (pathname.startsWith('/media/')) {
    await sendMedia(req, res, pathname.slice('/media/'.length));
    return;
  }
  if (pathname === '/api/health') {
    json(res, 200, { ok: true, sources: Object.keys(sourceNames) });
    return;
  }
  const source = searchParams.get('source');
  if (!Object.hasOwn(sourceNames, source)) {
    json(res, 400, { message: '请选择一个音乐平台。' });
    return;
  }
  if (pathname === '/api/search') {
    const query = searchParams.get('q')?.trim();
    const page = Number(searchParams.get('page') || 1);
    if (!query || query.length > 80 || !Number.isInteger(page) || page < 1 || page > 25) {
      json(res, 400, { message: '请输入 1 到 80 个字符的歌名或歌手。' });
      return;
    }
    const results = await search(source, query, page);
    json(res, 200, { ...results, more: page < 25 && results.more });
    return;
  }
  if (pathname === '/api/play') {
    const id = searchParams.get('id');
    if (!validTrackId(source, id)) {
      json(res, 400, { message: '曲目编号无效。' });
      return;
    }
    const media = await resolveTrack(source, id);
    if (!media) {
      json(res, 404, { message: '平台未提供可播音源，可打开原平台。' });
      return;
    }
    json(res, 200, { path: streamPath(source, media) });
    return;
  }
  json(res, 404, { message: '找不到这个地址。' });
}

createServer((req, res) => {
  handle(req, res).catch(error => {
    if (res.destroyed || res.writableEnded) return;
    if (res.headersSent) { res.destroy(); return; }
    console.error('Request failed', req.url?.split('?')[0], error.name);
    json(res, 502, { message: '音乐服务暂时无法连接原平台。' });
  });
}).listen(port, host, () => console.log(`PalmCadence listening at http://${host}:${port}`));
