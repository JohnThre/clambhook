# tomlc99 provenance

- Upstream: https://github.com/cktan/tomlc99
- Commit: `29076dfd095bbbbd50a3c1b2760d29f4b83e74ac`
- License: MIT; see `LICENSE`
- Imported files: `toml.c`, `toml.h`, and `LICENSE`
- Local modifications: none

The commit is pinned so native and Android builds do not depend on a network
fetch and parse the same TOML 1.0 configuration grammar everywhere.
