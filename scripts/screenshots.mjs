// README screenshots and a browser smoke test, against a demo instance (DEMO_DATA=true seeds five demo computers
// and three months of sessions). For the session shots add one real computer named "Test desktop" (VNC) and one
// named "Test files" (SSH with files on); without them those shots are skipped.
//
//   DATA_DIR=/tmp/remote-demo DEMO_DATA=true AUTH_MODE=none PORT=18307 GUACD_ADDR=127.0.0.1:4822 ./pocket-remote
//   CDP=http://127.0.0.1:19271 BASE=http://127.0.0.1:18307 node scripts/screenshots.mjs   # any Chrome with remote debugging
//   SHOTS=0 … node scripts/screenshots.mjs                                                  # checks only
// playwright-core comes from web/node_modules, or set PW_FROM to a package.json next to an install of it.
import { createRequire } from 'node:module'

const require = createRequire(process.env.PW_FROM || new URL('../web/package.json', import.meta.url))
let pw
try { pw = require('playwright') } catch { pw = require('playwright-core') }
const { chromium } = pw
const base = process.env.BASE || 'http://127.0.0.1:18307'
const shots = process.env.SHOTS !== '0'
const dir = process.env.OUT || new URL('../docs/screenshots/', import.meta.url).pathname
const browser = process.env.CDP ? await chromium.connectOverCDP(process.env.CDP) : await chromium.launch()
const errors = []

async function session({ phone = false, dark = false } = {}) {
  const ctx = await browser.newContext({
    viewport: phone ? { width: 390, height: 844 } : { width: 1440, height: 900 },
    deviceScaleFactor: phone ? 3 : 2, isMobile: phone, hasTouch: phone, colorScheme: dark ? 'dark' : 'light',
    userAgent: phone ? 'Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.0 Mobile/15E148 Safari/604.1' : undefined,
  })
  const page = await ctx.newPage()
  page.on('pageerror', e => errors.push(`pageerror: ${e}`))
  page.on('console', m => { if (m.type() === 'error' && !/Failed to load resource/.test(m.text())) errors.push(m.text()) })
  return page
}
async function go(page, path, wait = 1500) {
  await page.goto(base + path, { waitUntil: 'networkidle' }).catch(() => {})
  await page.evaluate(() => document.fonts.ready)
  await page.waitForTimeout(wait)
}
async function snap(page, name) { if (shots) await page.screenshot({ path: dir + name + '.png' }) }

const desk = await session()
await go(desk, '/', 300)
const machines = await desk.evaluate(() => fetch('/api/v1/machines').then(r => r.json()))
const id = name => machines.find(m => m.name === name)?.id
await go(desk, '/', 3500); await snap(desk, 'desktop-home')
await go(desk, '/sessions'); await snap(desk, 'desktop-sessions')
if (id('Test files')) { await go(desk, `/machines/${id('Test files')}/files`, 2500); await snap(desk, 'desktop-files') }
await go(desk, '/', 1200); await desk.locator('.page-head .btn.primary').click(); await desk.waitForTimeout(500); await snap(desk, 'desktop-add')
if (id('Test desktop')) {
  await go(desk, `/connect/${id('Test desktop')}`, 6000)
  await desk.mouse.move(700, 4); await desk.waitForTimeout(700); await snap(desk, 'desktop-session')
}
const phone = await session({ phone: true })
await go(phone, '/', 3000); await snap(phone, 'phone-home')
if (id('Test desktop')) { await go(phone, `/connect/${id('Test desktop')}`, 6000); await snap(phone, 'phone-session') }
await go(phone, '/sessions'); await go(phone, '/settings/connect')

if (errors.length) { console.error(errors.join('\n')); process.exitCode = 1 } else console.log('ok, no page errors')
await browser.close().catch(() => {})
