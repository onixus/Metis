const assert = require('node:assert/strict');
// Return a row only after the authorization contract has actually been asserted.
function verifiedProbe(role, path, status, allowed) {
  const expectedStatus = allowed ? 200 : 403;
  assert.equal(status, expectedStatus, `${role} ${path}`);
  return { role, path, status, expectedStatus };
}
module.exports = { verifiedProbe };
