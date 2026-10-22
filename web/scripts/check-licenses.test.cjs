const { test } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const { checkLicenses } = require('./check-licenses.cjs');
function fixture(extra, fontLicense = 'OFL-1.1') {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), 'metis-license-'));
  const packages = { '@fontsource-variable/inter': fontLicense, '@fontsource-variable/jetbrains-mono': 'OFL-1.1', 'synthetic-code': extra };
  const dependencies = {};
  for (const [name, license] of Object.entries(packages)) {
    const version = name.startsWith('@fontsource') ? '5.3.0' : '1.0.0';
    dependencies[name] = version;
    const dir = path.join(root, 'node_modules', name);
    fs.mkdirSync(dir, { recursive: true });
    fs.writeFileSync(path.join(dir, 'package.json'), JSON.stringify({ name, version, license }));
    fs.writeFileSync(path.join(dir, 'LICENSE'), license);
    if (name.startsWith('@fontsource')) {
      fs.mkdirSync(path.join(root, 'public/licenses'), { recursive: true });
      fs.writeFileSync(path.join(root, 'public/licenses', name.split('/')[1] + '-OFL-1.1.txt'), license);
    }
  }
  fs.writeFileSync(path.join(root, 'package.json'), JSON.stringify({ name: 'synthetic-private-root', private: true, version: '1.0.0', dependencies }));
  return root;
}
test('NF_L02_DS56_font_exception_does_not_admit_unreviewed_OFL_code_or_GPL_fonts', () => {
  for (const [code, font, allowed] of [['MIT', 'OFL-1.1', true], ['OFL-1.1', 'OFL-1.1', false], ['MIT', 'GPL-3.0', false]]) {
    const root = fixture(code, font);
    try { if (allowed) checkLicenses(root); else assert.throws(() => checkLicenses(root)); }
    finally { fs.rmSync(root, { recursive: true, force: true }); }
  }
});
test('NF_L02_DS56_missing_shipped_font_notice_fails', () => {
  const root = fixture('MIT');
  try { fs.unlinkSync(path.join(root, 'public/licenses/inter-OFL-1.1.txt')); assert.throws(() => checkLicenses(root)); }
  finally { fs.rmSync(root, { recursive: true, force: true }); }
});

test('NF_L02_DS56_font_exception_rejects_version_prefix_matches', () => {
  const root = fixture('MIT');
  try {
    const file = path.join(root, 'node_modules/@fontsource-variable/inter/package.json');
    const pkg = JSON.parse(fs.readFileSync(file));
    pkg.version = '5.3.0-beta.1';
    fs.writeFileSync(file, JSON.stringify(pkg));
    const manifest = JSON.parse(fs.readFileSync(path.join(root, 'package.json')));
    manifest.dependencies[pkg.name] = pkg.version;
    fs.writeFileSync(path.join(root, 'package.json'), JSON.stringify(manifest));
    assert.throws(() => checkLicenses(root), /Unreviewed font version/);
  } finally { fs.rmSync(root, { recursive: true, force: true }); }
});
