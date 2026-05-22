# F-Droid publishing notes

The Android application ID for F-Droid is `org.jpfchang.clambhook`.

Use signed annotated Git tags for update discovery. Do not create GitHub
Releases for F-Droid publication; F-Droid should build from the tagged source.

Suggested fdroiddata metadata:

```yaml
Categories:
  - Internet
License: GPL-3.0-only
AuthorName: Pengfan Chang
WebSite: https://jpfchang.org/clambhook
SourceCode: https://github.com/JohnThre/clambhook
IssueTracker: https://github.com/JohnThre/clambhook/issues
RepoType: git
Repo: https://github.com/JohnThre/clambhook.git
AutoName: Clambhook
UpdateCheckMode: Tags
AutoUpdateMode: Version
CurrentVersion: 0.1.0
CurrentVersionCode: 1
```

The build recipe must run `scripts/build-android-mobile-aar.sh` before the
Android Gradle package task so the embedded Go runtime is produced from source.
