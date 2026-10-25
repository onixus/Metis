// DA04/AD02 UI regression: isolated /me and win/loss response fixtures, no API mutations.
const { chromium } = require(process.env.METIS_PLAYWRIGHT_MODULE || 'playwright');
const assert = require('node:assert/strict');
const base = process.env.METIS_ACCEPTANCE_URL || 'http://127.0.0.1:18069';
if (!['127.0.0.1', 'localhost'].includes(new URL(base).hostname)) throw Error('Use an isolated loopback stand');
(async () => {
  const browser = await chromium.launch({ headless: true, ...(process.env.METIS_CHROMIUM_PATH ? { executablePath: process.env.METIS_CHROMIUM_PATH } : {}) });
  const cases = [
    { role: 'cpo', scope: 'strategic', allowed: true },
    { role: 'admin', scope: 'private', allowed: true },
    { role: 'marketing', scope: 'strategic', allowed: true },
    { role: 'service', scope: 'private', allowed: true },
    { role: 'pm', scope: 'private', allowed: true },
    { role: 'pm', scope: 'strategic', allowed: false },
    { role: 'finance', scope: 'private', allowed: false },
    { role: 'presale', scope: 'strategic', allowed: false },
    { role: 'marketing', scope: 'none', allowed: false },
  ];
  try {
    for (const { role, scope, allowed } of cases) {
      const context = await browser.newContext();
      await context.addInitScript(() => {
        window.__METIS_CONFIG__ = { authMode: 'token' };
        sessionStorage.setItem('metis.access_token', 'synthetic-ui-fixture');
      });
      const page = await context.newPage();
      const errors = [];
      const unexpected = [];
      await page.route('**/api/v1/**', route => {
        const routePath = new URL(route.request().url()).pathname;
        if (['/api/v1/products', '/api/v1/hubs', '/api/v1/links'].includes(routePath)) return route.fulfill({ json: [] });
        unexpected.push(routePath);
        return route.abort();
      });
      page.on('pageerror', error => errors.push(error.message));
      let winLossRequests = 0;
      let pnlRequests = 0;
      page.on('request', request => { if (request.url().includes('/economics/pnl/')) pnlRequests++; });
      await page.route('**/api/v1/me', route => route.fulfill({ json: { subject: 'synthetic-da04', roles: [role], all_products: scope, products: {}, finance: 'none', audience: 'internal' } }));
      await page.route('**/api/v1/marketing/win-loss', route => {
        winLossRequests++;
        return route.fulfill({ json: { won: 7, lost: 2, by_reason: [], by_segment: [] } });
      });
      await page.goto(`${base}/portfolio`);
      await page.waitForLoadState('networkidle');
      assert.equal(await page.getByRole('heading', { name: 'Win/loss по причинам', exact: true }).count(), allowed ? 1 : 0, `DA04 ${role}/${scope} report visibility without finance`);
      assert.equal(winLossRequests, allowed ? 1 : 0, `AD02 ${role}/${scope} marketing query gate`);
      assert.equal(pnlRequests, 0, `AD02 ${role}/${scope} finance:none never queries P&L`);
      assert.deepEqual(errors, []);
      assert.deepEqual(unexpected, [], 'UI fixture must not make unhandled API requests');
      await context.close();
    }
    console.log('DA04/AD02 portfolio finance-independent win/loss browser regression passed');
  } finally { await browser.close(); }
})().catch(error => { console.error(error); process.exitCode = 1; });
