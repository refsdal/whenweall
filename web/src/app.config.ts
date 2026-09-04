export const appConfig = {
  name: 'whenweall',
  tagline: 'Find a time everyone can make.',
  description:
    'whenweall is a free, fast scheduling poll: propose dates, share a link, let everyone vote, pick the winner.',
  /**
   * The canonical public origin, no trailing slash. Used for the absolute URLs a link preview
   * needs — `og:url` and `og:image` in `web/index.html` are asserted against this value by
   * `src/__tests__/index-html.test.ts`, because a static document cannot import this file.
   * Deliberately a constant rather than a build-time env var: one image is deployed to one
   * public domain, and a preview card pointing at production is the right card everywhere.
   */
  url: 'https://whenweall.com',
  supportEmail: 'hello@whenweall.com',
  repoUrl: 'https://github.com/refsdal/whenweall',
  locales: ['en', 'nb'] as const,
  defaultLocale: 'en' as const,
  /**
   * Brand accents that have to exist outside CSS (browser UI, emails, share cards).
   * The authoritative token values live in `src/styles.css`; these are the sRGB fallbacks.
   */
  brand: {
    primary: '#f3562e',
    paperLight: '#fbfaf7',
    paperDark: '#130e0b',
  },
} as const

export type AppLocale = (typeof appConfig.locales)[number]
