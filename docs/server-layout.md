# Server layout (file locations)

Everything lives in a tidy, predictable layout. You can also see this any time
from **Manage → File Locations** in the CLI.

| Path | What |
|------|------|
| `/root/FullPack` | The release bundle and downloaded archives. |
| `/root/FullPack/backups` | [Backup](backup-restore.md) `.tar.gz` files. |
| `/etc/fullpack` | Tunnel configs (one `.toml` per tunnel) and runtime state. |
| `/usr/local/bin/fullpack` | The binary itself. |
| `fullpack-<name>.service` | A systemd unit per tunnel. |
| `fullpack-monitor.service` | The [monitor service](monitor-service.md). |

The install directory is recorded in `/etc/fullpack/install_path`, which is what
the uninstaller reads to know what to remove.

---

<div dir="rtl">

## خلاصهٔ فارسی

همه‌چیز در یک ساختار مرتب و قابل‌پیش‌بینی است. همین را هر وقت خواستی از
`Manage → File Locations` در CLI هم می‌بینی.

`‎/root/FullPack` بستهٔ ریلیز و آرشیوهای دانلودشده ·
`‎/root/FullPack/backups` فایل‌های [پشتیبان](backup-restore.md) ·
`‎/etc/fullpack` کانفیگ تونل‌ها (برای هر تونل یک فایل `.toml`) و وضعیت اجرا ·
`‎/usr/local/bin/fullpack` خودِ باینری ·
`fullpack-<name>.service` یک یونیت systemd به‌ازای هر تونل ·
`fullpack-monitor.service` [سرویس مانیتور](monitor-service.md).

مسیر نصب در `/etc/fullpack/install_path` ثبت می‌شود و حذف‌کنندهٔ داخلی از روی
همان می‌فهمد چه چیزی را پاک کند.

</div>

---
[← Back to the docs index](README.md)

---

*Last verified against FullPack v1.8.4.*
