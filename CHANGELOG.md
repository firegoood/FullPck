# Changelog

All notable changes to FullPack are documented here.

## v1.8.10 — 2026-09-30

- کد یک‌بارمصرف ثبت سرور خارج پس از ساخت در صفحهٔ Servers قابل‌دیدن و انتخاب می‌ماند و دکمهٔ «Copy code» برای اتصال HTTP نیز مسیر کپی جایگزین دارد.

## v1.8.9 — 2026-09-30

- هم‌پوشانی فیلدهای نام و آدرس کنترلر در فرم **Servers → Add a server** برطرف شد؛ اکنون هر دو فیلد جداگانه و روی نمایشگر باریک نیز قابل‌دیدن‌اند.

## v1.8.8 — 2026-09-30

- خطای نحوی `add.js` که اجرای کل WebUI را متوقف و صفحه را روی Loading نگه می‌داشت برطرف شد.
- انتخاب Node در فرم ساخت تونل به رفتار سالم قبلی بازگشت و آزمون اجرای واقعی ماژول ورودی WebUI به CI افزوده شد.
- از این نسخه فقط بسته‌های آمادهٔ Linux برای `amd64` و `arm64` منتشر می‌شوند.

## v1.8.7 — 2026-09-30

- دسترسی دائمی به Servers و مدیریت Nodeها به نوار اصلی WebUI بازگشت و شمارندهٔ سرورها دوباره نمایش داده می‌شود.
- تاریخچهٔ upstream از CHANGELOG اصلی جدا شد تا دستورهای BackPack و مسیر SSH Fleet با قابلیت‌های FullPack اشتباه گرفته نشوند.

## v1.8.6 — 2026-09-30

- تغییرات BackPack v1.8.5 شامل سهمیهٔ ترافیک تونل، آزمون اتصال، بهبودهای امنیتی پروتکل و بازآرایی موتور تونل با FullPack ادغام شد.
- معماری Agent خروجی روی WebUI کنترلر، ثبت Node با کد یک‌بارمصرف، پورت پیش‌فرض 7654 و نصب بدون WebUI روی سرور خارج حفظ شد. مسیر SSH مدیریت Fleet و Terminal وب وارد نشد.
- نصب از Setup Link با نقش Kharej سازگار شد و پورت محلی 443 برای Telegram رزرو نمی‌شود.
- محدودیت حافظهٔ pool و آزادسازی اتصال‌های منتظر در صف از اصلاحات FullPack روی ساختار جدید موتور تونل بازگردانده شد.

## v1.8.5 — 2026-09-29

### Added

- One-command installation for Iran Controllers and managed foreign Nodes. The installer requests `sudo` when needed and can build from the FullPack source archive before a prebuilt release is available.
- Managed Node enrollment and Fleet controls on the existing Controller WebUI listener, without a local WebUI on the foreign server.

### Changed

- Rebranded the executable, configuration paths, installer, and release assets as FullPack.
- Changed the default WebUI port to `7654`.

### Fixed

- Made Node enrollment retry-safe and bounded recovery for provisioned enrollment.
- Preserved manual tunnel setup while adding managed Nodes.

BackPack upstream release history is preserved in [docs/UPSTREAM_CHANGELOG.md](docs/UPSTREAM_CHANGELOG.md).
