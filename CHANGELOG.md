# Changelog

All notable changes to FullPack are documented here.

## v1.8.13 — 2026-10-01

- Alertهای HTTP 5xx مانند `502` ساخت Managed با عنوان خطای عملیاتی نمایش داده می‌شوند؛ ثبت audit و ارسال هشدار باقی است و برچسب امنیتی 401/403/429 حفظ می‌شود.
- شکست Ping اولیه و پایان جلسهٔ Agent در journal کنترلر با علت پاک‌سازی‌شده، شمارندهٔ پیام‌ها و زمان آخرین دریافت معتبر ثبت می‌شود؛ دادهٔ محرمانه و payload وارد log نمی‌شود.
- خطای Ping بی‌پاسخ روی جلسهٔ متصل از Offline و ناتمام‌ماندن reconnect تفکیک می‌شود و جزئیات در بالای فرم ساخت دیده می‌شود.
- راهنمای HTTPS/WSS با pin گواهی self-signed روی همان پورت کنترلر برای مسیرهای HTTP دارای توقف ترافیک ثبت شد. آزمون اتصال واقعی Agent از طریق TLS با pin تا پس از deadline زنده‌بودن اضافه شد.

## v1.8.12 — 2026-10-01

- Online سرور پس از دریافت metadata دوباره بررسی می‌شود؛ قطع حین دریافت اطلاعات دیگر به‌صورت Online نمایش داده نمی‌شود.
- ترافیک معتبر و heartbeat رمز‌شدهٔ Agent، deadline اتصال را تمدید می‌کنند؛ قطع ۷۵ثانیه‌ای کانال سالم هنگام نرسیدن WebSocket Pong رفع شد.
- ساخت Managed پیش از ایجاد هر تونل، پاسخ معتبر Ping می‌گیرد و برای reconnect کوتاه حداکثر هشت ثانیه منتظر می‌ماند. عملیات ایجاد تونل خودکار تکرار نمی‌شود.
- سرور انتخاب‌شده با Offline شدن خودکار عوض نمی‌شود. راهنمای خطای ساخت برای مشکل اتصال اصلاح شد و علت‌های پاک‌سازی‌شدهٔ اتصال در journal ثبت می‌شوند.

## v1.8.11 — 2026-10-01

- خطای `api.nodePair is not a function` در ساخت تونل Managed رفع شد؛ تست با API واقعی پنل برای reverse و direct اضافه شد.
- انتخاب سرور خارج در فرم Managed از مرحلهٔ اول دیده می‌شود، سرورهای Offline مشخص‌اند و وضعیت بدون بستن صفحه به‌روز می‌شود.
- وضعیت Online از کانال احراز هویت‌شدهٔ Agent خوانده می‌شود؛ دریافت اطلاعات جانبی دیگر اتصال سالم را معطل نمی‌کند. درخواست اطلاعات محدود به پنج ثانیه و polling پنل بدون درخواست‌های هم‌پوشان است.
- قطع سریع اتصال Agent نیز مشمول backoff تصاعدی همراه با jitter است تا چرخهٔ اتصال و قطع پی‌درپی ایجاد نشود.
- یک سرور خارج می‌تواند هم‌زمان در چند کنترلر ایران ثبت شود؛ هر کنترلر هویت، اعتبارنامه، pin و اتصال مستقل دارد. افزودن کنترلر دوم اتصال اول و کارهای دیگر monitor را دوباره راه‌اندازی نمی‌کند.
- تونل‌های Managed جدید روی سرور خارج نام متمایزی دارند تا تونل‌های هم‌نام دو کنترلر روی هم نوشته نشوند. نام و ارتباط تونل‌های موجود حفظ می‌شود.
- Connection Test آدرس کامل پنل ایران را نیز می‌پذیرد و فقط hostname را در لینک تست قرار می‌دهد؛ پورت WebUI با پورت موقت تست اشتباه نمی‌شود. دستور تست از enrollment تفکیک شده است.

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
