// NF-L02 / ADR-0014,0017: an OFL exception is bound to reviewed font versions.
const { execFileSync } = require('node:child_process');
const { readFileSync } = require('node:fs');
const path = require('node:path');
const checker = require.resolve('license-checker-rseidelsohn/bin/license-checker-rseidelsohn.js');
const fonts = ['@fontsource-variable/inter@5.3.0', '@fontsource-variable/jetbrains-mono@5.3.0'];
const allowed = 'MIT;Apache-2.0;BSD-2-Clause;BSD-3-Clause;ISC;0BSD;Zlib;MPL-2.0;CC0-1.0;Unlicense;Python-2.0;BlueOak-1.0.0';
function checkLicenses(start) {
  const run = args => execFileSync(process.execPath, [checker, '--start', start, '--production', '--excludePrivatePackages', ...args], { stdio: 'pipe' });
  // Checker package filters use startsWith, so enforce exact identities first.
  const inventory = JSON.parse(run(['--json']).toString());
  for (const id of Object.keys(inventory)) {
    if (fonts.some(font => id.startsWith(font)) && !fonts.includes(id))
      throw Error(`Unreviewed font version: ${id}`);
  }
  for (const font of fonts) {
    if (!inventory[font]) throw Error(`Reviewed font missing: ${font}`);
  }
  run(['--onlyAllow', allowed, '--excludePackages', fonts.join(';')]);
  run(['--onlyAllow', 'OFL-1.1', '--includePackages', fonts.join(';')]);
  for (const [font, notice] of [['inter', 'inter-OFL-1.1.txt'], ['jetbrains-mono', 'jetbrains-mono-OFL-1.1.txt']]) {
    const installed = readFileSync(path.join(start, 'node_modules/@fontsource-variable', font, 'LICENSE'), 'utf8');
    if (readFileSync(path.join(start, 'public/licenses', notice), 'utf8') !== installed)
      throw Error(`Shipped notice differs for ${font}`);
  }
}
module.exports = { checkLicenses };
if (require.main === module) {
  try { checkLicenses(path.resolve(__dirname, '..')); console.log('NF-L02 production licenses and font notices PASS'); }
  catch (error) { console.error(error.stderr?.toString() || error.message); process.exitCode = 1; }
}
