# FullPack

## Quick Start

On each Ubuntu amd64 server, install the required system tools and FullPack:

```bash
sudo apt update
sudo apt install -y git curl tar
git clone https://github.com/firegoood/FullPck.git FullPack
cd FullPack
sudo bash install.sh
```

The installer opens the interactive menu. To reopen it later:

```bash
sudo fullpack
```

For a reverse tunnel, choose **Setup Iran → Reverse** on the Iran server. Copy the generated Setup Link. On the other server, choose **Setup Kharej → Reverse** and paste the link. Check the result under **Manage → Status**.

For a managed foreign server, keep the WebUI on Iran only: add the node from the Iran panel, then run `sudo fullpack node join` on the foreign server. The foreign server uses `fullpack-monitor` and has no local WebUI.

Based on BackPack by Amin Mohammadi (AminMGMT)

https://github.com/AminMGMT/BackPack
