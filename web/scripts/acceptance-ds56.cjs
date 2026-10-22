// DS56 real-API visual acceptance. Operator supplies Playwright, Chromium and temporary tokens.
const { chromium } = require(process.env.METIS_PLAYWRIGHT_MODULE || 'playwright');
const fs = require('node:fs');
const path = require('node:path');
const assert = require('node:assert/strict');
const { verifiedProbe } = require('./acceptance-probe-ds56.cjs');
const base = process.env.METIS_ACCEPTANCE_URL || 'http://127.0.0.1:18057';
if (!['127.0.0.1', 'localhost'].includes(new URL(base).hostname))
    throw Error('Acceptance requires an isolated loopback stand');
const root = process.env.METIS_ACCEPTANCE_TEMP || '/tmp/metis57-acceptance';
const output = process.env.METIS_ACCEPTANCE_OUTPUT || path.join(root, 'final');
const tokens = JSON.parse(fs.readFileSync(path.join(root, 'tokens.json')));
const ids = JSON.parse(fs.readFileSync(path.join(root, 'ids.json')));
fs.mkdirSync(output, { recursive: true });
const results = [], findings = [], apiChecks = [], networks = new Set();
const phase = process.env.METIS_ACCEPTANCE_PHASE || 'full';
const routes = [['products', '/'], ['graph', '/graph'], ['hub', '/hub'], ['delivery', '/delivery'], ['economics', '/economics'], ['portfolio', '/portfolio'], ['decisions', '/decisions'], ['dashboard-compliance', '/compliance'], ['admin', '/admin'], ['builder', '/products/new'], ['product', `/products/${ids.soar}`], ['edit', `/products/${ids.soar}/edit`], ['roadmap', `/products/${ids.soar}/roadmap`], ['discovery', `/products/${ids.edr}/discovery`], ['commitments', `/products/${ids.soar}/commitments`], ['compliance', `/products/${ids.soar}/compliance`], ['baseline', `/products/${ids.server}/compliance`], ['product-decisions', `/products/${ids.soar}/decisions`], ['other-product', `/products/${ids.backup}`], ...['signal', 'feature', 'hypothesis', 'insight', 'decision'].map(k => ['trace-' + k, `/trace/${k}/${ids[k]}`]), ['not-found', '/not-a-page']];
const selectedRole = name => name === 'economics' ? 'finance' : ['compliance', 'baseline', 'dashboard-compliance'].includes(name) ? 'compliance' : ['admin', 'builder', 'edit'].includes(name) ? 'admin' : 'cpo';
async function settle(page) {
    await page.locator('#root > *').first().waitFor();
    await page.waitForLoadState('networkidle');
    await page.waitForFunction(() => ![...document.querySelectorAll('.muted')].some(e => /^Загрузка/.test(e.textContent.trim())), null, { timeout: 12000 });
    await page.evaluate(() => document.fonts.ready);
}
async function inspect(page, role, name, theme, errors, shot = true) {
    const info = await page.evaluate(() => ({ theme: document.documentElement.dataset.theme, overflow: document.documentElement.scrollWidth > innerWidth + 1, primary: document.querySelectorAll('.btn-primary').length, text: document.querySelector('main')?.innerText || document.body.innerText, font: getComputedStyle(document.body).fontFamily }));
    const row = { role, name, theme, url: new URL(page.url()).pathname, heading: await page.locator('h1').allTextContents(), primary: info.primary, overflow: info.overflow, errors: [...errors], alerts: await page.locator('[role=alert]').allTextContents() };
    if (info.theme !== theme)
        findings.push({ role, name, theme, problem: 'theme mismatch' });
    if (info.overflow)
        findings.push({ role, name, theme, problem: 'document horizontal overflow' });
    if (info.primary > 1)
        findings.push({ role, name, theme, problem: 'multiple primary actions', count: info.primary });
    if (errors.length)
        findings.push({ role, name, theme, problem: 'JavaScript error', errors: [...errors] });
    if (!info.font.includes('Inter Variable'))
        findings.push({ role, name, theme, problem: 'wrong interface font' });
    if (role === 'presale' && (/SYN-PRIVATE|Бэклог фич|Сигналы спроса/.test(info.text)))
        findings.push({ role, name, theme, problem: 'private content visible to presale' });
    if (name === 'product') {
        const feature = await page.getByRole('button', { name: 'Новая фича', exact: true }).count();
        const signal = await page.getByRole('button', { name: 'Новый сигнал', exact: true }).count();
        assert.equal(feature, ['admin', 'cpo', 'pm'].includes(role) ? 1 : 0, role + ' feature action');
        assert.equal(signal, ['admin', 'cpo', 'pm', 'marketing'].includes(role) ? 1 : 0, role + ' signal action');
    }
    results.push(row);
    if (shot) {
        await page.screenshot({ path: path.join(output, `${role}-${name}-${theme}.png`), fullPage: true });
        row.screenshot = `${role}-${name}-${theme}.png`;
    }
    return row;
}
async function inspectResponsive(page, role, name, theme, errors) {
    await inspect(page, role, name, theme, errors);
    await page.setViewportSize({ width: 390, height: 844 });
    await inspect(page, role, 'responsive-' + name, theme, errors);
    await page.setViewportSize({ width: 1440, height: 1000 });
}
async function contextFor(browser, role, theme, mobile = false) {
    const context = await browser.newContext({ viewport: mobile ? { width: 390, height: 844 } : { width: 1440, height: 1000 }, colorScheme: theme, reducedMotion: 'reduce' });
    context.on('request', r => { if (!new URL(r.url()).hostname.match(/^(127\.0\.0\.1|localhost)$/))
        networks.add(new URL(r.url()).origin); });
    await context.addInitScript(({ token, theme }) => { if (!sessionStorage.getItem('ds56.initialized')) {
        sessionStorage.setItem('metis.access_token', token);
        sessionStorage.setItem('ds56.initialized', 'true');
    } if (!localStorage.getItem('apex.theme'))
        localStorage.setItem('apex.theme', theme); }, { token: tokens[role], theme });
    return context;
}
(async () => {
    const browser = await chromium.launch({ headless: true, ...(process.env.METIS_CHROMIUM_PATH ? { executablePath: process.env.METIS_CHROMIUM_PATH } : {}) });
    try {
        // All routes, every role profile, both themes. Independent read-only browser contexts.
        if (phase === 'details') {
            const prior = JSON.parse(fs.readFileSync(path.join(output, 'matrix.json')));
            const names = new Set(routes.map(r => r[0]));
            results.push(...prior.results.filter(r => names.has(r.name) || r.name.startsWith('mobile-') && names.has(r.name.slice(7))));
            apiChecks.push(...prior.apiChecks);
            findings.push(...prior.findings.filter(f => names.has(f.name) || f.name?.startsWith('mobile-') && names.has(f.name.slice(7))));
            assert.equal(results.length, routes.length * 28, 'Complete prior desktop/mobile matrix is required');
        }
        const jobs = phase === 'details' ? [] : Object.keys(tokens).flatMap(role => ['dark', 'light'].map(theme => ({ role, theme })));
        await Promise.all(Array.from({ length: 3 }, async () => {
            for (;;) {
                const job = jobs.shift();
                if (!job)
                    return;
                const { role, theme } = job;
                const ctx = await contextFor(browser, role, theme);
                const page = await ctx.newPage();
                let errors = [];
                page.on('pageerror', e => errors.push(e.message));
                for (const [name, url] of routes) {
                    errors = [];
                    await page.goto(base + url);
                    await settle(page);
                    await inspect(page, role, name, theme, errors, selectedRole(name) === role || name === 'product');
                }
                // Verify navigation is based on real /me roles, with independent scopes for finance.
                await page.goto(base + '/');
                await settle(page);
                const nav = await page.locator('.nav').innerText();
                const expected = { admin: ['Администрирование', 'Решения', 'Портфель'], cpo: ['Решения', 'Портфель'], compliance: ['Администрирование', 'Compliance'], finance: ['Экономика', 'Портфель'] }[role] || [];
                for (const label of expected)
                    assert.ok(nav.includes(label), `${role}: missing navigation ${label}`);
                if (!['admin', 'compliance'].includes(role))
                    assert.ok(!nav.includes('Администрирование'));
                if (role === 'presale') {
                    assert.ok(!nav.includes('Портфель'));
                    assert.equal(await page.getByRole('link', { name: 'Новый продукт', exact: true }).count(), 0);
                }
                for (const [apiPath, allowed] of [[`/products/${ids.soar}/signals`, !['pm-other', 'presale'].includes(role)], [`/products/${ids.edr}/economics/2026-09`, role === 'finance'], ['/decisions', ['cpo', 'admin'].includes(role)]]) {
                    const response = await ctx.request.get(base + '/api/v1' + apiPath, { headers: { Authorization: 'Bearer ' + tokens[role] } });
                    // Every recorded GET must match the role contract, including decisions/signals.
                    apiChecks.push(verifiedProbe(role, apiPath, response.status(), allowed));
                }
                if (['pm-other', 'presale'].includes(role)) {
                    const response = await ctx.request.post(base + `/api/v1/products/${ids.soar}/signals`, { headers: { Authorization: 'Bearer ' + tokens[role] }, data: { source: 'manual', text: 'Synthetic forbidden write DS56' } });
                    assert.equal(response.status(), 403, role + ' forbidden signal write');
                    apiChecks.push({ role, path: `/products/${ids.soar}/signals`, method: 'POST', status: response.status() });
                }
                if (['marketing', 'finance', 'presale'].includes(role)) {
                    const response = await ctx.request.post(base + `/api/v1/products/${ids.soar}/features`, { headers: { Authorization: 'Bearer ' + tokens[role] }, data: { name: 'Synthetic forbidden feature DS56', status: 'idea' } });
                    assert.equal(response.status(), 403, role + ' forbidden feature write');
                    apiChecks.push({ role, path: `/products/${ids.soar}/features`, method: 'POST', status: response.status() });
                }
                await ctx.close();
                console.log('DS56 matrix', role, theme, 'PASS');
            }
        }));
        // Mobile: every route, owner and read-only audience; screenshots for rich representative pages.
        if (phase !== 'details')
            for (const theme of ['dark', 'light'])
                for (const role of ['admin', 'finance', 'presale']) {
                    const ctx = await contextFor(browser, role, theme, true);
                    const page = await ctx.newPage();
                    let errors = [];
                    page.on('pageerror', e => errors.push(e.message));
                    for (const [name, url] of routes) {
                        errors = [];
                        await page.goto(base + url);
                        await settle(page);
                        await inspect(page, role, `mobile-${name}`, theme, errors, ['product', 'discovery', 'compliance', 'roadmap', 'economics'].includes(name));
                    }
                    await ctx.close();
                }
        // Tabs, nested forms and financial exports on populated screens.
        for (const theme of ['dark', 'light']) {
            const ctx = await contextFor(browser, 'admin', theme);
            const page = await ctx.newPage();
            let errors = [];
            page.on('pageerror', e => errors.push(e.message));
            for (const [name, url] of routes.filter(([name]) => ['admin', 'discovery', 'roadmap'].includes(name))) {
                await page.goto(base + url);
                await settle(page);
                const labels = await page.getByRole('tab').allTextContents();
                const expectedTabs = { admin: ['Аудит', 'Наборы требований', 'Шаблоны треков', 'Кастомные поля', 'Интеграции'], discovery: ['Гипотезы', 'Интервью', 'Инсайты', 'Evidence'], roadmap: ['Таймлайн', 'Now / Next / Later', 'По релизам', 'Релизы'] };
                assert.deepEqual(labels, expectedTabs[name], `${name} tab coverage`);
                for (const label of labels) {
                    await page.getByRole('tab', { name: label, exact: true }).click();
                    await settle(page);
                    await inspectResponsive(page, 'admin', `${name}-tab-${labels.indexOf(label)}`, theme, errors);
                    const actions = {
                        admin: { 'Наборы требований': 'Новая версия набора', 'Шаблоны треков': 'Новый шаблон' },
                        discovery: { 'Гипотезы': 'Новая гипотеза', 'Интервью': 'Новое интервью', 'Инсайты': 'Новый инсайт', 'Evidence': 'Добавить evidence' },
                        roadmap: { 'Релизы': 'Новый релиз' },
                    };
                    const action = actions[name][label];
                    if (action) {
                        const button = page.getByRole('button', { name: action, exact: true });
                        assert.equal(await button.count(), 1, `Expected ${name}/${label} form action`);
                        await button.click();
                        await settle(page);
                        await inspectResponsive(page, 'admin', `${name}-form-${labels.indexOf(label)}-0`, theme, errors);
                    }
                    if (name === 'admin' && label === 'Кастомные поля')
                        assert.equal(await page.getByRole('button', { name: 'Определить поле', exact: true }).count(), 1, 'Expected inline custom-field form');
                }
                await page.getByRole('tab').first().focus();
                await page.keyboard.press('End');
                assert.equal(await page.getByRole('tab').last().getAttribute('aria-selected'), 'true');
                assert.equal(await page.getByRole('tab').last().evaluate(e => getComputedStyle(e).outlineWidth), '2px');
                await page.keyboard.press('Home');
                assert.equal(await page.getByRole('tab').first().getAttribute('aria-selected'), 'true');
            }
            for (const [name, url, button] of [['commitment', `/products/${ids.soar}/commitments`, 'Новое обязательство'], ['decision', '/decisions', 'Новое решение'], ['track', `/products/${ids.soar}/compliance`, 'Запустить трек'], ['roadmap', `/products/${ids.soar}/roadmap`, 'Добавить в roadmap']]) {
                await page.goto(base + url);
                await settle(page);
                const btn = page.getByRole('button', { name: button, exact: true });
                assert.equal(await btn.count(), 1, `Expected form action: ${button}`);
                await btn.click();
                await settle(page);
                await inspectResponsive(page, 'admin', `${name}-create-form`, theme, errors);
            }
            for (const button of ['Новая фича', 'Новый сигнал', 'Новая модель оценки', 'Оценить фичу', 'Подробнее', 'Сдвинуть дату', 'Разобрать']) {
                await page.goto(base + `/products/${ids.soar}`);
                await settle(page);
                const control = page.getByRole('button', { name: button, exact: true }).first();
                assert.equal(await control.count(), 1, button);
                await control.click();
                await settle(page);
                await inspectResponsive(page, 'admin', 'product-form-' + ['Новая фича', 'Новый сигнал', 'Новая модель оценки', 'Оценить фичу', 'Подробнее', 'Сдвинуть дату', 'Разобрать'].indexOf(button), theme, errors);
            }
            for (const button of ['Приложить доказательство', 'Владелец / срок / затраты']) {
                await page.goto(base + `/products/${ids.soar}/compliance`);
                await settle(page);
                const control = page.getByRole('button', { name: button, exact: true }).first();
                assert.equal(await control.count(), 1, `Expected compliance action: ${button}`);
                await control.click();
                await settle(page);
                await inspectResponsive(page, 'admin', 'compliance-form-' + (button === 'Владелец / срок / затраты' ? 'gate' : 'evidence'), theme, errors);
            }
            await page.goto(base + '/delivery');
            await settle(page);
            const deliveryForm = page.getByRole('button', { name: 'Привязать или создать эпик', exact: true });
            assert.equal(await deliveryForm.count(), 1, 'Expected delivery link form action');
            await deliveryForm.click();
            await inspectResponsive(page, 'admin', 'delivery-link-form', theme, errors);
            await ctx.close();
            const finance = await contextFor(browser, 'finance', theme);
            const p = await finance.newPage();
            let financialErrors = [];
            p.on('pageerror', e => financialErrors.push(e.message));
            await p.goto(base + '/economics');
            await settle(p);
            await p.getByLabel('Владелец финансовой книги').selectOption(ids.edr);
            await p.getByLabel('Период', { exact: true }).fill('2026-09');
            await settle(p);
            for (const label of ['P&L и сценарии', 'Загрузить данные', 'Правила и показатели', 'Исходные строки']) {
                await p.getByRole('tab', { name: label, exact: true }).click();
                await settle(p);
                await inspectResponsive(p, 'finance', `economics-tab-${label === 'P&L и сценарии' ? 'report' : label === 'Загрузить данные' ? 'import' : label === 'Правила и показатели' ? 'rules' : 'source'}`, theme, financialErrors);
            }
            await p.getByRole('tab', { name: 'P&L и сценарии', exact: true }).click();
            await settle(p);
            const download = p.waitForEvent('download');
            await p.getByRole('button', { name: 'Экспорт CSV', exact: true }).click();
            const csv = await download;
            await csv.saveAs(path.join(output, `economics-${theme}.csv`));
            assert.ok(fs.readFileSync(path.join(output, `economics-${theme}.csv`), 'utf8').includes(ids.edr));
            await p.emulateMedia({ media: 'print' });
            assert.equal(await p.locator('html').evaluate(e => getComputedStyle(e).getPropertyValue('--bg-canvas').trim()), '#f5f7fb');
            assert.equal(await p.locator('html').evaluate(e => getComputedStyle(e).colorScheme), 'light');
            assert.equal(await p.locator('.theme-switch:visible').count(), 0);
            await p.pdf({ path: path.join(output, `economics-from-${theme}.pdf`), format: 'A4', landscape: true, printBackground: true });
            await finance.close();
        }
        // Real UI sign-out/sign-in in the same tab. Tokens are never screenshot or logged.
        const ctx = await contextFor(browser, 'cpo', 'dark');
        const page = await ctx.newPage();
        await page.goto(base + `/products/${ids.soar}`);
        await settle(page);
        await page.getByRole('button', { name: 'Все сигналы', exact: true }).click();
        await settle(page);
        assert.ok((await page.locator('main').innerText()).includes('SYN-PRIVATE-DS56'));
        await page.getByRole('button', { name: 'Выйти', exact: true }).click();
        await page.getByRole('textbox').fill(tokens.presale);
        await page.getByRole('button', { name: 'Войти с токеном', exact: true }).click();
        await settle(page);
        await page.goto(base + `/products/${ids.soar}`);
        await settle(page);
        assert.ok(!(await page.locator('main').innerText()).includes('SYN-PRIVATE'));
        await inspect(page, 'presale', 'session-switch', 'dark', [], true);
        await page.getByRole('button', { name: 'Выйти', exact: true }).click();
        await page.screenshot({ path: path.join(output, 'login-dark.png'), fullPage: true });
        await page.getByLabel('Тема оформления').selectOption('light');
        await page.screenshot({ path: path.join(output, 'login-light.png'), fullPage: true });
        await page.getByRole('textbox').fill('invalid-synthetic-token');
        await page.getByRole('button', { name: 'Войти с токеном', exact: true }).click();
        await settle(page);
        assert.ok(new URL(page.url()).pathname === '/login');
        await page.getByRole('textbox').fill('');
        await page.screenshot({ path: path.join(output, 'login-error-light.png'), fullPage: true });
        await ctx.close();
        assert.equal(fs.readFileSync(path.join(output, 'economics-dark.csv'), 'utf8'), fs.readFileSync(path.join(output, 'economics-light.csv'), 'utf8'), 'CSV data must not depend on theme');
        assert.equal(networks.size, 0, `External browser requests: ${[...networks]}`);
    }
    catch (error) {
        findings.push({ problem: 'runner assertion', message: error.message, stack: error.stack });
        console.error(error.message);
    }
    finally {
        fs.writeFileSync(path.join(output, 'matrix.json'), JSON.stringify({ testedAt: new Date().toISOString(), stand: 'real API / memory / HMAC / synthetic seed; external adapters disabled', results, apiChecks, externalOrigins: [...networks], findings }, null, 2));
        await browser.close();
    }
    console.log('DS56 acceptance:', results.length, 'views;', apiChecks.length, 'real authorization probes;', findings.length, 'findings');
    if (findings.length)
        process.exitCode = 1;
})().catch(e => { console.error(e); process.exit(1); });
