import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { BrowserRouter } from 'react-router-dom'
import '@fontsource-variable/inter'
import '@fontsource-variable/space-grotesk'
import './index.css'
import { App } from './App'
import { Portal } from './portal/Portal'
import { ApiError } from './api'
import { ready } from './i18n'

const queries = new QueryClient({
  defaultOptions: {
    queries: { staleTime: 15_000, retry: (n, err) => !(err instanceof ApiError && err.status < 500) && n < 2 },
  },
})

// The request site is the whole site on its own host name (the server marks
// the page), and lives under /request everywhere else.
const portalHost = !!document.querySelector('meta[name="boxoffice-portal"]')
const portalPath = location.pathname === '/request' || location.pathname.startsWith('/request/')
const portal = portalHost || portalPath

// The page starts in the language last shown here (or the browser's), once
// it's loaded, so it never flashes English first.
void ready.then(() =>
  createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <QueryClientProvider client={queries}>
      <BrowserRouter basename={portalPath && !portalHost ? '/request' : undefined}>{portal ? <Portal /> : <App />}</BrowserRouter>
    </QueryClientProvider>
  </StrictMode>,
  ),
)
