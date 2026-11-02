# Podman Quadlet

Rootless systemd units under `deploy/quadlet/`: network, mongo, nats, s3, emailbridge. Secrets via
`podman secret`, bind mounts with `:Z`, `AutoUpdate=registry` optional.

See `deploy/quadlet/README.md` for install, `systemctl --user`, and `loginctl enable-linger`.
