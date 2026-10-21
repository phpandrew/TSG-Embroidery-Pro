# TSG Embroidery Pro

A Windows desktop app (Go + [Wails](https://wails.io)) for viewing and editing **Tajima DST** embroidery files — built to replace dated shop software for the Tajima TFHX 1506.

- **View** — thread-simulated preview, stitch-by-stitch playback, rulers, hoop templates, jump display
- **Merge** — combine any number of DST files and lettering into one sew-out, reorder designs and colour blocks
- **Adjust** — move, rotate, mirror, center, resize (plain coordinate scaling, flagged beyond ±15%)
- **Lettering** — built-in Block satin font, plus pre-digitized glyph-library fonts (one DST per letter)
- **Threads & needles** — palette with needle assignments saved in the project (`.tep`), needle sequence for the operator
- **Tools** — remove small stitches, reverse block direction, combine same-colour blocks
- **Production** — printable worksheet, sew-time estimate, trim/jump settings that match the machine
- **Export** — clean DST: long moves split at 12.1 mm, trims as N jump codes, colour changes only where the needle changes, start at design centre
- **Auto-update** — checks GitHub Releases; installs only Ed25519-signed, checksum-verified builds

## Layout

```
main.go                     Wails window, native menu, dialogs
internal/dst                DST reader/writer (fuzz-tested)
internal/pattern            stitch model, transforms, stitch ops
internal/stitchgen          running stitch, satin columns, tatami fill, tie-ins
internal/lettering          text layout, Block font, glyph-library fonts
internal/document           multi-design document, undo/redo, merge/export, .tep projects
internal/updater            signed GitHub Releases updater
internal/app                API bound to the UI
frontend/dist               UI — plain HTML/CSS/JS, no npm dependencies
cmd/devserver               run the UI in a browser against the Go API
cmd/releasekey              generate / use the release signing key
cmd/samplegen               sample DSTs for testing
```

## Develop

Requirements: Go 1.26+, and the Wails CLI (`go install github.com/wailsapp/wails/v2/cmd/wails@v2.17.0`). On Windows, WebView2 is already part of Windows 10/11.

```sh
wails dev                                   # live desktop app
go test ./internal/... ./cmd/...            # tests
go run ./cmd/samplegen                      # writes samples/*.dst
go run ./cmd/devserver -open "samples/Team crest.dst"   # UI in a browser at http://127.0.0.1:34115
wails build -platform windows/amd64         # build/bin/tsg-embroidery-pro.exe (also works from Linux/macOS)
```

The frontend has no build step and no npm packages — nothing to `npm audit`, and the whole UI is reviewable as-is.

## Lettering fonts

The built-in **Block** font generates satin columns on the fly; it's fine for names and numbers from ~8 mm up. For production lettering, digitize each letter once in your current software and drop a folder into `%APPDATA%\TSG Embroidery Pro\fonts\<FontName>\`:

```json
{
  "name": "Varsity",
  "capHeight": 250,
  "glyphs": {
    "A": { "file": "A.dst", "advance": 230 },
    "Q": { "file": "Q.dst", "advance": 250, "descender": true }
  }
}
```

`capHeight` and `advance` are in 0.1 mm. Keep text within ±20% of the digitized height.

## GitHub setup (one time)

1. **Repo:** `github.com/phpandrew/TSG-Embroidery-Pro` (module path and update checks already point here). If you rename or move it, replace `phpandrew/TSG-Embroidery-Pro` across `*.go`, `go.mod` and `.github/workflows/*.yml`.
2. **Create the update-signing key** and commit the public half:
   ```sh
   go run ./cmd/releasekey gen
   git add internal/updater/release-key.pub
   ```
   Then in **Settings → Environments** create an environment named `release`, restrict it to tags `v*`, (optionally) add yourself as a required reviewer, and add the secret:
   ```sh
   gh secret set UPDATE_SIGNING_KEY --env release < release-signing-key.txt
   ```
   Back up the key in your password manager and delete the file. Lose it and existing installs can't auto-update to builds signed with a new key.
3. **Settings → Code security**: turn on Dependabot alerts and security updates, secret scanning with push protection, and private vulnerability reporting. CodeQL results arrive from the `Security` workflow.
4. **Rulesets**: protect `main` (require the `CI` checks, no force-push) and protect tags matching `v*` so only you can create release tags.

## Releasing

```sh
git tag v0.1.0
git push origin v0.1.0          # v0.2.0-beta.1 style tags publish as pre-releases
```

`release.yml` then: re-runs tests and govulncheck, builds Windows (amd64/arm64), Linux and macOS, writes `checksums.txt`, signs it with the release key, attaches a GitHub build-provenance attestation, and publishes the release. Installed copies pick it up at next launch (or **Help → Check for updates**), download it, verify signature + SHA-256, swap the exe and restart.

Verify a download by hand:
```sh
gh attestation verify tsg-embroidery-pro_windows_amd64.exe --repo OWNER/REPO
```

## Vulnerability checking

| What | Where | When |
|---|---|---|
| `govulncheck` (Go stdlib + modules, reachability-aware) | CI, Security, Release | every push/PR, weekly, every release |
| CodeQL (Go, JavaScript, Actions) security-extended | Security | every push/PR, weekly |
| OpenSSF Scorecard | Security | push to main, weekly |
| Dependabot (Go modules + pinned Actions) | `.github/dependabot.yml` | weekly PRs |
| staticcheck, `go vet`, 30 s fuzz of the DST parser | CI | every push/PR |

All third-party Actions are pinned to commit SHAs; Dependabot keeps them current.

## DST notes

- Units are 0.1 mm; one record moves at most ±12.1 mm.
- DST has **no colours** — threads and needles live in the project file and on the worksheet.
- DST has **no trim code** — Tajima trims on *N* consecutive jumps. Set *Jump codes per trim* (Production tab) to match the TFHX setting.
- Plain resize moves stitches; it doesn't regenerate them. Stay within ±15% and test-sew.

## License

MIT — see [LICENSE](LICENSE). DST bit layout follows the public format description used by [pyembroidery](https://github.com/EmbroidePy/pyembroidery) (MIT).
