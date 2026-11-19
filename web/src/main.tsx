import { lazy, StrictMode, Suspense } from 'react'
import { createRoot } from 'react-dom/client'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { BrowserRouter } from 'react-router-dom'
import '@fontsource-variable/inter'
import '@fontsource-variable/space-grotesk'
import './index.css'
import { ApiError } from './api'
import { ready } from './i18n'
import { PageBoundary } from './Broken'

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

// The request site and Box Office's own pages are fetched apart, so people
// asking for titles never download the admin's pages.
const Site = portal
  ? lazy(() => import('./portal/Portal').then((m) => ({ default: m.Portal })))
  : lazy(() => import('./App').then((m) => ({ default: m.App })))

// The page starts in the language last shown here (or the browser's), once
// it's loaded, so it never flashes English first.
void ready.then(() =>
  createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <QueryClientProvider client={queries}>
      <BrowserRouter basename={portalPath && !portalHost ? '/request' : undefined}>
        {/* The last resort, for a problem outside any one page (the menu, sign-in). */}
        <PageBoundary at="">
          <Suspense fallback={null}>
            <Site />
          </Suspense>
        </PageBoundary>
      </BrowserRouter>
    </QueryClientProvider>
  </StrictMode>,
  ),
)
