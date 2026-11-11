const { test } = require('node:test');
const assert = require('node:assert/strict');
const { spawnSync } = require('node:child_process');
const path = require('node:path');
const script = path.join(__dirname, '../../scripts/docker-image-tags.sh');
const tags = (image, ref) => spawnSync('bash', [script, image, ref], { encoding: 'utf8' });

test('stable tags promote both images to latest', () => {
  for (const image of ['blazux/prism', 'blazux/prism-workspace']) {
    const result = tags(image, 'refs/tags/v1.0.0');
    assert.equal(result.status, 0, result.stderr);
    assert.equal(result.stdout.trim(), `${image}:v1.0.0,${image}:latest`);
    assert.equal(result.stderr, '');
  }
});
test('prereleases are versioned without changing latest or unstable', () => {
  for (const version of ['v1.0.0-rc.1', 'v1.0.0-beta.2', 'v1.0.0-alpha', 'v2.10.3-preview.0']) {
    const result = tags('blazux/prism', 'refs/tags/' + version);
    assert.equal(result.status, 0, result.stderr);
    assert.equal(result.stdout.trim(), 'blazux/prism:' + version);
  }
});
test('branches publish only unstable, including branches resembling versions', () => {
  for (const ref of ['refs/heads/main', 'refs/heads/fix/tagging', 'refs/heads/v1.0.0']) {
    const result = tags('blazux/prism', ref);
    assert.equal(result.status, 0, result.stderr);
    assert.equal(result.stdout.trim(), 'blazux/prism:unstable');
  }
});
test('malformed release versions fail before any tag is emitted', () => {
  for (const version of ['v1', 'v1.0', '1.0.0', 'v01.0.0', 'v1.00.0', 'v1.0.00', 'v1.0.0-', 'v1.0.0-rc..1', 'v1.0.0-01', 'v1.0.0-rc.01', 'v1.0.0+build.1', 'v1.0.0 bad', 'v1.0.0\nlatest', 'v1.0.0-' + 'a'.repeat(125)]) {
    const result = tags('blazux/prism', 'refs/tags/' + version);
    assert.equal(result.status, 2, version);
    assert.equal(result.stdout, '', version);
    assert.match(result.stderr, /Invalid release ref/, version);
  }
  for (const ref of ['', 'refs/heads/', 'refs/pull/1/merge']) {
    const result = tags('blazux/prism', ref);
    assert.equal(result.status, 2, ref);
    assert.equal(result.stdout, '', ref);
  }
});
