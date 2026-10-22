const { test } = require('node:test');
const assert = require('node:assert/strict');
const { verifiedProbe } = require('./acceptance-probe-ds56.cjs');
test('DS56_NFS01_all_recorded_GET_probes_reject_access_leaks_denials_and_server_failures', () => {
  for (const path of ['/decisions', '/products/synthetic/signals', '/products/synthetic/economics/2026-09']) {
    for (const allowed of [true, false]) {
      const status = allowed ? 200 : 403;
      assert.equal(verifiedProbe('synthetic-role', path, status, allowed).expectedStatus, status);
      for (const wrong of [allowed ? 403 : 200, 401, 404, 500])
        assert.throws(() => verifiedProbe('synthetic-role', path, wrong, allowed));
    }
  }
});
