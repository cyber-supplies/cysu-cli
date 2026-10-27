import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { chmodSync, cpSync, mkdirSync, readFileSync, readdirSync } from 'node:fs';
import { join } from 'node:path';

const packageRoot = join(process.cwd(), 'dist', 'npm');
const readPackage = (folder) => JSON.parse(readFileSync(join(folder, 'package.json'), 'utf8'));
const mainFolder = join(packageRoot, 'cysu');
const main = readPackage(mainFolder);
const expectedPlatforms = [
  ['darwin', 'x64'],
  ['darwin', 'arm64'],
  ['linux', 'x64'],
  ['linux', 'arm64'],
  ['win32', 'x64'],
  ['win32', 'arm64'],
];

assert.equal(main.name, '@cyber-supplies/cysu');
assert.equal(main.bin.cysu, 'index.js');
if (process.env.GITHUB_REF_NAME) {
  assert.equal(main.version, process.env.GITHUB_REF_NAME.replace(/^v/, ''));
}

const platforms = readdirSync(packageRoot, { withFileTypes: true })
  .filter((entry) => entry.isDirectory() && entry.name !== 'cysu')
  .map((entry) => {
    const folder = join(packageRoot, entry.name);
    return { folder, manifest: readPackage(folder) };
  });

assert.equal(platforms.length, expectedPlatforms.length);
assert.equal(Object.keys(main.optionalDependencies).length, expectedPlatforms.length);
for (const [os, cpu] of expectedPlatforms) {
  const matches = platforms.filter(({ manifest }) =>
    manifest.os?.includes(os) && manifest.cpu?.includes(cpu));
  assert.equal(matches.length, 1, `expected one package for ${os}/${cpu}`);
  const manifest = matches[0].manifest;
  assert.equal(manifest.version, main.version);
  assert.equal(main.optionalDependencies[manifest.name], main.version);
}

const current = platforms.find(({ manifest }) =>
  manifest.os.includes(process.platform) && manifest.cpu.includes(process.arch));
assert.ok(current, `no package for ${process.platform}/${process.arch}`);
const packageName = current.manifest.name.split('/')[1];
const installedFolder = join(mainFolder, 'node_modules', '@cyber-supplies', packageName);
mkdirSync(installedFolder, { recursive: true });
cpSync(current.folder, installedFolder, { recursive: true });
const binary = join(installedFolder, Object.values(current.manifest.bin)[0]);
if (process.platform !== 'win32') chmodSync(binary, 0o755);

const run = spawnSync(process.execPath, [join(mainFolder, 'index.js'), 'help'], {
  encoding: 'utf8',
});
assert.equal(run.status, 0, run.stderr || run.stdout);
assert.ok((run.stdout + run.stderr).includes(`cysu ${main.version}`));
console.log(`Verified ${main.name}@${main.version} on ${process.platform}/${process.arch}`);
