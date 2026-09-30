# FullPack

## Quick Start

On the Iran server (Ubuntu amd64 or arm64), install as Controller. Enter your sudo password if prompted; the interactive menu opens after installation:

```bash
bash <(curl -fsSL https://raw.githubusercontent.com/firegoood/FullPck/main/install.sh) --role iran
```

On the foreign server, install as a managed Node. This does not start a local WebUI:

```bash
bash <(curl -fsSL https://raw.githubusercontent.com/firegoood/FullPck/main/install.sh) --role kharej
```

Add the foreign node in the Iran panel, then complete enrollment on the foreign server:

```bash
sudo fullpack node join
```

To reopen the CLI menu on either server:

```bash
sudo fullpack
```

For a reverse tunnel, choose **Setup Iran → Reverse** on the Iran server. Copy the generated Setup Link. On the other server, choose **Setup Kharej → Reverse** and paste the link. Check the result under **Manage → Status**.

The foreign server uses `fullpack-monitor` and has no local WebUI. For a manual tunnel without Fleet, use **Setup Kharej → Reverse** on the foreign server and paste the Setup Link from Iran.

Based on BackPack by Amin Mohammadi (AminMGMT)

https://github.com/AminMGMT/BackPack
