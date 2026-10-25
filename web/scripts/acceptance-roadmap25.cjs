// RM-04/05/06 browser acceptance against the real isolated memory/HMAC API.
// Operator provides Playwright, Chromium and a fresh visual-stand-ds56.py instance.
const { chromium } = require(process.env.METIS_PLAYWRIGHT_MODULE || 'playwright');
const fs = require('node:fs');
const path = require('node:path');
const assert = require('node:assert/strict');
const base = process.env.METIS_ACCEPTANCE_URL || 'http://127.0.0.1:18069';
if (!['127.0.0.1', 'localhost'].includes(new URL(base).hostname)) throw Error('Use an isolated loopback stand');
const root = process.env.METIS_ACCEPTANCE_TEMP;
if (!root) throw Error('METIS_ACCEPTANCE_TEMP is required');
const tokens = JSON.parse(fs.readFileSync(path.join(root, 'tokens.json')));
const ids = JSON.parse(fs.readFileSync(path.join(root, 'ids.json')));
const output = path.join(root, 'roadmap25');
fs.mkdirSync(output, { recursive: true });
const results = [];
const requests = [];
const errors = [];
let activePage;
let sequence = Date.now();
async function probe(route, token, method = 'GET', body, expected = 200) {
  const response = await fetch(base + '/api/v1' + route, { method, headers: { Authorization: 'Bearer ' + token, 'Content-Type': 'application/json' }, ...(body ? { body: JSON.stringify(body) } : {}) });
  assert.equal(response.status, expected, `${method} ${route}: ${await response.clone().text()}`);
  requests.push({ method, route, status: response.status });
  return response.status === 204 ? undefined : response.json();
}
async function settle(page) { await page.waitForLoadState('networkidle'); }
async function mutate(page, button) {
  const [response] = await Promise.all([page.waitForResponse(response => response.url().startsWith(base + '/api/v1/') && ['POST', 'PUT', 'PATCH'].includes(response.request().method())), button.click()]);
  assert.ok(response.status() >= 200 && response.status() < 300, `UI mutation ${response.status()}: ${await response.text()}`);
  await settle(page);
}
async function count(locator, expected, message) {
  await locator.first().waitFor({ state: expected ? 'visible' : 'hidden' });
  assert.equal(await locator.count(), expected, message);
}
async function screenshot(page, label, theme) {
  assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth + 1), false, label + ' horizontal overflow');
  await page.screenshot({ path: path.join(output, `${label}-${theme}.png`), fullPage: true });
}
(async () => {
  const browser = await chromium.launch({ headless: true, ...(process.env.METIS_CHROMIUM_PATH ? { executablePath: process.env.METIS_CHROMIUM_PATH } : {}) });
  try {
    for (const theme of ['dark', 'light']) {
      const context = await browser.newContext({ viewport: { width: 1440, height: 1000 }, colorScheme: theme, reducedMotion: 'reduce' });
      await context.addInitScript(({ token, theme }) => { sessionStorage.setItem('metis.access_token', token); localStorage.setItem('apex.theme', theme); }, { token: tokens.cpo, theme });
      const page = await context.newPage(); activePage = page;
      page.on('pageerror', error => errors.push(error.message));
      const title = `RM06 launch ${theme} ${++sequence}`;
      const launchDate = '2027-03-15';
      await page.goto(`${base}/products/${ids.soar}/roadmap`); await settle(page);
      await page.getByRole('button', { name: 'Добавить в roadmap', exact: true }).click();
      let form = page.locator('form').filter({ has: page.getByRole('heading', { name: 'Добавить в roadmap', exact: true }) });
      await form.getByLabel('Название', { exact: true }).fill(title);
      await form.getByLabel('Уровень запуска').selectOption('tier1');
      await form.getByRole('button', { name: 'Сохранить', exact: true }).click();
      await count(form, 1, 'Tier requires date; browser prevents submit');
      await form.getByLabel('Дата запуска', { exact: true }).fill(launchDate);
      await mutate(page, form.getByRole('button', { name: 'Сохранить', exact: true }));
      await form.waitFor({ state: 'detached' });
      await count(form, 0, 'Create completed');
      let item = (await probe(`/products/${ids.soar}/roadmap/items`, tokens.cpo)).find(item => item.title === title);
      assert.ok(item, 'Launch created through UI'); assert.equal(item.launch_tier, 'tier1'); assert.equal(item.launch_date, launchDate);
      await page.getByRole('tab', { name: 'Календарь запусков', exact: true }).click(); await settle(page);
      await count(page.getByRole('cell', { name: title, exact: true }), 1);
      await page.getByLabel('Уровень запуска').selectOption('tier2');
      await count(page.getByRole('cell', { name: title, exact: true }), 0, 'Tier filter');
      await page.getByRole('button', { name: 'Сбросить фильтры', exact: true }).click();
      await page.getByLabel('Запуски с', { exact: true }).fill('2027-04-01');
      await count(page.getByRole('cell', { name: title, exact: true }), 0, 'Period filter');
      await page.getByRole('button', { name: 'Сбросить фильтры', exact: true }).click();
      await screenshot(page, 'launch-calendar', theme);
      await page.setViewportSize({ width: 390, height: 844 }); await screenshot(page, 'launch-calendar-mobile', theme); await page.setViewportSize({ width: 1440, height: 1000 });
      await page.getByRole('tab', { name: 'Now / Next / Later', exact: true }).click(); await settle(page);
      let row = page.locator('li.item').filter({ has: page.getByText(title, { exact: true }) });
      await row.getByRole('button', { name: 'Изменить', exact: true }).click();
      form = row.locator('form');
      assert.equal(await form.getByLabel('Уровень запуска').inputValue(), 'tier1');
      assert.equal(await form.getByLabel('Дата запуска', { exact: true }).inputValue(), launchDate);
      await form.getByLabel('Название', { exact: true }).fill(title + ' edited');
      await mutate(page, form.getByRole('button', { name: 'Сохранить', exact: true }));
      await form.waitFor({ state: 'detached' });
      item = (await probe(`/products/${ids.soar}/roadmap/items`, tokens.cpo)).find(value => value.id === item.id);
      assert.equal(item.launch_tier, 'tier1', 'Unrelated edit preserves tier'); assert.equal(item.launch_date, launchDate, 'Unrelated edit preserves date');
      await page.getByRole('tab', { name: 'Календарь запусков', exact: true }).click(); await settle(page);
      await count(page.getByRole('cell', { name: title + ' edited', exact: true }), 1, 'Calendar cache refreshed after edit');
      await count(page.getByRole('cell', { name: title, exact: true }), 0);
      const safe = await probe(`/products/${ids.soar}/roadmap/launch-calendar`, tokens.presale);
      assert.equal(safe.entries.some(entry => entry.item_id === item.id), false, 'API excludes internal launch');
      await page.getByRole('tab', { name: 'Now / Next / Later', exact: true }).click(); await settle(page);
      row = page.locator('li.item').filter({ has: page.getByText(title + ' edited', { exact: true }) });
      await row.getByRole('button', { name: 'Изменить', exact: true }).click();
      form = row.locator('form');
      await form.getByLabel('Уровень запуска').selectOption('');
      await mutate(page, form.getByRole('button', { name: 'Сохранить', exact: true }));
      await form.waitFor({ state: 'detached' });
      const calendar = await probe(`/products/${ids.soar}/roadmap/launch-calendar`, tokens.cpo);
      assert.equal(calendar.entries.some(entry => entry.item_id === item.id), false, 'Removing tier clears launch');
      results.push({ requirement: 'RM06', theme, result: 'PASS', checks: ['create', 'required-date', 'tier-filter', 'period-filter', 'preserve-on-edit', 'cache-refresh', 'clear', 'audience-filter', 'desktop-mobile'] });

      // RM-04/05: lifecycle, EOL removal, and direct readiness bypass denial.
      const release = await probe(`/products/${ids.soar}/releases`, tokens.cpo, 'POST', { name: `RM05 release ${theme}`, version: `25.${sequence}`, eol: '2028-01-01' }, 201);
      await probe(`/products/${ids.soar}/releases`, tokens.cpo, 'POST', { name: 'RM05 bypass', version: `bypass-${sequence}`, status: 'ready_for_certification' }, 409);
      await page.getByRole('tab', { name: 'Релизы', exact: true }).click(); await settle(page);
      const card = page.locator('div.card').filter({ has: page.getByRole('heading', { name: `RM05 release ${theme} 25.${sequence}`, exact: true }) });
      await card.getByLabel('EOL', { exact: true }).fill('');
      await mutate(page, card.getByRole('button', { name: 'Снять дату EOL', exact: true }));
      assert.ok(!(await probe(`/releases/${release.id}`, tokens.cpo)).eol, 'EOL cleared through UI');
      await card.getByRole('button', { name: 'Изменить', exact: true }).first().click();
      form = card.locator('form');
      await form.getByLabel('Статус релиза').selectOption('released');
      await mutate(page, form.getByRole('button', { name: 'Сохранить', exact: true }));
      await form.waitFor({ state: 'detached' });
      assert.equal((await probe(`/releases/${release.id}`, tokens.cpo)).status, 'released');
      await card.getByRole('button', { name: 'Изменить', exact: true }).first().click();
      form = card.locator('form');
      assert.equal(await form.getByLabel('Ветка').isDisabled(), true, 'Released branch locked');
      await form.getByLabel('Статус релиза').selectOption('eol');
      await mutate(page, form.getByRole('button', { name: 'Сохранить', exact: true }));
      await form.waitFor({ state: 'detached' });
      assert.equal((await probe(`/releases/${release.id}`, tokens.cpo)).status, 'eol');
      await screenshot(page, 'release-lifecycle', theme);
      results.push({ requirement: 'RM04/RM05', theme, result: 'PASS', checks: ['readiness-bypass-denied', 'clear-eol', 'release', 'branch-locked', 'end-of-life'] });
      await probe(`/features/${ids.feature}/delivery-mapping`, tokens.cpo, 'PUT', { epic_key: 'SYN-25', project: 'SYN' });
      await page.goto(`${base}/delivery`); await settle(page);
      await page.getByLabel('Продукт').selectOption(ids.soar); await settle(page);
      await page.getByLabel('Фича для прогноза').selectOption(ids.feature); await settle(page);
      await page.getByText('Если истории throughput нет, сначала настройте доску и выполните сверку с трекером. Нулевой throughput не позволяет рассчитать дату.', { exact: true }).waitFor();
      await count(page.getByText('Сохранённые привязки и последние данные остаются доступны.', { exact: false }), 1, 'Disabled adapter state explained');
      await screenshot(page, 'forecast-missing-data', theme);
      results.push({ requirement: 'DL04', theme, result: 'PASS', checks: ['mapping-visible', 'forecast-selection', 'missing-data-explained'] });
      const riskSubject = `CT03 risk ${theme} ${sequence}`;
      await probe(`/features/${ids.feature}/shift-date`, tokens.cpo, 'POST', { planned_date: '2026-12-15', reason: 'Synthetic CT03 fixture reset' });
      const commitment = await probe(`/products/${ids.soar}/commitments`, tokens.cpo, 'POST', { kind: 'customer', counterparty: 'SYN', subject: riskSubject, due_date: '2027-01-01', basis: 'Synthetic acceptance', owner: 'synthetic-original', feature_id: ids.feature }, 201);
      await probe(`/features/${ids.feature}/shift-date`, tokens.cpo, 'POST', { planned_date: theme === 'dark' ? '2027-02-01' : '2027-02-02', reason: 'Synthetic CT03 acceptance shift' });
      let risk;
      for (let attempt = 0; attempt < 30 && !risk; attempt++) {
        risk = (await probe(`/products/${ids.soar}/commitment-alerts?open=false`, tokens.cpo)).find(alert => alert.commitment_id === commitment.id);
        if (!risk) await new Promise(resolve => setTimeout(resolve, 200));
      }
      assert.ok(risk, 'Shift produces commitment alert through real outbox worker');
      await page.goto(`${base}/products/${ids.soar}/commitments`); await settle(page);
      const riskRow = page.locator('#commitment-alert-' + risk.id);
      const actions = [
        { kind: 'assigned', recipient: 'synthetic-risk-owner', note: 'Synthetic owner assignment' },
        { kind: 'escalated', recipient: 'synthetic-escalation', note: 'Synthetic escalation reason' },
        { kind: 'noted', note: 'Synthetic mitigation plan' },
      ];
      for (const action of actions) {
        await riskRow.getByRole('button', { name: 'Обработать риск', exact: true }).click();
        const riskForm = page.locator('form').filter({ has: page.getByRole('heading', { name: 'Обработка риска: ' + riskSubject, exact: true }) });
        await riskForm.getByLabel('Действие').selectOption(action.kind);
        if (action.kind === 'assigned') await riskForm.getByLabel('Владелец риска', { exact: true }).fill(action.recipient);
        if (action.kind === 'escalated') await riskForm.getByLabel('Адресат эскалации', { exact: true }).fill(action.recipient);
        await riskForm.getByLabel('Обоснование / план обработки', { exact: true }).fill(action.note);
        await mutate(page, riskForm.getByRole('button', { name: 'Сохранить действие', exact: true }));
        await riskForm.waitFor({ state: 'detached' });
        await count(riskForm, 0, 'Risk action saved');
      }
      risk = (await probe(`/products/${ids.soar}/commitment-alerts?open=false`, tokens.cpo)).find(alert => alert.id === risk.id);
      assert.equal(risk.risk_owner, 'synthetic-escalation', 'Escalation transfers responsibility per ADR-0020');
      assert.deepEqual(risk.actions.map(action => action.kind), ['assigned', 'escalated', 'noted']);
      await riskRow.getByText('История обработки (3)', { exact: true }).click();
      await screenshot(page, 'commitment-risk-actions', theme);
      await mutate(page, riskRow.getByRole('button', { name: 'Подтвердить', exact: true }));
      await riskRow.waitFor({ state: 'detached' });
      await count(riskRow, 0, 'Acknowledged alert removed from open list');
      await page.getByLabel('Показать подтверждённые').check(); await settle(page);
      await count(riskRow, 1, 'Acknowledged alert retained in history');
      await probe(`/commitment-alerts/${risk.id}/actions`, tokens.presale, 'POST', { kind: 'noted', note: 'Denied synthetic action' }, 403);
      results.push({ requirement: 'CT03', theme, result: 'PASS', checks: ['outbox-alert', 'assign-owner', 'manual-escalation', 'mitigation-note', 'immutable-history', 'ack-filter', 'presale-action-denied'] });
      await context.close();
      const privateTitle = `RM06 private hidden ${theme} ${sequence}`;
      await probe(`/products/${ids.soar}/roadmap/items`, tokens.cpo, 'POST', { title: privateTitle, audience: 'internal', bucket: 'next', launch_tier: 'tier1', launch_date: launchDate }, 201);
      const publicTitle = `RM06 public launch ${theme} ${sequence}`;
      await probe(`/products/${ids.soar}/roadmap/items`, tokens.cpo, 'POST', { title: publicTitle, audience: 'sales_safe', bucket: 'next', launch_tier: 'tier2', launch_date: launchDate }, 201);
      const publicContext = await browser.newContext({ viewport: { width: 390, height: 844 }, colorScheme: theme });
      await publicContext.addInitScript(({ token, theme }) => { sessionStorage.setItem('metis.access_token', token); localStorage.setItem('apex.theme', theme); }, { token: tokens.presale, theme });
      const publicPage = await publicContext.newPage();
      publicPage.on('pageerror', error => errors.push(error.message));
      await publicPage.goto(`${base}/products/${ids.soar}/roadmap`); await settle(publicPage);
      await publicPage.getByRole('tab', { name: 'Календарь запусков', exact: true }).click(); await settle(publicPage);
      await count(publicPage.getByRole('cell', { name: publicTitle, exact: true }), 1, 'Presale sees allowed launch');
      await count(publicPage.getByRole('button', { name: 'Добавить в roadmap', exact: true }), 0, 'Presale cannot create roadmap');
      await count(publicPage.getByRole('cell', { name: privateTitle, exact: true }), 0, 'Private launches absent from UI');
      await screenshot(publicPage, 'launch-calendar-presale-mobile', theme);
      results.push({ requirement: 'RM02/RM06', theme, result: 'PASS', checks: ['sales-safe-launch-visible', 'private-launch-hidden', 'read-only-mobile'] });
      await publicContext.close();
      const readContext = await browser.newContext({ viewport: { width: 390, height: 844 }, colorScheme: theme });
      await readContext.addInitScript(({ token, theme }) => { sessionStorage.setItem('metis.access_token', token); localStorage.setItem('apex.theme', theme); }, { token: tokens.marketing, theme });
      const readPage = await readContext.newPage();
      readPage.on('pageerror', error => errors.push(error.message));
      await readPage.goto(`${base}/products/${ids.soar}/commitments`); await settle(readPage);
      await readPage.getByLabel('Показать подтверждённые').check(); await settle(readPage);
      await count(readPage.getByRole('link', { name: riskSubject, exact: true }).first(), 1, 'Read-only user sees risk history');
      await count(readPage.getByRole('button', { name: 'Обработать риск', exact: true }), 0, 'Read-only user cannot process risk');
      await probe(`/commitment-alerts/${risk.id}/actions`, tokens.marketing, 'POST', { kind: 'noted', note: 'Denied read-only action' }, 403);
      await screenshot(readPage, 'commitment-risk-read-only-mobile', theme);
      results.push({ requirement: 'CT03', theme, role: 'marketing', result: 'PASS', checks: ['history-readable', 'write-hidden', 'direct-write-denied', 'mobile'] });
      await readContext.close();
    }
    assert.deepEqual(errors, [], 'No browser errors');
    console.log(JSON.stringify({ result: 'PASS', results, apiChecks: requests.length }));
  } catch (error) {
    if (activePage && !activePage.isClosed()) {
      await activePage.screenshot({ path: path.join(output, 'failure.png'), fullPage: true });
      fs.writeFileSync(path.join(output, 'failure.txt'), await activePage.locator('main').innerText());
    }
    throw error;
  } finally {
    fs.writeFileSync(path.join(output, 'matrix.json'), JSON.stringify({ results, requests, errors }, null, 2) + '\n');
    await browser.close();
  }
})().catch(error => { console.error(error.stack); process.exitCode = 1; });
