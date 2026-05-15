// Service worker scoped to /atx/. Lifecycle handlers for PWA installability;
// push + notificationclick handlers below.

self.addEventListener('install', () => self.skipWaiting());
self.addEventListener('activate', e => {
  e.waitUntil(caches.keys().then(keys => Promise.all(keys.map(k => caches.delete(k)))));
  self.clients.claim();
});

self.addEventListener('push', (event) => {
  let payload = {};
  try { payload = event.data ? event.data.json() : {}; } catch (_) {}
  const title = payload.title || 'atx';
  const opts = {
    body: payload.body || '',
    data: payload.data || {},
    tag: payload.tag,
    icon: '/atx/static/icon-192.png',
    badge: '/atx/static/icon-192.png',
    renotify: !!payload.tag,
  };
  event.waitUntil(self.registration.showNotification(title, opts));
});

self.addEventListener('notificationclick', (event) => {
  event.notification.close();
  const url = (event.notification.data && event.notification.data.url) || '/atx/';
  event.waitUntil((async () => {
    const wins = await self.clients.matchAll({ type: 'window', includeUncontrolled: true });
    for (const win of wins) {
      const u = new URL(win.url);
      if (u.pathname.startsWith('/atx/')) {
        await win.focus();
        if ('navigate' in win) {
          try { await win.navigate(url); } catch (_) {}
        }
        return;
      }
    }
    await self.clients.openWindow(url);
  })());
});
