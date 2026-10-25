// PG01/02/06/08/10 browser acceptance on an isolated synthetic loopback API.
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
const output = path.join(root, 'portfolio31');
fs.mkdirSync(output, { recursive: true });
async function probe(route, token, method = 'GET', body, expected = 200) {
  const response = await fetch(base + '/api/v1' + route, { method, headers: { Authorization: 'Bearer ' + token, 'Content-Type': 'application/json' }, ...(body ? { body: JSON.stringify(body) } : {}) });
  assert.equal(response.status, expected, `${method} ${route}: ${await response.clone().text()}`);
  return response.status === 204 ? undefined : response.json();
}
async function settle(page) { await page.waitForLoadState('networkidle'); }
async function mutate(page, button) {
  const [response] = await Promise.all([page.waitForResponse(response => response.url().startsWith(base + '/api/v1/') && ['POST', 'PUT', 'PATCH'].includes(response.request().method())), button.click()]);
  assert.ok(response.ok(), `UI mutation ${response.status()}: ${await response.text()}`);
  await settle(page);
}
(async () => {
  const browser = await chromium.launch({ headless: true, ...(process.env.METIS_CHROMIUM_PATH ? { executablePath: process.env.METIS_CHROMIUM_PATH } : {}) });
  const errors = [];
  try {
    for (const theme of ['dark', 'light']) {
      const context = await browser.newContext({ viewport: { width: 1440, height: 1000 }, colorScheme: theme, reducedMotion: 'reduce' });
      await context.addInitScript(({ token, theme }) => { sessionStorage.setItem('metis.access_token', token); localStorage.setItem('apex.theme', theme); }, { token: tokens.cpo, theme });
      const page = await context.newPage();
      page.on('pageerror', error => errors.push(error.message));
      await page.goto(`${base}/portfolio`); await settle(page);
      await page.getByRole('heading', { name: 'Состав портфеля', exact: true }).waitFor();
      assert.ok(await page.locator('tbody tr').count() > 0);
      await page.screenshot({ path: path.join(output, `portfolio-${theme}.png`), fullPage: true });
      await page.setViewportSize({ width: 390, height: 844 });
      assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth + 1), false, 'Portfolio mobile overflow');
      await page.screenshot({ path: path.join(output, `portfolio-mobile-${theme}.png`), fullPage: true });
      await page.setViewportSize({ width: 1440, height: 1000 });
      await page.goto(`${base}/products/${ids.soar}`); await settle(page);
      const hierarchy = page.locator('#hierarchy');
      const capabilityName = `PG02 capability ${theme} ${Date.now()}`;
      const featureName = `PG02 feature ${theme} ${Date.now()}`;
      const requirementText = `PG02 requirement ${theme}`;
      await hierarchy.getByLabel('Название возможности', { exact: true }).fill(capabilityName);
      await mutate(page, hierarchy.getByRole('button', { name: 'Создать возможность', exact: true }));
      const capability = (await probe(`/products/${ids.soar}/capabilities`, tokens.cpo)).find(item => item.name === capabilityName);
      assert.ok(capability, 'PG02 persisted capability');
      const backlog = page.locator('#backlog');
      await backlog.getByRole('button', { name: 'Новая фича', exact: true }).click();
      const form = backlog.locator('form').filter({ has: page.getByRole('heading', { name: 'Новая фича', exact: true }) });
      await form.getByLabel('Фича', { exact: true }).fill(featureName);
      await form.getByLabel('Возможность', { exact: false }).selectOption(capability.id);
      await form.getByLabel('План', { exact: true }).fill('2027-01-01');
      await mutate(page, form.getByRole('button', { name: 'Сохранить', exact: true }));
      const feature = (await probe(`/products/${ids.soar}/features`, tokens.cpo)).find(item => item.name === featureName);
      assert.equal(feature.capability_id, capability.id, 'PG02 persisted assignment');
      await hierarchy.getByRole('button', { name: `Требования: ${featureName}`, exact: true }).click();
      await hierarchy.getByLabel('Текст требования', { exact: true }).fill(requirementText);
      await mutate(page, hierarchy.getByRole('button', { name: 'Добавить требование', exact: true }));
      assert.equal((await probe(`/features/${feature.id}/requirements`, tokens.cpo))[0].text, requirementText);
      await page.reload(); await settle(page);
      await hierarchy.getByRole('heading', { name: capabilityName, exact: true }).waitFor();
      await hierarchy.getByRole('button', { name: `Требования: ${featureName}`, exact: true }).click();
      await hierarchy.getByText(requirementText, { exact: true }).waitFor();
      await page.screenshot({ path: path.join(output, `hierarchy-${theme}.png`), fullPage: true });
      const dependent = await probe(`/products/${ids.soar}/features`, tokens.cpo, 'POST', { name: `PG08 dependent ${theme}`, status: 'planned', planned_date: '2027-01-01' }, 201);
      await probe('/links', tokens.cpo, 'POST', { type: 'shared_component', criticality: 'blocks', from_feature_id: dependent.id, to_feature_id: feature.id }, 201);
      await page.reload(); await settle(page);
      const sourceRow = backlog.locator('tbody tr').filter({ has: page.getByRole('cell', { name: featureName, exact: true }) });
      await sourceRow.getByRole('button', { name: 'Сдвинуть дату', exact: true }).click();
      const shiftForm = backlog.locator('form').filter({ has: page.getByRole('heading', { name: `Изменение плановой даты: ${featureName}`, exact: true }) });
      await shiftForm.getByLabel('Новая дата', { exact: true }).fill('2027-02-01');
      await shiftForm.getByLabel('Причина', { exact: true }).fill('Synthetic PG08 acceptance');
      await mutate(page, shiftForm.getByRole('button', { name: 'Сохранить', exact: true }));
      await shiftForm.getByText('план:', { exact: false }).waitFor();
      assert.ok((await shiftForm.innerText()).includes('01.02.2027'), 'PG08 implied date displayed');
      assert.equal((await probe(`/features/${dependent.id}`, tokens.cpo)).affected, true);

      await page.goto(`${base}/products/${ids.soar}/edit`); await settle(page);
      // Hub designation never changes the independent product type.
      await page.getByLabel('Тип', { exact: false }).selectOption('security');
      await page.getByLabel('Назначить хабом портфеля', { exact: true }).check();
      assert.equal(await page.getByLabel('Тип', { exact: false }).inputValue(), 'security');
      await mutate(page, page.getByRole('button', { name: 'Сохранить', exact: true }));
      const product = await probe(`/products/${ids.soar}`, tokens.cpo);
      assert.equal(product.type, 'security'); assert.equal(product.hub_manual, true);
      await context.close();
    }
    const context = await browser.newContext();
    await context.addInitScript(token => sessionStorage.setItem('metis.access_token', token), tokens.presale);
    const page = await context.newPage();
    await page.goto(`${base}/portfolio`); await settle(page);
    await page.getByRole('heading', { name: 'Состав портфеля', exact: true }).waitFor();
    await page.goto(`${base}/products/${ids.soar}`); await settle(page);
    assert.equal(await page.locator('#hierarchy').count(), 0, 'PG10 no private hierarchy for presale');
    assert.equal(await page.getByRole('link', { name: 'Редактировать', exact: true }).count(), 0);
    await probe(`/products/${ids.soar}/capabilities`, tokens.presale, 'GET', undefined, 403);
    await context.close();
    const pmContext = await browser.newContext();
    await pmContext.addInitScript(token => sessionStorage.setItem('metis.access_token', token), tokens.pm);
    const pmPage = await pmContext.newPage();
    await pmPage.goto(`${base}/products/${ids.soar}/edit`); await settle(pmPage);
    assert.equal(await pmPage.getByLabel('Назначить хабом портфеля', { exact: true }).isDisabled(), true, 'PG06 PM cannot change manual hub');
    await pmPage.getByLabel('Назначение, границы и источники', { exact: true }).fill('PG01 synthetic PM edit');
    await mutate(pmPage, pmPage.getByRole('button', { name: 'Сохранить', exact: true }));
    assert.equal((await probe(`/products/${ids.soar}`, tokens.cpo)).description, 'PG01 synthetic PM edit');
    await pmContext.close();
    assert.deepEqual(errors, []);
    fs.writeFileSync(path.join(output, 'results.json'), JSON.stringify({ passed: true, checks: ['PG01', 'PG02', 'PG06', 'PG08', 'PG10'], themes: ['dark', 'light'], errors }, null, 2));
    console.log('Portfolio PG01/02/06/08/10 browser acceptance passed');
  } finally { await browser.close(); }
})().catch(error => { console.error(error); process.exitCode = 1; });
