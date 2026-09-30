# Wanchuan patch pack

This fork treats local customizations as a source-overlay patch pack rather than trying to force every customization into Sub2API's runtime plugin ABI.

The upstream runtime plugin system remains useful for isolated capabilities, but several Wanchuan changes touch compile-time Go code, database/repository behavior, release logic and the admin frontend. Those cannot be safely hot-loaded as ordinary process plugins.

Long-lived integration branch: wanchuan/stable.

The patch pack uses four layers:

1. .wanchuan/patches/manifest.json declares custom ownership boundaries and regression commands.
2. .wanchuan/upstream.lock pins the exact upstream release commit currently integrated.
3. tools/wanchuan_patchpack.py classifies upstream changes. Changes outside protected paths can be auto-adapted; protected-path changes require a review PR.
4. .github/workflows/wanchuan-upstream-sync.yml checks the newest stable upstream release, performs a real three-way merge, runs compatibility gates, and either fast-forwards the custom base or opens a review PR.

Stable fork releases use <upstream>-wanchuan.<revision>, for example 2.9.6-wanchuan.1. The built-in updater is pinned to 1057300248/sub2api, so production instances continue using Sub2API's existing update mechanism while consuming only validated Wanchuan release artifacts.

The source tree deliberately keeps backend/cmd/server/VERSION equal to the upstream version. The Wanchuan revision is injected from the release tag into the release build workspace. Keeping the source VERSION untouched prevents every future upstream version bump from becoming an artificial merge conflict.

## Safety rules

- Merge conflicts never publish.
- Any upstream touch to a protected module or global high-risk path becomes a review PR.
- Automatic publication requires backend, frontend, custom-regression, release-helper and security gates in the sync workflow to pass.
- The base branch must not move during validation; otherwise the result becomes a PR instead of being pushed.
- Wanchuan revisions are marked as stable GitHub releases so /releases/latest remains compatible with the built-in updater.
