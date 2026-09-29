# FullPack

## شروع سریع (Quick Start)

روی هر سرور Ubuntu با معماری amd64، ابزارهای لازم و FullPack را نصب کنید:

```bash
sudo apt update
sudo apt install -y git curl tar
git clone https://github.com/firegoood/FullPck.git FullPack
cd FullPack
sudo bash install.sh
```

نصب‌کننده منوی تعاملی را باز می‌کند. برای اجرای دوباره:

```bash
sudo fullpack
```

برای ساخت تونل معکوس، در سرور ایران **Setup Iran → Reverse** را انتخاب کنید و Setup Link ساخته‌شده را کپی کنید. در سرور خارج **Setup Kharej → Reverse** را انتخاب کنید و لینک را وارد کنید. وضعیت را از **Manage → Status** بررسی کنید.

برای سرور خارجِ مدیریت‌شده، WebUI فقط روی ایران فعال می‌ماند: ابتدا نود را از پنل ایران اضافه کنید، سپس روی سرور خارج `sudo fullpack node join` را اجرا کنید. سرور خارج فقط از `fullpack-monitor` استفاده می‌کند و WebUI محلی ندارد.

Based on BackPack by Amin Mohammadi (AminMGMT)

https://github.com/AminMGMT/BackPack
