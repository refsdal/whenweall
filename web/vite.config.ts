import { defineConfig } from 'vite'
import viteReact from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'
import { paraglideVitePlugin } from '@inlang/paraglide-js'

const config = defineConfig({
  resolve: { tsconfigPaths: true },
  plugins: [
    paraglideVitePlugin({
      project: './project.inlang',
      outdir: './src/paraglide',
      strategy: ['cookie', 'preferredLanguage', 'baseLocale'],
      cookieName: 'whenweall_locale',
    }),
    tailwindcss(),
    viteReact(),
  ],
  build: { outDir: 'dist' },
  server: {
    proxy: {
      '/api': {
        target: 'http://localhost:3000',
        ws: true,
        changeOrigin: true,
        /**
         * `changeOrigin: true` only rewrites the proxied request's `Host` header to match
         * `target` — it does NOT touch the `Origin` header. The browser still sends
         * `Origin: http://localhost:5173` (this dev server's own origin), and
         * `internal/httpserver.CheckOrigin` (internal/httpserver/origin.go) rejects any mutating
         * request (POST/PUT/PATCH/DELETE) whose `Origin` header doesn't exactly match `APP_URL`
         * (`http://localhost:3000` in dev) with 403 `bad_origin` — verified by reading that
         * middleware, which compares the raw header value, not the TCP peer or the `Host` header.
         * Every login, signup, poll vote, and booking mutation goes through this proxy in dev, so
         * without the rewrite below those all 403 immediately.
         *
         * Rewriting (rather than deleting) the header keeps the semantics closest to a real
         * same-origin request — CheckOrigin also passes a request with NO Origin header at all
         * (curl, older clients), so simply stripping it would work too, but would stop exercising
         * the actual match-check path in dev.
         */
        configure: (proxy) => {
          proxy.on('proxyReq', (proxyReq) => {
            proxyReq.setHeader('Origin', 'http://localhost:3000')
          })
        },
      },
    },
  },
})

export default config
