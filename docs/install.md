# Installing FullPack

## The normal way

Choose the role on each VPS. The installer asks for sudo when needed:

```bash
# Iran Controller
bash <(curl -fsSL https://raw.githubusercontent.com/firegoood/FullPck/main/install.sh) --role iran

# Managed foreign Node
bash <(curl -fsSL https://raw.githubusercontent.com/firegoood/FullPck/main/install.sh) --role kharej
```

From the release after v1.8.7 onward, prebuilt archives are provided only for
Linux amd64 and arm64. On other recognized architectures the installer can
build from source, but no prebuilt update archive is promised.

If a prebuilt release exists, the installer downloads the archive for your
architecture (amd64/arm64) into `/root/FullPack`, **verifies it against the
checksum published with the release**, and installs the binary. Until the first
release is published, it downloads the source archive from the FullPack repository
and builds on the server with Go. The Iran role opens the menu when run in an
interactive terminal. The foreign role keeps the local WebUI off; add the Node
in the Iran panel, then run `sudo fullpack node join` on the foreign server.

Reopen the menu any time with:

```bash
sudo fullpack
```

Everything lands in a tidy layout — the release bundle in `/root/FullPack`,
backups in `/root/FullPack/backups`, tunnel configs in `/etc/fullpack`. See
[server layout](server-layout.md).

> **Building from source** also works from a local checkout: clone the repo and
> run `sudo bash install.sh --role iran` or `--role kharej` inside it. If the release
> download fails, it builds with Go, fetching modules **directly first** and via
> Iran-friendly mirrors (RunFlare, goproxy.cn) only when direct access fails.

---

## Offline install (the server cannot reach GitHub)

Download the release on any machine **with** internet, copy it to the server, and
install it there. Nothing is fetched from the VPS.

From the [releases page](https://github.com/firegoood/FullPck/releases/latest),
download the archive for the server's architecture — run `uname -m` on it:
`x86_64` → `fullpack_linux_amd64.tar.gz`, `aarch64` → `fullpack_linux_arm64.tar.gz`.

### With the installer (recommended)

It also records the layout for the uninstaller. Download `install.sh` and
`SHA256SUMS` alongside the archive, put all three in the **same folder** on the
VPS, and run it. It finds the local archive, verifies it against `SHA256SUMS`,
and never touches the network:

```bash
scp install.sh SHA256SUMS fullpack_linux_amd64.tar.gz root@SERVER_IP:/root/
ssh root@SERVER_IP "cd /root && sudo bash install.sh --role kharej"
```

Use `--role iran` for the Controller server.

### By hand

Upload the archive to the server, then as root:

```bash
sha256sum fullpack_linux_amd64.tar.gz        # compare against SHA256SUMS
tar xzf fullpack_linux_amd64.tar.gz
mkdir -p /etc/fullpack /root/FullPack/backups
install -m 0755 fullpack /usr/local/bin/fullpack
echo /root/FullPack > /etc/fullpack/install_path
printf '%s\n' kharej > /etc/fullpack/role  # use iran on the Controller
chmod 0600 /etc/fullpack/role
sudo fullpack
```

For a managed foreign Node installed by hand, run `sudo fullpack node join`
after adding the Node in the Iran panel. The installer above handles the role
marker automatically.

The `install_path` line is what the built-in uninstaller reads to know what to
remove; skip it and everything still runs, but uninstalling has to be done by
hand. `install -m 0755` already sets the executable bit, so no `chmod` is needed.

### Updating offline

The same way: repeat the steps with the newer archive. `install` replaces the
binary in place, and your tunnels in `/etc/fullpack` are untouched. Restart them
afterwards with `sudo fullpack` → **Manage → Restart ALL**.

---

## Updating online

**Main menu → 8) Update.** It downloads the release, verifies it against the
published SHA-256, installs it, and **rolls back automatically** if a tunnel does
not come back up. Anything that cannot be verified is refused rather than
installed. [More](updates.md).

## Uninstalling

**Main menu → 9) Uninstall** removes everything FullPack installed.

---

<div dir="rtl">

## خلاصهٔ فارسی

**نصب عادی:** یک دستور روی سرور که در صورت نیاز `sudo` می‌خواهد. آرشیو ریلیز
مخصوص معماری سرور را دانلود و با چک‌سام منتشرشده **تأیید** می‌کند؛ اگر هنوز
ریلیزی وجود نداشته باشد، کد FullPack را از مخزن می‌گیرد و روی سرور می‌سازد.
برای ایران `--role iran`
و برای سرور خارج `--role kharej` را بده. نصب ایران منو را باز می‌کند؛ نصب خارج
WebUI محلی را اجرا نمی‌کند. پس از افزودن نود در پنل ایران، روی خارج
`sudo fullpack node join` را اجرا کن. بعداً با `sudo fullpack` منو را باز کن.

**نصب آفلاین (سروری که به گیت‌هاب دسترسی ندارد):** فایل ریلیز را روی یک ماشین با
اینترنت دانلود کن و به سرور کپی کن. با `uname -m` معماری را ببین: `x86_64` یعنی
amd64 و `aarch64` یعنی arm64. بهترین راه این است که `install.sh` و `SHA256SUMS`
را هم کنار آرشیو در **یک پوشه** بگذاری و اسکریپت را اجرا کنی — خودش فایل محلی را
پیدا و تأیید می‌کند و اصلاً به شبکه دست نمی‌زند. روش دستی هم در بالا آمده؛ فقط
یادت باشد خط `install_path` را بنویسی، چون حذف‌کنندهٔ داخلی از روی آن می‌فهمد چه
چیزی را پاک کند.

**آپدیت:** از منوی اصلی گزینهٔ ۸ — با تأیید SHA-256 و **بازگشت خودکار** اگر تونل
بالا نیامد. آپدیت آفلاین هم همان مراحل نصب با آرشیو جدید است و کانفیگ‌های
`/etc/fullpack` دست‌نخورده می‌مانند.

</div>

---
[← Back to the docs index](README.md)

---

*Last verified against FullPack v1.8.11.*
