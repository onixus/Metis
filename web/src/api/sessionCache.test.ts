/// <reference types="node" />
import assert from 'node:assert/strict'
import { test } from 'node:test'
import { createSessionClient, discardSessionClient } from './sessionCache.ts'

function deferred<T>() {
  let resolve!: (value: T) => void
  const promise = new Promise<T>((done) => { resolve = done })
  return { promise, resolve }
}

test('TestNFS01_SessionChangeCancelsLatePrivateQuery', async () => {
  const cpo = createSessionClient()
  cpo.setQueryData(['me'], { roles: ['cpo'] })
  const pending = deferred<string>()
  const oldRequest = cpo.fetchQuery({ queryKey: ['private-signals'], queryFn: () => pending.promise }).catch(() => undefined)
  discardSessionClient(cpo)
  const presale = createSessionClient()
  presale.setQueryData(['me'], { roles: ['presale'] })
  pending.resolve('confidential demand')
  await oldRequest
  assert.equal(cpo.getQueryCache().getAll().length, 0)
  assert.equal(presale.getQueryData(['private-signals']), undefined)
  assert.deepEqual(presale.getQueryData(['me']), { roles: ['presale'] })
  discardSessionClient(presale)
})

test('TestNFS01_LateMutationCallbackCannotPopulateNextSession', async () => {
  const cpo = createSessionClient()
  const pending = deferred<string>()
  const mutation = cpo.getMutationCache().build(cpo, {
    mutationFn: () => pending.promise,
    onSuccess: (value) => { cpo.setQueryData(['private-signals'], value) },
  })
  const oldRequest = mutation.execute(undefined)
  discardSessionClient(cpo)
  const presale = createSessionClient()
  pending.resolve('confidential decision')
  await oldRequest
  assert.equal(presale.getQueryData(['private-signals']), undefined)
  assert.equal(presale.getMutationCache().getAll().length, 0)
  discardSessionClient(cpo)
  discardSessionClient(presale)
})
