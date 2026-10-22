// DS56: populated financial aggregates and live theme synchronization on the real stand.
const { chromium } = require(process.env.METIS_PLAYWRIGHT_MODULE || 'playwright');
const fs = require('node:fs');
const path = require('node:path');
const assert = require('node:assert/strict');
const base = process.env.METIS_ACCEPTANCE_URL;
assert.ok(['127.0.0.1', 'localhost'].includes(new URL(base).hostname));
const root = process.env.METIS_ACCEPTANCE_TEMP;
const output = path.join(root, 'final');
const tokens = JSON.parse(fs.readFileSync(path.join(root, 'tokens.json')));
(async () => {
  const browser = await chromium.launch({ headless: true, ...(process.env.METIS_CHROMIUM_PATH ? { executablePath: process.env.METIS_CHROMIUM_PATH } : {}) });
  const results = [];
  try {
    for (const role of Object.keys(tokens)) {
      for (const theme of ['dark', 'light']) {
        const context = await browser.newContext({ viewport: { width: 1440, height: 1000 }, colorScheme: theme });
        await context.addInitScript(({ token, theme }) => {
          sessionStorage.setItem('metis.access_token', token);
          localStorage.setItem('apex.theme', theme);
        }, { token: tokens[role], theme });
        const page = await context.newPage();
        await page.goto(base + '/portfolio');
        await page.getByLabel('Период', { exact: true }).fill('2026-09');
        await page.waitForLoadState('networkidle');
        await page.waitForFunction(() => ![...document.querySelectorAll('.muted')].some(e => /^Загрузка/.test(e.textContent.trim())), null, { timeout: 12000 });
        const response = await context.request.get(base + '/api/v1/economics/pnl/portfolio?period=2026-09', { headers: { Authorization: 'Bearer ' + tokens[role] } });
        const text = await page.locator('main').innerText();
        const allowed = ['admin', 'cpo', 'finance', 'finance-aggregates'].includes(role);
        assert.equal(response.status(), allowed ? 200 : 403, role + ' aggregate scope');
        assert.equal(await page.locator('html').getAttribute('data-theme'), theme);
        if (allowed) {
          const data = await response.json();
          assert.ok(data.products.length > 0, 'Seeded P&L must contain products');
          assert.ok(text.includes('EDR'));
        }
        assert.equal(await page.locator('button').filter({ hasText: /^Сохранить|Загрузить CSV/ }).count(), 0);
        await page.screenshot({ path: path.join(output, `${role}-portfolio-populated-${theme}.png`), fullPage: true });
        results.push({ role, theme, viewport: 'desktop', aggregateStatus: response.status(), populated: allowed });
        await page.setViewportSize({ width: 390, height: 844 });
        assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth + 1), false, role + ' populated mobile portfolio');
        await page.screenshot({ path: path.join(output, `${role}-portfolio-populated-mobile-${theme}.png`), fullPage: true });
        results.push({ role, theme, viewport: 'mobile', aggregateStatus: response.status(), populated: allowed });
        await context.close();
      }
    }
    const finance = await browser.newContext();
    await finance.addInitScript(token => sessionStorage.setItem('metis.access_token', token), tokens.finance);
    const tabsPage = await finance.newPage();
    await tabsPage.goto(base + '/economics');
    const tabs = tabsPage.getByRole('tab');
    await tabs.first().waitFor();
    assert.equal(await tabsPage.locator('[role=tab]:disabled').count(), 2);
    await tabs.first().focus();
    await tabsPage.keyboard.press('ArrowRight');
    assert.equal(await tabs.nth(1).getAttribute('aria-selected'), 'true');
    await tabsPage.keyboard.press('ArrowRight');
    assert.equal(await tabs.first().getAttribute('aria-selected'), 'true');
    await tabsPage.keyboard.press('ArrowLeft');
    assert.equal(await tabs.nth(1).getAttribute('aria-selected'), 'true');
    await tabsPage.keyboard.press('Home');
    await tabsPage.keyboard.press('End');
    assert.equal(await tabs.nth(1).getAttribute('aria-selected'), 'true');
    assert.equal(await tabs.nth(1).evaluate(e => getComputedStyle(e).outlineWidth), '2px');
    await finance.close();
    const context = await browser.newContext({ colorScheme: 'dark' });
    const a = await context.newPage();
    const b = await context.newPage();
    await a.goto(base + '/callback');
    await a.waitForURL(base + '/login');
    await b.goto(base + '/login');
    await a.getByLabel('Тема оформления').selectOption('light');
    await b.waitForFunction(() => document.documentElement.dataset.theme === 'light');
    assert.equal(await b.getByLabel('Тема оформления').inputValue(), 'light');
    await a.getByLabel('Тема оформления').selectOption('system');
    await a.emulateMedia({ colorScheme: 'light' });
    await a.waitForFunction(() => document.documentElement.dataset.theme === 'light');
    assert.equal(await a.getByLabel('Тема оформления').inputValue(), 'system');
    await a.evaluate(() => localStorage.clear());
    await b.waitForFunction(() => document.documentElement.dataset.theme === 'dark');
    assert.equal(await b.getByLabel('Тема оформления').inputValue(), 'dark');
    await context.close();
    const denied = await browser.newContext();
    await denied.addInitScript(() => Object.defineProperty(window, 'localStorage', { get() { throw new Error('Synthetic storage denial'); } }));
    const page = await denied.newPage();
    const errors = [];
    page.on('pageerror', error => errors.push(error.message));
    await page.goto(base + '/login');
    await page.getByLabel('Тема оформления').selectOption('light');
    assert.equal(await page.locator('html').getAttribute('data-theme'), 'light');
    assert.deepEqual(errors, []);
    await denied.close();
    fs.writeFileSync(path.join(output, 'session-matrix.json'), JSON.stringify({ testedAt: new Date().toISOString(), results, arrowKeysAndDisabledTabs: 'PASS', crossTab: 'PASS', systemChange: 'PASS', clearStorage: 'PASS', deniedStorage: 'PASS', tokenCallbackRedirect: 'PASS' }, null, 2));
    console.log('DS56 supplemental:', results.length, 'populated portfolio role/theme cases, cross-tab/system/clear/denied-storage PASS');
  } finally { await browser.close(); }
})().catch(error => { console.error(error.message); process.exitCode = 1; });
