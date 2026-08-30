# libyaml provenance

- Upstream: https://github.com/yaml/libyaml
- Release: `0.2.5`
- Commit: `2c891fc7a770e8ba2fec34fc6b545c672beb37e6`
- Source archive SHA-256: `fa240dbf262be053f3898006d502d514936c818e422afdcf33921c63bed9bf2e`
- License: MIT; see `LICENSE`
- Imported files: `include/yaml.h`, the parser/loader sources in `src/`, and
  `LICENSE`
- Local modifications: none

Only libyaml's read-side document parser is compiled. The pinned sources keep
Outline dynamic-access-key YAML parsing identical on desktop and Android and
avoid a runtime or build-time network dependency.
