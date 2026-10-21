# Security policy

## Reporting a vulnerability

Please report security issues privately through **GitHub → Security → Report a vulnerability** on this repository. Do not open a public issue. You'll get an acknowledgement within a few business days.

In scope:
- the auto-updater (signature/checksum bypass, downgrade, URL/host handling)
- parsing of DST files, `.tep` projects and glyph-library fonts (crashes, hangs, path traversal, memory exhaustion)
- the release pipeline in `.github/workflows`

## Supported versions

Only the latest release receives fixes. The app updates itself, so staying current is automatic unless update checks are turned off.

## How releases are protected

- Release binaries are built by GitHub Actions from a tag on `main`, with SHA-pinned Actions.
- `checksums.txt` is signed with an Ed25519 key that exists only as a GitHub Actions environment secret. The public key is compiled into the app (`internal/updater/release-key.pub`).
- The updater installs a build only if the signature verifies **and** the file's SHA-256 matches the signed list. It only contacts `api.github.com`, `github.com` and GitHub's release-asset hosts over HTTPS, with size limits on every download.
- Every release asset has a GitHub build-provenance attestation (`gh attestation verify <file> --repo <owner>/<repo>`).
- Development builds (`version = dev`) never check for or install updates.

## Untrusted input

DST and project files are treated as untrusted: record counts and file sizes are capped, the DST reader is fuzz-tested in CI, glyph fonts can only reference files inside their own folder, and the UI escapes all file-provided text and runs under a strict Content-Security-Policy.
