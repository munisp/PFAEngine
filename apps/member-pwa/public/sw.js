/* member-pwa service worker (hand-rolled, no build plugin).
 * Strategy:
 *   - static assets & navigations: cache-first (precache shell, then
 *     runtime-cache hashed assets), offline fallback page for navigations
 *   - /api GETs: network-first; successful responses are stored in
 *     CacheStorage so the last-known balance/statement survives offline.
 *     Cache hits are tagged with X-Pwa-Cache: hit so the app can show the
 *     offline indicator.
 */
const VERSION = 'member-pwa-v1'
const STATIC_CACHE = VERSION + '-static'
const API_CACHE = VERSION + '-api'
const PRECACHE = [
  '/',
  '/index.html',
  '/offline.html',
  '/manifest.webmanifest',
  '/theme.json',
  '/icons/icon-192.png',
  '/icons/icon-512.png',
]

self.addEventListener('install', (event) => {
  event.waitUntil(
    caches.open(STATIC_CACHE)
      .then((cache) => cache.addAll(PRECACHE))
      .then(() => self.skipWaiting()),
  )
})

self.addEventListener('activate', (event) => {
  event.waitUntil(
    caches.keys()
      .then((keys) => Promise.all(
        keys.filter((key) => !key.startsWith(VERSION)).map((key) => caches.delete(key)),
      ))
      .then(() => self.clients.claim()),
  )
})

self.addEventListener('fetch', (event) => {
  const req = event.request
  if (req.method !== 'GET') return
  const url = new URL(req.url)
  if (url.origin !== self.location.origin) return
  if (url.pathname.startsWith('/api/')) {
    event.respondWith(networkFirst(req))
    return
  }
  event.respondWith(cacheFirst(req))
})

function withCacheTag(headers) {
  const h = new Headers(headers)
  h.set('X-Pwa-Cache', 'hit')
  return h
}

async function networkFirst(req) {
  const cache = await caches.open(API_CACHE)
  try {
    const res = await fetch(req)
    // Cache successful API reads (e.g. last-known balance) for offline use.
    if (res.ok) await cache.put(req, res.clone())
    return res
  } catch (err) {
    const cached = await cache.match(req)
    if (cached) {
      return new Response(cached.body, {
        status: cached.status,
        statusText: cached.statusText,
        headers: withCacheTag(cached.headers),
      })
    }
    return new Response(JSON.stringify({ error: 'OFFLINE', message: 'No cached copy available' }), {
      status: 503,
      headers: { 'Content-Type': 'application/json', 'X-Pwa-Cache': 'miss' },
    })
  }
}

async function cacheFirst(req) {
  const cached = await caches.match(req)
  if (cached) return cached
  try {
    const res = await fetch(req)
    if (res.ok) {
      const cache = await caches.open(STATIC_CACHE)
      cache.put(req, res.clone())
    }
    return res
  } catch (err) {
    if (req.mode === 'navigate') {
      const shell = await caches.match('/index.html')
      return shell || caches.match('/offline.html')
    }
    throw err
  }
}
