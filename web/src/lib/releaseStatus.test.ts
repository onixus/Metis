/// <reference types="node" />
import assert from 'node:assert/strict'
import { test } from 'node:test'
import { releaseStatuses, resolveReleaseStatus, type ReleaseStatusChoice } from './releaseStatus.ts'

test('TestRM05_UntouchedEditorTracksLatestStatusWithoutRequestingTransition', () => {
  for (const current of ['planned', 'ready_for_certification', 'released', 'eol'] as const) {
    assert.deepEqual(resolveReleaseStatus(current, null), { value: current, update: undefined })
    assert.deepEqual(resolveReleaseStatus(current, { from: current, to: current }), { value: current, update: undefined })
  }
})

test('TestRM04_ExplicitTransitionRequiresTheSourceStatusTheUserSaw', () => {
  const draft: ReleaseStatusChoice = { from: 'planned', to: 'released' }
  assert.deepEqual(resolveReleaseStatus('planned', draft), { value: 'released', update: 'released' })
  // Readiness changed on the server while this form was open. The editor must
  // display readiness and omit the now-stale transition, keeping text edits intact.
  assert.deepEqual(resolveReleaseStatus('ready_for_certification', draft), { value: 'ready_for_certification', update: undefined })
  assert.deepEqual(resolveReleaseStatus('released', draft), { value: 'released', update: undefined })
  assert.deepEqual(resolveReleaseStatus('eol', draft), { value: 'eol', update: undefined })
  assert.deepEqual(resolveReleaseStatus('released', { from: 'released', to: 'eol' }), { value: 'eol', update: 'eol' })
})

test('TestRM04_EditorCannotRequestReadinessOrReopenAnIssuedRelease', () => {
  assert.deepEqual(releaseStatuses('planned'), ['planned', 'released'])
  assert.deepEqual(releaseStatuses('ready_for_certification'), ['ready_for_certification', 'released'])
  assert.deepEqual(releaseStatuses('released'), ['released', 'eol'])
  assert.deepEqual(releaseStatuses('eol'), ['eol'])
  for (const choice of [
    { from: 'planned', to: 'ready_for_certification' },
    { from: 'released', to: 'planned' },
    { from: 'eol', to: 'released' },
  ] as ReleaseStatusChoice[]) {
    assert.deepEqual(resolveReleaseStatus(choice.from, choice), { value: choice.from, update: undefined })
  }
})
