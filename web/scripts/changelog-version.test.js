// Run with: npm run test:scripts (node --test).
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { newestVersion } from './changelog-version.js';

const head = '# Changelog\n\nIntro with [a link](https://example.com/).\n\n## [Unreleased]\n\n';

test('the newest version section wins', () => {
  assert.equal(newestVersion(head + '## [0.2.0] - 2026-10-29\n\ntext\n\n## [0.1.10] - 2026-10-28\n'), '0.2.0');
});

test('a pre-release version is accepted', () => {
  assert.equal(newestVersion(head + '## [1.0.0-rc.1] - 2026-12-01\n'), '1.0.0-rc.1');
});

test('no version section is an error', () => {
  assert.throws(() => newestVersion(head), /no version section/);
});

test('an invalid newest version is an error', () => {
  assert.throws(() => newestVersion(head + '## [0.2] - 2026-10-29\n## [0.1.10] - 2026-10-28\n'), /invalid version "0\.2"/);
});
