# DST Embroidery Editor — Project Plan

A Go-based replacement for the dated software currently used to view, merge, resize, and letter Tajima DST files for the Tajima TFHX 1506.

---

## 1. Goals

**Replace current tool for daily production:**
- View designs and play back stitch sequence
- Merge two or more DST files
- Resize designs
- Simple text in fonts (names, numbers, short text)

**Stretch goals:**
- Name drop from CSV (one DST per name, for rostered.ca jerseys)
- Batch processing tied into the T-Dongle auto-feed
- Stitch processor for high-quality resizing (50–250%)

---

## 2. DST Format Notes

- **Header:** 512 bytes ASCII — label (`LA`), stitch count (`ST`), color count (`CO`), extents (`+X`, `-X`, `+Y`, `-Y`), etc.
- **Records:** 3 bytes each, encoding dx/dy plus flags for normal stitch, jump, color change (stop), and end.
- **End of file:** `0x00 0x00 0xF3`.
- **Max displacement per record:** ±121 units (0.1 mm) = 12.1 mm. Longer moves must be split.
- **No color data.** DST only says "change needle here." Thread colors live on the machine or in a sidecar file.
- **No trim command.** Tajima machines infer a trim from a run of consecutive jumps (usually 2–3, machine setting). Must match TFHX configuration.
- **Start point:** Tajima convention is to start at design center. Normalize output so the first move is a jump from center to the first stitch.
- **Return to origin** at the end is a machine setting, not in the file.

**Reference implementation:** pyembroidery (Python, MIT license) — port its DST reader/writer and other format handlers. No mature Go library exists.

---

## 3. Known Limitations

### Resize
DST is raw stitches, not objects (satin, fill, underlay). Plain scaling only moves coordinates; stitch count stays the same.
- **Scaling up:** gaps in fills, loose/snaggy satins, stitches past 12.1 mm need splitting.
- **Scaling down:** bunching, thread breaks, bulletproof patches.
- **Plain scaling:** safe to roughly ±10–20%.
- **With a stitch processor** (detect satins/fills, regenerate at original density): 50–250% is achievable — this is what Embrilliance does.

### Color
No thread colors in DST. Track palettes in app metadata/sidecar.

### Testing
Preview doesn't show pull/push distortion. Every algorithm change needs real sew-outs on the Tajima.

### Object-level features
Reshaping, stitch-angle changes, and true re-digitizing require object data (native formats like EMB/PXF, which are proprietary/undocumented) or a stitch processor that reconstructs objects.

---

## 4. Text / Fonts

### Route 1 — Pre-digitized glyph library (recommended)
- Digitize each letter once (in existing software) as a stitch file with underlay built in.
- Go app handles: layout, kerning, tie-in/tie-off, trims between letters, scaling within safe range.
- Reliable quality; this is how most commercial lettering works.
- Start with one block font and one script font.

### Route 2 — TrueType/OpenType auto-digitizing
- Convert glyph outlines to satin columns or fills with pull compensation and underlay.
- Hard. Text under ~6 mm from TTF usually sews poorly.
- Study Ink/Stitch (Python, GPL — read, don't copy; check each bundled font's license).

### Lettering extras
- Lettering along a path (rotate glyphs to curve tangent)
- Monogram builder (letters + frame + décor)
- Name list → one design per name

---

## 5. Feature Reference (from commercial software)

Researched across Wilcom EmbroideryStudio, Tajima DG/ML by Pulse, Hatch, Embird, Embrilliance, mySewnet, and Ink/Stitch.

### Viewing & inspection
| Feature | How it works |
|---|---|
| Stitch player / slow redraw | Steps through the stitch list in order, drawing each segment. |
| TrueView / thread simulation | Renders stitches as shaded thread with width/highlights instead of 1px lines. |
| Real-scale zoom | Uses monitor DPI so design shows at sewn size. |
| Stitch count & time estimate | Record count ÷ machine SPM, plus overhead per trim and color change. |
| Density advisor / heat map | Counts needle penetrations per area and highlights hot spots. |
| Production worksheet | Prints preview, size, stitch count, color list, needle assignments. |

### Transform
| Feature | How it works |
|---|---|
| Move / rotate / mirror / skew / center | Affine math on every stitch coordinate. |
| Plain resize | Scales coordinates only; stitch count unchanged. Safe ~±20%. |
| Resize with density adjustment | Rebuilds fills/columns at original spacing (Embird: density as stitch distance in 0.1 mm). |
| Stitch processor | Detects satins/fills from raw stitches and regenerates them; keeps original and recomputes each size from it. |
| Target stitch count | Auto-adjusts fill/column density to hit a prescribed stitch count. |

### Structure & sequence
| Feature | How it works |
|---|---|
| Split / join / erase | Cut the stitch list by range; selection becomes an independent object. |
| Reorder | Move color blocks/objects, then rebuild connectors. |
| Color sort | Group same-color blocks to reduce changes (only safe when blocks don't overlap). |
| Reverse stitches | Reverse order within a color so the end lands near the next block's start. |
| Insert/remove color changes, stops, trims | Splice command records into the list. |
| Change stitch type | Flip a single record between jump and running stitch. |
| Path optimization | Re-sequence objects to cut travel, jumps, and trims. |
| Auto start/end points | Choose object entry/exit points closest to neighbors (see Section 6). |

### Stitch-level editing
| Feature | How it works |
|---|---|
| Edit stitches | Select needle points; drag, insert, delete. |
| Remove small stitches | Delete stitches under a length threshold (~0.3 mm); apply always or on output. |
| Long-stitch handling | Split overlong stitches or replace with jump series. |
| Tie-ins / tie-offs | Small anchor stitches before/after jumps; should not scale on resize. |
| Break up / compensate stitches | Split long satins into staggered splits; nudge edges outward. |

### Quality & fabric
| Feature | How it works |
|---|---|
| Underlay | Edge-run, zigzag, or tatami under the cover stitches. |
| Pull compensation | Extends stitches past the outline (mm, %, or asymmetric). |
| Fabric presets | Density/underlay/compensation sets per fabric type. |

### Lettering & production
| Feature | How it works |
|---|---|
| Pre-digitized fonts | Glyph libraries with spacing/kerning data. |
| TrueType lettering | Converts installed fonts to stitches. |
| Lettering on a path | Glyphs laid along a curve, rotated to tangent. |
| Monogram builder | Letters + orientation + frame + décor into one design. |
| Name lists / team names | Import list → one design per name. |
| Multiple thread palettes | Several palettes stored for different fabric colors. |
| Thread brand matching | Nearest RGB/Lab match between brands (Madeira, Isacord, Robison-Anton). |
| Applique / cutwork / design separator | Placement and tack stops, cutwork lines, multi-hoop splitting. |
| Hoop templates | Overlay real Tajima hoop sizes; warn when out of bounds. |
| Batch convert | Process/convert whole folders. |
| Machine connection | Direct/LAN send to Tajima. |

### Object-level only (needs native files or stitch processor)
- Object reshape with automatic stitch regeneration
- Stitch angle / density edits per object
- Stitch types: satin, tatami, motif, fur, cascade, E-stitch
- Auto-digitizing from images, photo stitch

---

## 6. Entry/Exit Point Optimization

**Why:** After merges and name drops, block exits/entries are arbitrary, creating long jumps → trims. Each trim costs seconds and adds thread-break risk. Matters most for multi-name jersey runs.

### What's possible per block type
| Block type | Options |
|---|---|
| Running stitch (open) | Reverse block — 2 entry points, no quality loss. |
| Running stitch (closed loop) | Rotate stitch list to start anywhere on the loop. |
| Satin column | Reverse to enter from other end. Must split underlay and cover, reverse each, keep underlay first. |
| Fill (tatami) | Reverse row order to flip corners (underlay first). Arbitrary start requires regeneration. |
| Tie-ins/tie-offs | Strip before reversing, regenerate after. |

### Implementation
1. **Block detection** — classify each color/jump-separated block as run, satin, or fill, with underlay segment identified. (Shared with stitch processor.)
2. **Entry/exit solver** — each block has 2–4 valid entry/exit pairs. Minimize total connector length: greedy pass + 2-opt.
3. **Rebuild connectors** — under ~2–3 mm: short travel run hidden under next block. Over that: jump + trim per TFHX setting.
4. Run automatically after merge, name drop, and color sort.

---

## 7. Tech Stack

- **Language:** Go
- **UI:** Go native GUI (Fyne, Gio) is weak. Use **Wails** or a Go backend + browser canvas frontend — fits existing web experience and makes rendering easy.
- **Format code:** Port from pyembroidery (MIT).
- **Integration:** T-Dongle-S3 auto-feed for sending finished DSTs to the machine.

---

## 8. Roadmap

### Phase 1 — Core (pure stitch-list operations)
- [x] DST reader/writer (fuzz-tested)
- [x] Renderer with sequence playback, stitch count, time estimate
- [x] Thread palette + needle assignment (saved in .tep project)
- [x] Production worksheet (print / print-to-PDF)
- [~] Merge with trim handling — built, **needs TFHX sew-out test**
- [~] Combine same-colour blocks (full colour sort not yet)
- [~] Reverse block done; insert/remove stops/trims not yet
- [x] Remove small stitches; long stitches split on export
- [x] Plain resize (warns beyond ±15%), rotate, mirror, center
- [x] Center-start normalization on output
- [x] Hoop templates

### Phase 2 — Production
- [~] Tie-in/tie-off (lettering only)
- [ ] Split/join by stitch range
- [~] Glyph-library loader built + built-in Block stroke font; digitized fonts still to make
- [~] Text layout (advance widths, no kerning pairs yet)
- [ ] Name drop from CSV
- [ ] Batch mode + T-Dongle integration
- [ ] Density heat map
- [ ] Multi-format I/O (PES, EXP, JEF, VP3, XXX)

### Phase 3 — Advanced
- [ ] Block detection (run / satin / fill + underlay)
- [ ] Entry/exit optimizer
- [ ] Stitch processor for 50–250% resizing
- [ ] Lettering on a path, monogram builder
- [ ] Thread simulation preview
- [ ] TrueType → satin auto-digitizing (only if needed)

---

## 9. Build status (2026-10-20)

v0.1 scaffold built in Go + Wails, UI matches the mockup. Source: `G:\Web Designs\TSG Embroidery Pro\tsg-embroidery-pro` (git repo, one commit, not yet pushed).

- **Packages:** `internal/dst`, `pattern`, `stitchgen`, `lettering`, `document` (undo, merge/export, .tep projects), `updater`, `app`; frontend is plain HTML/CSS/JS (no npm).
- **GitHub:** CI (gofmt, vet, staticcheck, race tests, 30 s fuzz, Windows build), Security (CodeQL Go/JS/Actions, weekly govulncheck, OpenSSF Scorecard), Dependabot, tag-triggered Release (Windows amd64/arm64, Linux, macOS; signed checksums; build provenance). Actions pinned to SHAs.
- **Auto-update:** GitHub Releases; installs only if Ed25519 signature on checksums.txt + SHA-256 verify. Key via `go run ./cmd/releasekey gen`; private half goes in the `release` environment secret `UPDATE_SIGNING_KEY`.
- **Next:** test merged/lettered exports on the TFHX (trim jump count, start point), digitize a real block + script glyph font, colour sort, insert/remove stops.

## 10. References

- [pyembroidery](https://github.com/EmbroidePy/pyembroidery) — format reference (MIT)
- [Ink/Stitch](https://inkstitch.org/) — open-source digitizing reference (GPL)
- [Wilcom – Stitch editing](https://docs.wilcom.com/embroiderystudio/e4/en/MainHelp/Summary/summary_-_edit/Stitch_editing.htm)
- [Wilcom – Stitch Quality](https://docs.wilcom.com/embroiderystudio/26/en/OnlineHelp/Quality/PART_Quality/PART_Quality.htm)
- [Tajima DG17 brochure](https://www.tajima.com/wp-content/uploads/2024/12/DG17_202411_EN.pdf)
- [Pulse – DG/ML products](https://dgml14.pulsemicro.com/pulse-products.htm)
- [Hatch – Remove small stitches](https://help.hatchembroidery.com/portal/en/kb/hatch/how-to-faqs/general/articles/ho)
- [Embird – Editor menu](https://www.embird.net/manual/0525editor.htm)
- [Embird – Edit menu](https://www.embird.net/manual/0750edit.htm)
- [Embird – Resize with density](https://embird.net/sw/embird/tutorial/resize/resize.htm)
- [Embird Studio – Whole-design parameters](https://www.embird.net/machine-embroidery-tutorials/studio-next/par_whole.htm)
- [Embrilliance Essentials (stitch processor)](https://embrilliance.com/Help/Platform%20Win%201148/productessentials.htm)
- [mySewnet – Modify window](https://softwarehelp.mysewnet.com/MSM/100/StitchEditor/Topics/Modify_Window.htm)
- [Ink/Stitch v3.0.0 release notes](https://inkstitch.org/en/2023-05-26-v3.0.0)
- [makema – Scaling embroidery files](https://makema.de/en/blogs/faq/stickdateien-skalieren)
