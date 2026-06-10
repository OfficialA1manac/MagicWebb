# Deploying MagicWebb on a free VPS (Oracle Cloud Always Free)

The app is one Go binary: HTTP server + HTMX UI + chain indexer + keeper, all
in-process. It needs only outbound HTTPS (Supabase Postgres + Coston2 RPC) and
one inbound port. DB migrations run automatically at startup (embedded goose).

## 1. Provision the VPS (free forever)

1. Sign up at <https://cloud.oracle.com/free> (card required for identity
   check; Always Free shapes are never billed).
2. Create instance: **VM.Standard.A1.Flex** — 4 OCPU / 24 GB RAM (Ampere ARM,
   Always Free), image **Ubuntu 24.04**, add your SSH public key.
   - If A1 capacity is unavailable in your home region, retry off-peak or use
     the AMD **VM.Standard.E2.1.Micro** (1 GB — still plenty; the binary idles
     under 100 MB).
3. Networking: in the instance's VCN → Security List, add ingress rules for
   TCP **80** and **443** from `0.0.0.0/0`. (Port 22 is open by default.)
4. On Ubuntu itself, open the same ports:

   ```bash
   sudo iptables -I INPUT -p tcp --dport 80 -j ACCEPT
   sudo iptables -I INPUT -p tcp --dport 443 -j ACCEPT
   sudo netfilter-persistent save
   ```

## 2. Build the binary

On the VPS (simplest — no cross-compile concerns):

```bash
sudo snap install go --channel=1.25/stable --classic
git clone https://github.com/OfficialA1manac/MagicWebb.git
cd MagicWebb/backend
CGO_ENABLED=0 go build -ldflags="-s -w" -o /home/ubuntu/magicwebb ./cmd/server
```

(Or cross-compile from Windows: `$env:GOOS='linux'; $env:GOARCH='arm64'; $env:CGO_ENABLED='0'; go build -o magicwebb ./cmd/server` and `scp` it up. Use `GOARCH=amd64` for the E2.1.Micro shape.)

## 3. Configure

```bash
sudo mkdir -p /etc/magicwebb
sudo nano /etc/magicwebb/env        # contents below
sudo chmod 600 /etc/magicwebb/env
```

Minimum production env (see `.env.example` for the full list):

```ini
ENV=production
HTTP_ADDR=:8080
POSTGRES_URL=<Supabase pooler DSN>
RPC_URL=https://coston2-api.flare.network/ext/C/rpc
CHAIN_ID=114
MARKETPLACE_ADDR=0x6E5d2332a30bE3aBC35a0dC795583a06cfedFc9C
AUCTION_ADDR=0xAF76199b6BB77fEB1E1D8541C30557f3231F6F5c
OFFERBOOK_ADDR=0x9D38CB500551BfDD106CA8052C9Bfd146A5f6CbA
INDEX_FROM_BLOCK=31610228
JWT_SECRET=<openssl rand -hex 32>
KEEPER_KEY=<keeper private key — fund its address with C2FLR for gas>
SIWE_DOMAIN=<your domain, or the VPS public IP>
FRONTEND_URL=https://<your domain>
# WC_PROJECT_ID=<optional — WalletConnect v2 project id>
```

## 4. Run as a service

`/etc/systemd/system/magicwebb.service`:

```ini
[Unit]
Description=MagicWebb marketplace
After=network-online.target
Wants=network-online.target

[Service]
User=ubuntu
EnvironmentFile=/etc/magicwebb/env
ExecStart=/home/ubuntu/magicwebb
Restart=always
RestartSec=5
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=read-only

[Install]
WantedBy=multi-user.target
```

```bash
sudo systemctl daemon-reload
sudo systemctl enable --now magicwebb
journalctl -u magicwebb -f       # watch startup: migrations, indexer catch-up
```

## 5. TLS (free, automatic) — Caddy reverse proxy

```bash
sudo apt install -y caddy
```

`/etc/caddy/Caddyfile` (replace with your domain; an A record must point at
the VPS IP first — free subdomains: DuckDNS, or any domain you own):

```
yourdomain.example {
    reverse_proxy localhost:8080
}
```

```bash
sudo systemctl reload caddy
```

Caddy provisions Let's Encrypt certs automatically. No domain yet? Skip Caddy
and open port 8080 instead — wallets work fine over plain HTTP for testnet,
but use HTTPS before anything public-facing.

## 6. Update procedure

```bash
cd ~/MagicWebb && git pull
cd backend && CGO_ENABLED=0 go build -ldflags="-s -w" -o /home/ubuntu/magicwebb.new ./cmd/server
mv /home/ubuntu/magicwebb.new /home/ubuntu/magicwebb
sudo systemctl restart magicwebb
```

Migrations apply themselves on restart. The indexer resumes from the last
indexed block stored in Postgres.
