// EC07/EC09/EC11: real PostgreSQL API and browser, no response mocks.
const { chromium } = require(process.env.METIS_PLAYWRIGHT_MODULE || 'playwright');
const fs = require('node:fs');
const path = require('node:path');
const assert = require('node:assert/strict');
const base = process.env.METIS_ACCEPTANCE_URL;
assert.ok(['127.0.0.1', 'localhost'].includes(new URL(base).hostname));
const root = process.env.METIS_ACCEPTANCE_TEMP;
const token = JSON.parse(fs.readFileSync(path.join(root, 'tokens.json'))).finance;
async function api(method, route, body, expected = 200) {
  const response = await fetch(base + '/api/v1' + route, { method, headers: { Authorization: `Bearer ${token}`, 'Content-Type': 'application/json' }, ...(body ? { body: JSON.stringify(body) } : {}) });
  const text = await response.text();
  assert.equal(response.status, expected, `${method} ${route}: ${text.slice(0, 1000)}`);
  return text ? JSON.parse(text) : undefined;
}
(async () => {
  const products = await api('GET', '/products');
  const owner = products.find(p => p.key === 'edr').id;
  const browser = await chromium.launch({ headless: true, ...(process.env.METIS_CHROMIUM_PATH ? { executablePath: process.env.METIS_CHROMIUM_PATH } : {}) });
  const results = [];
  try {
    for (const [index, theme] of ['dark', 'light'].entries()) {
      const period = (process.env.METIS_UNIFICATION_PERIODS || '2026-10,2026-11').split(',')[index];
      const route = `/products/${owner}/economics/${period}`;
      const csv = `product_id,period,category,amount_minor,currency,team_id,headcount\n${owner},${period},revenue,50000,RUB,,\n${owner},${period},payroll,10000,RUB,ui_team,8\n`;
      const book = await api('POST', route + '/import', { filename: 'synthetic-ui.csv', content_base64: Buffer.from(csv).toString('base64'), template: {}, expected_version: 0 }, 201);
      await api('PUT', route + '/configuration', { expected_version: book.version, fields: [{ key: 'ui_profit', name: 'Прибыль для объединения', type: 'money', source: 'calculated', formula: 'revenue-payroll' }], rows: [] });
      const key = `ui_history_${theme}_${Date.now()}`;
      await api('POST', '/economics/fields', { key, name: 'Synthetic independent history', type: 'number', source: 'manual', effective_from: `${period}-01` });
      await api('POST', `/economics/fields/${key}/manual-value`, { product_id: owner, period, value: '17.25' }, 201);
      const ctx = await browser.newContext({ viewport: { width: 1440, height: 1000 }, colorScheme: theme, acceptDownloads: true });
      await ctx.addInitScript(({ token, theme }) => { sessionStorage.setItem('metis.access_token', token); localStorage.setItem('apex.theme', theme); }, { token, theme });
      const page = await ctx.newPage();
      const errors = []; page.on('pageerror', error => errors.push(error.message));
      await page.goto(base + '/economics');
      await page.getByLabel('Период', { exact: true }).fill(period);
      await page.getByLabel('Владелец финансовой книги').selectOption(owner);
      await page.getByRole('tab', { name: 'Объединение истории', exact: true }).click();
      const previewWait = page.waitForResponse(r => r.url().endsWith('/unification/preview') && r.request().method() === 'POST');
      await page.getByRole('button', { name: 'Проверить объединение', exact: true }).click();
      const response = await previewWait; assert.equal(response.status(), 200);
      const preview = await response.json();
      assert.ok(preview.excluded_model_fields.includes(key));
      await page.getByText('Сверка прибыли с хабом', { exact: true }).waitFor();
      const apply = page.getByRole('button', { name: 'Объединить историю', exact: true });
      assert.equal(await apply.isDisabled(), true);
      const downloadWait = page.waitForEvent('download');
      await page.getByRole('button', { name: 'Скачать точный план JSON', exact: true }).click();
      const download = await downloadWait;
      const downloadPath = path.join(root, `unification-plan-${theme}.json`);
      await download.saveAs(downloadPath);
      const exactPlan = JSON.parse(fs.readFileSync(downloadPath));
      assert.equal(exactPlan.digest, preview.digest);
      await page.getByRole('checkbox', { name: /Подтверждаю выбор книги единственным/ }).check();
      const applyWait = page.waitForResponse(r => r.url().endsWith('/unification/apply') && r.request().method() === 'POST');
      await apply.click();
      const applied = await applyWait; assert.equal(applied.status(), 200);
      assert.equal((await applied.json()).applied, true);
      await page.getByText('История объединена. Текущие расчёты используют выбранную книгу.', { exact: true }).waitFor();
      const pnl = await api('GET', `/economics/pnl/products/${owner}?period=${period}`);
      assert.equal(pnl.loaded_profit.amount, 40000);
      const archiveWait = page.waitForEvent('download');
      await page.getByRole('button', { name: 'Скачать архив объединения', exact: true }).click();
      const archiveDownload = await archiveWait;
      const archivePath = path.join(root, `unification-archive-${theme}.json`);
      await archiveDownload.saveAs(archivePath);
      const archive = JSON.parse(fs.readFileSync(archivePath));
      assert.equal(archive.book_versions.length, 2);
      assert.equal(archive.model_versions.length, 1);
      for (const width of [1440, 390]) {
        await page.setViewportSize({ width, height: 1000 });
        assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth + 1), false, 'unification page overflows viewport');
        await page.getByRole('status').scrollIntoViewIfNeeded();
        await page.evaluate(async () => { await document.fonts.ready; await new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))); });
        await page.screenshot({ path: path.join(root, `unification-${theme}-${width}.png`), fullPage: width > 600 });
      }
      assert.deepEqual(errors, []);
      results.push({ theme, period, preview: 'PASS', explicitConfirmation: 'PASS', apply: 'PASS', exactPlanDownload: 'PASS', archiveDownload: 'PASS', legacyPnL: 'PASS', mobile: 'PASS' });
      await ctx.close();
    }
    fs.writeFileSync(path.join(root, 'unification-browser.json'), JSON.stringify({ testedAt: new Date().toISOString(), results }, null, 2));
  } finally { await browser.close(); }
})().catch(error => { process.stderr.write(error.stack + '\n'); process.exitCode = 1; });
