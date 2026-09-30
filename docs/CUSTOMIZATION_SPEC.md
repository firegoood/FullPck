# مشخصات شخصی‌سازی FullPack

Last verified against FullPack v1.8.7.

<div dir="rtl">

## خلاصهٔ فارسی

این سند الزامات پایدار شخصی‌سازی را ثبت می‌کند: سرورهای مدیریت‌شده از طریق Agent خروجی و کانال امن به کنترلر وصل می‌شوند، تونل‌های دستی بدون Node همچنان کار می‌کنند، و تنظیمات امنیتی، Telegram، نصب و پشتیبان‌گیری باید هنگام به‌روزرسانی حفظ شوند. بخش‌های اجرایی پایین‌تر معیارهای پذیرش و بررسی رگرسیون را تعیین می‌کنند.

قید صریح کاربر: Telegram نباید پورت محلی 443 را اشغال کند. اتصال `api.telegram.org:443` فقط اتصال خروجی به مقصد خارجی است. IPC باید Unix socket محلی باشد و fallback تونل از پورت بلند روی `127.0.0.1` استفاده کند. نگاشت قدیمی مخصوص Telegram روی پورت محلی 443 باید به پورت بلند منتقل شود.

از انتشارهای بعد از `v1.8.7` فقط بسته‌های آمادهٔ Linux برای `amd64` و `arm64` ساخته و منتشر شوند. معماری‌های دیگر در صورت نیاز می‌توانند از سورس ساخته شوند؛ بستهٔ آماده و به‌روزرسانی خودکار برای آن‌ها تضمین نمی‌شود.

</div>

---

You are working on the CURRENT repository:

https://github.com/firegoood/FullPck

This is a substantial production architecture refactor.

Treat the CURRENT checked-out repository state as the source of truth. Do not rely on assumptions from this prompt when the repository can answer the question directly.

Before changing code, inspect all relevant:

- repository-local instructions such as AGENTS.md if present;
- git status and existing user changes;
- current tests and verification scripts;
- Fleet / Managed Servers implementation;
- `internal/node`;
- `internal/control`;
- `internal/webui`;
- `internal/monitor`;
- Telegram implementation;
- installer/update/uninstall logic;
- backup/restore logic;
- service definitions;
- manual tunnel creation;
- paired/managed tunnel creation;
- direct and reverse tunnel flows;
- tunnel engine lifecycle;
- WebUI path-prefix/TLS handling;
- alert-history timestamp rendering;
- relevant documentation.

Do NOT only produce a plan.

Implement the complete change, test it thoroughly, review it repeatedly, fix issues discovered during review, and leave the repository coherent and production-ready.

Do not ask for confirmation between implementation phases.

---

# 0. DEPLOYMENT ASSUMPTION — FRESH INSTALLS ONLY

This deployment is for FRESH INSTALLS.

Release artifact policy after v1.8.7: publish only Linux amd64 and arm64 archives, plus the installer, checksums, signature, and SBOM. Keep the release build, workflow upload list, and checksum contents aligned. Do not add another prebuilt architecture without an explicit user request.

There is NO requirement to migrate existing SSH-managed Fleet entries.

There is NO requirement to preserve:

- existing SSH usernames;
- SSH passwords;
- SSH ports;
- SSH fingerprints;
- legacy SSH-managed `nodes.json`;
- the old SSH Fleet transport;
- the old historical per-node-port Agent protocol.

Do not add migration complexity for obsolete SSH state.

However, do not break ordinary tunnel configuration, manual tunnel setup, backups, CLI flows, or unrelated configuration.

---

# 1. PRIMARY OBJECTIVE

Replace FullPack's SSH-based Managed Servers/Fleet control plane completely with a lightweight REVERSE PERSISTENT NODE AGENT architecture.

FullPack must no longer use SSH for Managed Server communication.

The target control connection direction is:

    KHAREJ NODE
        |
        | persistent outbound connection
        v
    IRAN CONTROLLER

The Iran Controller must NEVER require a new outbound management connection to the Kharej public IP.

This is a core requirement because the expected network environment may have:

    Iran -> Kharej public IP       BLOCKED

while:

    Kharej -> Iran                 WORKS

and:

    Kharej -> Iran reverse tunnel WORKS

Managed Nodes must remain controllable in that scenario as long as their existing outbound Agent session to Iran remains alive.

---

# 2. FINAL TARGET TOPOLOGY

Iran server:

    fullpack --webui
          |
          | existing configurable WebUI listener
          | default is normally :7654
          |
          +-- browser Web Panel
          +-- existing HTTP APIs
          +-- Fleet APIs
          +-- /_bp/node     machine-to-machine Agent gateway
          |
          +-- in-memory authenticated Node session registry

and independently:

    fullpack --monitor
          |
          +-- watchdog
          +-- Telegram bot
          +-- alerts
          +-- history
          +-- auto-backup
          +-- existing monitor responsibilities

Kharej managed Node:

    fullpack --monitor
          |
          +-- existing monitor jobs
          +-- Node Agent client
                  |
                  | outbound persistent WebSocket
                  | encrypted/authenticated with Noise
                  v
             Iran Controller configured WebUI port

Kharej does NOT need:

    fullpack --webui

for managed-node operation.

Do not require a Web Panel on the Kharej Node.

---

# 3. STRICT PORT REQUIREMENTS

These constraints are NON-NEGOTIABLE.

The management/control plane may use ONLY the already-configured WebUI listener on the Iran Controller.

The default WebUI port is currently normally:

    7654

but NO Agent/control-plane code may hard-code `7654`.

Always use the configured Controller URL/port.

Do NOT automatically bind or reserve:

    :80
    :443

for this feature.

Do NOT introduce another public management port such as:

    :7778
    :8443
    :9443
    :50051

Do NOT create a second public HTTP server for the Agent.

Do NOT create another `net.Listen`.

Do NOT create another `http.ListenAndServe`.

Do NOT create another TCP management listener on the Kharej Node.

Foreign managed Nodes must require:

    ZERO inbound management ports.

Outbound traffic to remote port 443, for example:

    Kharej ephemeral-port -> api.telegram.org:443

does NOT mean FullPack listens on local port 443 and is acceptable.

Tunnel/data ports are separate from this management-port restriction.

---

# 4. CONTROL PLANE AND DATA PLANE MUST STAY INDEPENDENT

Do NOT run Fleet management through a FullPack tunnel.

The Agent control channel must not depend on the tunnel it is expected to inspect or repair.

Keep:

    CONTROL PLANE:
    Kharej -> Iran:<configured WebUI port>
    persistent WebSocket + Noise

separate from:

    DATA PLANE:
    Kharej -> Iran:<configured tunnel port>
    existing FullPack reverse/direct tunnel transport

If a reverse tunnel crashes:

    Agent should ideally remain connected.

The operator must still be able to:

- see Node status;
- see tunnel status;
- retrieve logs;
- diagnose;
- start;
- stop;
- restart;
- edit/reapply where explicitly requested.

Do NOT intentionally tear down the Agent because one tunnel failed.

Do NOT restart/reapply healthy tunnels merely because the Agent reconnected.

Do NOT restart the Agent merely because one tunnel was restarted.

---

# 5. PRESERVE THE MOST IMPORTANT TUNNEL RELIABILITY PROPERTY

Do not weaken the existing:

    one tunnel
    one engine process
    one systemd unit

architecture.

Each tunnel must continue to be isolated.

A panic or bad configuration in one tunnel must not take down:

- another tunnel;
- the Node Agent;
- the Web Panel;
- the monitor;
- Telegram;
- watchdog.

Do not combine all tunnel engines into one process to simplify Fleet management.

---

# 6. REMOVE SSH FLEET MANAGEMENT COMPLETELY

This is a fresh-install redesign.

Remove FullPack's Managed-Server dependency on:

- `SSHRunner`;
- SSH connection pools;
- SSH username;
- SSH password;
- SSH port;
- host-key fingerprint;
- SSH TOFU handling;
- SSH Fleet installer;
- SSH Fleet upgrade flow;
- SSH reachability polling;
- SSH-specific Fleet errors;
- SSH fields in the WebUI;
- SSH fields in Fleet APIs;
- SSH-specific documentation.

Remove `fullpack node exec <base64>` if, after the redesign, it has no legitimate non-SSH use.

Keep/reuse the actual typed Node operation execution logic internally.

Search for `golang.org/x/crypto/ssh`.

Remove the SSH package usage if nothing else needs it.

Do NOT remove all of `golang.org/x/crypto` blindly if other unrelated code still depends on that module.

Do NOT remove ordinary SSH references from documentation that simply explains normal Linux/server administration.

Only remove SSH as FullPack's Fleet/Managed-Server transport.

---

# 7. REUSE THE CURRENT NODE OPERATION SECURITY MODEL

Do NOT create a second copy of tunnel-management business logic.

Reuse the current concepts around:

    node.Request
    node.Response
    node.Execute(...)

wherever practical.

The architecture should remain conceptually:

    Controller
       |
       | typed request
       v
    authenticated Agent session
       |
       v
    node.Execute(request)
       |
       v
    typed response

Do not duplicate:

- apply logic;
- start/stop/restart logic;
- tunnel settings logic;
- logs logic;
- status logic;
- diagnostics logic.

Keep `internal/manage` as the underlying management seam where the repository currently uses it.

---

# 8. NEVER CREATE A GENERIC REMOTE SHELL

This is a HARD SECURITY BOUNDARY.

Do not add any generic operation such as:

    exec
    shell
    run
    command
    run_command
    arbitrary_file
    read_path

that accepts arbitrary commands or paths from the Controller.

Remote operations must remain an explicit allow-list of typed actions.

Examples can include existing legitimate operations such as:

- hello;
- apply;
- list;
- status;
- settings;
- logs;
- link test;
- start;
- stop;
- restart;
- diagnostics;
- other existing typed Fleet operations.

Remote deletion must retain explicit safe semantics.

Do not silently add a generic command channel because it is convenient.

---

# 9. AGENT LOCATION AND PROCESS MODEL

Do NOT create:

    fullpack-agent.service

Do NOT create another long-lived management daemon.

Integrate the Node Agent client into the existing:

    fullpack-monitor.service

as another independently supervised monitor job.

The existing monitor architecture intentionally isolates supervised jobs.

Preserve that model.

An Agent panic must not kill:

- watchdog;
- Telegram;
- alerts;
- history;
- auto-backup.

A Telegram panic must not kill the Agent.

A watchdog panic must not kill the Agent.

Use the existing monitor supervision conventions where appropriate.

The Agent job should reconnect after ordinary network/session loss rather than exiting permanently.

---

# 10. AGENT RECONNECT

Use exponential reconnect backoff with jitter.

A reasonable production shape is approximately:

    1s
    2s
    4s
    8s
    ...
    capped around 30s

plus jitter.

Do not create a reconnect storm when:

- Controller restarts;
- network returns;
- many Nodes reconnect together.

A normal Agent network disconnect is not a process panic.

Do not abuse the monitor panic-restart mechanism as the ordinary reconnect mechanism.

Reconnect should be part of the Agent client's normal control loop.

---

# 11. WEBSOCKET ENDPOINT ON THE EXISTING WEBUI LISTENER

Add a machine-to-machine Agent endpoint:

    /_bp/node

or an equivalently clear reserved internal path if repository constraints require another exact name.

It MUST share the exact same existing WebUI `http.Server` and configured WebUI port.

Do not create a second listener.

Important:

The browser Web Panel currently supports an optional/secret path prefix.

The machine endpoint must NOT depend on the browser path prefix.

Changing the browser/UI path prefix must not disconnect all Nodes.

Wire the Agent endpoint outside the browser base-prefix restriction while still using the SAME `http.Server`.

Conceptually:

    incoming request
          |
          +-- /_bp/node
          |      -> machine Agent gateway
          |
          +-- everything else
                 -> existing panel path-prefix/security routing

The Agent endpoint must not use the normal browser session cookie as its authentication mechanism.

---

# 12. WEBSOCKET ORIGIN HANDLING

This is a machine-to-machine WebSocket endpoint.

Do not rely on browser `Origin` as Agent authentication.

Noise authentication is authoritative.

At the same time, do not blindly copy:

    CheckOrigin: func(...) bool { return true }

without consciously documenting why machine-to-machine behavior requires the chosen policy.

Configure WebSocket upgrade behavior deliberately.

---

# 13. PROTOCOL LAYERS

Keep the architecture conceptually layered:

    TCP
      ↓
    HTTP upgrade on existing WebUI listener
      ↓
    WebSocket carrier
      ↓
    Noise authenticated/encrypted session
      ↓
    bounded encrypted application messages
      ↓
    versioned control envelope
      ↓
    typed node.Request / node.Response
      ↓
    node.Execute()

Do not conflate these layers unnecessarily.

WebSocket provides transport/framing.

Noise provides Agent authentication and control-channel encryption.

The typed Node protocol provides allowed operations.

---

# 14. DO NOT INVENT CUSTOM CRYPTOGRAPHY

The project already depends on:

    github.com/flynn/noise

Inspect the current repository's existing Noise usage.

Reuse/extract an already-tested repository Noise pattern and primitives where they satisfy the new requirements.

Do not invent a novel cryptographic construction.

If no existing repository helper is directly reusable:

- keep the wrapper minimal;
- document the selected Noise handshake pattern;
- document how PSK material is used;
- add explicit handshake/authentication tests;
- do not weaken authentication for convenience.

Use strong cryptographic randomness.

Permanent Node secrets should provide at least 32 bytes of cryptographically secure entropy.

---

# 15. BOOTSTRAP CREDENTIAL MUST NOT EQUAL PERMANENT NODE CREDENTIAL

This distinction is REQUIRED.

Enrollment credential:

    temporary
    one-time
    short-lived

Permanent Node credential:

    distinct
    per-node
    long-lived until rotation/revocation

Do not use the enrollment token forever as the normal Agent credential.

Recommended enrollment lifecycle:

1. Controller creates:
       node_id
       one-time enrollment token
       expiration
       enrollment record

2. Operator enters/pastes enrollment material on Kharej.

3. Node establishes an authenticated enrollment exchange using the bootstrap credential.

4. During that protected enrollment exchange, provision a DISTINCT permanent per-node authentication secret.

5. Persist permanent credential on both authorized sides.

6. Atomically invalidate the enrollment token.

7. Enrollment token can never authenticate an ordinary Agent session again.

8. Reusing the enrollment token must fail.

9. Expired enrollment token must fail.

10. Revoked enrollment token must fail.

Exact protocol details are implementation decisions, but these invariants are mandatory.

---

# 16. NODE IDENTITY

A Node identity is based on:

    node_id
    +
    cryptographic Node credential

Observed IP is metadata only.

Remote/source IP must NEVER be used as the identity or authentication factor.

A Node may:

- be behind NAT;
- change IP;
- reconnect from another address;
- have a public IP filtered from Iran;
- have an address Iran cannot dial at all.

This must not break identity.

A claimed `node_id` before successful cryptographic authentication is untrusted input.

Do not treat it as authenticated until the Noise/authentication exchange completes.

---

# 17. SECRET STORAGE

Node Agent permanent credential on Kharej must be stored under an appropriate `/etc/fullpack/...` path.

Requirements:

- root-owned;
- mode `0600`;
- never logged;
- never exposed by ordinary status/list APIs;
- never returned to browser clients;
- never embedded in process command-line arguments;
- never exposed in `ps`;
- never copied into diagnostic output.

Controller-side secrets require equivalent protection.

If backup/restore includes them, preserve the existing security model for secret-bearing backups and document it.

Do not log enrollment codes.

---

# 18. SAFE ENROLLMENT UX

Replace the current SSH Add Server flow.

The UI must no longer ask for:

- management IP for Controller dialing;
- SSH port;
- SSH username;
- SSH password;
- SSH fingerprint.

Preferred Controller flow:

    Servers
      -> Add Server
      -> choose/display name
      -> Generate enrollment code

Preferred Kharej flow:

    sudo fullpack node join

Then paste the enrollment material interactively.

Prefer no-echo input for sensitive enrollment material.

Do NOT require this:

    fullpack node join --token SECRET

because secrets in command-line arguments may appear in shell history/process inspection.

If non-interactive automation is supported, provide a safe stdin/file-descriptor based mechanism.

Enrollment code format must be versioned, for example conceptually:

    BPENROLL1:...

Exact encoding is your implementation decision.

After successful join:

- write permanent Agent configuration;
- ensure `fullpack-monitor.service` is installed/enabled/running;
- restart/reload monitor only if actually required;
- Agent begins its outbound connection;
- Node becomes `Online` only after successful normal permanent authentication and hello.

---

# 19. PROTOCOL VERSIONING

The Agent control protocol must have an explicit protocol version.

Unsupported/unknown versions must be rejected cleanly BEFORE any Node operation executes.

Do not silently guess incompatible protocol semantics.

Version mismatch should produce a useful operator-facing status/error without leaking secret information.

---

# 20. REQUEST/RESPONSE MULTIPLEXING

One persistent Agent connection must support multiple concurrent logical requests.

Do not serialize the entire Fleet behind one outstanding RPC.

Use an explicit envelope conceptually similar to:

    request:
      version
      type
      id
      request

    response:
      version
      type
      id
      response

The exact Go representation is your decision.

Requirements:

- unique request IDs;
- concurrent requests;
- correct response-to-request matching;
- context cancellation;
- operation-specific timeout/deadline;
- bounded pending-request map;
- clean failure of pending calls on session loss;
- no cross-Node response delivery;
- no response delivered to the wrong request;
- unknown request ID handled safely;
- unknown message type rejected safely.

Do not add gRPC/protobuf.

---

# 21. WEBSOCKET CONCURRENCY AND BACKPRESSURE

Be explicit about Gorilla WebSocket concurrency.

Use one clear owner for WebSocket writes.

Preferred architecture:

    many callers
         |
         v
    bounded outbound queue
         |
         v
    ONE writer goroutine
         |
         v
    WebSocket

Do NOT permit an unbounded writer queue.

Do NOT allocate without limit when a slow/non-reading peer stops consuming.

When outbound capacity is exhausted:

- block with context/backpressure when appropriate;
- or fail the caller cleanly;
- do not grow memory indefinitely.

Likewise:

- bound pending RPC count;
- bound stream count;
- bound WebSocket message size;
- bound decrypted message size.

A slow/non-reading Agent must not cause unbounded Controller memory growth.

---

# 22. SESSION REGISTRY AND RECONNECT RACES

Implement a concurrency-safe Controller session registry.

For each Node track useful metadata such as:

- Node ID;
- display name;
- lifecycle state;
- current live session;
- local opaque session ID/generation;
- last connected;
- last disconnected;
- last seen;
- disconnect/error reason;
- Node hello/info;
- Node FullPack version;
- Agent protocol version;
- observed remote address as metadata only.

A live authenticated session means Online.

No live authenticated session means Offline/Pending/etc. according to enrollment state.

Do not periodically open new Controller->Node connections to determine status.

Use the existing session heartbeat/liveness.

---

# 23. SESSION REPLACEMENT MUST BE RACE-SAFE

A reconnect race is expected in production.

Assign every accepted live session a monotonically increasing local generation number or equivalent opaque unique session identity.

Replacing the current session must be atomic.

CRITICAL INVARIANT:

If session A is replaced by session B, and A closes later, A MUST NOT be able to:

- delete B from the registry;
- mark the Node Offline;
- fail B's requests;
- overwrite B's last-seen/session metadata.

Cleanup must compare session identity/generation before mutating registry ownership.

Add an explicit regression test for this.

---

# 24. ONE AUTHORITATIVE LIVE SESSION PER NODE

At most one session is authoritative for a Node.

If a correctly authenticated new connection arrives for the same Node:

- replace the old session deterministically;
- fail or close old pending operations cleanly;
- make the new session authoritative atomically;
- prevent old cleanup races as specified above.

Do not keep ambiguous multiple writers/controllers for one Node.

---

# 25. HEARTBEAT / LIVENESS

Use lightweight liveness on the existing WebSocket session.

Do not create separate polling TCP connections.

Use:

- WebSocket ping/pong where appropriate;
- application heartbeat only where useful;
- read deadlines;
- write deadlines;
- sensible idle timeout.

Do not send large status reports as heartbeat messages.

Detailed metrics/status should be retrieved by explicit typed operations or an intentionally low-frequency mechanism.

---

# 26. TLS BEHAVIOR

The Web Panel may operate as:

    http://controller:<port>

or:

    https://controller:<port>

The Agent must therefore support:

    ws://
    wss://

Noise remains required for Agent authentication/control-channel protection.

For `wss://`:

- use normal TLS certificate/hostname verification;
- never globally set `InsecureSkipVerify: true` as a convenience.

If the project supports operator-managed/self-signed panel certificates, integrate with the existing trust model or use explicit trusted CA/certificate/fingerprint pinning.

Do not silently disable TLS verification.

If the Controller uses ordinary `ws://`, Noise still protects Agent management payloads.

Do not require port 443.

---

# 27. AGENT CONFIG MUST USE THE CONFIGURED CONTROLLER URL

The Node must persist the actual enrolled Controller endpoint.

Example:

    http://controller.example:7654

or:

    https://controller.example:8123

Do not reconstruct or assume port 7654 later.

Changing the configured WebUI port must be supported through explicit reconfiguration/re-enrollment semantics.

No Agent/control-plane code may assume 7654 internally.

7654 is only the default test/example configuration.

---

# 28. MANAGED FLEET UI

Refactor the current Servers/Fleet UI around Agent-managed Nodes.

Keep Servers as a permanent, visible section in the WebUI dock. The path to Add Server, enrollment, and Node management must be discoverable even when no Nodes exist or all Nodes are Online. Upstream navigation changes must not hide this section.

User should be able to see states such as:

    Pending
    Online
    Offline
    Revoked

and appropriate metadata:

- display name;
- hostname;
- OS;
- architecture;
- FullPack version;
- protocol version;
- last seen;
- last connected;
- useful disconnect reason;
- observed address as non-authoritative metadata.

Actions should include appropriate existing Fleet capabilities:

- logs;
- status;
- diagnostics;
- drift check;
- paired tunnel creation;
- paired tunnel editing;
- start;
- stop;
- restart;
- revoke/remove management association.

Remove SSH-specific wording and fields.

---

# 29. REVOKE SEMANTICS

Revoking a Node must:

- mark its credential revoked;
- reject future Agent authentication;
- disconnect current session;
- fail pending operations cleanly;
- not delete the Node's running tunnels automatically;
- not silently delete paired tunnels.

Revocation removes Controller management authority, not running data-plane state unless the operator explicitly requests a separate destructive action.

---

# 30. MANAGED PAIRED TUNNEL WORKFLOW

Preserve the useful Managed Server behavior:

From the Iran Web Panel, an enrolled Online Node can receive the remote side of a tunnel configuration through the Agent.

Reuse current desired-state/pairing logic where practical.

Creating a Managed/Paired Tunnel should configure both sides from one operation.

Edits should preserve:

- values that must match on both ends;
- valid side-specific tuning;
- existing pairing semantics.

Start/stop/restart should continue to operate on both ends when requested.

If the far side fails:

- local-side behavior should remain explicit;
- result must clearly report partial failure.

Do not hide partial success.

---

# 31. DELETE SEMANTICS

Preserve safe deletion behavior.

Do not interpret deleting the local tunnel as implicit permission to destroy the remote tunnel.

Remote destructive actions require explicit operator intent.

Do not weaken existing safety semantics merely because remote execution is easier through the Agent.

---

# 32. DRIFT CHECK

Preserve drift detection.

Drift checking should compare desired paired state with actual state and report differences.

Do not automatically repair/reapply drift unless the project already has explicit operator-approved semantics for that.

Do not create an automatic control loop that fights manual changes.

---

# 33. MANUAL TUNNEL CREATION MUST REMAIN FIRST-CLASS

THIS REQUIREMENT IS NON-NEGOTIABLE.

Managed Nodes are a convenience.

Managed Nodes are NOT a replacement for manual tunnel setup.

Manual/local tunnel creation must continue working when:

    number of enrolled Nodes = 0

The user must still be able to configure tunnels manually as before.

Preserve and regression-test all relevant current flows including:

    /api/tunnel/create
    /api/direct/create

and the existing CLI Setup Iran / Setup Kharej workflows.

The UI must expose both conceptual paths:

    Add Tunnel
      - Managed / Paired
      - Manual / Local

Exact wording is your design choice.

Do NOT make:

    "No Managed Servers exist"

mean:

    "Add Tunnel is unavailable."

That regression is unacceptable.

---

# 34. DO NOT REMOVE MANUAL ADVANCED OPTIONS

Manual mode must retain current applicable options such as:

- Reverse;
- Direct;
- transport selection;
- backup addresses;
- failover/load balancing where currently supported;
- Fine Tune;
- performance presets;
- manual role/address/port configuration;
- existing advanced transport settings;
- UDP behavior where currently supported;
- current direct tunnel options.

Do not simplify manual mode merely because managed pairing exists.

---

# 35. REVERSE TUNNEL DIRECTION MUST REMAIN UNCHANGED

Do not change the fundamental reverse data-tunnel model.

Normal reverse tunnel remains conceptually:

    Kharej -> Iran

The Agent also being:

    Kharej -> Iran:<configured WebUI port>

does not mean they are the same connection.

They remain separate sockets with separate responsibilities.

---

# 36. TELEGRAM — GOAL

Telegram may be blocked from Iran.

Telegram notifications/control must remain usable.

Do NOT require a listener on port 80 or 443.

Do NOT move the full Telegram bot logic or persistent Bot Token storage to Kharej.

Prefer:

    Bot logic and TLS endpoint logic on Iran
    Internet egress through a healthy foreign Agent

without making the Agent a general-purpose proxy.

---

# 37. TELEGRAM — PRESERVE END-TO-END TLS SEMANTICS

Do NOT implement Telegram egress as a generic HTTP RPC carrying:

    URL
    arbitrary headers
    arbitrary HTTP method
    arbitrary destination

Do NOT turn the Agent into an open HTTP proxy.

Preferred architecture:

    Iran monitor / Telegram HTTP client
             |
             | TLS intended for api.telegram.org
             v
    restricted local IPC
             |
             v
    Iran Controller
             |
             | multiplexed encrypted Agent stream
             v
    Kharej Agent
             |
             | raw TCP only to api.telegram.org:443
             v
    Telegram

The Kharej Agent should act only as a restricted byte relay for the Telegram destination.

TLS to Telegram should remain end-to-end from the Iran Telegram client to Telegram where practical, so the Kharej Agent relays opaque TLS bytes and does not need to parse the Bot API or persist the Bot Token.

The Agent should not receive arbitrary destination host/port values from an untrusted caller.

The allowed destination must be strongly constrained to Telegram Bot API service.

---

# 38. TELEGRAM STREAM MULTIPLEXING

Telegram long polling may hold a connection open for tens of seconds.

Do NOT model this as an ordinary short management RPC with one global short timeout.

Support logical byte streams over the Agent connection.

Use explicit stream semantics conceptually like:

    stream_open
    stream_data
    stream_close
    stream_error

with:

- stream ID;
- bounded number of streams;
- bounded per-stream buffering;
- bounded data chunk size;
- cancellation;
- half/full close semantics as required;
- clean teardown on Agent disconnect.

Do not assume one WebSocket frame equals an arbitrary TCP stream boundary.

Do not let a stream produce unbounded memory growth.

---

# 39. TELEGRAM OPERATION-SPECIFIC TIMEOUTS

Management RPC deadlines and Telegram long-poll lifetimes are different.

Use operation-specific deadlines.

Do not apply an ordinary short management timeout to Telegram long polling.

Cancellation must propagate through the full chain:

    Telegram/monitor context
       ->
    local IPC
       ->
    Controller stream
       ->
    Agent stream
       ->
    outbound TCP connection

Cancelling long polling must release:

- Controller resources;
- Agent resources;
- goroutines;
- buffers;
- sockets.

Add tests.

---

# 40. TELEGRAM LOCAL IPC BETWEEN MONITOR AND WEBUI

`fullpack --monitor` and `fullpack --webui` are intentionally separate processes.

Preserve this separation.

If Agent sessions are owned by the WebUI/Controller process, provide narrow local IPC so the monitor can request restricted Telegram egress.

Prefer a Unix domain socket such as conceptually:

    /run/fullpack/control.sock

This is NOT a public TCP port.

Requirements:

- secure owner/group;
- restrictive permissions;
- cleanup stale socket safely;
- no browser authentication dependency;
- no public bind;
- no generic command execution;
- no generic arbitrary TCP CONNECT API available remotely.

The IPC should expose only the narrow behavior needed.

---

# 41. WEBUI STOPPED — EXPECTED BEHAVIOR

It is acceptable that Managed Node control through the Agent is unavailable while:

    fullpack --webui

is intentionally stopped.

Do NOT move the Agent gateway/session registry into `fullpack-monitor` merely to keep Fleet control active while the WebUI is stopped.

Preserve existing process responsibilities.

When WebUI is stopped:

- existing tunnel engine processes must continue;
- watchdog must continue;
- monitor must continue;
- alerts must continue where their available relay path permits;
- history/auto-backup must continue.

Do not make tunnel uptime depend on the WebUI.

---

# 42. TELEGRAM FALLBACK BEHAVIOR

The current repository already has Telegram reachability logic using tunnel-based egress.

Do not destroy working resilience casually.

Preferred behavior:

1. Agent-based restricted Telegram egress when:
       WebUI Controller is running
       AND at least one suitable authenticated foreign Agent is available.

2. Fall back sensibly to an existing valid tunnel-based/direct relay method where currently supported.

3. Telegram failure must not crash monitor.

4. One failed foreign Node should allow trying another healthy connected Node.

Do not make Telegram worse when the WebUI is intentionally stopped if an existing tunnel-based relay still works.

---

# 43. TELEGRAM NODE FAILOVER

When multiple healthy foreign Nodes exist, support selecting a suitable connected Node for Telegram egress.

If the selected Node disconnects:

- fail the affected stream cleanly;
- permit the Telegram client/bot logic to reconnect/retry;
- allow another healthy Agent to be selected.

Do not migrate a live raw TCP stream invisibly between Nodes.

Retry with a new connection instead.

Keep selection logic simple and deterministic.

---

# 44. TELEGRAM SECURITY

Ensure the Telegram relay cannot be used to reach arbitrary external hosts.

Review specifically for:

- arbitrary host injection;
- arbitrary port injection;
- DNS rebinding assumptions;
- destination change after validation;
- redirects if any HTTP-aware fallback exists;
- generic CONNECT behavior accidentally exposed;
- oversized stream buffering;
- leaked Bot Token.

Prefer resolving/dialing the fixed Telegram endpoint within trusted code.

---

# 45. REMOTE SELF-UPDATE IS NOT REQUIRED FOR MVP

SSH previously provided remote install/upgrade.

Do not recreate arbitrary remote installation through a generic shell.

For this refactor:

Remote self-update is OPTIONAL.

If the existing verified updater can be exposed safely as a narrow typed operation without materially expanding risk/scope, implement it.

Otherwise:

- remove old SSH remote-upgrade behavior;
- retain verified manual update as the supported path;
- document it clearly.

Do not weaken the Agent architecture or add arbitrary command execution merely to keep a one-click remote upgrade button.

If implemented, remote update must mean only:

    run FullPack's existing verified update path

not:

    execute an arbitrary installer URL/command.

---

# 46. INITIAL NODE INSTALLATION

It is acceptable that initial installation on Kharej remains a one-time manual installation using the project's normal installer/offline installation process.

After FullPack is installed:

    fullpack node join

enrolls it with the Controller.

Do not require SSH from the Controller to bootstrap installation.

This architecture deliberately avoids Iran -> Kharej management dialing.

---

# 47. BACKUP / RESTORE

Inspect existing backup/restore behavior.

Integrate:

- Controller Node registry;
- permanent Node credentials if appropriate;
- enrollment state if appropriate;
- Node Agent local config where appropriate

without weakening secret handling.

Do not back up expired one-time enrollment tokens unnecessarily.

Restoring the Iran Controller should have documented, deterministic behavior for managed Nodes.

Do not silently duplicate active identities.

If restored Controller credentials intentionally restore existing per-node authority, test reconnection behavior.

If re-enrollment is required instead, make that explicit.

Choose the cleaner model after inspecting current backup architecture and document it.

---

# 48. INSTALL / UNINSTALL / SERVICE INTEGRATION

Fresh install must support the roles cleanly.

Iran Controller:

- normal FullPack installation;
- WebUI service;
- monitor service;
- configured WebUI port;
- Agent gateway on same WebUI listener.

Kharej Managed Node:

- FullPack binary;
- monitor service;
- Agent config after join;
- NO required WebUI service;
- NO Agent systemd service;
- NO inbound management firewall rule.

`fullpack node join` must ensure the monitor service needed for the Agent is actually usable.

Uninstall must clean up Agent configuration appropriately.

Do not accidentally delete ordinary tunnel configurations when merely revoking/removing a managed relationship.

---

# 49. ALERT `NaN d ago` BUG

Fix the existing visible Alert UI defect where a timestamp can render:

    NaN d ago

This is independent of the Agent refactor but is currently user-visible.

Requirements:

- parse valid server timestamps correctly;
- handle malformed timestamps;
- handle missing timestamps;
- never render NaN;
- use a safe fallback such as absolute/raw/unknown text;
- add formatter/parser tests.

Do not mask the underlying timestamp bug with a string replacement only.

---

# 50. NO UNNECESSARY DEPENDENCIES

The repository already contains:

    github.com/gorilla/websocket
    github.com/flynn/noise

Prefer using existing dependencies/utilities.

Do not add:

- gRPC;
- protobuf;
- Redis;
- external message broker;
- database server;
- another VPN;
- another web framework;
- another long-lived runtime daemon

unless current repository constraints prove it genuinely necessary.

Keep the project's single-binary operational style.

---

# 51. FAILURE SEMANTICS

Explicitly implement and test these cases.

## Controller restart

- tunnel engines continue running;
- monitor continues independently;
- Nodes reconnect;
- session registry rebuilds naturally;
- reconnects are jittered;
- healthy tunnels are NOT automatically reapplied/restarted.

## WebUI intentional stop

- Agent management unavailable;
- tunnel engines continue;
- monitor continues;
- watchdog continues;
- Telegram may use existing alternative relay where available.

## Node monitor restart

- tunnel engine processes continue;
- Agent reconnects;
- no tunnel config lost;
- no healthy tunnel restarted merely because Agent returned.

## Reverse tunnel crash

- Agent can remain online;
- Controller can inspect logs/status and restart tunnel.

## Agent connection loss

- existing tunnels continue;
- Node becomes Offline;
- pending calls fail cleanly;
- stream resources close;
- Agent retries.

## Iran cannot dial Kharej

- irrelevant to management;
- management works through existing Kharej -> Iran Agent session.

## Kharej cannot dial Controller WebUI port

- Node Offline;
- existing tunnels remain;
- do not delete/reconfigure them automatically.

## Invalid/revoked Node credential

- session rejected;
- no Node operation runs;
- no useful secret detail leaked.

---

# 52. SECURITY REVIEW — REQUIRED

Before declaring completion, conduct a dedicated security review for:

- authentication bypass;
- WebSocket upgrade accepted as authenticated before Noise completes;
- Node ID spoofing;
- one Node impersonating another;
- enrollment-token reuse;
- expired enrollment acceptance;
- revoked Node reconnect;
- permanent-secret leakage;
- enrollment-code leakage;
- browser/API exposure of Node secrets;
- secrets in logs;
- secrets in command line;
- cross-Node request delivery;
- cross-session response delivery;
- protocol downgrade/version confusion;
- replay assumptions;
- stale session deleting replacement session;
- unbounded pending RPCs;
- unbounded writer queue;
- unbounded stream buffers;
- oversized frames;
- malformed encrypted messages;
- malformed JSON;
- unknown operation handling;
- arbitrary shell accidentally introduced;
- arbitrary filesystem access;
- unsafe Unix-socket permissions;
- Telegram relay becoming a generic proxy;
- TLS verification being disabled;
- goroutine leaks;
- socket/connection leaks.

Fix issues discovered.

---

# 53. CONCURRENCY REVIEW — REQUIRED

Perform a separate concurrency/resource review.

Explicitly examine:

- registry locking;
- session replacement;
- session shutdown;
- pending request map;
- writer queue;
- reader loop;
- stream registry;
- context cancellation;
- heartbeat timers;
- reconnect timers;
- WebSocket close paths;
- Node revoke while requests are in flight;
- Controller shutdown;
- monitor shutdown;
- Unix IPC shutdown;
- Telegram long-poll cancellation.

Run race-sensitive tests where practical.

---

# 54. EXECUTION DISCIPLINE

This task is large.

Maintain an implementation checklist in working notes.

Do not declare a phase complete because it compiles.

Before editing:

1. inspect repository-local instructions;
2. record `git status`;
3. preserve unrelated user changes;
4. run baseline test/verify commands;
5. record pre-existing failures separately.

After EACH major architectural phase:

1. run narrow relevant tests;
2. inspect the diff;
3. search for accidental regressions;
4. fix issues before moving on.

Keep the repository buildable as much as practical.

Do not rewrite unrelated code.

Prefer small coherent changes.

Before deleting legacy SSH Fleet code:

- first make the replacement Agent path work;
- test it;
- then remove obsolete SSH implementation;
- then perform repository-wide searches for dead SSH-Fleet references.

---

# 55. IMPLEMENTATION PHASES

Complete all phases in this task.

## Phase A — Baseline

Inspect:

- architecture;
- Fleet;
- Node protocol;
- WebUI routing;
- monitor;
- Telegram;
- manual tunnel flows;
- tests;
- installer;
- updater;
- backup;
- services.

Run baseline checks.

## Phase B — Regression protection

Before major refactor, add/expand tests covering:

- manual reverse tunnel creation;
- manual direct tunnel creation;
- current tunnel engine lifecycle;
- Managed pairing behavior worth preserving.

## Phase C — Protocol core

Implement/test:

- versioned envelope;
- Noise session;
- bounded message handling;
- request multiplexing;
- session registry;
- session generation;
- replacement races.

## Phase D — Enrollment

Implement/test:

- enrollment token;
- expiry;
- one-time semantics;
- permanent distinct Node credential;
- revoke;
- secure persistence;
- `fullpack node join`.

## Phase E — Controller gateway

Add `/_bp/node` to the EXISTING WebUI listener.

No second listener.

Ensure it bypasses browser base-prefix routing but does not bypass its own Agent authentication.

## Phase F — Agent client

Integrate into existing monitor service as independently supervised job.

Implement:

- connect;
- authenticate;
- hello;
- reconnect;
- heartbeat/liveness;
- RPC execution.

## Phase G — Fleet backend

Replace SSH-based calls with live Agent-session calls.

Remove reachability-by-dialing.

Use session state.

## Phase H — Fleet UI

Replace SSH Add Server with enrollment UI.

Add useful Agent status/error states.

## Phase I — Managed tunnel operations

Move managed pairing/edit/action/log/drift flows to Agent RPC.

Preserve existing safe semantics.

## Phase J — Telegram relay

Implement restricted Telegram raw TLS-byte egress over authenticated Agent streams and narrow local Unix IPC.

Retain sensible existing relay fallback.

## Phase K — SSH cleanup

Remove obsolete Fleet SSH code, state, APIs, UI, docs, dependencies where safe.

## Phase L — NaN timestamp fix

Fix and test alert relative-time rendering.

## Phase M — Install/backup/docs

Update:

- installer;
- service behavior;
- backup/restore;
- uninstall;
- architecture docs;
- Managed Servers docs;
- Telegram docs;
- CLI docs;
- security/design docs.

## Phase N — Full verification

Run all repository-native tests/checks.

## Phase O — Concurrency review

Review races/leaks/backpressure and fix them.

## Phase P — Security review

Review Agent auth/enrollment/Telegram relay and fix issues.

## Phase Q — Manual tunnel regression review

Explicitly retest manual/local operation with ZERO Managed Nodes.

---

# 56. MINIMUM ACCEPTANCE TESTS

At minimum verify all of the following.

1. Baseline tests/checks were run before changes and pre-existing failures recorded.

2. `go test ./...` passes after implementation.

3. Repository-native verify/lint/vet checks pass where present.

4. Race detector is run on new concurrency-heavy packages where practical.

5. Fleet stores no SSH username/password/fingerprint/SSH port.

6. `SSHRunner` and obsolete SSH Fleet path are gone.

7. Controller Agent gateway uses the SAME existing WebUI listener.

8. No second public management listener exists.

9. 80 is not automatically bound/reserved.

10. 443 is not automatically bound/reserved.

11. Foreign managed Node has no inbound management listener.

12. Agent uses configured Controller WebUI port, not hard-coded 7654.

13. Changing browser path prefix does not move/break `/_bp/node`.

14. Wrong enrollment token fails.

15. Expired enrollment token fails.

16. Enrollment token cannot be reused.

17. Enrollment token cannot authenticate a normal post-enrollment Agent session.

18. Permanent Node credential differs from enrollment credential.

19. Wrong permanent credential fails.

20. Revoked Node cannot reconnect.

21. Observed remote IP changes do not change Node identity.

22. Agent authenticates and connects outbound.

23. Multiple concurrent RPC calls receive correct responses.

24. Unknown request ID is handled safely.

25. Unsupported protocol version is rejected before operation execution.

26. Pending requests fail cleanly on disconnect.

27. Writer queue is bounded.

28. Slow/non-reading Agent does not cause unbounded memory growth.

29. Session replacement is atomic.

30. Superseded old session closing after replacement does NOT mark Node Offline.

31. Reconnect restores Online status.

32. Controller restart does not stop tunnel processes.

33. Controller restart does not automatically restart/reapply healthy tunnels.

34. WebUI stop does not stop monitor.

35. WebUI stop does not stop tunnel engines.

36. Agent/monitor restart does not stop tunnel engines.

37. Agent reconnect does not reconfigure otherwise healthy tunnels.

38. Iran -> Kharej management connectivity can be blocked and existing reverse Agent management still works.

39. Reverse tunnel can fail while Agent remains manageable.

40. Logs/status/restart can work while the data tunnel is broken, provided Agent path is alive.

41. Manual tunnel creation works with ZERO Managed Nodes.

42. Existing manual reverse setup still works.

43. Existing manual direct setup still works.

44. Existing CLI Iran setup still works.

45. Existing CLI Kharej setup still works.

46. `/api/tunnel/create` remains functional.

47. `/api/direct/create` remains functional.

48. Fine Tune/manual advanced settings remain available where currently supported.

49. Managed paired tunnel creation works over Agent RPC.

50. Managed paired edit semantics remain correct.

51. Managed start/stop/restart works.

52. Drift checking works.

53. Local delete does not silently destroy the remote tunnel.

54. Telegram works when Iran cannot directly reach Telegram but a healthy foreign Agent exists.

55. Telegram Agent relay cannot connect to arbitrary destinations.

56. Telegram TLS byte stream handles long polling.

57. Telegram long-poll cancellation releases all Controller/Agent resources.

58. Telegram foreign-Node failure permits retry via another healthy Node.

59. Bot Token is not persisted on foreign Nodes.

60. Bot Token is not logged.

61. Existing tunnel-based Telegram fallback remains usable where appropriate.

62. Stopping WebUI does not crash Telegram monitor.

63. Alert UI never renders `NaN d ago`.

64. Backup/restore behavior for new Node credentials is tested/documented.

65. Uninstall cleans Agent-specific configuration appropriately.

66. No generic remote shell or arbitrary filesystem RPC exists.

67. Repository-wide search shows no obsolete SSH-Fleet user-facing instructions remain.

---

# 57. MANUAL TUNNEL FINAL REGRESSION GATE

Before finishing, explicitly test this exact scenario:

Fresh Iran FullPack installation.

No Managed Nodes enrolled.

User opens Web Panel.

User chooses Add Tunnel.

User can create a Manual/Local reverse tunnel.

User can create a Manual/Local direct tunnel.

User can use the applicable current advanced options.

This MUST work.

Do not finish if Add Tunnel requires a Managed Node.

---

# 58. FINAL ARCHITECTURAL INVARIANTS

The finished design must satisfy all of these:

    SSH Fleet:
    REMOVED

    Iran Controller public management listener:
    ONE existing WebUI listener only

    Default example port:
    7654

    Hard-coded 7654:
    NO

    Port 80 required:
    NO

    Port 443 required:
    NO

    Kharej inbound management port:
    ZERO

    Agent direction:
    Kharej -> Iran

    Agent carrier:
    WebSocket

    Agent authentication/encryption:
    Noise using per-node permanent credential

    Enrollment credential:
    one-time, temporary, distinct from permanent credential

    Agent process:
    existing fullpack-monitor service

    Separate Agent service:
    NO

    Browser WebUI required on Kharej:
    NO

    Control plane depends on tunnel:
    NO

    Tunnel depends on Agent session:
    NO

    Tunnel process isolation preserved:
    YES

    Concurrent RPC:
    YES

    Bounded queues:
    YES

    Session-generation race protection:
    YES

    Generic remote shell:
    NEVER

    Manual Tunnel:
    FIRST-CLASS AND PRESERVED

    Telegram:
    Iran logic + restricted foreign egress

    Generic HTTP/TCP proxy exposed by Agent:
    NO

---

# 59. CLEANUP REQUIREMENTS

After replacement works and is tested:

- remove SSH Fleet implementation;
- remove SSH Fleet state;
- remove SSH Fleet UI;
- remove SSH Fleet docs;
- remove dead `node exec` support if no longer used;
- remove obsolete installation text saying Panel manages Nodes through SSH;
- update Managed Servers docs;
- update architecture docs;
- update Telegram docs;
- update Web Panel docs;
- update CLI docs;
- update service-layout docs;
- update backup docs;
- update security/design decisions;
- update tests;
- remove dead imports/dependencies.

Preserve project licensing, NOTICE and attribution.

Do not perform unrelated rebranding as part of this refactor.

---

# 60. FINAL REVIEW PASSES

Before producing the final report, perform at least THREE deliberate review passes after implementation.

REVIEW 1 — Functional/regression:

- Manual Tunnel;
- Managed Tunnel;
- Direct/Reverse;
- WebUI;
- monitor;
- Agent;
- Telegram;
- backup/install.

REVIEW 2 — Concurrency/resources:

- session replacement;
- goroutines;
- queues;
- maps;
- timers;
- cancellation;
- sockets;
- streams;
- reconnect.

REVIEW 3 — Security:

- enrollment;
- permanent credentials;
- Noise;
- revoke;
- WebSocket endpoint;
- TLS;
- secrets;
- Telegram egress;
- arbitrary-command/proxy exposure.

Fix all issues found and rerun relevant tests.

Do not simply state that the reviews were done.

Report concrete issues found and fixed.

---

# 61. FINAL REPORT

When implementation is complete, report clearly:

1. Architecture implemented.

2. Major files/packages changed.

3. SSH Fleet components removed.

4. Exact Agent connection direction.

5. Exact public listeners/ports after the refactor.

6. Confirmation that 80/443 are not consumed by the management feature.

7. Confirmation that foreign Nodes expose no management listener.

8. Agent configuration location and permissions.

9. Controller credential/state storage and permissions.

10. Enrollment lifecycle:
        temporary credential
        -> permanent credential
        -> invalidation.

11. Noise pattern/reuse chosen and why.

12. Session replacement strategy.

13. Queue/backpressure limits.

14. Managed Node online/offline semantics.

15. Behavior when Iran cannot dial the foreign IP.

16. Behavior when reverse tunnel is down.

17. Behavior when WebUI is stopped.

18. Telegram egress architecture.

19. Telegram fallback behavior.

20. Confirmation that Bot Token is not persisted on foreign Nodes.

21. Confirmation that Telegram relay is not a generic proxy.

22. Confirmation that Manual Tunnel creation remains available with zero Managed Nodes.

23. Confirmation that `/api/tunnel/create` remains working.

24. Confirmation that `/api/direct/create` remains working.

25. Tests/checks actually executed and their exact results.

26. Race/security issues discovered and fixed.

27. Any remaining limitations.

Do NOT claim production readiness if required tests are failing.

Do NOT hide failing tests.

Do NOT weaken an invariant merely to make the implementation easier.

If the exact current repository structure makes a different implementation materially cleaner, you may make the smallest justified deviation, but:

- document the deviation;
- explain why;
- preserve every architectural/security/port/manual-tunnel invariant in this specification.
