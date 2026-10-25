// Optional visual acceptance runner. Uses an operator-provided Playwright installation.
const {chromium}=require(process.env.METIS_PLAYWRIGHT_MODULE || 'playwright');
const fs=require('fs');
(async()=>{
const browser=await chromium.launch({headless:true, ...(process.env.METIS_CHROMIUM_PATH ? {executablePath:process.env.METIS_CHROMIUM_PATH} : {})});
const out=require('path').resolve(__dirname, '../../docs/plans/21-screenshots');fs.mkdirSync(out,{recursive:true});
const products=[{id:'00000000-0000-0000-0000-000000000001',key:'DEMO',name:'Референсный продукт',type:'software',lifecycle:'active',owner:'Демо-команда'},{id:'00000000-0000-0000-0000-000000000002',key:'HUB',name:'Общая платформа',type:'platform',lifecycle:'active',owner:'Демо-команда'}];
for(const [version,port,themes] of [['before',process.env.METIS_BEFORE_PORT || 5179,['light']],['after',process.env.METIS_AFTER_PORT || 5180,['dark','light']]]){
 for(const theme of themes){
 const context=await browser.newContext({viewport:{width:1440,height:1000},colorScheme:theme});
 await context.addInitScript(({theme})=>{localStorage.setItem('apex.theme',theme);sessionStorage.setItem('metis.access_token','synthetic-visual-fixture')},{theme});
 const page=await context.newPage();let errors=[];page.on('pageerror',e=>errors.push(e.message));
 await page.route('**/api/v1/**',route=>{const path=new URL(route.request().url()).pathname.replace('/api/v1','');let body=[];
 if(path==='/me')body={subject:'demo-user',roles:['cpo','admin','finance','compliance'],finance:'full',all_products:'private',products:{},audience:'internal'};
 if(path==='/products')body=products;
 if(path==='/hubs')body=[{product_id:products[1].id,manual:true,computed:false,in_degree:1}];
 if(path==='/links')body=[{id:'link-demo',from_product_id:products[0].id,to_product_id:products[1].id,type:'shared_component',criticality:'blocks'}];
 return route.fulfill({status:200,contentType:'application/json',body:JSON.stringify(body)});
 });
 for(const [name,path] of [['products','/'],['graph','/graph'],['builder','/products/new']]){
 await page.goto(`http://127.0.0.1:${port}${path}`);await page.getByRole('heading', {level:1}).waitFor();await page.waitForTimeout(900);await page.screenshot({path:`${out}/${version}-${name}-${theme}.png`,fullPage:true});
 if(version==='after'){
 if(await page.locator('html').getAttribute('data-theme')!==theme)throw Error('wrong theme');
 }
 }
 if(version==='after'){
 await page.goto(`http://127.0.0.1:${port}/`);await page.getByLabel('Тема оформления').selectOption('system');await page.emulateMedia({colorScheme:theme==='dark'?'light':'dark'});await page.waitForTimeout(100);
 if(await page.locator('html').getAttribute('data-theme')==theme)throw Error('system change ignored');
 await page.getByLabel('Тема оформления').selectOption(theme);await page.reload();
 if(await page.getByLabel('Тема оформления').inputValue()!==theme)throw Error('persistence failed');
 await page.setViewportSize({width:390,height:844});await page.screenshot({path:`${out}/after-mobile-${theme}.png`,fullPage:true});
 await page.emulateMedia({media:'print'});const scheme=await page.locator('html').evaluate(e=>getComputedStyle(e).getPropertyValue('--bg-canvas').trim());if(scheme!=='#f5f7fb')throw Error('print not light');
 }
 if(errors.length)throw Error(errors.join('\n'));await context.close();
 }
}
console.log('DS56 visual: products, graph, builder before/light and after/dark+light; mobile, persistence, system and print PASS. Synthetic API fixtures only.');await browser.close();
})().catch(e=>{console.error(e);process.exit(1)});
