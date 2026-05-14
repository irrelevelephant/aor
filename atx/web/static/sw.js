// Service worker scoped to /atx/. Lifecycle handlers for PWA installability;
// push handlers are added when web-push goes live.
self.addEventListener('install', () => self.skipWaiting());
self.addEventListener('activate', e => {
  e.waitUntil(caches.keys().then(keys => Promise.all(keys.map(k => caches.delete(k)))));
  self.clients.claim();
});
