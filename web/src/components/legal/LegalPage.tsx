import type { ReactNode } from 'react'

export interface LegalSection {
  title: string
  body: string
}

/**
 * Shared shell for the prose-only legal pages (/privacy, /terms). Section bodies are single
 * i18n strings with `\n\n` between paragraphs — one message key per section keeps the
 * paraglide catalogues manageable for text this long.
 */
export function LegalPage({
  title,
  updated,
  intro,
  sections,
  children,
}: {
  title: string
  updated: string
  intro: string
  sections: LegalSection[]
  children?: ReactNode
}) {
  return (
    <main className="mx-auto w-full max-w-2xl px-5 py-12 sm:px-8">
      <h1 className="display text-3xl sm:text-4xl">{title}</h1>
      <p className="mt-2 text-sm text-muted-foreground">{updated}</p>
      <p className="mt-6 leading-relaxed text-muted-foreground">{intro}</p>

      <div className="mt-10 flex flex-col gap-8">
        {sections.map((section) => (
          <section key={section.title}>
            <h2 className="text-lg font-semibold">{section.title}</h2>
            {section.body.split('\n\n').map((paragraph, i) => (
              <p key={i} className="mt-2 leading-relaxed text-muted-foreground">
                {paragraph}
              </p>
            ))}
          </section>
        ))}
      </div>
      {children}
    </main>
  )
}
