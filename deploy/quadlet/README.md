# EmailBridge Quadlet units

Rootless Podman units for the small tier. Compose stacks in `../compose/` are the
cross-engine path; these units are the Podman-native install.

## Prerequisites

- Podman 4.4+ with Quadlet support
- `loginctl enable-linger $(whoami)` so user units survive logout
- Data directory: `mkdir -p ~/.local/share/emailbridge/{nats,mongo,s3,data}`

## Secrets

Generate the secret files with the image itself, then create Podman secrets from them:

```bash
mkdir -p secrets
podman run --rm --userns=keep-id -v "$PWD/secrets:/secrets:Z" ghcr.io/radiobros/emailbridge:v1.0.0 secrets init --dir /secrets
podman secret create mongo_keyfile      ./secrets/mongo_keyfile
podman secret create mongo_root_password ./secrets/mongo_root_password
podman secret create mongo_uri          ./secrets/mongo_uri
podman secret create s3_access_key      ./secrets/s3_access_key
podman secret create s3_secret_key      ./secrets/s3_secret_key
podman secret create keys               ./secrets/keys
podman secret create auth_secret        ./secrets/auth_secret
```

## Install

```bash
mkdir -p ~/.config/containers/systemd
cp deploy/quadlet/*.network deploy/quadlet/*.container ~/.config/containers/systemd/
systemctl --user daemon-reload
systemctl --user start emailbridge-nats emailbridge-mongo emailbridge-s3 emailbridge
systemctl --user enable emailbridge-nats emailbridge-mongo emailbridge-s3 emailbridge
```

Status and logs:

```bash
systemctl --user status emailbridge
journalctl --user -u emailbridge -f
```

## Auto-update

Units set `AutoUpdate=registry`. Enable the timer:

```bash
systemctl --user enable --now podman-auto-update.timer
```
