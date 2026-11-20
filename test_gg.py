import json, os, subprocess, tempfile, unittest

GG = os.path.join(os.path.dirname(os.path.abspath(__file__)), "gg")
T = "2026-11-19T15:00:00"


class Base(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.home = os.path.join(self.tmp.name, "home")
        self.repo = os.path.realpath(os.path.join(self.tmp.name, "repo"))
        os.mkdir(self.repo)
        subprocess.run(["git", "init", "-q"], cwd=self.repo, check=True)
        subprocess.run(["git", "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "--allow-empty", "-m", "i"], cwd=self.repo, check=True)

    def gg(self, *args, now=T, cwd=None, stdin=None):
        env = dict(os.environ, GG_HOME=self.home, GG_NOW=now)
        return subprocess.run([GG, *args], cwd=cwd or self.repo, env=env, input=stdin, capture_output=True, text=True)

    def hook(self, event, now, **kw):
        return self.gg("hook", now=now, stdin=json.dumps(dict(hook_event_name=event, cwd=self.repo, **kw)))

    def seed(self):
        for i, t, b in [("900", "English n-gram", "4h"), ("890", "Quanpin 8M", "4h"), ("896", "Wanxiang lexicon", "1h")]:
            self.assertEqual(self.gg("add", i, t, "--why", "w " + i, "--budget", b).returncode, 0)
        self.gg("rank", "900", "890", "896")
        self.assertEqual(self.gg("start", "896").returncode, 0)


class Tests(Base):
    LATE = "2026-11-20T00:46:00"

    def test_replay(self):
        self.seed()
        out = self.hook("UserPromptSubmit", self.LATE).stdout
        for s in ("OVER BUDGET", "9h46m", "WARNING: #900"):
            self.assertIn(s, out)
        self.assertTrue(out.rstrip().splitlines()[-1].startswith("Rules:"))

    def test_stop_blocks(self):
        self.seed()
        o = json.loads(self.hook("Stop", self.LATE).stdout)
        self.assertEqual(o["decision"], "block")
        self.assertIn("896", o["reason"])
        self.assertIn("900", o["reason"])
        self.assertEqual(self.hook("Stop", self.LATE, stop_hook_active=True).stdout, "")

    def test_start_refused(self):
        self.seed()
        path = os.path.join(self.home, os.listdir(self.home)[0])
        before = open(path).read()
        r = self.gg("start", "900")
        self.assertNotEqual(r.returncode, 0)
        self.assertEqual(open(path).read(), before)

    def test_park(self):
        self.seed()
        self.assertEqual(self.gg("park", "896", "stuck", now=self.LATE).returncode, 0)
        self.assertIn("Nothing active. Next by rank: #900.", self.gg("status", now=self.LATE).stdout)
        self.assertEqual(self.hook("Stop", self.LATE).stdout, "")

    def test_no_ledger(self):
        for ev in ("UserPromptSubmit", "SessionStart", "Stop"):
            r = self.hook(ev, T)
            self.assertEqual((r.returncode, r.stdout), (0, ""))
        self.assertEqual(self.gg("status").stdout, "")

    def test_accumulates(self):
        self.gg("add", "1", "x", "--why", "w", "--budget", "3h")
        self.gg("start", "1", now="2026-11-19T10:00:00")
        self.gg("park", "1", "n", now="2026-11-19T10:30:00")
        self.gg("start", "1", now="2026-11-19T12:00:00")
        self.assertIn("50m spent of 3h", self.gg("status", now="2026-11-19T12:20:00").stdout)

    def test_worktree(self):
        self.seed()
        wt = os.path.join(self.tmp.name, "wt")
        subprocess.run(["git", "worktree", "add", "-q", wt, "-b", "b"], cwd=self.repo, check=True)
        self.assertEqual(self.gg("status", cwd=wt).stdout, self.gg("status").stdout)
        self.assertIn("#896", self.gg("status", cwd=wt).stdout)

    def test_under_budget(self):
        self.seed()
        now = "2026-11-19T15:30:00"
        self.assertEqual(self.hook("Stop", now).stdout, "")
        self.assertNotIn("OVER BUDGET", self.gg("status", now=now).stdout)


if __name__ == "__main__":
    unittest.main()
