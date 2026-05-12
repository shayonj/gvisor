# Networking

[TOC]

gVisor implements its own network stack called [netstack][netstack]. All aspects
of the network stack are handled inside the Sentry — including TCP connection
state, control messages, and packet assembly — keeping it isolated from the host
network stack. Data link layer packets are written directly to the virtual
device inside the network namespace setup by Docker or Kubernetes.

Configuring the network stack may provide performance benefits, but isn't the
only step to optimizing gVisor performance. See the
[Production guide][Production guide] for more.

The IP address and routes configured for the device are transferred inside the
sandbox. The loopback device runs exclusively inside the sandbox and does not
use the host. You can inspect them by running:

```bash
docker run --rm --runtime=runsc alpine ip addr
```

## Network passthrough

For high-performance networking applications, you may choose to disable the user
space network stack and instead use the host network stack, including the
loopback. Note that this mode decreases the isolation to the host.

Add the following `runtimeArgs` to your Docker configuration
(`/etc/docker/daemon.json`) and restart the Docker daemon:

```json
{
    "runtimes": {
        "runsc": {
            "path": "/usr/local/bin/runsc",
            "runtimeArgs": [
                "--network=host"
            ]
       }
    }
}
```

### Checkpoint/restore with `--network=host`

Sandboxes using `--network=host` can be checkpointed and restored. Because
host file descriptors cannot cross a checkpoint boundary, any socket open at
checkpoint time is closed unconditionally before the state file is written,
and the application sees `EBADF` on the next operation against that socket
(`read`, `write`, `accept`, `sendto`, `recvfrom`, etc.). `epoll_wait` returns
immediately with `EPOLLERR | EPOLLHUP`, and includes `EPOLLRDHUP` if it was
requested, so blocked tasks unblock cleanly. Applications are expected to
detect the error and reconnect after restore. This applies even with
`--leave-running`, because the kernel cannot keep two processes referencing
the same host fd safely. Stack-level configuration (`/proc/net/{dev,snmp}`
handles and TCP buffer sizes derived from `/proc/sys/net/ipv4/*`) is read from
the post-restore host during restore, so workloads that query these via
`/proc` see values from the new host rather than the checkpointed one.

## Disabling external networking

To completely isolate the host and network from the sandbox, external networking
can be disabled. The sandbox will still contain a loopback provided by netstack.

Add the following `runtimeArgs` to your Docker configuration
(`/etc/docker/daemon.json`) and restart the Docker daemon:

```json
{
    "runtimes": {
        "runsc": {
            "path": "/usr/local/bin/runsc",
            "runtimeArgs": [
                "--network=none"
            ]
       }
    }
}
```

### Disable GSO {#gso}

If your Linux is older than 4.14.77, you can disable Generic Segmentation
Offload (GSO) to run with a kernel that is newer than 3.17. Add the
`--gso=false` flag to your Docker runtime configuration
(`/etc/docker/daemon.json`) and restart the Docker daemon:

> Note: Network performance, especially for large payloads, will be greatly
> reduced.

```json
{
    "runtimes": {
        "runsc": {
            "path": "/usr/local/bin/runsc",
            "runtimeArgs": [
                "--gso=false"
            ]
       }
    }
}
```

[netstack]: /docs/architecture_guide/networking/
[Production guide]: /docs/user_guide/production/
