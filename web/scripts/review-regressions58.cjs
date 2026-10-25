// PR58 P2 regressions. Release scenario uses the real isolated API.
// Forecast scenario explicitly mocks ONLY the two GET projection endpoints:
// /products/:id/delivery and /features/:id/forecast. Identity, products,
// features, commitments and the full React application remain real.
// This focused mock proves UI cache/polling behavior, not tracker or model correctness.
const { chromium } = require(process.env.METIS_PLAYWRIGHT_MODULE || 'playwright');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const base = process.env.METIS_ACCEPTANCE_URL || 'http://127.0.0.1:18099';
if (!['127.0.0.1', 'localhost'].includes(new URL(base).hostname)) throw Error('Use an isolated loopback stand');
const root = process.env.METIS_ACCEPTANCE_TEMP;
if (!root) throw Error('METIS_ACCEPTANCE_TEMP is required');
const tokens = JSON.parse(fs.readFileSync(path.join(root, 'tokens.json')));
const ids = JSON.parse(fs.readFileSync(path.join(root, 'ids.json')));
const output = path.join(root, 'review58');
fs.mkdirSync(output, { recursive: true });
const results = [];
const requests = [];
const errors = [];
const projectionRequests = [];
let activePage;
async function api(route, method = 'GET', body) {
  const response = await fetch(base + '/api/v1' + route, { method, headers: { Authorization: 'Bearer ' + tokens.cpo, 'Content-Type': 'application/json' }, ...(body ? { body: JSON.stringify(body) } : {}) });
  const text = await response.text();
  assert.ok(response.ok, `${method} ${route}: ${response.status} ${text}`);
  requests.push({ route, method, status: response.status });
  return text ? JSON.parse(text) : undefined;
}
async function context(browser, theme = 'light') {
  const ctx = await browser.newContext({ viewport: { width: 1440, height: 1000 }, colorScheme: theme, reducedMotion: 'reduce' });
  await ctx.addInitScript(({ token, theme }) => { sessionStorage.setItem('metis.access_token', token); localStorage.setItem('apex.theme', theme); }, { token: tokens.cpo, theme });
  return ctx;
}
async function settle(page) { await page.waitForLoadState('networkidle'); }
async function requested(promise, message) {
  let timer;
  try {
    return await Promise.race([promise, new Promise((_, reject) => { timer = setTimeout(() => reject(Error(message)), 10000); })]);
  } finally { clearTimeout(timer); }
}
async function releaseStatusRegression(browser) {
  const ctx = await context(browser);
  const page = await ctx.newPage(); activePage = page;
  page.on('pageerror', error => errors.push(error.message));
  const title = 'PR58 release regression ' + Date.now();
  const version = '58-' + Date.now();
  const release = await api(`/products/${ids.soar}/releases`, 'POST', { name: title, version, status: 'planned' });
  await page.goto(`${base}/products/${ids.soar}/roadmap`); await settle(page);
  await page.getByRole('tab', { name: 'Релизы', exact: true }).click(); await settle(page);
  const card = page.locator('div.card').filter({ has: page.getByRole('heading', { name: `${title} ${version}`, exact: true }) });
  await card.getByRole('button', { name: 'Изменить', exact: true }).first().click();
  const form = card.locator('form');
  const editedName = title + ' preserved local draft';
  await form.getByLabel('Название', { exact: true }).fill(editedName);
  assert.equal(await form.getByLabel('Статус релиза').inputValue(), 'planned');
  // Another authorized actor publishes while the local text editor remains open.
  await api(`/releases/${release.id}`, 'PUT', { name: title, version, status: 'released' });
  const notesResponse = page.waitForResponse(response => response.url().endsWith(`/releases/${release.id}/notes`) && response.request().method() === 'PUT');
  await card.getByRole('button', { name: 'Сохранить release notes', exact: true }).click();
  assert.equal((await notesResponse).status(), 200); await settle(page);
  assert.equal(await form.getByLabel('Название', { exact: true }).inputValue(), editedName, 'Server refresh preserves the unsaved text');
  assert.equal(await form.getByLabel('Статус релиза').inputValue(), 'released', 'Displayed status follows refreshed server state');
  const savedResponse = page.waitForResponse(response => response.url().endsWith(`/releases/${release.id}`) && response.request().method() === 'PUT');
  await form.getByRole('button', { name: 'Сохранить', exact: true }).click();
  const saved = await savedResponse;
  const submitted = saved.request().postDataJSON();
  assert.ok(submitted.status === undefined || submitted.status === 'released', 'Save must not submit the previous planned status');
  assert.equal(saved.status(), 200, 'Text save must succeed after the external lifecycle transition');
  await form.waitFor({ state: 'detached' });
  const current = await api(`/releases/${release.id}`);
  assert.equal(current.status, 'released'); assert.equal(current.name, editedName);
  await page.screenshot({ path: path.join(output, 'release-status-refresh.png'), fullPage: true });
  results.push({ requirement: 'RM04/RM05', scenario: 'open-editor-server-status-refresh', result: 'PASS', displayedStatus: 'released', submittedStatus: submitted.status ?? 'omitted', responseStatus: saved.status(), checks: ['local-text-preserved', 'display-follows-server', 'no-backward-status-request', 'save200', 'released-state-preserved'], backend: 'real API, no mocked responses' });
  await ctx.close(); activePage = undefined;
}
async function forecastFreshnessRegression(browser) {
  const feature = await api(`/products/${ids.soar}/features`, 'POST', { name: 'PR58 forecast projection fixture ' + Date.now(), planned_date: '2027-04-01' });
  await api(`/products/${ids.soar}/commitments`, 'POST', { kind: 'customer', counterparty: 'SYN', subject: 'PR58 forecast deadline', due_date: '2027-04-01', basis: 'Synthetic browser regression', owner: 'synthetic-owner', feature_id: feature.id });
  const ctx = await context(browser, 'dark');
  const page = await ctx.newPage(); activePage = page;
  page.on('pageerror', error => errors.push(error.message));
  const sourceA = '2026-10-25T12:00:00Z', sourceB = '2026-10-25T12:00:20Z';
  const sync = (source, stale) => ({ last_success_at: source, last_attempt_at: source, lag: stale ? 1800000000000 : 0, stale });
  let currentSync = sync(sourceA, false);
  let forecastCalls = 0, overviewCalls = 0;
  let holdOverview = false, overviewHeld = false, releaseOverview;
  let overviewRequestedResolve;
  const overviewRequested = new Promise(resolve => { overviewRequestedResolve = resolve; });
  let releaseSecondForecast;
  let secondRequestedResolve;
  const secondRequested = new Promise(resolve => { secondRequestedResolve = resolve; });
  await page.route(base + `/api/v1/products/${ids.soar}/delivery`, async route => {
    assert.equal(route.request().method(), 'GET'); overviewCalls++;
    projectionRequests.push({ endpoint: 'delivery', call: overviewCalls, sync: currentSync });
    if (holdOverview && !overviewHeld) { overviewHeld = true; overviewRequestedResolve(); await new Promise(resolve => { releaseOverview = resolve; }); }
    await route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ mappings: [{ product_id: ids.soar, feature_id: feature.id, epic_key: 'SYN-58', project: 'SYN', created_at: sourceA }], sprints: [], metrics: [], sync: currentSync, enabled: false }) });
  });
  await page.route(base + `/api/v1/features/${feature.id}/forecast`, async route => {
    assert.equal(route.request().method(), 'GET'); forecastCalls++;
    const call = forecastCalls;
    const source = currentSync.last_success_at;
    projectionRequests.push({ endpoint: 'forecast', call, source });
    if (call === 2) { secondRequestedResolve(); await new Promise(resolve => { releaseSecondForecast = resolve; }); }
    await route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ feature_id: feature.id, product_id: ids.soar, epic_key: 'SYN-58', remaining: 8, throughput: [4, 6, 5], iteration_days: 14, samples: 10000, from: '2026-10-25', calculated_at: source, p50: '2026-11-10', p85: call === 1 ? '2026-11-20' : '2026-12-20', p95: '2027-01-01', interval_low: '2026-11-01', interval_high: '2026-12-30', sync: sync(source, false) }) });
  });
  await page.clock.install({ time: new Date('2026-10-25T12:00:00Z') });
  await page.goto(base + '/delivery'); await settle(page);
  await page.getByLabel('Продукт').selectOption(ids.soar); await settle(page);
  await page.getByLabel('Фича для прогноза').selectOption(feature.id);
  const within = page.getByText('P85 укладывается в срок при допущениях модели', { exact: true });
  await within.first().waitFor(); assert.equal(await within.count(), 2, 'Initial projection compares both feature plan and active commitment');
  assert.equal(forecastCalls, 1);
  const initialOverviewCalls = overviewCalls;
  currentSync = sync(sourceA, true);
  const stalePoll = page.waitForResponse(response => response.url().endsWith(`/products/${ids.soar}/delivery`));
  await page.clock.runFor(15100); await stalePoll; await page.clock.runFor(50);
  await page.getByText('Данные устарели — сравнение требует сверки', { exact: true }).first().waitFor();
  assert.equal(await within.count(), 0, 'A stale live projection must remove deadline confidence');
  assert.equal(forecastCalls, 1, 'A stale flag change does not calculate a different model from the same snapshot');
  assert.ok(overviewCalls > initialOverviewCalls, 'Live sync changed through the existing 15-second polling');
  await page.screenshot({ path: path.join(output, 'forecast-stale-projection.png'), fullPage: true });
  currentSync = sync(sourceB, false);
  const freshPoll = page.waitForResponse(response => response.url().endsWith(`/products/${ids.soar}/delivery`));
  await page.clock.runFor(15100); await freshPoll; await page.clock.runFor(50); await requested(secondRequested, 'New source snapshot did not request a fresh forecast');
  assert.equal(forecastCalls, 2, 'A new successful source snapshot automatically requests a new forecast');
  assert.equal(await within.count(), 0, 'Previous forecast cannot confirm a new source snapshot while recalculation is pending');
  const refreshed = page.waitForResponse(response => response.url().endsWith(`/features/${feature.id}/forecast`));
  releaseSecondForecast(); await refreshed; await page.clock.runFor(50);
  await within.first().waitFor();
  assert.equal(await within.count(), 2); assert.equal(forecastCalls, 2);
  await page.getByText('20.12.2026', { exact: true }).first().waitFor();
  await page.screenshot({ path: path.join(output, 'forecast-refreshed-projection.png'), fullPage: true });
  // Re-enter Delivery with the old fresh cache while the authoritative overview
  // request is still pending. A newly mounted observer must not trust that cache.
  holdOverview = true;
  await page.getByRole('link', { name: 'Продукты', exact: true }).click(); await settle(page);
  await page.getByRole('link', { name: 'Delivery', exact: true }).click();
  await page.getByLabel('Продукт').selectOption(ids.soar);
  await requested(overviewRequested, 'Cached remount did not request the authoritative overview');
  await page.getByLabel('Фича для прогноза').selectOption(feature.id);
  await page.getByText('Проверяем актуальность данных', { exact: false }).first().waitFor();
  assert.equal(await within.count(), 0, 'Cached fresh sync cannot confirm deadlines before the current overview request completes');
  await page.screenshot({ path: path.join(output, 'forecast-cached-overview-pending.png'), fullPage: true });
  const confirmedOverview = page.waitForResponse(response => response.url().endsWith(`/products/${ids.soar}/delivery`));
  releaseOverview(); await confirmedOverview;
  await within.first().waitFor();
  assert.equal(await within.count(), 2, 'The confirmed current projection enables matching forecast comparison again');
  results.push({ requirement: 'DL04', scenario: 'live-sync-staleness-and-source-refresh', result: 'PASS', checks: ['initial-plan-and-commitment-comparison', 'clock-driven15s-poll', 'stale-removes-confidence', 'same-source-no-extra-model-call', 'new-source-auto-requery', 'pending-new-source-no-old-confidence', 'new-forecast-visible', 'cached-remount-pending-no-confidence', 'confirmed-overview-restores-comparison'], overviewCalls, forecastCalls, mockedReadEndpointsOnly: ['/products/{id}/delivery', '/features/{id}/forecast'] });
  await ctx.close(); activePage = undefined;
}
(async () => {
  const browser = await chromium.launch({ headless: true, ...(process.env.METIS_CHROMIUM_PATH ? { executablePath: process.env.METIS_CHROMIUM_PATH } : {}) });
  try {
    await releaseStatusRegression(browser);
    await forecastFreshnessRegression(browser);
    assert.deepEqual(errors, [], 'No JavaScript errors');
    console.log(JSON.stringify({ result: 'PASS', results, realApiChecks: requests.length, projectionRequests: projectionRequests.length, browserErrors: errors.length }));
  } catch (error) {
    if (activePage && !activePage.isClosed()) {
      await activePage.screenshot({ path: path.join(output, 'failure.png'), fullPage: true });
      fs.writeFileSync(path.join(output, 'failure.txt'), await activePage.locator('main').innerText());
    }
    throw error;
  } finally {
    fs.writeFileSync(path.join(output, 'matrix.json'), JSON.stringify({ results, requests, projectionRequests, errors }, null, 2) + '\n');
    await browser.close();
  }
})().catch(error => { console.error(error.stack); process.exitCode = 1; });
