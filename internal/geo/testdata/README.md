# MaxMind test MMDB fixtures

These files are vendored from https://github.com/maxmind/MaxMind-DB
(`test-data/` directory) for use in this package's tests. They contain a
small, stable set of records with documented expected values so tests do
not depend on a real GeoLite2 license or network access.

Files:
- `GeoIP2-City-Test.mmdb` — city-level records
- `GeoIP2-Country-Test.mmdb` — country-only records (for coverage of the
  code path where city/location fields are absent)

The MaxMind-DB project is dual-licensed under MIT and Apache 2.0. These
fixtures remain subject to their upstream license terms.

Source: https://github.com/maxmind/MaxMind-DB/tree/main/test-data
