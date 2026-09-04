import { readFileSync, existsSync } from 'node:fs'
import path from 'node:path'
import { describe, expect, it } from 'vitest'
import { appConfig } from '#/app.config'

/**
 * `web/index.html` is the whole document every visitor and every crawler receives: this is a pure
 * SPA, so the per-route `head` blocks in `src/routes/**` are injected by React after load and a
 * link-preview bot — Slackbot, Twitterbot, WhatsApp, Facebook, Discord, none of which run
 * JavaScript — never sees them. Whatever unfurls in a group chat comes from THIS file, so it is
 * asserted here rather than left to a manual check.
 */
const webRoot = path.resolve(import.meta.dirname, '../..')
const html = readFileSync(path.join(webRoot, 'index.html'), 'utf8')

/** The `content` of a `<meta>` matched by property/name, or null. Attribute order-insensitive. */
function meta(key: string): string | null {
  const pattern = new RegExp(
    `<meta[^>]*(?:property|name)=["']${key}["'][^>]*content=["']([^"']*)["']|` +
      `<meta[^>]*content=["']([^"']*)["'][^>]*(?:property|name)=["']${key}["']`,
    'i',
  )
  const match = html.match(pattern)
  if (!match) return null
  return match[1] ?? match[2] ?? null
}

/** The `href` of a `<link rel="...">`, or null. */
function linkHref(rel: string): string | null {
  const match = html.match(new RegExp(`<link[^>]*rel=["']${rel}["'][^>]*href=["']([^"']*)["']`, 'i'))
  return match ? (match[1] ?? null) : null
}

/** A `/`-rooted URL in the document resolves against `web/public` at build time. */
function publicAsset(url: string): string {
  return path.join(webRoot, 'public', url.replace(/^\//, ''))
}

describe('index.html link preview metadata', () => {
  it('carries the Open Graph tags an unfurl needs', () => {
    expect(meta('og:site_name')).toBe(appConfig.name)
    expect(meta('og:title')).toBeTruthy()
    expect(meta('og:description')).toBe(appConfig.description)
    expect(meta('og:type')).toBe('website')
    expect(meta('og:url')).toBe(appConfig.url)
  })

  it('points og:image at an absolute URL for an image that exists', () => {
    const image = meta('og:image')
    // Facebook and several others refuse a relative og:image outright.
    expect(image).toMatch(/^https:\/\//)
    expect(image?.startsWith(appConfig.url)).toBe(true)
    expect(existsSync(publicAsset(image!.slice(appConfig.url.length)))).toBe(true)
    // Consumers that read the dimensions skip a fetch; og.png is 1200x630.
    expect(meta('og:image:width')).toBe('1200')
    expect(meta('og:image:height')).toBe('630')
    expect(meta('og:image:alt')).toBeTruthy()
  })

  it('carries a large-image Twitter card', () => {
    expect(meta('twitter:card')).toBe('summary_large_image')
    expect(meta('twitter:title')).toBe(meta('og:title'))
    expect(meta('twitter:description')).toBe(meta('og:description'))
    expect(meta('twitter:image')).toBe(meta('og:image'))
  })

  it('links an apple-touch-icon that exists', () => {
    const icon = linkHref('apple-touch-icon')
    expect(icon).toBeTruthy()
    expect(existsSync(publicAsset(icon!))).toBe(true)
  })

  it('links a web manifest naming icons that exist', () => {
    const href = linkHref('manifest')
    expect(href).toBeTruthy()

    const manifestPath = publicAsset(href!)
    expect(existsSync(manifestPath)).toBe(true)

    const manifest = JSON.parse(readFileSync(manifestPath, 'utf8'))
    expect(manifest.name).toBe(appConfig.name)
    expect(manifest.icons.length).toBeGreaterThan(0)
    for (const icon of manifest.icons) {
      expect(existsSync(publicAsset(icon.src)), `${icon.src} is missing`).toBe(true)
    }
  })
})
