# Troubleshooting

Ordered by how often each one is actually the answer, not by how interesting it
is. Work down the list; most problems are settled in the first two sections.

Everything here can be done from `sudo fullpack` → **Health Check** and
**Diagnose**. The commands are given as well, for when the menu is not
convenient — over a phone, from a script, or when the panel is the thing that
is broken.

---

## The tunnel says it is up and nothing goes through

This is the most expensive failure in the product and it has a short list of
causes. The tunnel holds its control channel, the panel shows green, and
traffic dies — so nothing looks wrong anywhere.

FullPack now notices this on its own: the watchdog watches for **one direction
moving while the other is frozen**, which is what a stall looks like from the
outside, and reports it before it restarts anything. If you have a Telegram bot
configured you will have had a message. This is what to do about it.

### 1. Check the MTU first

It is the cause more often than everything else here put together, and it is
invisible: the handshake is small and gets through, the first real transfer is
full-sized and does not.

```
sudo fullpack        # → Diagnose → the tunnel
```

Look for the path MTU line. If it is below 1500, clamp to it:

```
sudo fullpack        # → Manage tunnels → <tunnel> → TCP MSS clamp
```

A sensible clamp is **path MTU − 40** for IPv4, **− 60** for IPv6. If Diagnose
could not probe the path, try 1400 first and go down in steps of 20.

For a layer-3 tunnel, set `auto_mtu = true` instead and let it find the number.

### 2. Check the service at the far end

The tunnel delivers each connection one hop further, to whatever the forwarded
port points at. If nothing is listening there, every connection dies one step
past the end of the tunnel — and the tunnel itself is perfectly healthy.

On the **kharej** machine:

```
ss -tlnp | grep <the backend port>
```

FullPack reports this itself when it can see it. The panel's tunnel card shows a
"last hop" warning, and so does the terminal:

```
fullpack tunnel status <name>
...
last hop  127.0.0.1:8080 refused (42 failed)
```

`refused` is a service that is not running. `timeout` is usually a firewall on
the same machine. The two have different fixes, which is why the word is there.

### 3. Check the two ends agree

Every paired setting has to match, and a mismatch produces exactly this failure:
connected, carrying nothing. The token, the transport, the packet profile, the
FEC scheme, `stealth`, the encapsulation.

The fastest check is to read the two files side by side:

```
sudo cat /etc/fullpack/<name>.toml          # on this machine
```

and the same on the other. `token`, `transport`, the port, and — for a layer-3
tunnel — `carrier`, `encap`, `fec_data`/`fec_parity` and `profile`.

If the far end is a server the panel manages, do not compare files at all —
build the pair from the panel instead. Creating a tunnel there writes **both**
ends, so the paired settings cannot disagree, and an existing tunnel can be
linked to the server holding its other half from the tunnel's card. See
[managed servers](managed-servers.md).

### 4. Look at what it is actually carrying

```
fullpack tunnel status <name>
```

```
name      kharej-443
role      server
transport tcp
address   0.0.0.0:443
state     online
peer      203.0.113.9:51234
carried   1.4 GB in, 22.8 MB out (3s ago)
```

Bytes in and bytes out are counted separately, and that is the whole point:
**one climbing and the other frozen** is the stall above. **Both frozen** is an
idle tunnel, which is not a fault — most tunnels are idle most of the time.

Run it twice a few seconds apart. If neither number moved while you know traffic
is being sent at it, the tunnel is not carrying.

The figure is dated because a reading nobody can date is a reading nobody can
use; if the engine has not written one recently the line is absent rather than
stale.

---

## The tunnel will not connect at all

### It never connected once

Then it is configuration, not the network. In order:

```
fullpack check -c /etc/fullpack/<name>.toml
```

This validates the file without starting anything, and it catches the quiet one:
a file describing two kinds of tunnel, where the engine runs the first and
silently ignores the rest. It also asks the engine itself, so a file it passes
is one the engine starts on this machine — run it on the server the tunnel will
run on, as the user it runs as.

Then check, in this order:

- **The token matches.** It is the single most common mistake and it produces no
  message that names it: the server refuses the connection and neither log says
  why, because saying why would tell an attacker they were close.
- **The port matches.** The client's `remote_addr` port must be the server's
  `bind_addr` port.
- **The transport matches.** `tcp` on one end and `tcpmux` on the other is two
  different protocols.
- **The firewall.** On the Iran server, the tunnel port must be reachable:
  ```
  sudo ufw allow <port>/tcp     # or /udp for kcp, quic, udp
  ```

### It connected before and stopped

Then something changed on the path, not in the config.

```
sudo fullpack        # → Diagnose → the tunnel
```

If the address is unreachable but the server is up, the IP has probably been
filtered. Two answers, both built in:

- **Backup addresses** — a second IP, a different port, a CDN edge.
  `Manage tunnels → <tunnel> → Backup server addresses`
- **A transport fallback chain** — for when it is the *carrier* being blocked
  rather than the address. `Manage tunnels → <tunnel> → Transport fallback
  chain`. Both ends need the same list; see [transport fallback](transport-fallback.md).

If you do not know which of the two it is: if a plain `nc -vz <ip> <port>` from
outside Iran works and from inside does not, the address is filtered. If it
works from both and the tunnel still will not come up, the carrier is.

---

## The tunnel keeps restarting

A tunnel that mostly works is harder to notice than one that is plainly down,
and several real faults present this way. FullPack reports repeated restarts as
**one** condition rather than twenty separate lines, so check the alerts first:

```
sudo fullpack        # → Alerts
```

Common causes, in order:

- **MTU.** A path that drops full-sized packets kills the session on the first
  real transfer, every time. See the top of this page.
- **A keepalive set too tight.** `keepalive_period` too low tears down a tunnel
  that is merely slow. On a bad path, raise it.
- **The far end is restarting.** Check the other machine before changing
  anything on this one.

---

## The panel will not open

```
systemctl status fullpack-webui
journalctl -u fullpack-webui -n 50
```

The panel answers under a **secret path** and nowhere else, so a bookmark
without it gets a 404 rather than a login page. The path is printed at startup
and is shown in the menu:

```
sudo fullpack        # → Web Panel
```

If the port was changed and the page did not come back, the address is in the
same screen.

---

## A managed server shows as offline

The managed Node keeps an authenticated outbound Agent session; nothing is
opened on the far side. So "offline"
means one of:

- **The machine or its monitor service is down**, or it cannot reach the
  configured Controller URL over the existing WebUI listener.
- **The Agent configuration or certificate trust changed.** Check the
  `fullpack-monitor` journal on the Node and the WebUI journal on the
  Controller. If the Node was rebuilt, revoke the old entry and enroll it
  again with a new one-time code.
- **The Controller was restored without its sealing key.** The fleet list can
  remain visible while the sealed Agent credentials are unavailable; restore
  the key backup or re-enroll the affected Nodes. See
  [backup and restore](backup-restore.md) for the recovery.

The card records the last disconnect reason reported by the Agent.

---

## After an update

FullPack takes a snapshot before every update and rolls back on its own if the
tunnels do not come back. If you are reading this, that either worked or you
want to do it by hand:

```
sudo fullpack        # → Update → Rollback
```

If tunnels came back but something behaves differently, check that the two ends
are on the same version:

```
fullpack version
```

A new end talking to an old one is supported and tested in both directions, but
a feature added on one side is not available until both have it.

---

## Messages that refuse something on purpose

These are not faults. Each is a check doing its job, and each says what it saw.

- **`refusing a connection from <addr>: too many connections that have not
  proved the token`** — someone holds more half-open connections to a tunnel
  port than a real client ever does (128 from one host or /64, 1024 in all).
  The real client is not affected once it has connected from its address, so a
  tunnel that works while this scrolls past is being probed, not broken. If a
  kharej that has *never* connected is refused, it gets in when the flood's
  connections time out. Why it works this way:
  [ADR 0004](adr/0004-what-an-unproven-peer-may-hold.md).
- **`wss: the server did not prove it holds the tunnel token`** — on a `wss`
  kharej. The Iran server answers every upgrade with a proof made from the
  token and the TLS session; this one was missing or wrong. Either the tokens
  differ, or something on the path is terminating the TLS and talking to the
  kharej itself — a CDN in *proxied* mode set up for `ws` rather than `wss`, or
  a box doing interception. The first case is a config fix; the second is what
  the check is for. An Iran server on a build older than v1.8.5 sends no proof
  and is accepted, unless it already answered once in this run.
- **`<file> does not parse (…); it was kept as <file>.unreadable-<n> — starting
  from an empty one`** — a state file (the fleet, the panel's settings, the
  bot's) was damaged. Nothing was lost: the damaged file sits beside the new
  one. Compare the two, and move the old one back once it is fixed, with the
  service stopped. A file that parsed but had one value of the wrong type says
  *the rest was read, and a copy was kept* instead.
- **`socks: that destination is refused: it is this machine, or link-local`**
  — the built-in proxy was asked for the server's own loopback, an unspecified
  address, a link-local address (the cloud metadata service lives there) or a
  multicast one. It refuses those on every server, with or without a password.
- **`a new address for "<name>" needs its password entered again`** — in the
  fleet. The stored password is not sent to an address it was not given for;
  type it again with the new address.

---

## Reading the logs

```
journalctl -u fullpack-<name> -f       # one tunnel, live
journalctl -u fullpack-monitor -n 100   # the watchdog, the bot, the alerts
journalctl -u fullpack-webui -n 100     # the panel
```

The engine logs at `info` by default. For a fault you are actively chasing,
raise it for that tunnel and watch:

```
sudo sed -i 's/^log_level = .*/log_level = "debug"/' /etc/fullpack/<name>.toml
sudo systemctl restart fullpack-<name>
journalctl -u fullpack-<name> -f
```

Put it back to `info` afterwards. `trace` on a busy tunnel writes a line per
connection and will fill a journal quickly.

---

## When you are stuck

Collect this before asking anyone:

```
fullpack version
fullpack tunnel list
sudo fullpack        # → Diagnose, and copy the output
journalctl -u fullpack-<name> -n 200
```

The same four from **both** machines. A tunnel has two ends and nearly every
question about one of them is answered by the other.

---

<div dir="rtl">

## خلاصهٔ فارسی

به ترتیبِ اینکه هر کدام **چند وقت یک‌بار واقعاً جوابند** مرتب شده، نه به ترتیب
جذابیت. از بالا برو؛ بیشتر مشکل‌ها در دو بخش اول تمام می‌شوند. همهٔ این‌ها از
`sudo fullpack` → **Health Check** و **Diagnose** هم در دسترس است.

### تونل می‌گوید بالاست ولی چیزی رد نمی‌شود

گران‌ترین خرابی این محصول، و فهرست علت‌هایش کوتاه است. خود FullPack این را
می‌بیند: watchdog دنبال حالتی می‌گردد که **یک جهت حرکت می‌کند و جهت دیگر یخ زده**
— شکلِ بیرونیِ یک stall — و قبل از هر restart خبر می‌دهد.

۱. **اول MTU.** از همهٔ بقیه رویِ‌هم بیشتر علت است و نامرئی است: handshake کوچک
است و رد می‌شود، اولین انتقال واقعی کامل است و نمی‌شود. `Diagnose` خط path MTU را
می‌دهد؛ اگر زیر ۱۵۰۰ بود، در `Manage tunnels → <tunnel> → TCP MSS clamp` به همان
اندازه clamp کن: **path MTU منهای ۴۰** برای IPv4 و **منهای ۶۰** برای IPv6. اگر
probe جواب نداد، از ۱۴۰۰ شروع کن و ۲۰ تا ۲۰ تا پایین بیا. برای تونل لایه‌۳
به‌جایش `auto_mtu = true` بگذار تا خودش پیدا کند.

۲. **سرویسِ آن‌طرف.** تونل هر اتصال را یک قدم جلوتر تحویل می‌دهد؛ اگر آنجا چیزی
گوش نمی‌دهد، هر اتصال یک قدم بعدِ تونل می‌میرد و خود تونل کاملاً سالم است. روی
سرور **خارج**: `ss -tlnp | grep <port>`. خود FullPack هم وقتی ببیند می‌گوید —
`last hop ... refused` یعنی سرویس بالا نیست، `timeout` معمولاً یعنی فایروال روی
همان ماشین. این دو راه‌حلشان فرق دارد و برای همین کلمه‌اش نوشته شده.

۳. **دو طرف باید بخوانند.** هر تنظیمی که جفتی است باید یکی باشد و ناهماهنگی دقیقاً
همین خرابی را می‌دهد: وصل، بدون عبور. `token`، `transport`، پورت، و برای لایه‌۳
`carrier`، `encap`، `fec_data`/`fec_parity` و `profile`. اگر آن‌طرف سروری است که
پنل مدیریتش می‌کند، اصلاً فایل‌ها را مقایسه نکن — جفت را از خود پنل بساز، چون
**هر دو سر** را می‌نویسد.

۴. **ببین واقعاً چه حمل می‌کند:** `fullpack tunnel status <name>`. بایت ورودی و
خروجی جدا شمرده می‌شوند و نکته همین است: **یکی بالا برود و دیگری یخ باشد** همان
stall است؛ **هر دو یخ** یعنی تونل بیکار است، که خرابی نیست.

### تونل اصلاً وصل نمی‌شود

**اگر هیچ‌وقت وصل نشده،** مشکل تنظیمات است نه شبکه. اول
`fullpack check -c /etc/fullpack/<name>.toml`، بعد به همین ترتیب: توکن یکی باشد
(رایج‌ترین اشتباه، و هیچ پیامی اسمش را نمی‌برد — سرور رد می‌کند و هیچ لاگی نمی‌گوید
چرا، چون گفتنش به مهاجم می‌گوید نزدیک شده)، پورت یکی باشد، ترنسپورت یکی باشد، و
فایروال سرور ایران پورت تونل را باز داشته باشد.

**اگر قبلاً وصل می‌شده و حالا نه،** چیزی در مسیر عوض شده نه در فایل. `Diagnose`
بگیر. اگر آدرس در دسترس نیست ولی سرور بالاست، احتمالاً IP فیلتر شده. دو جواب، هر
دو داخلی: **آدرس‌های پشتیبان** برای IP فیلترشده، و **زنجیرهٔ fallback ترنسپورت**
برای وقتی خودِ *حامل* بلاک شده. اگر نمی‌دانی کدام است: `nc -vz <ip> <port>` از
بیرون ایران جواب بدهد و از داخل ندهد یعنی آدرس فیلتر است؛ از هر دو جواب بدهد و
تونل بالا نیاید یعنی حامل.

### تونل مدام ری‌استارت می‌شود

اول `sudo fullpack → Alerts` — ری‌استارت‌های پیاپی به‌صورت **یک** وضعیت گزارش
می‌شوند نه بیست خط جدا. علت‌های رایج به ترتیب: **MTU** (مسیری که پکت کامل را دور
می‌ریزد، سشن را روی اولین انتقال واقعی می‌کشد)، **keepalive خیلی تنگ** (روی مسیر
بد بالاتر ببر)، و **ری‌استارت‌شدنِ آن‌طرف** — قبل از دست‌زدن به این ماشین آن یکی را
ببین.

### پنل باز نمی‌شود

`systemctl status fullpack-webui` و `journalctl -u fullpack-webui -n 50`. پنل زیر
یک **مسیر مخفی** جواب می‌دهد و جای دیگری نه، پس bookmark بدون آن ۴۰۴ می‌گیرد نه
صفحهٔ ورود. مسیر در `sudo fullpack → Web Panel` نوشته شده.

### سرور مدیریت‌شده offline است

Node با Agent از طریق اتصال خروجی به آدرس Controller وصل می‌شود. اگر offline است،
وضعیت `fullpack-monitor` و دسترسی Node به پورت WebUI کنترلر را بررسی کن. اگر
گواهی یا تنظیمات اتصال تغییر کرده‌اند، لاگ `fullpack-monitor` روی Node و
`fullpack-webui` روی Controller را ببین. برای Node بازسازی‌شده، ورودی قبلی را
لغو و با کد یک‌بارمصرف جدید ثبت کن.

### بعد از آپدیت

قبل از هر آپدیت snapshot گرفته می‌شود و اگر تونل‌ها برنگردند خودش برمی‌گردد.
دستی: `sudo fullpack → Update → Rollback`. اگر تونل‌ها برگشتند ولی رفتار فرق
دارد، نسخهٔ دو طرف را با `fullpack version` مقایسه کن — نسخهٔ جدید با قدیم کار
می‌کند و در هر دو جهت تست شده، ولی قابلیتی که یک طرف اضافه کرده تا وقتی هر دو
نداشته باشند در دسترس نیست.

### پیام‌هایی که عمداً چیزی را رد می‌کنند

این‌ها خطا نیستند؛ هر کدام یک بررسی است که کارش را می‌کند:

- **`too many connections that have not proved the token`** — کسی بیش از حدِ یک
  کلاینت واقعی اتصالِ نیمه‌باز نگه داشته (۱۲۸ از یک میزبان یا یک /64، ۱۰۲۴ در کل).
  کلاینتی که یک بار از همان آدرس وصل شده رد نمی‌شود؛ تونلی که کار می‌کند و این پیام
  را می‌بیند دارد اسکن می‌شود، خراب نیست. ([ADR 0004](adr/0004-what-an-unproven-peer-may-hold.md))
- **`wss: the server did not prove it holds the tunnel token`** — روی خارجِ `wss`.
  یا توکن‌ها فرق دارند، یا چیزی در مسیر TLS را باز می‌کند و خودش جواب می‌دهد (CDN
  در حالت proxied که برای `ws` تنظیم شده، یا جعبهٔ شنود). سرور ایرانِ قدیمی‌تر از
  v1.8.5 اثباتی نمی‌فرستد و پذیرفته می‌شود، مگر در همین اجرا یک بار جواب داده باشد.
- **`… it was kept as <file>.unreadable-<n>`** — یک فایل وضعیت (ناوگان، تنظیمات
  پنل، ربات) خراب بود. چیزی از دست نرفته: فایل خراب کنار فایل تازه مانده؛ درستش کن
  و با سرویسِ خاموش سر جایش برگردان.
- **`socks: that destination is refused`** — پراکسی داخلی به loopback، آدرس
  link-local (سرویس metadata ابر همین‌جاست)، unspecified یا multicast خودِ سرور
  وصل نمی‌شود؛ با رمز یا بی‌رمز.
- **`a new address for "<name>" needs its password entered again`** — در ناوگان،
  رمزِ ذخیره‌شده به آدرسی که برایش داده نشده فرستاده نمی‌شود؛ با آدرس تازه دوباره
  واردش کن.

### خواندن لاگ

```
journalctl -u fullpack-<name> -f
journalctl -u fullpack-monitor -n 100
journalctl -u fullpack-webui -n 100
```

پیش‌فرض `info` است. برای خطایی که دنبالش هستی موقتاً `log_level = "debug"` بگذار و
بعد برش گردان. `trace` روی تونل شلوغ برای هر اتصال یک خط می‌نویسد و journal را
سریع پر می‌کند.

### وقتی گیر کردی

قبل از پرسیدن از کسی این چهارتا را جمع کن — **از هر دو ماشین**:
`fullpack version`، `fullpack tunnel list`، خروجی `Diagnose`، و
`journalctl -u fullpack-<name> -n 200`. تونل دو سر دارد و تقریباً هر سؤالی دربارهٔ
یک سر را آن یکی جواب می‌دهد.

</div>

---
[← Back to the docs index](README.md)

---

*Last verified against FullPack v1.8.8.*
