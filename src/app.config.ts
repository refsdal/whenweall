export const appConfig = {
  name: 'samla',
  tagline: 'Find a time everyone can make.',
  description:
    'samla is a free, fast scheduling poll: propose dates, share a link, let everyone vote, pick the winner.',
  supportEmail: 'hello@samla.app',
  repoUrl: 'https://github.com/andersro93/scheduler',
  locales: ['en', 'nb'] as const,
  defaultLocale: 'en' as const,
} as const

export type AppLocale = (typeof appConfig.locales)[number]
