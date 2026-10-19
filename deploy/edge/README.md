# GoFi on edge hosts

Files for running the single static `gofi` binary on a constrained Linux host
(ARM SBC, vehicle gateway, sensor hub) without Docker or Kubernetes. The
operator guide is [docs/deployment.md, "Edge and embedded"](../../docs/deployment.md#edge-and-embedded);
every environment variable is in [docs/configuration.md](../../docs/configuration.md).

| File | Purpose |
|---|---|
| `gofi.service` | systemd unit: `Restart=always`, `MemoryMax=192M`, `ProtectSystem=strict`, seccomp/capability hardening, state under `/var/lib/gofi` |
| `gofi.env.example` | environment for the unit: `GOFI_PROFILE=edge`, in-process driver, file-backed state, sink spool, small history |
| `gofi.openrc` | OpenRC service script for Alpine/busybox hosts |

## systemd install

```sh
sudo useradd --system --home /var/lib/gofi --shell /usr/sbin/nologin gofi
sudo install -m 0755 gofi-edge-linux-arm64 /usr/local/bin/gofi   # from `task build:cross`
sudo install -d -m 0750 -o gofi -g gofi /var/lib/gofi
sudo install -d -m 0750 /etc/gofi
sudo install -m 0640 deploy/edge/gofi.env.example /etc/gofi/gofi.env
sudo install -m 0644 deploy/edge/gofi.service /etc/systemd/system/gofi.service
sudo systemctl daemon-reload
sudo systemctl enable --now gofi
journalctl -u gofi -f
```

`MemoryMax` in the unit is what GoFi reads (cgroup `memory.max`) to set its
Go memory limit, so size the two together: `MemoryMax` is the hard kill
line, `GOFI_GOMEMLIMIT_FRACTION` (0.7 in the edge profile) is where the GC
starts working hard. Add ~40 MB per Python sandbox worker if flows use
Python stages.

## OpenRC / busybox hosts (Alpine, OpenWrt-like)

There is no cgroup delegation by default, so set the memory budget
explicitly with `GOFI_MEMORY_LIMIT` and rely on `GOFI_PROFILE=edge` for the
rest. `gofi.openrc` uses `supervise-daemon` for restart-on-crash and
`rc_ulimit` for file descriptors; `start-stop-daemon` works too but does not
restart. Logs go through `logger` to busybox syslog; set
`GOFI_LOG_FORMAT=text` and rotate `/var/log/messages` with busybox
`logrotate` or syslogd's `-s/-b` size limits.

```sh
apk add --no-cache ca-certificates
install -m 0755 gofi-edge-linux-arm64 /usr/local/bin/gofi
install -m 0755 deploy/edge/gofi.openrc /etc/init.d/gofi
install -m 0640 deploy/edge/gofi.env.example /etc/gofi/gofi.env
rc-update add gofi default && rc-service gofi start
```

Note for a read-only root (squashfs/overlay): put `/var/lib/gofi` on the
writable partition; GoFi needs nothing else writable. The binary is static
and needs only `/etc/ssl/certs/ca-certificates.crt` for HTTPS sinks.
