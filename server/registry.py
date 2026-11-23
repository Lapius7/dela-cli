#!/usr/bin/env python3
"""dela registry: issues per-project IDs (subdomain owners) and validates sish password auth.

  POST /v1/register  (public, via nginx)   -> 201 {"id","token"}
  POST /v1/release   (public)  {"id","token"} -> 204
  POST /v1/tcp       (public)  {"id","token","name"} -> 200 {"port"} / 401 / 429
                     TCP forwarding (dela tcp): a stable public port per (id, name), taken from
                     DELA_REGISTRY_TCP_MIN..MAX. At most DELA_REGISTRY_TCP_PER_ID names per id.
  POST /v1/auth      (sish only, 127.0.0.1) {"user","password","remote_addr"} -> 200 / 401

Only sha256(token) is stored. Registrations are capped per client IP per day, and IDs
unused for IDLE_DAYS are purged.
"""
import hashlib, hmac, json, os, re, secrets, sqlite3, threading, time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

DB = os.environ.get("DELA_REGISTRY_DB", "/var/lib/dela-registry/registry.db")
ADDR = os.environ.get("DELA_REGISTRY_ADDR", "127.0.0.1")
PORT = int(os.environ.get("DELA_REGISTRY_PORT", "18092"))
PER_IP_PER_DAY = int(os.environ.get("DELA_REGISTRY_PER_IP_PER_DAY", "20"))
IDLE_DAYS = int(os.environ.get("DELA_REGISTRY_IDLE_DAYS", "90"))
TCP_MIN = int(os.environ.get("DELA_REGISTRY_TCP_MIN", "20000"))
TCP_MAX = int(os.environ.get("DELA_REGISTRY_TCP_MAX", "29999"))
TCP_PER_ID = int(os.environ.get("DELA_REGISTRY_TCP_PER_ID", "5"))
NAME_RE = re.compile(r"^[a-z0-9]([a-z0-9-]{0,18}[a-z0-9])?$")  # same rule as dela --name
ALPHABET = "abcdefghijklmnopqrstuvwxyz0123456789"
ID_LEN = 10

lock = threading.Lock()
db = sqlite3.connect(DB, check_same_thread=False, isolation_level=None)
db.executescript(
    """
CREATE TABLE IF NOT EXISTS ids (id TEXT PRIMARY KEY, token_hash TEXT NOT NULL,
  created INTEGER NOT NULL, last_seen INTEGER NOT NULL, ip TEXT);
CREATE INDEX IF NOT EXISTS ids_ip ON ids(ip, created);
CREATE TABLE IF NOT EXISTS tcp_ports (id TEXT NOT NULL, name TEXT NOT NULL,
  port INTEGER NOT NULL UNIQUE, PRIMARY KEY (id, name));
"""
)


def h(token: str) -> str:
    return hashlib.sha256(token.encode()).hexdigest()


def new_id() -> str:
    return secrets.choice("abcdefghijklmnopqrstuvwxyz") + "".join(
        secrets.choice(ALPHABET) for _ in range(ID_LEN - 1)
    )


def check(id_: str, token: str) -> bool:
    row = db.execute("SELECT token_hash FROM ids WHERE id=?", (id_,)).fetchone()
    # compare against a dummy when unknown so timing doesn't reveal valid ids
    return hmac.compare_digest(row[0] if row else "0" * 64, h(token)) and row is not None


def purge():
    db.execute("DELETE FROM ids WHERE last_seen < ?", (int(time.time()) - IDLE_DAYS * 86400,))
    db.execute("DELETE FROM tcp_ports WHERE id NOT IN (SELECT id FROM ids)")


def tcp_port(id_: str, name: str):
    """Return (status, port). The same (id, name) always gets the same port.

    This is a convenience for a stable address, not access control: sish's password hook
    cannot tell which port a client asks to bind (see docs/server.md).
    """
    if not NAME_RE.match(name):
        return 400, None
    row = db.execute("SELECT port FROM tcp_ports WHERE id=? AND name=?", (id_, name)).fetchone()
    if row:
        return 200, row[0]
    n = db.execute("SELECT COUNT(*) FROM tcp_ports WHERE id=?", (id_,)).fetchone()[0]
    if n >= TCP_PER_ID:
        return 429, None
    for _ in range(50):
        port = secrets.choice(range(TCP_MIN, TCP_MAX + 1))
        try:
            db.execute("INSERT INTO tcp_ports VALUES (?,?,?)", (id_, name, port))
            return 200, port
        except sqlite3.IntegrityError:
            continue
    return 503, None


class H(BaseHTTPRequestHandler):
    server_version = "dela-registry"

    def log_message(self, *a):
        pass

    def reply(self, code, obj=None):
        body = json.dumps(obj).encode() if obj is not None else b""
        self.send_response(code)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.send_header("Cache-Control", "no-store")
        self.end_headers()
        if self.command != "HEAD":
            self.wfile.write(body)

    def body(self):
        try:
            n = int(self.headers.get("Content-Length", "0"))
            return json.loads(self.rfile.read(min(n, 4096)) or b"{}")
        except Exception:
            return {}

    def client_ip(self):
        return self.headers.get("X-Real-IP") or self.client_address[0]

    def do_POST(self):
        path = self.path.split("?")[0]
        with lock:
            if path == "/v1/register":
                ip = self.client_ip()
                since = int(time.time()) - 86400
                n = db.execute("SELECT COUNT(*) FROM ids WHERE ip=? AND created>?", (ip, since)).fetchone()[0]
                if n >= PER_IP_PER_DAY:
                    return self.reply(429, {"error": "too many registrations; try again tomorrow"})
                token = secrets.token_hex(24)
                now = int(time.time())
                for _ in range(10):
                    id_ = new_id()
                    try:
                        db.execute("INSERT INTO ids VALUES (?,?,?,?,?)", (id_, h(token), now, now, ip))
                        return self.reply(201, {"id": id_, "token": token})
                    except sqlite3.IntegrityError:
                        continue
                return self.reply(500, {"error": "could not allocate id"})
            if path == "/v1/release":
                b = self.body()
                if check(str(b.get("id", "")), str(b.get("token", ""))):
                    db.execute("DELETE FROM ids WHERE id=?", (b["id"],))
                    db.execute("DELETE FROM tcp_ports WHERE id=?", (b["id"],))
                    return self.reply(204)
                return self.reply(401, {"error": "invalid id or token"})
            if path == "/v1/tcp":
                b = self.body()
                id_, name = str(b.get("id", "")), str(b.get("name", ""))
                if not check(id_, str(b.get("token", ""))):
                    return self.reply(401, {"error": "invalid id or token"})
                code, port = tcp_port(id_, name)
                if code == 200:
                    db.execute("UPDATE ids SET last_seen=? WHERE id=?", (int(time.time()), id_))
                    return self.reply(200, {"port": port})
                return self.reply(code, {"error": "tcp port unavailable"})
            if path == "/v1/auth":
                if self.client_address[0] not in ("127.0.0.1", "::1"):
                    return self.reply(403)
                b = self.body()
                u, p = str(b.get("user", "")), str(b.get("password", ""))
                if check(u, p):
                    db.execute("UPDATE ids SET last_seen=? WHERE id=?", (int(time.time()), u))
                    return self.reply(200, {})
                return self.reply(401, {})
        self.reply(404, {"error": "not found"})

    def do_GET(self):
        self.reply(404, {"error": "not found"})


def janitor():
    while True:
        with lock:
            purge()
        time.sleep(3600)


if __name__ == "__main__":
    threading.Thread(target=janitor, daemon=True).start()
    ThreadingHTTPServer((ADDR, PORT), H).serve_forever()
