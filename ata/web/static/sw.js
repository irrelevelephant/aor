// No-op service worker — required for PWA installability but we don't want caching.
// Clear any previously cached data from older versions.
self.addEventListener('activate', e => {
  e.waitUntil(caches.keys().then(keys => Promise.all(keys.map(k => caches.delete(k)))));
  self.clients.claim();
});

self.addEventListener('install', () => self.skipWaiting());
