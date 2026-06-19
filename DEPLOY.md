# Deploy — maze-runner on AWS EC2

Single Go binary (UI embedded), runs as a systemd service on an EC2 instance.
Access is restricted to the office network by security-group rules; the
admin/spectator UI is additionally gated by a password.

Fill in the placeholders (`<KEY>.pem`, `<PUBLIC_IP>`, `<SG_ID>`,
`<OFFICE_CIDR>`, `<PASSWORD>`, `<PROFILE>`) with your own values; none are
committed here.

## Topology

- One listener on `:8080`.
  - `/join/{id}` — contestants connect here.
  - `/` (spectator UI) and `/spectate` (start/restart control) — gated by HTTP
    Basic Auth (`MAZE_ADMIN_PASS`).
- Network access is limited to the office CIDR(s) via the security group.
  Automated Enforcement strips `0.0.0.0/0` rules in sandbox accounts, but
  specific-CIDR rules survive — so allowlist the office range, never the world.

## One-time instance setup

1. **Launch** an EC2 instance (Amazon Linux 2023) **with an SSH key pair**.
   Download the `.pem`, lock it down:
   ```bash
   chmod 400 ~/path/to/<KEY>.pem
   ```
2. **Security group** — allow only the office CIDR(s):
   ```bash
   P=<PROFILE>
   SG=<SG_ID>
   for cidr in <OFFICE_CIDR> <OFFICE_CIDR_2>; do
     # SSH for management
     aws-vault exec $P -- aws ec2 authorize-security-group-ingress --group-id $SG \
       --ip-permissions "IpProtocol=tcp,FromPort=22,ToPort=22,IpRanges=[{CidrIp=$cidr,Description='office SSH'}]"
     # game + admin UI
     aws-vault exec $P -- aws ec2 authorize-security-group-ingress --group-id $SG \
       --ip-permissions "IpProtocol=tcp,FromPort=8080,ToPort=8080,IpRanges=[{CidrIp=$cidr,Description='maze port'}]"
   done
   ```

## Quick deploy (script)

`deploy.sh` does build → copy → install/restart in one shot (first-time and
redeploy). It writes the systemd unit with the admin password each run.

```bash
MAZE_HOST=<PUBLIC_IP> MAZE_KEY=~/path/to/<KEY>.pem ./deploy.sh
# prompts for the admin password (hidden), or pass MAZE_ADMIN_PASS in env
```

Optional env: `MAZE_USER` (default `ec2-user`), `MAZE_ROOMS` (20), `MAZE_TICK`
(300), `GOARCH` (`amd64`, use `arm64` for Graviton).

The manual steps below are the same thing broken out, for reference.

## Build

Single static binary, UI embedded via `go:embed`. Match the instance arch
(`amd64` for x86_64, `arm64` for Graviton):

```bash
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o maze-server .
```

## Transfer + install

Copy the binary over SSH, then set up the service:

```bash
scp -i ~/path/to/<KEY>.pem maze-server ec2-user@<PUBLIC_IP>:/tmp/maze-server
ssh -i ~/path/to/<KEY>.pem ec2-user@<PUBLIC_IP>
```

On the box (replace `<PASSWORD>`):
```bash
sudo mv /tmp/maze-server /usr/local/bin/maze-server
sudo chmod +x /usr/local/bin/maze-server

sudo tee /etc/systemd/system/maze.service >/dev/null <<'UNIT'
[Unit]
Description=maze-runner
After=network.target
[Service]
ExecStart=/usr/local/bin/maze-server -rooms 20 -tick 300
Environment=MAZE_ADMIN_PASS=<PASSWORD>
Restart=always
User=root
[Install]
WantedBy=multi-user.target
UNIT

sudo systemctl daemon-reload
sudo systemctl enable --now maze
sleep 2 && systemctl is-active maze
```

## Access

**Admin / spectator (you):** browse `http://<PUBLIC_IP>:8080/` — the browser
prompts for credentials. Leave the username blank, enter the `MAZE_ADMIN_PASS`
value. Start/restart and configure rooms/tick from there.

**Contestants:** connect a client to the game port (any language; example client
in `client/`):
```bash
go run ./client -server ws://<PUBLIC_IP>:8080 <name>
# or build and hand out:
go build -o maze-client ./client && ./maze-client -server ws://<PUBLIC_IP>:8080 <name>
```

## Server flags

| Flag | Default | Meaning |
|------|---------|---------|
| `-rooms` | 15 | maze size (rooms per side) |
| `-extra` | 0.1 | fraction of extra passages (multiple solutions) |
| `-tick` | 300 | tick rate (ms) |
| `-addr` | `:8080` | listen address (also reads `PORT` env) |

`MAZE_ADMIN_PASS` env gates the admin UI + `/spectate`. Unset = unauthenticated
(logs a warning).

## Redeploy (code changed)

```bash
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o maze-server .
scp -i ~/path/to/<KEY>.pem maze-server ec2-user@<PUBLIC_IP>:/tmp/maze-server
ssh -i ~/path/to/<KEY>.pem ec2-user@<PUBLIC_IP> \
  'sudo mv /tmp/maze-server /usr/local/bin/maze-server && sudo chmod +x /usr/local/bin/maze-server && sudo systemctl restart maze'
```

## Teardown

```bash
P=<PROFILE>
aws-vault exec $P -- aws ec2 terminate-instances --instance-ids <INSTANCE_ID>
# optional: remove the SG rules once the instance is gone
```
