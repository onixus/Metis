// PG03/04/08, AD02: real synthetic loopback API regression for PR60 review fixes.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const base = process.env.METIS_ACCEPTANCE_URL;
const root = process.env.METIS_ACCEPTANCE_TEMP;
if (!base || !root || !['127.0.0.1', 'localhost'].includes(new URL(base).hostname)) throw Error('Isolated loopback stand required');
const token = JSON.parse(fs.readFileSync(path.join(root, 'tokens.json'))).cpo;
async function request(route, method = 'GET', body, status = 200) {
  const response = await fetch(base + '/api/v1' + route, { method, headers: { Authorization: 'Bearer ' + token, 'Content-Type': 'application/json' }, ...(body ? { body: JSON.stringify(body) } : {}) });
  assert.equal(response.status, status, `${method} ${route}: ${await response.clone().text()}`);
  return response.json();
}
(async () => {
  const stamp = String(Date.now());
  const products = [];
  for (const kind of ['provider', 'consumer', 'downstream', 'other']) products.push(await request('/products', 'POST', { key: `review-${kind}-${stamp}`, name: `Synthetic review ${kind}`, type: 'security' }, 201));
  const createFeature = (product, name, planned_date) => request(`/products/${product.id}/features`, 'POST', { name, status: 'planned', planned_date }, 201);
  const source = await createFeature(products[0], 'Supplier', '2027-01-01');
  const replacement = await createFeature(products[0], 'Replacement', '2027-03-01');
  const consumer = await createFeature(products[1], 'Consumer', '2027-01-01');
  const downstream = await createFeature(products[2], 'Downstream', '2027-01-01');
  await request('/links', 'POST', { type: 'integration', criticality: 'blocks', from_feature_id: downstream.id, to_feature_id: consumer.id }, 201);
  const body = { name: 'Synthetic contract review', provider_product_id: products[0].id, consumer_product_id: products[1].id, provider_feature_ids: [source.id], consumer_feature_ids: [consumer.id], criticality: 'blocks' };
  const contract = await request('/contracts', 'POST', body, 201);
  await request(`/features/${source.id}/shift-date`, 'POST', { planned_date: '2027-02-01', reason: 'Synthetic regression' });
  for (const feature of [consumer, downstream]) assert.equal((await request(`/features/${feature.id}`)).implied_date, '2027-02-01');
  body.provider_feature_ids = [replacement.id];
  await request(`/contracts/${contract.id}`, 'PUT', body);
  for (const feature of [consumer, downstream]) assert.equal((await request(`/features/${feature.id}`)).implied_date, '2027-03-01');
  body.provider_feature_ids = [];
  await request(`/contracts/${contract.id}`, 'PUT', body);
  for (const feature of [consumer, downstream]) {
    const current = await request(`/features/${feature.id}`);
    assert.equal(current.affected, false); assert.equal(current.implied_date, undefined); assert.equal(current.affected_by, undefined);
    assert.equal(current.planned_date, '2027-01-01');
  }
  assert.equal((await request(`/contracts/${contract.id}`)).ready_date, '2027-01-01');
  const before = (await request('/links')).length;
  for (const invalid of [
    { type: 'integration', from_product_id: products[2].id, to_product_id: products[3].id },
    { type: 'integration', from_product_id: products[0].id, to_product_id: products[1].id },
    { type: 'commercial', from_product_id: products[1].id, to_product_id: products[0].id },
  ]) await request('/links', 'POST', { ...invalid, criticality: 'blocks', contract_id: contract.id }, 400);
  assert.equal((await request('/links')).length, before);
  const result = { passed: true, checks: ['PG04_PG08_rebind_remove_clears_consumer_and_downstream', 'PG10_ready_date_tracks_actual_dependencies', 'AD02_NFS01_foreign_reversed_nonintegration_contract_associations_rejected'] };
  fs.writeFileSync(path.join(root, 'contract-review31-results.json'), JSON.stringify(result, null, 2));
  console.log(JSON.stringify(result));
})().catch(error => { console.error(error); process.exitCode = 1; });
