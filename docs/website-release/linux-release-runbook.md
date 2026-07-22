# GNU/Linux Release Runbook (Owner)

End-user GNU/Linux installers are distributed **only from store.clambercloud.com** as free `.deb`, `.rpm`, Flatpak, and AppImage packages. GitHub stays source-only and view-only. Every release artifact is SHA-256 checksummed and GPG-signed with the configured ClambHook release key.

This runbook covers the owner-held steps. None of these publish anything to GitHub.

## Supported distributions

| Distro | Package | Recipe |
| --- | --- | --- |
| Ubuntu, Debian, PureOS | `.deb` | `debian/` |
| Fedora, Rocky Linux | `.rpm` | `packaging/rpm/clambhook.spec` |
| Bazzite, any distro | Flatpak | `packaging/flatpak/com.clambhook.Clambhook.yaml` |
| Any distro | AppImage | `packaging/appimage/build-appimage.sh` |

Validation is run with `scripts/validate-linux-distros.sh` before release. See `packaging/README.md` for the container harness details.

## 0. One-time setup

### 0.1 GNU/Linux build host

Use a dedicated GNU/Linux build host (x86_64 or aarch64) with:

- Go toolchain matching `go.mod`.
- `gcc`, `pkg-config`.
- JDK 17+ (Kotlin/Compose desktop controller build; provides `javac`/`gradle`).
- libsodium-dev (Go daemon build).
- libsecret (runtime; used via `secret-tool` CLI for token storage).
- For `.deb`: `dpkg-buildpackage`, `dpkg-deb`.
- For `.rpm`: `rpmbuild`.
- For Flatpak: `flatpak-builder` plus `org.freedesktop.Platform//24.08`, `org.freedesktop.Sdk//24.08`, `org.freedesktop.Sdk.Extension.openjdk17//24.08`, `org.freedesktop.Sdk.Extension.golang//24.08`.
- For AppImage: the script downloads `linuxdeploy` + `appimagetool` on first run.
- GnuPG with the release key available.

### 0.2 GPG release key

Your configured git signing key is `EAA876B70B1832F5` (signing subkey of `6FF4807EAD977A9B`, Pengfan Chang <developer@jpfchang.org>). Confirm it signs:

```sh
echo test | gpg --batch --yes --pinentry-mode loopback \
    --local-user EAA876B70B1832F5 --detach-sign --armor
```

If you want a different key for releases, set `GPG_KEY` in your release shell. Make sure the public key is published at `https://store.clambercloud.com/clambhook/clambhook-release-key.asc` so users can verify.

`scripts/release-linux.sh` requires GPG signing by default. For an internal-only build-validation run that will never be published, set `REQUIRE_SIGNING=0`.

### 0.3 Cloudflare R2 + website

```sh
export CLAMBHOOK_R2_BUCKET=clambhook-artifacts
```

Confirm the `clambhook-artifacts` R2 bucket is bound to the `clambercloud.com` / `store.clambercloud.com` Pages project and that the artifact routes under `/api/clambhook/*` are wired to read the environment variables documented in `clambercloud.com/wrangler.toml`.

## 1. Validate before building

From the repo root on the build host:

```sh
make test
make lint
make build-linux
make test-linux
```

Then run the cross-distro validation harness:

```sh
scripts/validate-linux-distros.sh
```

This builds and headless-smoke-tests Ubuntu, Debian, Fedora, Rocky, PureOS (via Debian), and Bazzite (via Fedora + Flatpak). Do not proceed if any distro fails.

## 2. Build, checksum, sign, and upload

From the repo root on the GNU/Linux build host:

```sh
export CLAMBHOOK_R2_BUCKET=clambhook-artifacts
export GPG_KEY=EAA876B70B1832F5
export VERSION="$(git describe --tags --always --dirty | sed 's/^v//')"
make release-linux
make upload-release-linux
```

`make release-linux` (via `scripts/release-linux.sh`) will:

1. Build `.deb`, `.rpm`, Flatpak, and AppImage packages.
2. Write a SHA-256 checksum for each package.
3. GPG-sign each `.sha256` file (detached `.sha256.sig`).
4. Generate `clambhook-linux-manifest.json` with version, `publishedAt`, architecture, and per-package URLs + SHA-256 hashes.
5. GPG-sign the manifest (`.sig`).

`make upload-release-linux` (via `scripts/upload-release-linux.sh`) will:

1. Upload versioned artifacts to `r2://clambhook-artifacts/clambhook/linux/${VERSION}/${ARCH}/`.
2. Overwrite the latest stable/beta keys at `r2://clambhook-artifacts/clambhook/linux/stable/` (or `beta/`).
3. Upload the manifest and its signature.

Outputs land under `dist/linux/`:

- `clambhook-${VERSION}-${ARCH}.deb`
- `clambhook-${VERSION}-${ARCH}.deb.sha256` + `.sig`
- `clambhook-${VERSION}-${ARCH}.rpm`
- `clambhook-${VERSION}-${ARCH}.rpm.sha256` + `.sig`
- `clambhook-${VERSION}-${ARCH}.flatpak`
- `clambhook-${VERSION}-${ARCH}.flatpak.sha256` + `.sig`
- `clambhook-${VERSION}-${ARCH}.AppImage`
- `clambhook-${VERSION}-${ARCH}.AppImage.sha256` + `.sig`
- `clambhook-linux-manifest.json` + `.sig`

For a beta build: `UPDATE_CHANNEL=beta make release-linux && UPDATE_CHANNEL=beta make upload-release-linux`.

## 3. Set Cloudflare Pages variables

After upload, set the public artifact URLs on the `clambercloud` Pages project (development + production):

```txt
CLAMBHOOK_STABLE_LINUX_DEB_URL
CLAMBHOOK_STABLE_LINUX_DEB_SHA256_URL
CLAMBHOOK_STABLE_LINUX_RPM_URL
CLAMBHOOK_STABLE_LINUX_RPM_SHA256_URL
CLAMBHOOK_STABLE_LINUX_FLATPAK_URL
CLAMBHOOK_STABLE_LINUX_FLATPAK_SHA256_URL
CLAMBHOOK_STABLE_LINUX_APPIMAGE_URL
CLAMBHOOK_STABLE_LINUX_APPIMAGE_SHA256_URL
CLAMBHOOK_STABLE_LINUX_MANIFEST_URL
```

Use the `clambhook/linux/stable/` keys uploaded in step 2. For beta, set the corresponding `CLAMBHOOK_BETA_*` variables pointing at `clambhook/linux/beta/`.

## 4. Verify endpoints

```sh
curl -sI "https://store.clambercloud.com/api/clambhook/download?platform=linux&pkg=deb"
curl -sI "https://store.clambercloud.com/api/clambhook/download?platform=linux&pkg=rpm"
curl -sI "https://store.clambercloud.com/api/clambhook/download?platform=linux&pkg=flatpak"
curl -sI "https://store.clambercloud.com/api/clambhook/download?platform=linux&pkg=appimage"
curl -s  "https://store.clambercloud.com/api/clambhook/linux-manifest" | head
```

Each download endpoint should redirect (or proxy) to the matching R2 object. The manifest should return JSON with version, publishedAt, architecture, and packages.

## 5. User verification (publish on the download page)

Tell users to verify the download against the published SHA-256:

```sh
sha256sum clambhook-${VERSION}-${ARCH}.deb
# Compare with the value served at:
# /api/clambhook/download?platform=linux&pkg=deb&format=sha256
```

If the `.sig` files are also served, users can verify the GPG signature over the checksum:

```sh
curl -fsSL https://store.clambercloud.com/clambhook/clambhook-release-key.asc | gpg --import
gpg --verify clambhook-${VERSION}-${ARCH}.deb.sha256.sig clambhook-${VERSION}-${ARCH}.deb.sha256
```

## 6. Sign the release tag

```sh
./scripts/sign-release-tag.sh "v$VERSION" create   # create + GPG-sign tag at HEAD
# or, if the tag already exists:
./scripts/sign-release-tag.sh "v$VERSION"
git push origin "v$VERSION"
```

GitHub will show the tag as Verified. Do not attach the `.deb`, `.rpm`, Flatpak, AppImage, or any installer artifact to the GitHub release.

## 7. Release checklist

- [ ] `make test`, `make lint`, `make build-linux`, `make test-linux` pass.
- [ ] `scripts/validate-linux-distros.sh` passes for all six supported distros.
- [ ] `make release-linux` completed; `dist/linux/` contains all four packages + checksums + signatures + manifest.
- [ ] `make upload-release-linux` completed; R2 keys `clambhook/linux/stable/` (or `beta/`) are updated.
- [ ] Cloudflare Pages variables for the channel are set to the new R2 URLs.
- [ ] `/api/clambhook/download?platform=linux&pkg=*` and `/api/clambhook/linux-manifest` return the new version.
- [ ] Download page verification instructions are up to date.
- [ ] Release tag GPG-signed and pushed (Verified on GitHub).
- [ ] No installer artifact attached to the GitHub release (source-only).
