/* Add a tunnel: pick the side, then the transport family, then the transport,
 * then the settings that side actually has.
 *
 * The families and the presets are served rather than written into the page, so
 * a transport added to the CLI menu appears here on its own and the two can
 * never describe different things.
 *
 * CLI: 1 Setup Iran, 2 Setup Kharej.
 */

import { $$, el, esc, dialogSubtitle } from '../lib/dom.js';
import { isUp } from '../lib/tstate.js';
import { NUMERIC } from '../lib/numeric.js';
import * as api from '../api.js';
import { setupLinkHTML, bindSetupLink } from '../ui/setuplink.js';
import * as store from '../store.js';
import { openScreen } from '../ui/screen.js';
import { oops, toast } from '../ui/toast.js';
import { go } from '../router.js';
import { createTunnelForMode } from '../lib/addmode.js';

export function addView(ctx) {
  openScreen('add', {
    pick: '.dlg',
    bind: async (root, close) => {
      dialogSubtitle(root, store.get().stats, 'this server’s end — the other is built from its setup link');
      let opts = { families: [], presets: [] };
      try { opts = await api.tunnelOptions(); } catch (e) { oops(e); }

      /* families */
      /* The family list, the variants and the presets are all painted from
         /api/tunnel/options below, once the shape is known. */

      /* Steps and the pick-one groups are the preview's own handlers, rebound
         in screen.js. What is chosen is remembered here, and the form follows:
         reverse and direct are two different shapes, and each side of a tunnel
         is asked for different things — the Iran side owns the forwarded ports,
         the kharej side owns the address that is dialled. */
      const chosen = { side: 'server', direction: 'reverse', transport: null,
                       family: 0, carrier: 'pck', preset: null };

      /* The preset the form is already showing counts as chosen.
       *
       * It started as null and was only filled when somebody pressed one of
       * the buttons — but one of them is marked `on` in the markup, so the
       * form opens with Balance visibly selected. Accepting what is on screen
       * therefore sent no preset at all, and the tunnel was written without
       * one: the config had no preset line, the edit dialog read back an empty
       * value, and its menu fell to whichever option the preview was drawn
       * with. What the operator saw when they made the tunnel and what they
       * saw when they edited it disagreed, and neither was wrong about the
       * other — nothing had been recorded either way.
       *
       * Read from the markup rather than hard-coded, so the default is
       * whichever button carries `on`, and it stays right if that changes. */
      /* Reverse and direct each have a row of presets, and both are in the
         page. The first `.rp.on` in the document is the reverse row's, so a
         direct tunnel read Balance from a row it could not see. Only the row
         in the shape on screen counts — a step being hidden is not the row
         being hidden, which is why steps are passed over. */
      const inShape = n => {
        for (let at = n; at && at !== root; at = at.parentElement) {
          if (at.hidden && !at.classList.contains('step')) return false;
        }
        return true;
      };
      const markedPreset = () => {
        const live = [...root.querySelectorAll('.rp')].filter(inShape);
        const b = live.find(x => x.classList.contains('on')) || live[0];
        return (b?.querySelector('.key2, .k3')?.textContent || b?.dataset.pre || '')
          .trim().toLowerCase() || null;
      };

      const show = (sel, on) => root.querySelectorAll(sel)
        .forEach(n => { n.hidden = !on; });

      /* What each transport actually has, taken from the predicates in
         internal/manage/config.go rather than guessed:
           isMux   tcpmux, wsmux, wssmux, kcp, xdi, spoof, pck  -> the mux_* knobs
           isKCP   kcp, xdi, spoof, pck                          -> the kcp_* knobs and FEC
           needsTLS wss, wssmux                                  -> a certificate
         and the two raw carriers own their own drawer. A field that does not
         belong to the chosen transport is not shown, because writing it would
         put a key in the config that transport never reads. */
      const isMux = t => ['tcpmux', 'wsmux', 'wssmux', 'kcp', 'xdi', 'spoof', 'pck'].includes(t);
      const isKCP = t => ['kcp', 'xdi', 'spoof', 'pck'].includes(t);
      const needsTLS = t => ['wss', 'wssmux'].includes(t);
      /* Both taken from internal/manage/config.go, because the server refuses
         these combinations by name and a form that offers them is a form that
         collects an answer only to have it rejected. */
      const isWS = t => ['ws', 'wss', 'wsmux', 'wssmux'].includes(t);
      const isDatagram = t => ['udp', 'kcp', 'xdi', 'quic', 'pck'].includes(t);

      /* dw-ft fine tune · dw-sp spoof · dw-pk packet carrier · dw-cn connection */
      const drawer = id => root.querySelector('#dw-' + id);

      function applyFields() {
        const t = chosen.transport || '';
        const on = (sel, yes) => root.querySelectorAll(sel).forEach(n => {
          const row = n.closest('.f3, .tg3, .fhead') || n;
          row.hidden = !yes;
        });
        on('[name^="tune.mux"]', isMux(t));
        on('[name^="tune.kcp"]', isKCP(t));
        on('[data-when="tls"]', needsTLS(t));

        /* The packet-carrier drawer only means anything on its own transport —
           on plain TCP it was offering settings nothing reads. The spoof drawer
           is not here at all any more: spoof is a direct carrier, so applyShape
           decides it. */
        const pk = drawer('pk');
        if (pk) { pk.hidden = t !== 'pck'; if (t !== 'pck') pk.classList.remove('open'); }

        /* The other server's settings follow the transport too.
         *
         * They were dead markup before — conn.edgeIP carried data-when="client-ws",
         * a value nothing matches, so it never appeared at all. Moving them into
         * a section of their own made them appear on every transport instead,
         * which is worse: a CDN edge offered on a plain TCP tunnel is a question
         * with no right answer, and the server rejects it by name if it is
         * filled in.
         *
         * ConnTune.apply is the authority for both of these. */
        on('[name="peerConn.edgeIP"]', isWS(t));
        on('[name="peerConn.proxy"]', !isDatagram(t));
      }

      /* Throughput is offered only where it applies — presetSuitsTransport. */
      function applyPresets() {
        const t = chosen.transport || '';
        root.querySelectorAll('.rp').forEach(b => {
          const key = b.querySelector('.key2')?.textContent.trim();
          const hide = key === 'throughput' && t !== 'kcp';
          b.hidden = hide;
          if (hide && b.classList.contains('on')) {
            b.classList.remove('on');
            root.querySelector('.rp:not([hidden])')?.classList.add('on');
          }
        });
        /* Whatever is marked is what will be sent. The `on` class is what the
           operator can see, so it is the one source of truth here — and it is
           re-read after the list changes, because Throughput disappearing moves
           the selection and the payload has to follow the screen. */
        chosen.preset = markedPreset();
      }

      /* The variants come from /api/tunnel/options, so a transport added to the
         CLI menu appears here on its own — and picking a family actually
         changes the list, which it did not before. */
      /* The families come from the server too.
       *
       * They were four hard-coded buttons, so removing the Experimental family
       * from the source left the button behind — pointing at a family that no
       * longer exists. Drawn from the same answer as the transports, the two
       * cannot drift apart again. */
      function paintFamilies() {
        const row = root.querySelector('.fam');
        if (!row || !opts.families?.length) return;
        row.innerHTML = opts.families.map((f, i) =>
          `<button class="${i === (chosen.family ?? 0) ? 'on' : ''}" data-fn="setFam" data-args="'${i}'">
            <b>${esc(f.label)}</b></button>`).join('');
      }

      /* The direct carriers, painted from the server rather than written into
         the page.
         
         They were a fourth hardcoded list — after the CLI wizard's, the panel's
         options endpoint and the create path's — and the four drifted: a
         carrier added to the others was offered nowhere here, and the endpoint
         behind this screen refused what this screen did not know about. There
         is one list now and this reads it. */
      async function paintCarriers() {
        const grid = root.querySelector('.step3direct .trgrid');
        if (!grid) return;
        let carriers = [];
        try { carriers = (await api.directOptions()).carriers || []; } catch (e) { return; }
        if (!carriers.length) return;
        const marked = grid.querySelector('.dc.on')?.dataset.car;
        const initial = carriers.find(c => c.value === marked) || carriers[0];
        grid.innerHTML = carriers.map((c, i) => {
          const needsRoot = c.needsRoot ? '<span class="root2">needs root</span>' : '';
          return `<button class="dc${c.value === initial.value ? ' on' : ''}" data-car="${esc(c.value)}"
            data-fn="setCar" data-args="'${esc(c.value)}'" title="${esc(c.desc || '')}">
            <span class="tn2"><b>${esc(c.label)}</b>
            <span class="key2">${esc(c.value)}</span>${needsRoot}</span></button>`;
        }).join('');
        chosen.carrier = initial.value;
        applyShape();
      }
      paintCarriers();

      function paintTransports() {
        const list = root.querySelector('#trlist');
        const fam = opts.families?.[chosen.family ?? 0];
        if (!list || !fam) return;
        list.innerHTML = fam.entries.map((e, i) => {
          const root2 = /needs root/i.test(e.desc || '') ? '<span class="root2">needs root</span>' : '';
          return `<button class="tr${i === 0 ? ' on' : ''}" data-fn="setTr" data-args="'${e.value}'">
            <span class="tn2"><b>${esc(e.label)}</b>
            <span class="key2">${esc(e.value)}</span>${root2}</span></button>`;
        }).join('');
        chosen.transport = fam.entries[0]?.value || null;
        applyFields();
        applyPresets();
      }

      /* The drawers' controls were drawings.
       *
       * Every switch in the advanced drawers is a <div class="sw3"> and every
       * dropdown a <div class="sel4"> — the preview drew them that way, and
       * nothing ever made them controls. They have no name, so nothing they
       * showed was ever submitted, and the dropdowns opened no menu at all
       * because there was none to open. Fourteen switches and nine menus, all
       * of them ornamental.
       *
       * The ids already say what each one is: ft-nodelay, pk-flags, sp-profile,
       * cn-simpleAuth — a drawer prefix and the field's own name. So the wiring
       * is derived rather than listed: a hidden input named after the field
       * carries the value, and the drawing in front of it drives that input.
       * Nothing here invents a setting; every name below exists in FineTune,
       * SpoofTune, PckTune or ConnTune.
       */
      const DRAWER_GROUP = { ft: 'tune', sp: 'spoof', pk: 'pck', cn: 'conn' };

      const nameOfControl = (id, explicit) => {
        // A control outside the drawers says what it sets outright: the naming
        // convention only holds where there is a drawer to take the prefix from.
        if (explicit) return explicit;
        const at = id.indexOf('-');
        if (at < 0) return '';
        const group = DRAWER_GROUP[id.slice(0, at)];
        const field = id.slice(at + 1);
        return group && field ? `${group}.${field}` : '';
      };

      /* What each menu can be set to. The lists the server sends are used where
         it sends one, so the panel never offers an interface the machine does
         not have or a profile the engine does not know. */
      function choicesFor(id) {
        const auto = { value: '', label: 'Automatic — let the kernel choose the route' };
        const ifaces = () => [auto, ...(opts.interfaces || []).map(v => ({ value: v, label: v }))];
        switch (id) {
          case 'ft-logLevel':
            return ['debug', 'info', 'warn', 'error'].map(v => ({ value: v, label: v }));
          case 'sp-profile':
            return (opts.spoofProfiles || []).map(v => ({ value: v, label: v }));
          case 'sp-uplink':
          case 'sp-downlink':
            return [{ value: '', label: 'Same as the packet profile' },
              ...(opts.spoofProfiles || []).map(v => ({ value: v, label: v }))];
          case 'pk-flags':
            return [{ value: '', label: 'Default — push+ack, what a connection carrying data sends' },
              ...(opts.pckFlags || []).map(v => ({ value: v, label: v }))];
          default:
            return id.endsWith('interface') || id.endsWith('Iface') ? ifaces() : [];
        }
      }

      function wireDrawerControls() {
        /* Plain inputs in the drawers had ids and no names either — sp-mtu,
           sp-portMin, sp-sockBuf and the rest. Same convention, same fix: the
           id says which field it is, so it gets that name. */
        root.querySelectorAll('input[id]:not([name])').forEach(i => {
          const name = nameOfControl(i.id, i.dataset.name);
          if (name) i.name = name;
        });

        root.querySelectorAll('.sw3[id], .sw3[data-name]').forEach(sw => {
          const name = nameOfControl(sw.id || '', sw.dataset.name);
          if (!name || sw.dataset.wired) return;
          sw.dataset.wired = '1';
          const input = el('input', { type: 'checkbox', name, hidden: true });
          input.checked = sw.classList.contains('on');
          input.dataset.drawn = '1';
          sw.after(input);
          sw.setAttribute('role', 'switch');
          sw.setAttribute('aria-checked', String(input.checked));
          sw.tabIndex = 0;
          const flip = () => {
            sw.classList.toggle('on');
            input.checked = sw.classList.contains('on');
            delete input.dataset.drawn;
            sw.setAttribute('aria-checked', String(input.checked));
          };
          sw.addEventListener('click', flip);
          sw.addEventListener('keydown', ev => {
            if (ev.key === ' ' || ev.key === 'Enter') { ev.preventDefault(); flip(); }
          });
        });

        root.querySelectorAll('.sel4[id], .sel4[data-name]').forEach(sel => {
          const name = nameOfControl(sel.id || '', sel.dataset.name);
          const choices = choicesFor(sel.id);
          if (!name || !choices.length || sel.dataset.wired) return;
          sel.dataset.wired = '1';

          const input = el('input', { type: 'text', name, hidden: true });
          // The label it was drawn with is the default, so an untouched menu
          // means what it appears to mean.
          const shown = sel.childNodes[0]?.textContent?.trim() || '';
          const start = choices.find(c => c.label === shown) || choices[0];
          input.value = start.value;
          input.dataset.drawn = '1';
          sel.after(input);
          sel.setAttribute('role', 'combobox');
          sel.tabIndex = 0;

          const menu = el('div', { class: 'sel4menu', hidden: true });
          choices.forEach(c => {
            const opt = el('button', { type: 'button', class: 'sel4opt', text: c.label });
            opt.addEventListener('click', ev => {
              ev.stopPropagation();
              input.value = c.value;
              delete input.dataset.drawn;
              sel.childNodes[0].textContent = c.label;
              close4();
            });
            menu.append(opt);
          });
          sel.append(menu);

          const close4 = () => { menu.hidden = true; sel.classList.remove('open4'); };
          const open4 = () => {
            // One at a time: two menus open at once is two answers to one
            // question, and the second click lands on whichever is on top.
            root.querySelectorAll('.sel4menu').forEach(m => { m.hidden = true; });
            root.querySelectorAll('.sel4.open4').forEach(x => x.classList.remove('open4'));
            menu.hidden = false;
            sel.classList.add('open4');
          };
          sel.addEventListener('click', ev => {
            if (ev.target.closest('.sel4opt')) return;
            menu.hidden ? open4() : close4();
          });
          sel.addEventListener('keydown', ev => {
            if (ev.key === ' ' || ev.key === 'Enter') { ev.preventDefault(); menu.hidden ? open4() : close4(); }
            if (ev.key === 'Escape') close4();
          });
        });

        // A click anywhere else closes whatever is open.
        root.addEventListener('click', ev => {
          if (ev.target.closest('.sel4')) return;
          root.querySelectorAll('.sel4menu').forEach(m => { m.hidden = true; });
          root.querySelectorAll('.sel4.open4').forEach(x => x.classList.remove('open4'));
        });
      }

      /* What a direct tunnel should start out as.
       *
       * /api/direct/defaults answers with a subnet nothing on this machine is
       * using, a free interface name and a preset. The route and the handler
       * were both registered; the wrapper that calls them was never written, so
       * the panel never asked. The two fields that endpoint exists for are the
       * two an operator is least able to guess — the tunnel's own /30 has to
       * avoid every subnet already on the box, and picking one by hand is how
       * you end up with a tunnel that comes up and blackholes the route it was
       * built for.
       *
       * Only empty fields are filled, and only once per side: this runs from
       * applyShape, which fires on every change, and overwriting what somebody
       * has typed because they clicked something else is worse than not
       * suggesting at all. */
      /* The fields this fills, and only these.
       *
       * The endpoint also answers with a preset and a tunnel port. The preset
       * is a row of buttons rather than a field, and the tunnel port exists on
       * both shapes of this form and already has a Random button beside it —
       * filling either from here would be reaching past what the endpoint is
       * for. The subnet is what it is for: a tunnel's own /30 has to avoid
       * every subnet already on the box, and picking one by hand is how you get
       * a tunnel that comes up and blackholes the route it was built for. */
      const SUGGESTED_FIELDS = ['localIp', 'peerIp'];

      const suggestedFor = {};
      let suggestion = null;

      /* Fetched once per side, applied every time the shape is drawn.
       *
       * Applying it on the fetch alone does not work, and the reason is worth
       * writing down: this bind rearranges the form into five steps after the
       * template loads, so the field that exists when the answer arrives is not
       * the field the operator ends up typing into. Re-applying is free — it
       * only ever writes into a field that is still empty — and it lands
       * whenever the field appears, in whatever order the rest of the bind
       * happens to run. */
      function applySuggestion() {
        if (!suggestion) return;
        for (const name of SUGGESTED_FIELDS) {
          const value = suggestion[name];
          if (!value) continue;
          for (const f of root.querySelectorAll(`[name="${name}"]`)) {
            if (!f.value) f.value = value;
          }
        }
      }

      async function suggestDirect(side) {
        applySuggestion();
        if (suggestedFor[side]) return;
        suggestedFor[side] = true;
        try {
          suggestion = await api.directDefaults(side === 'server' ? 'iran' : 'kharej');
        } catch (e) {
          suggestedFor[side] = false;
          return;
        }
        applySuggestion();
      }

      function applyShape() {
        const direct = chosen.direction === 'direct';
        const directionNote = root.querySelector('#dirnote');
        if (directionNote) directionNote.textContent = direct
          ? 'Direct — Iran dials the foreign server on the tunnel port.'
          : 'Reverse — the foreign server dials Iran on the tunnel port.';
        show('.step3rev', !direct);
        show('.step3direct', direct);
        const peerGroup = root.querySelector('#peerGrp');
        if (peerGroup) peerGroup.hidden = direct;
        if (direct) suggestDirect(chosen.side);

        /* "server" is the Iran side, "client" the kharej side — the words the
           create endpoints use. */
        show('[data-when="server"]', chosen.side === 'server');
        show('[data-when="client"]', chosen.side === 'client');
        /* A field that belongs to one carrier, or to one side of one carrier.
           These read as conditions joined by "-": "client-spoof" is the kharej
           side of the spoof carrier, "sni" is the sni carrier whichever side
           this is. Nothing evaluated them before, so a row marked for one
           carrier was shown on every direct tunnel, on both sides — including
           the one telling the operator it was required. */
        root.querySelectorAll('[data-when]').forEach(n => {
          const when = n.dataset.when;
          if (when === 'server' || when === 'client' || when === 'tls') return;
          const row = n.closest('.f3, .tg3, .f, .row') || n;
          const ok = when.split('-').every(part => {
            if (part === 'server' || part === 'client') return chosen.side === part;
            return chosen.carrier === part;
          });
          row.hidden = !ok;
        });
        /* Groups that were moved out of .step3rev / .step3direct carry the mode
           they belong to, because the container that used to decide it is no
           longer their parent. */
        root.querySelectorAll('[data-mode]').forEach(g => {
          const wrong = g.dataset.mode !== (direct ? 'dir' : 'rev');
          g.hidden = wrong;
          // A drawer left open in the other mode would spring back open with
          // its own settings when the operator switched away and back.
          if (wrong) g.classList.remove('open');
        });
        // Managed mode hides the token and address fields it supplies itself.
        root.querySelectorAll('.tokgone, .addrgone').forEach(n => { n.hidden = true; });

        /* The spoof settings belong to the spoof carrier. They used to hang off
           a reverse transport that no longer exists, which left every one of
           them unreachable — the drawer was in a section where its condition
           could never be true. */
        const sp = drawer('sp');
        if (sp) {
          const wanted = direct && chosen.carrier === 'spoof';
          sp.hidden = !wanted;
          if (!wanted) sp.classList.remove('open');
        }

        /* The forged source cannot be learned from the traffic, so the kharej
           side of a spoof carrier has to be told where its peer is. */
        const spoofField = root.querySelector('[name="spoofPeerIp"]')?.closest('.f3, div');
        if (spoofField) spoofField.hidden = !(direct && chosen.carrier === 'spoof'
                                              && chosen.side === 'client');
        if (!direct) { applyFields(); applyPresets(); }
        /* Direct has no applyPresets, so without this an untouched direct
           form sent no preset and the server took its default: Turbo, under a
           row showing Balance. */
        else chosen.preset = markedPreset();
      }

      /* The result reports one local end for Manual mode and both ends for a
         Managed pair. Manual mode keeps the token/port handoff below. */
      function finish() {
        const steps = [...root.querySelectorAll('.step[data-s]')];
        const step = steps[steps.length - 1];
        if (!step) return;

        /* One result pane handles both modes. The manual handoff is rendered
         * from the local create response; Managed shows paired status. */
        const hand = step.querySelector('.handpane');
        const conn = step.querySelector('.connpane');
        if (hand) hand.remove();
        if (conn) conn.hidden = false;
        if (conn) runBuild(conn);
      }

      /* Building this end, said out loud.
       *
       * Two stages, each a fact from the answer rather than a timer: the config
       * written, and the service up. Then the setup link — the other end is
       * the operator's to build, and the line that does it is what this screen
       * owes them, the way the menu's wizard prints it. */
      let building = false;
      async function runBuild(conn) {
        if (!conn || building) return;
        building = true;
        const rows = [...conn.querySelectorAll('.sg')];
        const title = conn.querySelector('#connTitle');
        const sub = conn.querySelector('#connSub');
        const result = conn.querySelector('#connResult');
        const spin = conn.querySelector('.spin');
        const onNode = creationMode === 'managed' ? (nodeSel?.value || '') : '';
        const direct = chosen.direction === 'direct';
        const t0 = Date.now();
        rows.slice(2).forEach(r => { r.hidden = !onNode; });
        ['Writing the configuration', 'Starting it here',
          `Writing on ${onNode}`, `Starting on ${onNode}`].forEach((lb, i) => {
          const tx = rows[i]?.querySelector('.tx6');
          if (tx) tx.textContent = lb;
          rows[i]?.classList.remove('done', 'doing', 'failed');
        });
        if (title) title.textContent = 'Building…';
        if (sub) sub.textContent = 'This server’s end, from what you filled in.';

        const settle = (i, ok) => {
          if (!rows[i]) return;
          rows[i].classList.remove('doing');
          rows[i].classList.add(ok ? 'done' : 'failed');
          const d = rows[i].querySelector('.dur');
          if (d) d.textContent = ((Date.now() - t0) / 1000).toFixed(1) + 's';
        };
        const start = i => rows[i]?.classList.add('doing');
        const pause = ms => new Promise(r => setTimeout(r, ms));

        start(0);
        let out;
        try {
          out = await submitTunnel();
        } catch (e) {
          settle(0, false);
          spin?.setAttribute('hidden', '');
          if (title) title.textContent = 'Nothing was created';
          if (sub) sub.textContent = 'This server refused the settings.';
          if (result) {
            result.innerHTML = `<div class="doneline warn"><span class="tick">!</span><div>
              <b>${esc(e.message || 'The panel could not build the tunnel')}</b>
              <span>Go back and change what it names, then press Create again.</span>
            </div></div>`;
          }
          building = false;
          return;
        }

        const { r, name, payload } = out;
        const partial = r.status === 'partial';

        settle(0, !!r.service || !onNode);
        await pause(260);

        start(1); await pause(200);
        settle(1, r.active !== false);

        if (onNode) {
          await pause(260);
          start(2); await pause(240);
          settle(2, !partial);

          await pause(200);
          start(3); await pause(200);
          settle(3, !partial && r.active !== false && r.peer?.active !== false);
        }

        spin?.setAttribute('hidden', '');
        store.refresh();

        const good = !partial && r.active !== false && (!onNode || r.peer?.active !== false);
        if (title) title.textContent = !onNode ? 'This end was created'
          : good ? 'Both services started' : partial ? 'Only this end was built' : 'Created, not up yet';
        if (sub) {
          sub.textContent = !onNode
            ? 'Set up the other machine with the same port, transport and token.'
            : good
            ? 'Check the tunnel card for the connection between the two servers.'
            : partial
              ? `This server has it. ${onNode} does not.`
              : 'The config is written, but one of the services has not started.';
        }
        if (result) {
          result.innerHTML = !onNode
            ? `<div class="doneline"><span class="tick">✓</span><div>
                 <b>${esc(name || 'The tunnel')} is saved on this server</b>
                 <span>Open FullPack on the other machine and choose the opposite side.</span>
                 <span>Name: <code>${esc(name)}</code> · Port: <code>${esc(payload.tunnelPort || '')}</code> · ${direct ? 'Carrier' : 'Transport'}: <code>${esc(direct ? payload.carrier : payload.transport)}</code></span>
                 <span>Security token: <code>${esc(payload.token || '')}</code></span>
               </div></div>`
            : good
            ? `<div class="doneline"><span class="tick">✓</span><div>
                 <b id="doneName">${esc(name || 'The tunnel')}: both services started</b>
                 <span>Written here and on ${esc(onNode)}.</span>
               </div></div>`
            : `<div class="doneline warn"><span class="tick">!</span><div>
                 <b>${esc(partial ? (r.peerError || 'The other end was not written')
                                  : 'Give it a moment')}</b>
                 <span>${esc(partial
                   ? (r.peerHint || `Open the tunnel's setup link and paste it on ${onNode}.`)
                   : 'The tunnel card turns green on its own when it connects.')}</span>
               </div></div>`;
        }
        building = false;
      }

      /* The numbered header follows the Back and Continue buttons. */
      root.addEventListener('step', ev => {
        const { at, of } = ev.detail;
        root.querySelectorAll('.steps .st2').forEach(x =>
          x.classList.toggle('on', Number(x.dataset.s) === at));
        const back = root.querySelector('#backb');
        if (back) back.disabled = at === 0;
        paintNav(at);
        if (at === of - 1) finish();
      });

      root.addEventListener('pick', ev => {
        const { fn, value, el } = ev.detail;
        if (fn === 'setSide') chosen.side = value;
        if (fn === 'mode3') chosen.direction = value === 'dir' ? 'direct' : 'reverse';
        if (fn === 'setFam') { chosen.family = Number(value); paintTransports(); }
        if (fn === 'setTr') { chosen.transport = value; applyFields(); applyPresets(); }
        if (fn === 'setCar') chosen.carrier = value;
        if (fn === 'setPre') chosen.preset = (el.querySelector('.key2, .k3')?.textContent
          || el.textContent).trim().toLowerCase().split(/\s+/)[0];
        applyShape();
        if (fn === 'setPre' || fn === 'setTr' || fn === 'setSide' || fn === 'mode3') showPresetDefaults();
      });

      /* The Fine Tune drawer calls itself "preset defaults" and showed empty
         boxes: the endpoint that says what a preset produces was never asked.
         The numbers go in as placeholders rather than values, because a value
         would be posted, and a drawer that posts everything it displays is a
         drawer that overrides the preset it is describing. */
      async function showPresetDefaults() {
        let d = {};
        try {
          d = await api.tunnelDefaults({
            preset: chosen.preset || '',
            role: chosen.side || 'server',
            transport: chosen.transport || '',
          }) || {};
        } catch (e) { return; }
        root.querySelectorAll('input[name^="tune."]').forEach(inp => {
          const key = inp.name.slice('tune.'.length);
          const v = d[key];
          inp.placeholder = (v === undefined || v === null || v === '') ? '' : String(v);
        });
        paintPresetSets(d);
      }

      /* Under the preset cards: what the chosen one actually sets on this
         transport, read from the same answer the Fine Tune drawer shows. The
         step was three buttons and a screen of nothing; this is the thing an
         operator comparing them wants to see. */
      const DIRECT_SETS = {
        turbo: [['Socket buffer', '8 MB'], ['Queue', 'fq_codel'], ['Suits', 'most links']],
        balance: [['Socket buffer', 'smallest'], ['Queue', 'fq_codel'], ['Suits', 'small VPS']],
        aggressive: [['Socket buffer', '32 MB'], ['Queue', 'deep'], ['Suits', 'fast, bursty links']],
      };
      function paintPresetSets(d) {
        root.querySelectorAll('.rpgrid').forEach(grid => {
          let box = grid.nextElementSibling;
          if (!box || !box.classList.contains('rp-sets')) {
            box = el('div', { class: 'rp-sets' });
            grid.after(box);
          }
          const direct = !!grid.closest('[data-mode="dir"], .step3direct');
          const p = chosen.preset || 'turbo';
          let cells;
          if (direct) cells = DIRECT_SETS[p] || [];
          else {
            const n = (k, unit = '') => (d[k] ? [d[k] + unit] : []);
            cells = [
              ['Keepalive', ...n('keepAlive', ' s')], ['Heartbeat', ...n('heartbeat', ' s')],
              ['Channel', ...n('channelSize')],
              ...(isMux(chosen.transport) ? [['Mux streams', ...n('muxCon')]] : []),
              ...(isKCP(chosen.transport) ? [
                ['KCP window', ...(d.kcpSndWnd ? [`${d.kcpSndWnd}/${d.kcpRcvWnd}`] : [])],
                ['FEC', ...(d.kcpDataShards ? [`${d.kcpDataShards}+${d.kcpParityShards}`] : [])]] : []),
              ['No-delay', d.nodelay ? 'on' : 'off'],
            ].filter(c => c.length === 2);
          }
          const label = p[0].toUpperCase() + p.slice(1);
          box.innerHTML = `<div class="rp-sets-h"><b>What ${esc(label)} sets</b>
              <small>${direct ? 'on this direct tunnel' : `on ${esc((chosen.transport || '').toUpperCase())}`} · change any of it under Optional → Fine Tune</small></div>
            <div class="rp-sets-g">${cells.map(([k, v], i) =>
              `<div style="--d:${i * 35}ms"><span>${esc(k)}</span><b>${esc(String(v))}</b></div>`).join('')}</div>`;
        });
      }
      showPresetDefaults();

      /* Setup Iran and Setup Kharej are two entries in the CLI menu, so they are
         two links here; ?step= opens the wizard part-way, which is what a
         "now do the other side" link needs. */
      const q = ctx.query;
      if (q.get('side')) chosen.side = q.get('side') === 'kharej' ? 'client' : 'server';
      if (q.get('kind')) chosen.direction = q.get('kind') === 'direct' ? 'direct' : 'reverse';
      const markGroup = (fn, value) => {
        const b = [...root.querySelectorAll(`[data-fn="${fn}"]`)]
          .find(x => (x.dataset.args || '').includes(value));
        if (b) [...b.parentElement.children].forEach(x => x.classList.toggle('on', x === b));
      };
      markGroup('setSide', chosen.side);
      markGroup('mode3', chosen.direction === 'direct' ? 'dir' : 'rev');

      paintFamilies();
      paintTransports();
      wireDrawerControls();
      applyShape();

      const stepTo = Number(q.get('step') || 0);
      if (stepTo) {
        const steps = [...root.querySelectorAll('.step[data-s]')];
        const at = Math.min(stepTo, steps.length - 1);
        steps.forEach((x, i) => { x.hidden = i !== at; });
        /* the same event Continue raises, so the header and the last step
           behave identically however the step was reached */
        setTimeout(() => root.dispatchEvent(
          new CustomEvent('step', { detail: { at, of: steps.length } })), 0);
      }

      /* Random asks the server for a port that is free on THIS machine, which
         is the right answer for exactly one field: the tunnel port, which this
         side binds.
       *
       * It used to be bound to every button labelled Random, and one of those
       * sat beside Forwarded ports — a field whose own hint says the value is
       * forwarded to the same port on the kharej machine. A port chosen for
       * being free here is, by construction, one nothing is listening on
       * there, so the button could only ever produce a tunnel that comes up,
       * reports a peer, and refuses every connection at the last hop. */
      [...root.querySelectorAll('.withb')]
        .filter(w => w.querySelector('input[name="tunnelPort"]'))
        .forEach(wrap => wrap.querySelectorAll('button').forEach(b => {
          if (!/^random$/i.test(b.textContent.trim())) return;
          b.addEventListener('click', async () => {
            try {
              const r = await api.tunnelSuggest();
              const field = wrap.querySelector('input[name="tunnelPort"]');
              if (field && r.port) field.value = r.port;
            } catch (e) { oops(e); }
          });
        }));

      /* Forwarded ports get a Random of their own, asked for by name: a port
         free on this server, added to the list. It is a port for users to
         reach this server on, and the kharej hands it to the same port there —
         which the hint under the field now says in as many words, because the
         service on the kharej has to be listening on it. */
      root.querySelectorAll('input[name="ports"]').forEach(inp => {
        if (inp.closest('.withb')) return;
        const wrap = el('div', { class: 'withb' });
        inp.before(wrap);
        wrap.append(inp);
        const b = el('button', { type: 'button', class: 'mini3 rnd', text: 'Random' });
        wrap.append(b);
        b.addEventListener('click', async () => {
          try {
            const r = await api.tunnelSuggest();
            if (!r.port) return;
            const have = inp.value.split(',').map(x => x.trim()).filter(Boolean);
            if (!have.includes(String(r.port))) have.push(String(r.port));
            inp.value = have.join(', ');
            inp.dispatchEvent(new Event('input', { bubbles: true }));
          } catch (e) { oops(e); }
        });
        const hint = wrap.parentElement?.querySelector('.hint');
        if (hint) hint.textContent = 'Users connect to these ports on this server. A bare port is handed to the same '
          + 'port on the kharej, so the service there has to listen on it — 443=8080 sends this server’s 443 to '
          + 'the kharej’s 8080. Random adds a port that is free here. Separate several with commas.';
      });

      [...root.querySelectorAll('button')]
        .filter(b => /show as a cli command/i.test(b.textContent.trim()))
        .forEach(b => b.addEventListener('click', async () => {
          const get = n => root.querySelector(`[name="${n}"], #${n}`)?.value?.trim() || '';
          const line = ['sudo fullpack',
            chosen.direction === 'direct' ? 'direct' : 'reverse',
            chosen.side === 'server' ? '--iran' : '--kharej',
            chosen.transport ? '--transport ' + chosen.transport : '',
            get('aname') ? '--name ' + get('aname') : '',
          ].filter(Boolean).join(' ');
          try { await navigator.clipboard.writeText(line); toast('Command copied.'); }
          catch (e) { toast(line); }
        }));

      /* Manual setup keeps a copyable token and port in the result pane.
         Managed setup sends paired values over the authenticated Agent. */

      const nodeSel = root.querySelector('#anode');
      const nodeGrp = root.querySelector('#nodeGrp');
      let creationMode = 'manual';
      let manualSide = chosen.side;
      let manualPeerAddrs = [];
      const originalLede = root.querySelector('.step[data-s="0"] .lede2')?.innerHTML || '';
      const originalSubtitle = root.querySelector('.dh small, .ttl small')?.textContent || '';
      const modeBar = el('div', { class: 'creation-mode', role: 'group', 'aria-label': 'Tunnel creation mode' }, [
        el('button', { type: 'button', class: 'mode-choice on', text: 'Manual / Local' }),
        el('button', { type: 'button', class: 'mode-choice', text: 'Managed / Paired' }),
      ]);
      root.querySelector('.steps')?.before(modeBar);
      modeBar.after(nodeGrp);
      nodeGrp.querySelector('label').textContent = 'Foreign managed server (required)';
      const nodeStatus = el('div', { class: 'hint', role: 'status', text: 'Checking managed servers…' });
      const nodeRefresh = el('button', { type: 'button', class: 'nb', text: 'Refresh servers' });
      nodeGrp.append(nodeStatus, nodeRefresh);
      const [manualButton, managedButton] = modeBar.querySelectorAll('button');
      /* The form in three parts, which is what it has always been without
       * saying so.
       *
       * A tunnel's settings fall into three kinds and the markup already knows
       * which is which: data-when="server" belongs to this machine,
       * data-when="client" to the other one, and a field with neither is one
       * both ends have to agree on. Until now that only decided what to hide,
       * because you filled in one side and went and filled in the other. When
       * the panel writes both, it is the structure of the form.
       */
      function markSides(root) {
        root.querySelectorAll('.step3rev .grp3, .step3direct .grp3').forEach(g => {
          const label = g.querySelector('.gl3');
          if (!label || label.querySelector('.sidechip')) return;
          const fields = [...g.querySelectorAll('[name]')].filter(f => !f.closest('#nodeGrp'));
          const local = fields.filter(f => f.closest('[data-when]'));

          /* Marked only when the whole group is one kind. A group that mixes
           * them gets nothing: the fields inside already say which is which,
           * and a heading that claims "both servers" over a name that is this
           * machine's alone is worse than a heading that claims nothing.
           *
           * A group with no fields at all — the transport and the preset are
           * chosen with buttons — is shared: those are carried to the other end
           * with everything else, and the form's own convention is that what is
           * not marked as one machine's belongs to both. */
          const chip = !fields.length ? 'both servers'
            : local.length === fields.length ? 'this server'
            : local.length === 0 ? 'both servers'
            : '';
          if (!chip) return;
          label.append(el('span', { class: 'sidechip managed-chip', text: chip }));
        });
      }

      /* The other server's own settings.
       *
       * These four cannot be worked out from this end — which proxy that
       * machine dials through, which CDN edge it fronts, which interface it
       * leaves by, what it falls back to. Everything else about the far end is
       * derived from this one; these are the only answers that have to be
       * given. They were unreachable before: the form hid them because you were
       * not setting up that side, and you were not setting up that side, so
       * they could only be set by logging into it.
       *
       * The fields are the existing ones, moved and renamed — peerConn.* rather
       * than conn.*, so the submit puts them on the far end instead of this one.
       */
      const movedPeerFields = [];
      function buildPeerGroup(root) {
        const rev = root.querySelector('.step3rev');
        if (!rev || rev.querySelector('#peerGrp')) return;
        /* Every ConnTune setting that is the client's own.
         *
         * SimpleAuth is left behind deliberately: it is applied before the role
         * check in ConnTune.apply, so it belongs to both ends and is not the
         * far end's to answer alone. */
        const wanted = ['conn.edgeIP', 'conn.proxy', 'conn.interface', 'conn.localAddr',
          'conn.fallbackAddrs', 'conn.loadBalance', 'conn.healthFailover'];
        const fields = wanted
          .map(n => {
            const el2 = root.querySelector(`.step3rev [name="${n}"]`)
              // The switches and menus are wired to a hidden input beside them,
              // so the row is found from the control when there is no field yet.
              || root.querySelector(`.step3rev [id$="${n.split('.')[1]}"]`);
            return el2?.closest('.f3, .tg3');
          })
          .filter(Boolean);
        if (!fields.length) return;

        const grid = el('div', { class: 'fgrid' });
        fields.forEach(f => {
          // data-when decided which side saw the field; the section it is
          // moving into answers that now, so the marker goes and the field is
          // taken out of the hidden state it was left in. What must survive is
          // the transport rule, and that lives in applyFields.
          movedPeerFields.push({ row: f, parent: f.parentElement, next: f.nextSibling,
            when: f.getAttribute('data-when'), name: f.querySelector('[name]').name });
          f.removeAttribute('data-when');
          f.hidden = false;
          const input = f.querySelector('[name]');
          input.name = 'peerConn.' + input.name.slice('conn.'.length);
          // The labels said "kharej only" when this side was the one being set
          // up. Under a heading that names the server, that is now noise.
          grid.append(f);
        });
        const grp = el('div', { class: 'grp3', id: 'peerGrp' }, [
          el('div', { class: 'gl3', text: 'The other server' }, [
            el('span', { class: 'sidechip', text: 'that machine only' }),
          ]),
          grid,
        ]);
        root.querySelector('.step[data-s="1"]')?.append(grp);
        applyFields();
      }

      function restorePeerGroup() {
        for (const item of [...movedPeerFields].reverse()) {
          item.row.querySelector('[name]').name = item.name;
          if (item.when !== null) item.row.setAttribute('data-when', item.when);
          item.parent.insertBefore(item.row, item.next);
        }
        movedPeerFields.length = 0;
        root.querySelector('#peerGrp')?.remove();
        root.querySelectorAll('.managed-chip').forEach(c => c.remove());
      }

      /* No token to read, so no token to type.
       *
       * There are four token fields in this form — each side has one it
       * generates and one it pastes, for reverse and for direct — because the
       * secret used to be carried between two machines by a person. One of them
       * has no name attribute at all, so the value it shows has never been
       * submitted by anything.
       *
       * None of that survives contact with a panel that writes both ends. The
       * token is generated here, kept in a variable, and put into the payload
       * on the way out; every field is hidden and none of them is read. That is
       * one place the secret exists instead of four, and nothing depends on
       * which of the four the operator happened to be looking at.
       */
      let autoTok = '';

      // The manual Iran-side flow needs a fresh token to share with its peer.
      const bytes = new Uint8Array(32);
      crypto.getRandomValues(bytes);
      const suggestedToken = [...bytes].map(b => b.toString(16).padStart(2, '0')).join('');
      const manualToken = root.querySelector('#atok');
      if (manualToken) manualToken.name = 'token';
      root.querySelectorAll('#atok, .step3direct input[name="token"][value]').forEach(input => {
        input.value = suggestedToken;
        input.closest('.withb')?.querySelector('button')?.addEventListener('click', () => {
          navigator.clipboard?.writeText(input.value).catch(oops);
        });
      });

      function autoToken(root) {
        root.querySelectorAll('[name="token"], #atok').forEach(i => {
          const box = i.closest('.f3');
          // Marked, not just hidden: applyShape sets the same attribute from
          // the side, and would put them back on the next change.
          if (box) box.classList.add('tokgone');
        });
        applyShape();
        api.tunnelToken()
          .then(r => { autoTok = r.token || ''; })
          .catch(() => { /* the create will say the token is missing */ });
      }

      function selectCreationMode(mode) {
        if (mode === creationMode) return;
        creationMode = mode;
        manualButton.classList.toggle('on', mode === 'manual');
        managedButton.classList.toggle('on', mode === 'managed');
        const managed = mode === 'managed';
        const kharej = root.querySelector('[data-fn="setSide"][data-args*="client"]');
        const iran = root.querySelector('[data-fn="setSide"][data-args*="server"]');
        const lede = root.querySelector('.step[data-s="0"] .lede2');
        const subtitle = root.querySelector('.dh small, .ttl small');
        const addrs = [...root.querySelectorAll('[name="peerAddr"], [name="serverAddr"]')];
        if (managed) {
          manualSide = chosen.side;
          manualPeerAddrs = addrs.map(n => n.value);
          chosen.side = 'server';
          markGroup('setSide', 'server');
          if (kharej) kharej.hidden = true;
          if (lede) lede.innerHTML = 'This panel writes the Iran side and the selected managed server together.';
          if (subtitle) subtitle.textContent = 'Both ends are written from here';
          nodeGrp.hidden = false;
          nodeSel.options[0].textContent = 'Choose an online managed server';
          markSides(root);
          buildPeerGroup(root);
          autoToken(root);
          const available = [...nodeSel.options].filter(o => o.value && !o.disabled);
          if (!nodeSel.value && available.length === 1) nodeSel.value = available[0].value;
        } else {
          restorePeerGroup();
          if (kharej) kharej.hidden = false;
          chosen.side = manualSide;
          markGroup('setSide', manualSide);
          if (lede) lede.innerHTML = originalLede;
          if (subtitle) subtitle.textContent = originalSubtitle;
          nodeGrp.hidden = true;
          nodeSel.value = '';
          peerIP = '';
          addrs.forEach((n, i) => { n.value = manualPeerAddrs[i] || ''; });
          root.querySelectorAll('.tokgone, .addrgone').forEach(n => n.classList.remove('tokgone', 'addrgone'));
        }
        nodeSel.dispatchEvent(new Event('change'));
        applyShape();
        paintNav(Number(root.querySelector('.step:not([hidden])')?.dataset.s || 0));
      }
      manualButton.addEventListener('click', () => selectCreationMode('manual'));
      managedButton.addEventListener('click', () => selectCreationMode('managed'));

      // Both creation modes use the same three-step form. The explicit mode
      // controls only the fields and the API that receives the submission.
      function paintNav(at) {
        const next = root.querySelector('#nextb');
        const note = root.querySelector('#note4');
        if (!next) return;
        const last = root.querySelectorAll('.step[data-s]').length - 1;
        if (at === last) {
          next.textContent = 'Close';
          next.disabled = false;
          next.onclick = () => { close(); go('/'); };
        } else {
          next.textContent = at === last - 1 ? 'Create the tunnel' : 'Continue';
          next.onclick = null;
        }
        if (note) note.textContent = at === last - 1
          ? (creationMode === 'managed' ? 'Both ends are written when you press this.'
             : 'This end is written when you press this.') : '';
      }
      paintNav(0);

      const nodeMsg = root.querySelector('#nodeMsg');
      const nodeAddr = new Map();   // name -> the address it reported
      let peerIP = '';               // the one for the server that was picked
      let nodeTimer = null;
      let checkingNodes = false;
      async function refreshNodes() {
        if (checkingNodes || !root.isConnected) return;
        clearTimeout(nodeTimer);
        checkingNodes = true;
        nodeRefresh.disabled = true;
        try {
          const state = await api.nodesCached();
          if (!root.isConnected) return;
          const selected = nodeSel.value;
          const nodes = state.nodes || [];
          const live = nodes.filter(n => n.online && !n.revoked);
          nodeAddr.clear();
          nodeSel.replaceChildren(el('option', { value: '', text: 'Choose an online managed server' }));
          for (const n of nodes) {
            const online = n.online && !n.revoked;
            nodeSel.append(el('option', { value: n.name,
              text: `${n.name} — ${online ? 'Online' : n.revoked ? 'Revoked' : 'Offline'}`, disabled: !online }));
            const ip = n.info?.ipv4;
            if (online && ip && ip !== '-') nodeAddr.set(n.name, ip);
          }
          if (live.some(n => n.name === selected)) nodeSel.value = selected;
          else if (creationMode === 'managed' && live.length === 1) nodeSel.value = live[0].name;
          nodeStatus.textContent = live.length ? `${live.length} managed server(s) online.`
            : nodes.length ? 'No server is online. Check the foreign monitor service; this list refreshes automatically.'
              : 'No managed servers enrolled. Add one under Servers and run fullpack node join on it.';
          nodeSel.dispatchEvent(new Event('change'));
        } catch (e) {
          if (root.isConnected) nodeStatus.textContent = 'Could not update server status. Retrying automatically…';
        } finally {
          checkingNodes = false;
          nodeRefresh.disabled = false;
          if (root.isConnected) nodeTimer = setTimeout(refreshNodes, 6000);
        }
      }
      nodeRefresh.addEventListener('click', refreshNodes);
      refreshNodes();

      nodeSel?.addEventListener('change', () => {

        /* The address is not asked for, because it is already known.
         *
         * A managed server dials this panel, and reports what it is when it
         * gets there — hostname, version, addresses. This side of a direct
         * tunnel needs that address, and it is a worse answer coming from a
         * person: it can be mistyped, and it goes stale when the machine's
         * address changes. So the field goes, the value is carried in the
         * payload, and it is shown here as a fact rather than a question.
         */
        peerIP = nodeAddr.get(nodeSel.value) || '';
        root.querySelectorAll('[name="peerAddr"], [name="serverAddr"]').forEach(addr => {
          const box = addr.closest('.f3');
          if (box) box.classList.toggle('addrgone', creationMode === 'managed' && !!nodeSel.value && !!peerIP);
          if (creationMode === 'managed' && peerIP) addr.value = peerIP;
        });
        applyShape();

        if (nodeMsg) {
          nodeMsg.hidden = !(nodeSel.value && peerIP);
          if (!nodeMsg.hidden) {
            nodeMsg.querySelector('span:last-child').textContent =
              `${nodeSel.value} reports its address as ${peerIP}. Nothing else about it needs entering.`;
          }
        }
      });


      /* Building the tunnel.
       *
       * It used to hang off a button that is not in this markup, so the form
       * collected everything and posted it nowhere. It is a function now, run
       * when the wizard reaches its last step — which is also where the result
       * is shown, so pressing Continue on the step before it is the commit.
       */
      async function submitTunnel() {
        /* Only what applies: a hidden field belongs to the other side or the
           other kind of tunnel, and sending it would describe a tunnel nobody
           asked for.
           
           A step that is not the one on screen is hidden too, and that is a
           different thing entirely. The tunnel is built from the last step, by
           which point every step holding a field is hidden — so testing for any
           hidden ancestor collected nothing at all and posted an empty form.
           The panes are skipped over; everything else still counts. */
        const irrelevant = n => {
          for (let at = n.parentElement; at && at !== root; at = at.parentElement) {
            if (at.hidden && !at.classList.contains('step')) return true;
          }
          return false;
        };
        const payload = {};
        root.querySelectorAll('input[name], select[name]').forEach(n => {
          if (irrelevant(n)) return;
          /* A drawer switch or menu nobody touched says nothing. Posting what
             it was drawn with sent every knob in Fine Tune on every create, and
             a tunnel given its own numbers is a tunnel off its preset: pick
             Aggressive, get no preset line and the drawing's buffers — which
             are Turbo's. Touched, it is sent, off included. */
          if (n.dataset.drawn) return;
          const wired = n.hidden && n.type === 'checkbox';
          const v = n.type === 'checkbox' ? n.checked : n.value.trim();
          if (v === '' || (v === false && !wired)) return;
          const keys = n.name.split('.'), last = keys.pop();
          let at = payload;
          for (const k of keys) at = at[k] ??= {};
          at[last] = NUMERIC.has(n.name) ? Number(v) : v;
        });
        payload.name ||= '';
        if (chosen.direction === 'direct') {
          payload.side = chosen.side === 'server' ? 'iran' : 'kharej';
          payload.carrier = chosen.carrier;
        } else {
          payload.role = chosen.side;
          payload.transport = chosen.transport;
        }
        if (chosen.preset) payload.preset = chosen.preset;

        const onNode = creationMode === 'managed' ? (nodeSel?.value || '') : '';
        const direct = chosen.direction === 'direct';
        // Collected by name like everything else, then lifted out: it describes
        // the other machine and must not be written onto this one.
        const peerConn = payload.peerConn;
        delete payload.peerConn;
        // Every token field is hidden in this flow, so the collector skipped
        // them all — correctly. The one the panel generated goes in here, and
        // so does the address the server reported for itself.
        if (onNode && autoTok) payload.token = autoTok;
        if (onNode && peerIP && direct) payload.peerAddr ||= peerIP;

        const r = await createTunnelForMode(api, creationMode, onNode, direct, payload, peerConn);
        return { r, onNode, name: payload.name, payload };
      }

      ctx.setTeardown(() => { clearTimeout(nodeTimer); close(); });
    },
  }).catch(oops);
}
