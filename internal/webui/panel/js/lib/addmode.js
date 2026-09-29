// Keep the creation route independent of incidental selector and Node state.
export async function createTunnelForMode(api, mode, node, direct, payload, peerConn) {
  if (mode === 'manual') {
    return direct ? api.directCreate(payload) : api.tunnelCreate(payload);
  }
  if (mode !== 'managed') throw new Error('Unknown tunnel creation mode');
  if (!node) throw new Error('Choose an online managed server');
  return api.nodePair({
    node,
    kind: direct ? 'direct' : 'reverse',
    [direct ? 'direct' : 'tunnel']: payload,
    ...(peerConn ? { peerConn } : {}),
  });
}
