import type { Criticality, Link, LinkType, Me, Product } from '../api/types.ts'

/** Mirrors ActionWriteGraph: the API remains the authority for every mutation. */
export function canWriteGraph(me: Me | undefined, productId: string): boolean {
  if (!me || !productId) return false
  if (me.roles.some(role => ['admin', 'cpo', 'service'].includes(role))) return true
  return me.roles.includes('pm') && (me.products[productId] ?? me.all_products) === 'private'
}

export function filterGraph(products: Product[], links: Link[], filters: { types: ReadonlySet<LinkType>; productId: string; lifecycle: string }) {
  let visibleLinks = links.filter(link => filters.types.has(link.type))
  if (filters.productId) visibleLinks = visibleLinks.filter(link => link.from_product_id === filters.productId || link.to_product_id === filters.productId)
  const neighbors = new Set([filters.productId])
  for (const link of visibleLinks) { neighbors.add(link.from_product_id); neighbors.add(link.to_product_id) }
  const visibleProducts = products.filter(product => (!filters.lifecycle || product.lifecycle === filters.lifecycle) && (!filters.productId || neighbors.has(product.id)))
  const ids = new Set(visibleProducts.map(product => product.id))
  return { products: visibleProducts, links: visibleLinks.filter(link => ids.has(link.from_product_id) && ids.has(link.to_product_id)) }
}

export interface LinkEndpoints { fromProduct: string; toProduct: string; fromFeature?: string; toFeature?: string }

export function linkBody(endpoints: LinkEndpoints, type: LinkType, criticality: Criticality) {
  if (!endpoints.fromProduct || !endpoints.toProduct) throw Error('Выберите оба продукта')
  if (!!endpoints.fromFeature !== !!endpoints.toFeature) throw Error('Выберите обе фичи')
  if (endpoints.fromFeature) return { type, criticality, from_feature_id: endpoints.fromFeature, to_feature_id: endpoints.toFeature }
  return { type, criticality, from_product_id: endpoints.fromProduct, to_product_id: endpoints.toProduct }
}

export function featureHandlesReady(fromId: string | null | undefined, toId: string | null | undefined, from: readonly { id: string }[] | undefined, to: readonly { id: string }[] | undefined): boolean {
  return !!fromId && !!toId && !!from?.some(feature => feature.id === fromId) && !!to?.some(feature => feature.id === toId)
}
