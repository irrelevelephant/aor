const CACHE = 'ata-v1';
const SHELL = [
  '/static/style.css',
  '/static/app.js',
  '/static/icon.svg',
];

self.addEventListener('install', e => {
  e.waitUntil(
    caches.open(CACHE)
      .then(c => c.addAll(SHELL))
      .then(() => self.skipWaiting())
  );
});

self.addEventListener('activate', e => {
  e.waitUntil(
    caches.keys().then(keys =>
      Promise.all(keys.filter(k => k !== CACHE).map(k => caches.delete(k)))
    )
  );
  self.clients.claim();
});

self.addEventListener('fetch', e => {
  const { request } = e;
  if (request.method !== 'GET') return;
  // Serve cached static assets; let everything else (navigations, SSE, API) hit the network.
  const url = new URL(request.url);
  if (url.pathname.startsWith('/static/')) {
    e.respondWith(caches.match(request).then(r => r || fetch(request)));
  }
});
