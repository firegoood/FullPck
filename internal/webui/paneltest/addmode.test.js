import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { createTunnelForMode, managedNodeSelection } from '../panel/js/lib/addmode.js';

function fakeAPI() {
  const calls = [];
  return {
    calls,
    tunnelCreate: async p => { calls.push(['reverse', p]); return { status: 'ok' }; },
    directCreate: async p => { calls.push(['direct', p]); return { status: 'ok' }; },
    nodePair: async p => { calls.push(['paired', p]); return { status: 'ok' }; },
  };
}

test('refresh never silently changes the chosen managed destination', () => {
  const offline = { name: 'TR', online: false };
  const other = { name: 'DE', online: true };
  assert.equal(managedNodeSelection('TR', [offline, other], 'managed'), 'TR');
  assert.equal(managedNodeSelection('TR', [other], 'managed'), '');
  assert.equal(managedNodeSelection('', [other], 'managed'), 'DE');
  assert.equal(managedNodeSelection('', [other], 'manual'), '');
  assert.equal(managedNodeSelection('', [other, { ...offline, online: true }], 'managed'), '');
});

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

test('managed reverse and direct creation use the real API and panel base path', async () => {
  const oldDocument = globalThis.document, oldFetch = globalThis.fetch;
  globalThis.document = { documentElement: { dataset: { base: '/secret-panel' } } };
  const sent = [];
  globalThis.fetch = async (url, options) => {
    sent.push({ url, method: options.method, body: JSON.parse(options.body) });
    return new Response(JSON.stringify({ status: 'ok' }));
  };
  try {
    const api = await import('../panel/js/api.js');
    await createTunnelForMode(api, 'managed', 'kharej', false, { name: 'rev', transport: 'tcp' });
    await createTunnelForMode(api, 'managed', 'kharej', true, { name: 'dir', carrier: 'spoof' });
    assert.deepEqual(sent, [
      { url: '/secret-panel/api/node/pair', method: 'POST', body: { node: 'kharej', kind: 'reverse', tunnel: { name: 'rev', transport: 'tcp' } } },
      { url: '/secret-panel/api/node/pair', method: 'POST', body: { node: 'kharej', kind: 'direct', direct: { name: 'dir', carrier: 'spoof' } } },
    ]);
  } finally { globalThis.document = oldDocument; globalThis.fetch = oldFetch; }
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
