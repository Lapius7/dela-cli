import os
import sys
import unittest

os.environ["DELA_REGISTRY_DB"] = ":memory:"
os.environ["DELA_REGISTRY_TCP_MIN"] = "20000"
os.environ["DELA_REGISTRY_TCP_MAX"] = "20009"
os.environ["DELA_REGISTRY_TCP_PER_ID"] = "3"
sys.path.insert(0, os.path.dirname(__file__))
import registry  # noqa: E402


def add_id(id_):
    registry.db.execute("INSERT INTO ids VALUES (?,?,?,?,?)", (id_, registry.h("t"), 0, 2**31, "x"))


class TcpPortTest(unittest.TestCase):
    def setUp(self):
        registry.db.execute("DELETE FROM tcp_ports")
        registry.db.execute("DELETE FROM ids")
        add_id("aaaaaaaaaa")
        add_id("bbbbbbbbbb")

    def test_same_name_gets_same_port_in_range(self):
        code, port = registry.tcp_port("aaaaaaaaaa", "ssh")
        self.assertEqual(code, 200)
        self.assertTrue(20000 <= port <= 20009)
        self.assertEqual(registry.tcp_port("aaaaaaaaaa", "ssh"), (200, port))

    def test_ports_are_unique_across_ids(self):
        ports = {registry.tcp_port("aaaaaaaaaa", n)[1] for n in ("a", "b", "c")}
        ports |= {registry.tcp_port("bbbbbbbbbb", n)[1] for n in ("a", "b", "c")}
        self.assertEqual(len(ports), 6)

    def test_per_id_cap(self):
        for n in ("a", "b", "c"):
            self.assertEqual(registry.tcp_port("aaaaaaaaaa", n)[0], 200)
        self.assertEqual(registry.tcp_port("aaaaaaaaaa", "d")[0], 429)
        # already allocated names keep working at the cap
        self.assertEqual(registry.tcp_port("aaaaaaaaaa", "a")[0], 200)

    def test_rejects_bad_name(self):
        self.assertEqual(registry.tcp_port("aaaaaaaaaa", "../x")[0], 400)
        self.assertEqual(registry.tcp_port("aaaaaaaaaa", "")[0], 400)

    def test_pool_exhausted(self):
        registry.TCP_PER_ID = 100
        try:
            for i in range(10):
                registry.db.execute("INSERT INTO tcp_ports VALUES (?,?,?)", ("bbbbbbbbbb", "n%d" % i, 20000 + i))
            self.assertEqual(registry.tcp_port("aaaaaaaaaa", "x")[0], 503)
        finally:
            registry.TCP_PER_ID = 3

    def test_purge_drops_ports_of_deleted_ids(self):
        registry.tcp_port("aaaaaaaaaa", "ssh")
        registry.db.execute("DELETE FROM ids WHERE id=?", ("aaaaaaaaaa",))
        registry.purge()
        n = registry.db.execute("SELECT COUNT(*) FROM tcp_ports").fetchone()[0]
        self.assertEqual(n, 0)


if __name__ == "__main__":
    unittest.main()
