# FullPack

## شروع سریع (Quick Start)

روی سرور ایران با Ubuntu و معماری amd64، نصب را با نقش کنترلر انجام دهید. در صورت نیاز، رمز `sudo` درخواست می‌شود و پس از نصب منو باز می‌شود:

```bash
bash <(curl -fsSL https://raw.githubusercontent.com/firegoood/FullPck/main/install.sh) --role iran
```

روی سرور خارج نقش نود مدیریت‌شده را انتخاب کنید تا WebUI محلی اجرا نشود:

```bash
bash <(curl -fsSL https://raw.githubusercontent.com/firegoood/FullPck/main/install.sh) --role kharej
```

پس از افزودن نود از پنل ایران، ثبت‌نام را روی سرور خارج کامل کنید:

```bash
sudo fullpack node join
```

برای اجرای دوبارهٔ منوی CLI روی هر سرور:

```bash
sudo fullpack
```

برای ساخت تونل معکوس دستی، در سرور ایران **Setup Iran → Reverse** را انتخاب کنید و Setup Link ساخته‌شده را کپی کنید؛ روی سرور خارج از **Setup Kharej → Reverse** استفاده کنید. سرور خارجِ مدیریت‌شده از `fullpack-monitor` استفاده می‌کند و WebUI محلی ندارد.

Based on BackPack by Amin Mohammadi (AminMGMT)

https://github.com/AminMGMT/BackPack
