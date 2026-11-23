# Server setup for TCP forwarding and load balancing

This covers the sish and registry settings that `dela tcp` and `dela --replica` need. It is written from sish's documentation (<https://docs.ssi.sh/cli>) and was **not tried against a running sish**. Check flag names against your sish version before rolling out.

## TCP forwarding (`dela tcp`)

dela asks the registry (`POST /_api/v1/tcp`) for a stable public port, then runs `ssh -R <port>:<target>`. sish must therefore honor the requested port:

| sish flag | Value | Why |
|---|---|---|
| `--bind-random-ports` | `false` | The default (`true`) ignores the requested port and assigns a random one. dela prints a warning if the port it gets differs from the registry's. |
| `--port-bind-range` | `20000-29999` | Must cover the registry range (`DELA_REGISTRY_TCP_MIN` / `DELA_REGISTRY_TCP_MAX`, default 20000-29999). The sish default is `0,1024-65535`. |

Also open that port range in the server's firewall.

Registry (`server/registry.py`) settings:

| Variable | Default | Meaning |
|---|---|---|
| `DELA_REGISTRY_TCP_MIN` / `DELA_REGISTRY_TCP_MAX` | `20000` / `29999` | Pool of public TCP ports |
| `DELA_REGISTRY_TCP_PER_ID` | `5` | Max TCP names per project ID |

The new public route must be proxied next to the existing `register` / `release` routes, for example in nginx:

```nginx
location = /_api/v1/tcp { proxy_pass http://127.0.0.1:18092/v1/tcp; proxy_set_header X-Real-IP $remote_addr; }
```

The new table is created with `CREATE TABLE IF NOT EXISTS`, so restarting the registry migrates an existing database. Ports are released with the project ID (`/v1/release` or the idle purge).

### Limit: the port allocation is not access control

sish's password hook only receives the user, password and remote address. It cannot tell which port a client asks to bind, so the registry cannot stop an authenticated user from requesting another project's port. A port that is already bound is refused (without `--tcp-load-balancer`), but a port that is free at that moment can be taken by anyone with an ID, and IDs are free to obtain. Treat the public TCP ports as open to everyone and protect the forwarded service itself.

## Load balancing (`--replica`)

Load balancing is a **server-wide** sish setting. dela's `--replica` only lifts its own local check against starting two tunnels with the same project ID and name.

| sish flag | Effect |
|---|---|
| `--http-load-balancer=true` | Several clients may bind the same HTTP subdomain; requests are spread across them. With `--append-user-to-subdomain` the host name contains the project ID, so only holders of that ID's token can join. |
| `--tcp-load-balancer=true` | Several clients may bind the same TCP port. |

Because the setting is global, enabling `--http-load-balancer` also lets two dela processes without `--replica` balance if the local check is bypassed (for example on two PCs sharing one `.delaconfig`); the flag is a guard against mistakes, not an enforcement.

**Recommendation:** enable `--http-load-balancer` on the public server. Leave `--tcp-load-balancer` **off** unless you accept the risk above: with it on, anyone with an ID could bind an allocated port too and receive a share of that project's connections. Without it, `dela tcp --replica` cannot connect a second process and is refused by sish.

## Not verified

- sish flag behavior (`--bind-random-ports=false`, `--port-bind-range`, the load balancers) on a running server.
- The text sish prints for a TCP forward. dela treats a line containing `try.lapius7.si:<port>` as "established", and otherwise assumes it is established when `ssh` is still alive 16 seconds after start (`ExitOnForwardFailure=yes` makes a failed forward exit sooner).
- The nginx route above on the production server.
