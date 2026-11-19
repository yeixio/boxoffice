// Box Office's service worker. It shows the push notifications the server
// sends to this device, and opens the page one is about when it's tapped.
// It keeps no copies of pages: Box Office always needs its server.

self.addEventListener('install', () => self.skipWaiting())
self.addEventListener('activate', (event) => event.waitUntil(self.clients.claim()))

self.addEventListener('push', (event) => {
  let message = {}
  try {
    message = event.data ? event.data.json() : {}
  } catch {
    // Not from Box Office: shown as it is, below.
    message = { body: event.data ? event.data.text() : '' }
  }
  event.waitUntil(
    self.registration.showNotification(message.title || 'Box Office', {
      body: message.body || '',
      // A newer one about the same thing takes the older one's place.
      tag: message.tag || undefined,
      icon: '/icons/icon-192.png',
      badge: '/icons/badge-96.png',
      data: { url: message.url || '/' },
    }),
  )
})

self.addEventListener('notificationclick', (event) => {
  event.notification.close()
  const url = new URL((event.notification.data && event.notification.data.url) || '/', self.location.origin).href
  event.waitUntil(
    (async () => {
      // A window that's open already is brought forward and taken there.
      const open = await self.clients.matchAll({ type: 'window', includeUncontrolled: true })
      for (const client of open) {
        try {
          await client.focus()
          await client.navigate(url)
          return
        } catch {
          // Try the next one, or a new window.
        }
      }
      await self.clients.openWindow(url)
    })(),
  )
})
