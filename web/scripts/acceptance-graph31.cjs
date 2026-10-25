// PG-03/04/05/09, AD-02: real loopback API and browser, synthetic data only.
const { chromium } = require(process.env.METIS_PLAYWRIGHT_MODULE || 'playwright');
const fs = require('node:fs');
const path = require('node:path');
const assert = require('node:assert/strict');
const base = process.env.METIS_ACCEPTANCE_URL || 'http://127.0.0.1:18078';
if (!['127.0.0.1', 'localhost'].includes(new URL(base).hostname)) throw Error('Use isolated loopback stand');
const root = process.env.METIS_ACCEPTANCE_TEMP;
if (!root) throw Error('METIS_ACCEPTANCE_TEMP required');
const tokens = JSON.parse(fs.readFileSync(path.join(root, 'tokens.json')));
const ids = JSON.parse(fs.readFileSync(path.join(root, 'ids.json')));
const output = path.join(root, 'graph31'); fs.mkdirSync(output, { recursive: true });
const results = [], errors = [];
async function probe(route, method = 'GET', body, status = 200) {
  const response = await fetch(base + '/api/v1' + route, { method, headers: { Authorization: 'Bearer ' + tokens.cpo, 'Content-Type': 'application/json' }, ...(body ? { body: JSON.stringify(body) } : {}) });
  assert.equal(response.status, status, `${method} ${route}: ${await response.clone().text()}`);
  return status === 204 ? null : response.json();
}
async function settle(page) { await page.waitForLoadState('networkidle'); }
async function mutate(page, button) {
  const [response] = await Promise.all([page.waitForResponse(r => r.url().startsWith(base + '/api/v1/') && ['POST', 'PUT'].includes(r.request().method())), button.click()]);
  assert.ok(response.ok(), `mutation ${response.status()}: ${await response.text()}`); await settle(page);
}
async function saveHeldContract(page, form, label) {
  let release, fetchedResolve;
  const held = new Promise(resolve => { release = resolve; });
  const fetched = new Promise(resolve => { fetchedResolve = resolve; });
  const matcher = url => url.origin === new URL(base).origin && url.pathname.startsWith('/api/v1/contracts');
  const handler = async route => {
    if (!['POST', 'PUT'].includes(route.request().method())) return route.continue();
    // The mutation reaches the real isolated API. Hold only its browser response.
    const response = await route.fetch();
    fetchedResolve(); await held; await route.fulfill({ response });
  };
  await page.route(matcher, handler);
  const responsePromise = page.waitForResponse(response => matcher(new URL(response.url())) && ['POST', 'PUT'].includes(response.request().method()));
  try {
    await form.getByRole('button', { name: 'Сохранить контракт', exact: true }).click();
    await fetched;
    assert.equal(await form.getByLabel('Название контракта', { exact: true }).isDisabled(), true, label + ' input disabled while pending');
    assert.equal(await form.getByRole('button', { name: 'Добавить пару версий', exact: true }).isDisabled(), true, label + ' matrix action disabled while pending');
    assert.equal(await form.getByRole('button', { name: 'Отмена', exact: true }).isDisabled(), true, label + ' cancel disabled while pending');
    await screenshot(page, 'contract-pending-' + label);
  } finally { release(); }
  const response = await responsePromise;
  assert.ok(response.ok(), `contract ${response.status()}: ${await response.text()}`);
  await settle(page); await page.unroute(matcher, handler);
}
async function screenshot(page, name) {
  assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth + 1), false, name + ' overflow');
  await page.screenshot({ path: path.join(output, name + '.png'), fullPage: true });
}
(async () => {
  const browser = await chromium.launch({ headless: true, ...(process.env.METIS_CHROMIUM_PATH ? { executablePath: process.env.METIS_CHROMIUM_PATH } : {}) });
  try {
    const context = await browser.newContext({ viewport: { width: 1440, height: 1000 }, reducedMotion: 'reduce' });
    await context.addInitScript(token => sessionStorage.setItem('metis.access_token', token), tokens.cpo);
    const page = await context.newPage();
    page.on('pageerror', error => errors.push(error.message));
    await page.goto(base + '/graph?product=' + ids.edr); await settle(page);
    assert.equal(await page.getByRole('combobox', { name: 'Продукт', exact: true }).inputValue(), ids.edr);
    results.push('PG09 deep link product filter');
    await page.getByRole('button', { name: 'Сбросить', exact: true }).click();
    await page.waitForFunction(() => !new URL(location.href).searchParams.has('product')); await settle(page);
    assert.equal(await page.getByRole('combobox', { name: 'Продукт', exact: true }).inputValue(), '');
    const products = await probe('/products');
    const consumer = products.find(p => p.id === ids.edr), provider = products.find(p => p.id === ids.soar);
    assert.ok(consumer && provider);
    await page.getByRole('button', { name: 'Создать связь', exact: true }).click();
    let form = page.getByRole('form', { name: 'Создание связи', exact: true });
    await form.getByRole('combobox', { name: 'Продукт-потребитель', exact: true }).selectOption(consumer.id);
    await form.getByRole('combobox', { name: 'Продукт-поставщик', exact: true }).selectOption(provider.id);
    await form.getByRole('combobox', { name: 'Тип связи', exact: true }).selectOption('commercial');
    await mutate(page, form.getByRole('button', { name: 'Создать связь', exact: true }));
    assert.ok((await probe('/links')).some(l => l.from_product_id === consumer.id && l.to_product_id === provider.id && l.type === 'commercial'));
    results.push('PG03 accessible product link form persists');
    const stamp = String(Date.now()), name = 'Synthetic PG04 ' + stamp;
    const cf = await probe('/products/' + consumer.id + '/features', 'POST', { name: 'Synthetic consumer ' + stamp, status: 'planned' }, 201);
    const pf = await probe('/products/' + provider.id + '/features', 'POST', { name: 'Synthetic provider ' + stamp, status: 'planned' }, 201);
    async function connectFeatures(from, to, fromFeature, toFeature, cycle = false) {
      await page.getByRole('button', { name: 'Создать связь', exact: true }).click();
      const linkForm = page.getByRole('form', { name: 'Создание связи', exact: true });
      await linkForm.getByRole('combobox', { name: 'Уровень связи', exact: true }).selectOption('feature');
      await linkForm.getByRole('combobox', { name: 'Продукт-потребитель', exact: true }).selectOption(from);
      await linkForm.getByRole('combobox', { name: 'Продукт-поставщик', exact: true }).selectOption(to); await settle(page);
      await linkForm.getByRole('combobox', { name: 'Фича-потребитель', exact: true }).selectOption(fromFeature);
      await linkForm.getByRole('combobox', { name: 'Фича-поставщик', exact: true }).selectOption(toFeature);
      if (cycle) {
        const [response] = await Promise.all([page.waitForResponse(r => r.url().endsWith('/api/v1/links') && r.request().method() === 'POST'), linkForm.getByRole('button', { name: 'Создать связь', exact: true }).click()]);
        assert.equal(response.status(), 409); await settle(page);
        await page.getByRole('status').filter({ hasText: 'цикл' }).waitFor();
        await linkForm.getByRole('button', { name: 'Отмена', exact: true }).click();
      } else await mutate(page, linkForm.getByRole('button', { name: 'Создать связь', exact: true }));
    }
    await connectFeatures(consumer.id, provider.id, cf.id, pf.id);
    await connectFeatures(provider.id, consumer.id, pf.id, cf.id, true);
    results.push('PG03 feature link form; PG05 reverse cycle rejected with path');
    await page.getByRole('button', { name: 'Создать контракт', exact: true }).click();
    form = page.getByRole('form', { name: 'Новый контракт', exact: true });
    await form.getByLabel('Название контракта', { exact: true }).fill(name);
    await form.getByRole('combobox', { name: 'Продукт-поставщик', exact: true }).selectOption(provider.id);
    await form.getByRole('combobox', { name: 'Продукт-потребитель', exact: true }).selectOption(consumer.id); await settle(page);
    const consumerFeature = [cf], providerFeature = [pf];
    await form.getByRole('group', { name: 'Поставщик', exact: true }).getByLabel(providerFeature[0].name, { exact: true }).check();
    await form.getByRole('group', { name: 'Потребитель', exact: true }).getByLabel(consumerFeature[0].name, { exact: true }).check();
    await form.getByLabel('Версия интерфейса', { exact: true }).fill('v1');
    await form.getByLabel('Владелец контракта', { exact: true }).fill('Synthetic owner');
    await form.getByRole('button', { name: 'Добавить пару версий', exact: true }).click();
    await form.getByLabel('Версия поставщика', { exact: true }).fill('1.0');
    await form.getByLabel('Версия потребителя', { exact: true }).fill('2.0');
    await saveHeldContract(page, form, 'create');
    let contract = (await probe('/contracts')).find(c => c.name === name); assert.ok(contract);
    assert.deepEqual(contract.compatibility, [{ provider_version: '1.0', consumer_version: '2.0', compatible: true }]);
    let details = page.locator('details').filter({ has: page.locator('summary').filter({ hasText: name }) });
    await details.locator('summary').click();
    await details.getByRole('button', { name: 'Изменить контракт', exact: true }).click();
    form = page.getByRole('form', { name: 'Изменение контракта', exact: true });
    assert.equal(await form.getByLabel('Версия интерфейса', { exact: true }).inputValue(), 'v1');
    await form.getByRole('combobox', { name: 'Статус контракта', exact: true }).selectOption('active');
    await form.getByRole('combobox', { name: 'Критичность контракта', exact: true }).selectOption('accelerates');
    await form.getByLabel('Версия интерфейса', { exact: true }).fill('v2');
    await saveHeldContract(page, form, 'edit');
    contract = (await probe('/contracts')).find(c => c.name === name);
    assert.equal(contract.status, 'active'); assert.equal(contract.interface_version, 'v2');
    const contractLinks = (await probe('/links')).filter(l => l.contract_id === contract.id);
    assert.equal(contractLinks.length, 2); assert.ok(contractLinks.every(l => l.criticality === 'accelerates'));
    results.push('PG04 contract create/read/edit compatibility/features/status persists; pending input/matrix/cancel actions disabled on held real POST/PUT responses');
    await page.getByRole('status').filter({ hasText: 'цикл' }).click();
    for (const theme of ['light', 'dark']) {
      await page.getByRole('combobox', { name: 'Тема оформления', exact: true }).selectOption(theme);
      await screenshot(page, 'graph-' + theme);
      await page.setViewportSize({ width: 390, height: 844 }); await screenshot(page, 'graph-mobile-' + theme);
      await page.getByRole('combobox', { name: 'Продукт', exact: true }).selectOption(consumer.id);
      await page.waitForFunction(id => new URL(location.href).searchParams.get('product') === id, consumer.id);
      await settle(page); await page.waitForTimeout(500); await screenshot(page, 'graph-mobile-focused-' + theme);
      await page.getByRole('button', { name: 'Сбросить', exact: true }).click();
      await page.waitForFunction(() => !new URL(location.href).searchParams.has('product')); await settle(page);
      await page.setViewportSize({ width: 1440, height: 1000 });
    }
    await page.getByRole('combobox', { name: 'Статус контракта', exact: true }).selectOption('active');
    await screenshot(page, 'graph-contract-filter'); results.push('PG09 contract status filter desktop mobile themes');
    const reader = await browser.newContext({ viewport: { width: 1440, height: 1000 } });
    await reader.addInitScript(token => sessionStorage.setItem('metis.access_token', token), tokens.marketing);
    const readerPage = await reader.newPage(); readerPage.on('pageerror', error => errors.push(error.message));
    await readerPage.goto(base + '/graph'); await settle(readerPage);
    assert.equal(await readerPage.getByRole('button', { name: 'Создать связь', exact: true }).count(), 0);
    assert.equal(await readerPage.getByRole('button', { name: 'Создать контракт', exact: true }).count(), 0);
    results.push('AD02 read-only role has no graph/contract mutation controls');
    await reader.close();
    assert.deepEqual(errors, []);
    fs.writeFileSync(path.join(output, 'results.json'), JSON.stringify({ results, errors }, null, 2));
    console.log(JSON.stringify({ results, errors }, null, 2));
  } finally { await browser.close(); }
})().catch(error => { console.error(error); process.exitCode = 1; });
