// Keep the creation route independent of incidental selector and Node state.
export async function createTunnelForMode(api, mode, node, direct, payload, peerConn) {
  if (mode === 'manual') {
    return direct ? api.directCreate(payload) : api.tunnelCreate(payload);
  }
  if (mode !== 'managed') throw new Error('Unknown tunnel creation mode');
  if (!node) {
    const error = new Error('Choose an online managed server');
    error.fix = 'managed-node';
    throw error;
  }
  return api.nodePair({
    node,
    kind: direct ? 'direct' : 'reverse',
    [direct ? 'direct' : 'tunnel']: payload,
    ...(peerConn ? { peerConn } : {}),
  });
}

// A status refresh must never choose a different destination for the user.
// Keep an existing offline choice; a removed choice requires a new selection.
export function managedNodeSelection(previous, nodes, mode) {
  if (previous) return nodes.some(n => n.name === previous) ? previous : '';
  const live = nodes.filter(n => n.online && !n.revoked);
  return mode === 'managed' && live.length === 1 ? live[0].name : '';
}
