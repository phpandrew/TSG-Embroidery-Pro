# Regenerates the fixtures and expected.json with pyembroidery (pip install pyembroidery).
# Usage: python3 internal/formats/testdata/gen_fixtures.py  (not run by go test)
import json, os, sys
import pyembroidery as pe
from pyembroidery import STITCH, JUMP, TRIM, COLOR_CHANGE, STOP, END, COMMAND_MASK

repo = os.path.abspath(os.path.join(os.path.dirname(os.path.abspath(__file__)), "../../.."))
out = os.path.join(repo, "internal/formats/testdata")
samples = {"ANDREW": "ANDREW.dst", "alphabet": "alphabet.dst", "crest": "Team crest.dst"}

def synth():
    p = pe.EmbPattern()
    # 3 squares, each its own colour, with trims + jumps between; y-down internal
    for i, (ox, oy) in enumerate([(0, 0), (300, 0), (0, 300)]):
        if i > 0:
            p.add_command(TRIM)
            p.add_stitch_absolute(JUMP, ox, oy)
        side = 200
        pts = [(ox, oy), (ox + side, oy), (ox + side, oy + side), (ox, oy + side), (ox, oy)]
        for a, b in zip(pts, pts[1:]):
            for k in range(0, 5):
                x = a[0] + (b[0] - a[0]) * k / 5
                y = a[1] + (b[1] - a[1]) * k / 5
                p.add_stitch_absolute(STITCH, x, y)
        p.add_stitch_absolute(STITCH, ox, oy)
        if i < 2:
            p.add_thread(pe.EmbThread(["#ff0000", "#00ff00", "#0000ff"][i]))
            p.add_command(COLOR_CHANGE)
    p.add_thread(pe.EmbThread("#0000ff"))
    p.add_command(END)
    return p

def summarise(path):
    p = pe.read(path)
    st = cc = tr = 0
    xs, ys = [], []
    first = None
    for x, y, c in p.stitches:
        c &= COMMAND_MASK
        if c == STITCH:
            st += 1; xs.append(x); ys.append(y)
            if first is None: first = [x, y]
        elif c in (COLOR_CHANGE, STOP):
            cc += 1
        elif c == TRIM:
            tr += 1
    return {"stitches": st, "colorChanges": cc, "trims": tr,
            "minX": min(xs), "minY": min(ys), "maxX": max(xs), "maxY": max(ys), "first": first}

designs = {k: pe.read(os.path.join(repo, "samples", v)) for k, v in samples.items()}
designs["square"] = synth()
pe.write_dst(designs["square"], os.path.join(out, "square.dst"))
expected = {}
for name, pat in designs.items():
    for ext, kw in [("pes", {}), ("pec", {}), ("jef", {}), ("exp", {})]:
        fn = f"{name}.{ext}"
        path = os.path.join(out, fn)
        pe.write(pat.copy() if hasattr(pat, "copy") else pat, path, kw)
        if os.path.getsize(path) > 200_000:
            os.remove(path); print("skip", fn); continue
        expected[fn] = summarise(path)
    if name == "ANDREW":
        fn = "ANDREW_v6.pes"
        path = os.path.join(out, fn)
        pe.write(pat.copy(), path, {"pes version": 6})
        expected[fn] = summarise(path)
with open(os.path.join(out, "expected.json"), "w") as f:
    json.dump(expected, f, indent=1, sort_keys=True)
for k, v in expected.items(): print(k, v)
