# FullPack

## Quick Start

On each Ubuntu amd64 server, install the required tools and clone FullPack:

```bash
sudo apt update
sudo apt install -y git curl tar
git clone https://github.com/firegoood/FullPck.git FullPack
cd FullPack
```

On the Iran server, install as Controller. The interactive menu starts the WebUI and monitor:

```bash
sudo bash install.sh --role iran
```

On the foreign server, install as a managed Node. This does not start a local WebUI:

```bash
sudo bash install.sh --role kharej
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
