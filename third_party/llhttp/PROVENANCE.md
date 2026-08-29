# llhttp provenance

- Upstream: https://github.com/nodejs/llhttp
- Version: `9.4.3` (`release/v9.4.3`)
- Release archive SHA-256:
  `1eb813c7437b31a87496a1cd3ed79f00746720f5e7e29c79b42c02cb69f36c39`
- License: MIT (see `LICENSE` and `LICENSE-MIT`)
- Imported files: the complete upstream release archive.
- Local modifications: none.

ClambHook compiles `src/api.c`, `src/http.c`, and `src/llhttp.c` into a private
static library. Keeping the generated parser and public header in-tree avoids
distro package drift and gives native and package builds the same parser.
