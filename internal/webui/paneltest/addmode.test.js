import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { createTunnelForMode } from '../panel/js/lib/addmode.js';

function fakeAPI() {
  const calls = [];
  return {
    calls,
    tunnelCreate: async p => { calls.push(['reverse', p]); return { status: 'ok' }; },
    directCreate: async p => { calls.push(['direct', p]); return { status: 'ok' }; },
    nodePair: async p => { calls.push(['paired', p]); return { status: 'ok' }; },
  };
}

test('manual reverse and direct always use local APIs even with a selected Node', async () => {
  const api = fakeAPI();
  const fineTune = { tune: { muxConcurrency: 4 }, peerAddr: '203.0.113.9' };
  await createTunnelForMode(api, 'manual', '', false, fineTune);
  await createTunnelForMode(api, 'manual', 'online-node', false, fineTune);
  await createTunnelForMode(api, 'manual', 'online-node', true, fineTune);
  assert.deepEqual(api.calls, [['reverse', fineTune], ['reverse', fineTune], ['direct', fineTune]]);
});

test('managed mode requires a Node and carries paired settings', async () => {
  const api = fakeAPI();
  await assert.rejects(createTunnelForMode(api, 'managed', '', false, {}), /Choose an online/);
  await createTunnelForMode(api, 'managed', 'online-node', true,
    { carrier: 'spoof' }, { fallbackAddrs: '198.51.100.2' });
  assert.deepEqual(api.calls, [['paired', {
    node: 'online-node', kind: 'direct', direct: { carrier: 'spoof' },
    peerConn: { fallbackAddrs: '198.51.100.2' },
  }]]);
});

test('switching back to manual cannot send a paired request', async () => {
  const api = fakeAPI();
  await createTunnelForMode(api, 'managed', 'online-node', false, { name: 'first' });
  await createTunnelForMode(api, 'manual', 'online-node', false, { name: 'second' });
  assert.deepEqual(api.calls.map(c => c[0]), ['paired', 'reverse']);
});

test('the Add Tunnel view keeps explicit mode and manual handoff', () => {
  const src = readFileSync(new URL('../panel/js/views/add.js', import.meta.url), 'utf8');
  assert.match(src, /let creationMode = 'manual'/);
  assert.match(src, /selectCreationMode\('manual'\)/);
  assert.match(src, /selectCreationMode\('managed'\)/);
  assert.match(src, /Security token: <code>/);
  assert.match(src, /createTunnelForMode\(api, creationMode/);
});
