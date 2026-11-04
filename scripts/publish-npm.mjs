import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { chmodSync, readFileSync, readdirSync } from 'node:fs';
import { join } from 'node:path';

const root = join(process.cwd(), 'dist', 'npm');
const folders = readdirSync(root, { withFileTypes: true })
  .filter((entry) => entry.isDirectory())
  .map((entry) => join(root, entry.name));
const packages = folders.map((folder) => ({
  folder,
  manifest: JSON.parse(readFileSync(join(folder, 'package.json'), 'utf8')),
}));
const main = packages.find(({ manifest }) => manifest.name === '@cyber-supplies/cysu');
assert.ok(main, 'main npm package is missing');
assert.equal(packages.length, 7, 'expected the launcher and six platform packages');
const version = process.env.GITHUB_REF_NAME?.replace(/^v/, '');
if (process.argv[2] !== '--dry-run') assert.ok(version, 'a version tag is required');
if (version) assert.equal(main.manifest.version, version);

for (const { manifest } of packages) {
  assert.equal(manifest.version, main.manifest.version);
}
const ordered = [
  ...packages.filter(({ manifest }) => manifest !== main.manifest)
    .sort((a, b) => a.manifest.name.localeCompare(b.manifest.name)),
  main,
];

for (const { folder, manifest } of ordered) {
  // Downloading a GitHub artifact resets file modes; npm needs executable bins.
  for (const binary of Object.values(manifest.bin ?? {})) {
    chmodSync(join(folder, binary), 0o755);
  }
  const dryRun = process.argv[2] === '--dry-run';
  const args = dryRun
    ? ['pack', '--dry-run', folder]
    : ['publish', folder, '--access', 'public'];
  console.log(`${dryRun ? 'Checking' : 'Publishing'} ${manifest.name}@${manifest.version}`);
  const result = spawnSync('npm', args, { stdio: 'inherit', shell: process.platform === 'win32' });
  assert.equal(result.status, 0, `npm failed for ${manifest.name}`);
}
