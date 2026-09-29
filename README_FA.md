# FullPack

## شروع سریع (Quick Start)

روی هر سرور Ubuntu با معماری amd64، ابزارهای لازم را نصب و FullPack را دریافت کنید:

```bash
sudo apt update
sudo apt install -y git curl tar
git clone https://github.com/firegoood/FullPck.git FullPack
cd FullPack
```

روی سرور ایران نصب را با نقش کنترلر انجام دهید:

```bash
sudo bash install.sh --role iran
```

روی سرور خارج نقش نود مدیریت‌شده را انتخاب کنید تا WebUI محلی اجرا نشود:

```bash
sudo bash install.sh --role kharej
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
