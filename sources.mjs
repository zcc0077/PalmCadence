import Meting from '@meting/core';
import JSON5 from 'json5';

export const sourceNames = {
  tencent: 'QQ 音乐',
  netease: '网易云',
  kuwo: '酷我',
  kugou: '酷狗',
  apple: 'Apple Music',
};
const pageSize = 18;
const appleStorefront = process.env.APPLE_STOREFRONT || 'US';
const appleOrigins = ['music.apple.com', 'itunes.apple.com'];
const mediaDomains = {
  tencent: ['music.qq.com', 'qqmusic.qq.com', 'y.qq.com'],
  netease: ['music.126.net', 'music.163.com'],
  kuwo: ['kuwo.cn', 'kwcdn.com', 'kwcdn.net'],
  kugou: ['kugou.com'],
  apple: ['itunes.apple.com', 'mzstatic.com'],
};
const idPatterns = {
  tencent: /^[a-z0-9]{1,24}$/i,
  netease: /^\d{1,18}$/,
  kuwo: /^\d{1,18}$/,
  kugou: /^[a-f0-9]{32}$/i,
  apple: /^\d{1,18}$/,
};
const links = {
  tencent: id => `https://y.qq.com/n/ryqq/songDetail/${id}`,
  netease: id => `https://music.163.com/song?id=${id}`,
  kuwo: id => `https://www.kuwo.cn/play_detail/${id}`,
  kugou: id => `https://www.kugou.com/song/#hash=${id}`,
};

export function validTrackId(source, id) {
  return Boolean(idPatterns[source]?.test(id || ''));
}

export function mediaUrl(source, value) {
  try {
    const url = new URL(value);
    if (!['https:', 'http:'].includes(url.protocol) || url.port || url.username || url.password) return null;
    if (!mediaDomains[source].some(domain => url.hostname === domain || url.hostname.endsWith('.' + domain))) return null;
    return url;
  } catch {
    return null;
  }
}

function parseResponse(value) {
  return typeof value === 'string' ? JSON.parse(value) : value;
}

function meting(source) {
  return new Meting(source).format(true);
}

async function getText(url) {
  const response = await fetch(url, {
    headers: { 'User-Agent': 'PalmCadence/0.1' },
    signal: AbortSignal.timeout(9000),
  });
  if (!response.ok) throw new Error('Platform HTTP ' + response.status);
  return response.text();
}

function plain(value) {
  return String(value ?? '').replace(/&(?:nbsp|amp|lt|gt|quot|#39);/g, entity => ({
    '&nbsp;': ' ', '&amp;': '&', '&lt;': '<', '&gt;': '>', '&quot;': '"', '&#39;': "'",
  })[entity]).trim();
}

function toTrack(source, id, title, artist, album) {
  if (!validTrackId(source, id) || !title) return null;
  return {
    source,
    id: String(id),
    title: plain(title),
    artist: plain(artist),
    album: plain(album),
    url: links[source](id),
  };
}

function appleTrack(row) {
  if (!validTrackId('apple', row.trackId) || !row.trackName || !row.trackViewUrl) return null;
  const url = new URL(row.trackViewUrl);
  if (url.protocol !== 'https:' || !appleOrigins.includes(url.hostname)) return null;
  return {
    source: 'apple',
    id: String(row.trackId),
    title: plain(row.trackName),
    artist: plain(row.artistName),
    album: plain(row.collectionName),
    url: url.href,
    preview: Boolean(mediaUrl('apple', row.previewUrl)),
  };
}

async function appleResults(endpoint, params) {
  const url = new URL(endpoint, 'https://itunes.apple.com');
  for (const [key, value] of Object.entries(params)) url.searchParams.set(key, value);
  url.searchParams.set('country', appleStorefront);
  const response = JSON.parse(await getText(url));
  if (!Array.isArray(response.results)) throw new Error('Invalid Apple result');
  return response.results;
}

async function searchKuwo(query, page) {
  const url = new URL('https://search.kuwo.cn/r.s');
  url.search = new URLSearchParams({
    all: query, ft: 'music', itemset: 'web_2013', client: 'kt',
    pn: page - 1, rn: pageSize, rformat: 'json', encoding: 'utf8',
  });
  const data = JSON5.parse(await getText(url));
  if (!Array.isArray(data.abslist)) {
    if (Number(data.TOTAL) === 0) return { tracks: [], more: false };
    throw new Error('Invalid Kuwo result');
  }
  const tracks = data.abslist.map(row => toTrack(
    'kuwo', String(row.MUSICRID || '').replace(/^MUSIC_/, ''),
    row.SONGNAME || row.NAME, row.ARTIST, row.ALBUM
  )).filter(Boolean);
  return { tracks, more: Number(data.TOTAL) > page * pageSize && tracks.length > 0 };
}

export async function search(source, query, page) {
  if (source === 'kuwo') return searchKuwo(query, page);
  if (source === 'apple') {
    const rows = await appleResults('/search', {
      term: query, media: 'music', entity: 'song', limit: 60,
    });
    const tracks = rows.map(appleTrack).filter(Boolean);
    return { tracks: tracks.slice((page - 1) * pageSize, page * pageSize), more: tracks.length > page * pageSize };
  }
  const rows = parseResponse(await meting(source).search(query, { page, limit: pageSize }));
  if (!Array.isArray(rows)) throw new Error('Invalid music provider result');
  const tracks = rows.map(row => toTrack(
    source, row.url_id ?? row.id, row.name,
    Array.isArray(row.artist) ? row.artist.join(' / ') : row.artist, row.album,
  )).filter(Boolean);
  if (rows.length && !tracks.length) throw new Error('Invalid music provider rows');
  return { tracks, more: rows.length >= pageSize };
}

export async function resolveTrack(source, id) {
  if (source === 'apple') {
    const rows = await appleResults('/lookup', { id, entity: 'song' });
    const match = rows.find(row => String(row.trackId) === id && appleTrack(row));
    const url = mediaUrl(source, match?.previewUrl);
    return url && { url, extension: 'm4a' };
  }
  const result = parseResponse(await meting(source).url(id, 128));
  const url = mediaUrl(source, result?.url);
  if (!url) return null;
  const extension = /\.(mp3|m4a|aac)$/i.exec(url.pathname)?.[1].toLowerCase();
  return extension ? { url, extension } : null;
}
