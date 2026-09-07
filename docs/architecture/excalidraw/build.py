#!/usr/bin/env python3
"""Generates the hand-laid-out Excalidraw views in this directory.

Run `python3 build.py` after editing; the .excalidraw files are the artefacts
and open directly in excalidraw.com or the VS Code Excalidraw extension.
Layout is explicit (no auto-layout) so lines stay orthogonal and boxes aligned.
"""
import json
import os
import random

HERE = os.path.dirname(os.path.abspath(__file__))
random.seed(7)

FONT = 2  # Helvetica
INK = "#1e1e1e"
GO = ("#e8eefb", "#3b5bdb")     # goroutines / people
SYS = ("#fff3bf", "#e67700")    # the system / secrets
STORE = ("#e6fcf5", "#0ca678")  # data at rest
EXT = ("#f4f4f5", "#868e96")    # external systems
FRAME = ("transparent", "#adb5bd")


class Doc:
    def __init__(self):
        self.elements = []
        self.n = 0

    def _id(self, prefix):
        self.n += 1
        return f"{prefix}{self.n}"

    def _base(self, kind, x, y, w, h, stroke, fill):
        return {
            "id": self._id(kind[0]), "type": kind, "x": x, "y": y, "width": w, "height": h,
            "angle": 0, "strokeColor": stroke, "backgroundColor": fill, "fillStyle": "solid",
            "strokeWidth": 1, "strokeStyle": "solid", "roughness": 0, "opacity": 100,
            "groupIds": [], "frameId": None, "roundness": None, "seed": random.randint(1, 2**31),
            "version": 1, "versionNonce": random.randint(1, 2**31), "isDeleted": False,
            "boundElements": [], "updated": 1, "link": None, "locked": False,
        }

    def text(self, x, y, w, h, s, size=14, align="center", valign="middle", container=None, color=INK):
        t = self._base("text", x, y, w, h, color, "transparent")
        lines = s.split("\n")
        t.update({
            "text": s, "originalText": s, "fontSize": size, "fontFamily": FONT,
            "textAlign": align, "verticalAlign": valign, "containerId": container,
            "lineHeight": 1.25, "baseline": size, "autoResize": True,
        })
        self.elements.append(t)
        return t

    def box(self, x, y, w, h, label, palette=EXT, size=14, rounded=True, dashed=False):
        fill, stroke = palette
        r = self._base("rectangle", x, y, w, h, stroke, fill)
        if rounded:
            r["roundness"] = {"type": 3}
        if dashed:
            r["strokeStyle"] = "dashed"
        self.elements.append(r)
        if label:
            lines = label.split("\n")
            th = len(lines) * size * 1.25
            t = self.text(x + 8, y + (h - th) / 2, w - 16, th, label, size=size, container=r["id"])
            r["boundElements"].append({"id": t["id"], "type": "text"})
        return r

    def frame(self, x, y, w, h, title, size=15):
        r = self._base("rectangle", x, y, w, h, FRAME[1], FRAME[0])
        r["strokeStyle"] = "dashed"
        r["roundness"] = {"type": 3}
        self.elements.append(r)
        self.text(x + 12, y + 8, w - 24, size * 1.25, title, size=size, align="left", color="#495057")
        return r

    def arrow(self, pts, a=None, b=None, label=None, dashed=False, size=12, label_at=None, head=True):
        """pts: absolute polyline points. a/b: boxes to bind to. label_at: index of the
        segment to centre the label on (default: the longest). Labels sit just above a
        horizontal segment or just right of a vertical one, never on the line."""
        x0, y0 = pts[0]
        xs = [p[0] for p in pts]
        ys = [p[1] for p in pts]
        ar = self._base("arrow", x0, y0, max(xs) - min(xs), max(ys) - min(ys), INK, "transparent")
        ar.update({
            "points": [[px - x0, py - y0] for px, py in pts],
            "lastCommittedPoint": None, "startArrowhead": None, "endArrowhead": "arrow" if head else None,
            "elbowed": False,
        })
        if dashed:
            ar["strokeStyle"] = "dashed"
        for key, el in (("startBinding", a), ("endBinding", b)):
            if el is not None:
                ar[key] = {"elementId": el["id"], "focus": 0, "gap": 2}
                el["boundElements"].append({"id": ar["id"], "type": "arrow"})
            else:
                ar[key] = None
        self.elements.append(ar)
        if label:
            segs = [(i, abs(pts[i + 1][0] - pts[i][0]) + abs(pts[i + 1][1] - pts[i][1])) for i in range(len(pts) - 1)]
            i = label_at if label_at is not None else max(segs, key=lambda s: s[1])[0]
            mx = (pts[i][0] + pts[i + 1][0]) / 2
            my = (pts[i][1] + pts[i + 1][1]) / 2
            lines = label.split("\n")
            tw = max(len(l) for l in lines) * size * 0.55 + 8
            th = len(lines) * size * 1.25
            horizontal = pts[i][1] == pts[i + 1][1]
            tx = mx - tw / 2 if horizontal else mx + 6
            ty = my - th - 3 if horizontal else my - th / 2
            self.text(tx, ty, tw, th, label, size=size)
        return ar

    def save(self, name):
        doc = {
            "type": "excalidraw", "version": 2, "source": "docs/architecture/excalidraw/build.py",
            "elements": self.elements,
            "appState": {"viewBackgroundColor": "#ffffff", "gridSize": 20},
            "files": {},
        }
        path = os.path.join(HERE, name)
        with open(path, "w") as f:
            json.dump(doc, f, indent=1)
        print("wrote", path)


# Anchor helpers: point on a box edge. f is the fraction along that edge.
def L(b, f=0.5): return (b["x"], b["y"] + b["height"] * f)
def R(b, f=0.5): return (b["x"] + b["width"], b["y"] + b["height"] * f)
def T(b, f=0.5): return (b["x"] + b["width"] * f, b["y"])
def B(b, f=0.5): return (b["x"] + b["width"] * f, b["y"] + b["height"])


def h_elbow(p, q, xm=None):
    """Horizontal-first elbow from p to q: p → (xm, p.y) → (xm, q.y) → q."""
    if p[1] == q[1]:
        return [p, q]
    xm = (p[0] + q[0]) / 2 if xm is None else xm
    return [p, (xm, p[1]), (xm, q[1]), q]


def v_elbow(p, q, ym=None):
    """Vertical-first elbow from p to q: p → (p.x, ym) → (q.x, ym) → q."""
    if p[0] == q[0]:
        return [p, q]
    ym = (p[1] + q[1]) / 2 if ym is None else ym
    return [p, (p[0], ym), (q[0], ym), q]


# ---------------------------------------------------------------------------
# 1. Context view
# ---------------------------------------------------------------------------
def context():
    d = Doc()
    d.text(60, 20, 900, 24, "HourStats · system context", size=20, align="left")
    d.text(60, 48, 900, 18, "One Go process on Fly.io reads the Bluesky firehose, scores sentiment, and posts summaries back to Bluesky.",
           size=13, align="left", color="#495057")

    js = d.box(60, 200, 300, 90, "Bluesky Jetstream firehose\njetstream2.us-west.bsky.network\nWebSocket · app.bsky.feed.post commits", EXT)
    op = d.box(40, 460, 200, 70, "Operator\nfly CLI · fly ssh · fly proxy 9111", GO)
    fly = d.box(400, 450, 240, 90, "Fly.io platform\nmachine · volume · secrets\nlogs · metrics", EXT)
    hs = d.box(640, 300, 300, 130, "HourStats bot\nsingle Go binary on Fly.io\nhourstats-prod · hourstats-staging", SYS, size=15)

    gframe = d.frame(1400, 90, 380, 250, "Google")
    gem = d.box(1420, 125, 340, 90, "Gemini API\ngenerativelanguage.googleapis.com\ngemini-2.5-pro, fallback gemini-2.5-flash", EXT)
    news = d.box(1420, 235, 340, 80, "Google News RSS\nnews.google.com/rss · US, GB, AU", EXT)
    s3 = d.box(1420, 370, 340, 70, "AWS S3\nhourstats-sqlite-backups · us-west-2", STORE)
    wiki = d.box(1420, 470, 340, 60, "Wikipedia\nPortal:Current_events/YYYY_Month_D", EXT)
    bframe = d.frame(1400, 560, 380, 250, "Bluesky network")
    av = d.box(1420, 595, 340, 90, "Public AppView\npublic.api.bsky.app\napp.bsky.feed.getPosts · no auth", EXT)
    pds = d.box(1420, 705, 340, 90, "PDS bsky.social\ncreateSession · createRecord · uploadBlob\nputRecord (pin) · getPosts (viewer state)", EXT)
    readers = d.box(1860, 715, 220, 70, "Bluesky readers\nfollow @hourstats.bsky.social", GO)

    d.arrow(h_elbow(R(js), L(hs, 0.3), xm=520), js, hs, "firehose events\n~all public posts")
    d.arrow([R(op), L(fly)], op, fly, "deploy · logs · ssh · stats API")
    d.arrow(h_elbow(R(fly), L(hs, 0.7), xm=640 - 20), fly, hs, "hosts")

    # right-hand fan-out from hs, one anchor per target, elbows at staggered x
    targets = [
        (gem, 0.10, 1010, "group TF-IDF terms · alt text · validate exemplars\n150 calls per rolling 24 h"),
        (news, 0.25, 1000, "headline context for grouping · 3 s, best effort"),
        (s3, 0.40, 990, "daily backup of essential tables"),
        (wiki, 0.55, 980, "link targets only · no HTTP calls"),
        (av, 0.70, 990, "hydrate engagement · 25 URIs per call, 10 workers"),
        (pds, 0.85, 1000, "summary, sparkline, trending reply, daily quote,\nyearly chart, weekly and monthly reports"),
    ]
    for tgt, f, xm, label in targets:
        pts = h_elbow(R(hs, f), L(tgt), xm=xm)
        d.arrow(pts, hs, tgt, label, dashed=(tgt is wiki), label_at=2)
    d.arrow([R(pds), L(readers)], pds, readers, "posts appear in\nfeeds and threads")
    d.save("context.excalidraw")


# ---------------------------------------------------------------------------
# 2. Static component view
# ---------------------------------------------------------------------------
def components():
    d = Doc()
    d.text(60, 20, 1200, 24, "HourStats · static components", size=20, align="left")
    d.text(60, 48, 1200, 18, "Goroutines inside the one process, the packages each one drives, the shared SQLite store, and the services each part calls.",
           size=13, align="left", color="#495057")

    js = d.box(60, 130, 220, 70, "Jetstream firehose\nWebSocket", EXT)
    proc = d.frame(340, 90, 1320, 400, "hourstats process · cmd/hourstats · one Fly machine")

    consumer = d.box(380, 130, 260, 100, "Jetstream consumer\ninternal/jetstream · stats\nEnglish filter · tokenize\ncursor saved every 10 s", GO)
    sched = d.box(800, 120, 520, 120, "Scheduler loop\nwall-clock tickers + SIGTERM\n5 min stall check · 5 min WAL checkpoint · 30 min stats snapshot\nan overlapping tick is skipped, not queued", GO)
    flusher = d.box(380, 330, 260, 100, "Write flusher\ninternal/store\nbatch 1500 rows or 2 s", GO)
    cycle = d.box(760, 320, 300, 130, "Analysis cycle · cycleGuard\nhydrator · analyzer · topics\nsparkline · client · formatter\nprod: hourly at :55", GO)
    job = d.box(1090, 320, 260, 130, "Background job · jobs guard\nbackup · sparkline · client\ndaily 00:00 · yearly 01:00\nreports", GO)
    api = d.box(1380, 320, 260, 130, "Stats HTTP API\ninternal/statsapi · sparkline\n:9111 GET /stats/*\nprivate network only", GO)
    op = d.box(1700, 340, 200, 90, "Operator\nfly proxy 9111\nhourstats-stats CLI", EXT)

    store = d.box(380, 560, 260, 130, "internal/store\nSQLite WAL · /data/hourstats-{profile}.db\nwriteDB 1 conn · readDB 3 conns\nmaintDB 1 conn for checkpoints", STORE)
    av = d.box(730, 560, 160, 80, "Public AppView\ngetPosts", EXT)
    gem = d.box(920, 560, 160, 80, "Gemini +\nGoogle News", EXT)
    pds = d.box(1110, 560, 160, 80, "PDS bsky.social\nposts · blobs · pin", EXT)
    s3 = d.box(1300, 560, 120, 80, "AWS S3\nbackups", STORE)

    d.arrow([R(js), L(consumer)], js, consumer)
    d.arrow([B(consumer), T(flusher)], consumer, flusher, "writeCh · 50k buffer · ≤2 s send")
    d.arrow([(sched["x"], 160), (consumer["x"] + consumer["width"], 160)], sched, consumer, "stall → ForceReconnect")
    d.arrow([B(sched, 0.25), T(cycle)], sched, cycle, "analysisCh")
    d.arrow([B(sched, 0.83), T(job)], sched, job, "backupCh · yearlyPostCh")
    d.arrow([R(sched, 0.5), (1510, R(sched, 0.5)[1]), T(api, 0.5)], sched, api)
    d.arrow([L(op), R(api)], op, api)

    d.arrow([B(flusher), T(store)], flusher, store, "FlushPostBatch · FlushTokenBatch")
    # store bus: cycle, job and api all join one line into the store
    bus_y = 515
    d.arrow([B(cycle, 0.08), (cycle["x"] + cycle["width"] * 0.08, bus_y), (560, bus_y), T(store, 0.7)], cycle, store, "reads + writes", label_at=0)
    d.arrow([B(job, 0.08), (job["x"] + job["width"] * 0.08, bus_y), (560, bus_y)], job, None, head=False)
    d.arrow([B(api, 0.08), (api["x"] + api["width"] * 0.08, bus_y), (560, bus_y)], api, None, head=False)

    def under(src, dst, f_dst=0.5):
        x = dst["x"] + dst["width"] * f_dst
        return (x - src["x"]) / src["width"]
    d.arrow([B(cycle, under(cycle, av)), T(av)], cycle, av, "hydrate")
    d.arrow([B(cycle, under(cycle, gem)), T(gem)], cycle, gem, "group · validate")
    d.arrow(v_elbow(B(cycle, 0.96), T(pds, 0.25), ym=540), cycle, pds, "post", label_at=1)
    d.arrow([B(job, under(job, pds, 0.6)), T(pds, 0.6)], job, pds)
    d.arrow([B(job, under(job, s3, 0.4)), T(s3, 0.4)], job, s3, "00:00 UTC")
    d.save("components.excalidraw")


# ---------------------------------------------------------------------------
# 3. Deployment view
# ---------------------------------------------------------------------------
def deployment():
    d = Doc()
    d.text(60, 20, 1200, 24, "HourStats · deployment", size=20, align="left")
    d.text(60, 48, 1200, 18, "One Fly.io machine per app, one persistent volume, secrets injected at boot, everything else reached over the public internet.",
           size=13, align="left", color="#495057")

    devf = d.frame(60, 110, 300, 250, "Developer machine")
    dev = d.box(80, 150, 260, 70, "make deploy-prod\nfly deploy -c fly.prod.toml --ha=false", EXT)
    ops = d.box(80, 240, 260, 100, "fly secrets set · fly ssh console\nfly proxy 9111 · fly logs (stdout JSON)\nFly metrics: Prometheus, FlyV1 token", EXT)

    bf = d.frame(420, 110, 300, 250, "Image build · Dockerfile on Fly builders")
    b1 = d.box(440, 150, 260, 70, "golang:1.24-alpine\ngo mod download\ngo build ./cmd/hourstats", GO)
    b2 = d.box(440, 260, 260, 70, "alpine:3.21\nca-certificates · tzdata · sqlite\n/usr/local/bin/hourstats", GO)

    flyf = d.frame(780, 110, 680, 640, "Fly.io · region sjc")
    prodf = d.frame(800, 150, 640, 330, "App hourstats-prod · one machine, --ha=false")
    vm = d.box(820, 190, 290, 110, "shared-cpu-1x · 1 GB RAM · 512 MB swap\nGOMEMLIMIT 800MiB · GOGC 75 · TZ UTC\nkill_signal SIGTERM · kill_timeout 15 s", GO)
    binp = d.box(1140, 190, 270, 110, "hourstats process\nprofile prod · cycle hourly at :55\nTRENDING on · REPORTS on\nstats API :9111", GO)
    secrets = d.box(820, 340, 260, 120, "Fly secrets (env at boot)\nBLUESKY_HANDLE\nBLUESKY_PASSWORD\nGOOGLE_AI_API_KEY\nAWS_ACCESS_KEY_ID\nAWS_SECRET_ACCESS_KEY", SYS, size=12)
    vol = d.box(1140, 360, 270, 90, "Volume data → /data\nhourstats-prod.db, -wal, -shm\nbackups/ kept 7 days", STORE)
    stagef = d.frame(800, 510, 640, 130, "App hourstats-staging · stopped when idle")
    svm = d.box(820, 545, 600, 70, "Same image and shape\nprofile staging · cycle at :25 · own volume · DRY_RUN per test", GO)
    d.text(800, 665, 640, 34, "Logs: the process writes JSON lines to stdout and Fly collects them.\nMetrics: Fly platform Prometheus endpoint.",
           size=12, align="left", color="#495057")

    extf = d.frame(1620, 110, 340, 640, "External services")
    jsb = d.box(1640, 150, 300, 80, "Jetstream\njetstream2.us-west.bsky.network\n3 other public hosts as fallbacks", EXT)
    av = d.box(1640, 260, 300, 60, "public.api.bsky.app\nengagement hydration", EXT)
    pds = d.box(1640, 350, 300, 60, "bsky.social\nsession · posts · blobs · pin", EXT)
    gem = d.box(1640, 440, 300, 80, "generativelanguage.googleapis.com\nnews.google.com", EXT)
    s3 = d.box(1640, 550, 300, 80, "S3 hourstats-sqlite-backups\nus-west-2 · prod/{ts}.db", STORE)

    d.arrow([R(dev), L(b1)], dev, b1, "fly deploy")
    d.arrow([B(b1), T(b2)], b1, b2)
    d.arrow([R(b2), (760, R(b2)[1]), (760, L(vm)[1]), L(vm)], b2, vm, "image", label_at=1)
    d.arrow([R(vm), L(binp)], vm, binp)
    d.arrow([B(binp), T(vol)], binp, vol, "SQLite")
    d.arrow([T(secrets, 0.5), B(vm, 0.45)], secrets, vm, "env at boot", dashed=True)
    d.arrow([R(ops), (390, R(ops)[1]), (390, 90), (T(binp)[0], 90), T(binp)], ops, binp, "private network only · fly proxy", dashed=True, label_at=2)

    fan = [(jsb, 0.15, "wss"), (av, 0.35, "https"), (pds, 0.55, "https"), (gem, 0.75, "https"), (s3, 0.95, "https · 00:00 UTC")]
    for tgt, f, label in fan:
        p = R(binp, f)
        xm = 1440 + f * 40
        pts = [p, (xm, p[1]), (xm, L(tgt)[1]), L(tgt)]
        d.arrow(pts, binp, tgt, label, label_at=2)
    d.save("deployment.excalidraw")


if __name__ == "__main__":
    context()
    components()
    deployment()
