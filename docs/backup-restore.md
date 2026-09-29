# Backup & restore

Everything in one portable `.tar.gz`: every tunnel and token, the web-panel
password, Telegram settings, TLS certificates, and the auto-refresh schedule.
Backups live in `/root/FullPack/backups`.

## Restoring onto a different machine

A backup includes the encrypted credentials and identities of managed Node
Agents, but excludes the separate key (`node.key`) that decrypts them. The
short-lived pending enrollment file is also excluded.

Restoring onto the same machine is unaffected: the key is already there and the
restore leaves it alone.

Restoring onto a **different** machine brings the fleet list back, but Agent
sessions cannot authenticate until the fleet key is imported. Keep the key
separately from the archive, or remove and re-enroll each Node with a new
one-time code.

**On the machine that has the fleet, before you need it:**

`sudo fullpack` → **Backup & Restore** → **Show the fleet key**

Store it somewhere the backup is not. Keeping them together undoes the only
thing sealing them achieves.

**On the new machine, after restoring the backup:**

`sudo fullpack` → **Backup & Restore** → **Restore the fleet key**

It refuses if that machine already has a key of its own, because overwriting one
would make every Agent credential currently sealed there unreadable and there is no
undo — move the existing key aside yourself if that is genuinely what you mean.

Both entries only appear when a managed Node has a sealed credential, so a
single-machine install never sees them.


## Restore

Restoring **re-registers and starts every tunnel**, and traffic totals carry on
from where the backup left off rather than resetting to zero.

## Where you can do it

- the **CLI** — **Backup & Restore**,
- the [web panel](web-panel.md) — **Settings**, or
- the [Telegram bot](telegram-bot.md) — **Backup** button.

> Keep a backup file private — it contains tokens and the panel password.

---

<div dir="rtl">

## خلاصهٔ فارسی

همه‌چیز در یک فایل `.tar.gz` قابل‌حمل: تمام تونل‌ها و توکن‌ها، رمز پنل وب،
تنظیمات تلگرام، گواهی‌های TLS و زمان‌بندی ری‌فرش خودکار. فایل‌ها در
`/root/FullPack/backups` ذخیره می‌شوند.

**بازگردانی** همهٔ تونل‌ها را دوباره ثبت و استارت می‌کند و آمار ترافیک از همان
جایی که بوده ادامه پیدا می‌کند، نه از صفر.

از سه جا می‌شود انجامش داد: منوی CLI (گزینهٔ Backup & Restore)،
[پنل وب](web-panel.md) در بخش Settings، یا دکمهٔ Backup در
[ربات تلگرام](telegram-bot.md).

> فایل پشتیبان را خصوصی نگه دار — توکن‌ها و رمز پنل داخلش است.

</div>

---
[← Back to the docs index](README.md)

## Getting a backup off the machine

Backups are written to `/var/backups/fullpack` — on the server they describe.
The case they exist for is the case where that server is gone, so a copy
somewhere else is the only one that will be there.

**Backup & Restore → Copy backups off this machine.** It takes a command, with
`{}` standing for the backup file's path:

```
rclone copy {} remote:fullpack/
scp {} backup@10.0.0.9:/srv/fullpack/
restic backup {}
```

A command rather than a list of providers, because whatever you already use to
move files is the thing that works and the thing that does not need a release
to support a new destination.

It runs **directly, not through a shell**, so a `;` or a `|` in it is a word
rather than syntax — a destination read from a file cannot become a second
command. For a pipeline, write `sh -c '...'` and the decision is visible to
anyone reading the config.

The screen offers to try it immediately with your newest backup. Take the
offer: a destination that does not work fails the same way every week, and
without this you find out much later.

The weekly automatic backup sends a copy too, and reports the two separately.
A backup that could not be *written* is a full disk; a copy that could not be
*sent* is a destination that has changed. They have different fixes, so rolling
them into one message would send you to look at the wrong one.

## Testing a restore without performing one

A recovery procedure that has never been run is the ordinary state of a
disaster-recovery plan, and it is why they fail: the first time anybody
exercises it is the day it has to work, on a machine that is already gone, with
whatever the backup turned out not to contain.

**Backup & Restore → Test a restore.** It stages the archive exactly as a real
restore would — the same reader, the same name checks, the same size limits,
the same refusals, with the same wording — into a scratch directory, tells you
what it holds, and throws it away. **Nothing on the machine changes.**

It reports the tunnels, the panel settings, the Telegram configuration and the
certificates it would put back, and it warns about the two things that are
silent otherwise:

- a backup with **no tunnel configuration at all**, which would restore a
  machine with nothing on it;
- managed Node credentials **sealed with a key the archive does not
  contain**. That is deliberate — a stolen backup must not carry a fleet with
  it — and it means restoring onto a *different* machine gives you the server
  list and no way to reach any of them. Take the fleet key now and keep it
  somewhere else; the drill is where you find that out while it is still cheap.

Run it after any change to what the machine holds, and once before you need it.

---

*Last verified against FullPack v1.8.4.*
