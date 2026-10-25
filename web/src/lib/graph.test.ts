import { test } from 'node:test'
import assert from 'node:assert/strict'
import { canWriteGraph, featureHandlesReady, filterGraph, linkBody } from './graph.ts'
import type { Link, Me, Product } from '../api/types.ts'

const product = (id: string, lifecycle = 'active') => ({ id, lifecycle }) as Product
const link = (id: string, from: string, to: string, type = 'integration') => ({ id, from_product_id: from, to_product_id: to, type }) as Link

test('PG09_filters_intersect_type_product_status_without_dangling_edges', () => {
  const products = [product('a'), product('b'), product('c', 'retired'), product('d')]
  const links = [link('ab', 'a', 'b'), link('bc', 'b', 'c'), link('ad', 'a', 'd', 'commercial')]
  const visible = filterGraph(products, links, { types: new Set(['integration']), productId: 'b', lifecycle: 'active' })
  assert.deepEqual(visible.products.map(p => p.id), ['a', 'b'])
  assert.deepEqual(visible.links.map(l => l.id), ['ab'])
  assert.deepEqual(filterGraph(products, links, { types: new Set(), productId: '', lifecycle: '' }).links, [])
})
test('PG03_PG05_endpoint_validation_never_downgrades_mixed_feature_connection', () => {
  assert.throws(() => linkBody({ fromProduct: 'a', toProduct: 'b', fromFeature: 'f' }, 'integration', 'blocks'), /обе фичи/)
  assert.throws(() => linkBody({ fromProduct: 'a', toProduct: '' }, 'commercial', 'desirable'), /оба продукта/)
  assert.deepEqual(linkBody({ fromProduct: 'a', toProduct: 'b', fromFeature: 'f', toFeature: 'g' }, 'integration', 'blocks'), { type: 'integration', criticality: 'blocks', from_feature_id: 'f', to_feature_id: 'g' })
})
test('PG09_feature_handles_require_both_loaded_endpoints', () => {
  assert.equal(featureHandlesReady('f', 'g', [{ id: 'f' }], undefined), false)
  assert.equal(featureHandlesReady('f', 'g', [{ id: 'other' }], [{ id: 'g' }]), false)
  assert.equal(featureHandlesReady('f', 'g', [{ id: 'f' }], [{ id: 'g' }]), true)
})
test('AD02_PG03_graph_write_gate_denies_strategic_and_unassigned_pm', () => {
  const me: Me = { subject: 'synthetic', finance: 'none', audience: 'internal', roles: ['pm'], all_products: 'none', products: { a: 'private', b: 'strategic' } }
  assert.equal(canWriteGraph(undefined, 'a'), false)
  assert.equal(canWriteGraph(me, 'a'), true)
  for (const id of ['b', 'c', '']) assert.equal(canWriteGraph(me, id), false)
  assert.equal(canWriteGraph({ ...me, roles: ['marketing'] }, 'a'), false)
  assert.equal(canWriteGraph({ ...me, roles: ['cpo'] }, 'b'), true)
})
