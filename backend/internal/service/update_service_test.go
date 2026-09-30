//go:build unit

package service

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type updateServiceCacheStub struct {
	data string
}

func (s *updateServiceCacheStub) GetUpdateInfo(context.Context) (string, error) {
	if s.data == "" {
		return "", errors.New("cache miss")
	}
	return s.data, nil
}

func (s *updateServiceCacheStub) SetUpdateInfo(_ context.Context, data string, _ time.Duration) error {
	s.data = data
	return nil
}

type updateServiceGitHubClientStub struct {
	release        *GitHubRelease
	recentReleases []*GitHubRelease
	recentErr      error
	latestRepo     string
}

func (s *updateServiceGitHubClientStub) FetchLatestRelease(_ context.Context, repo string) (*GitHubRelease, error) {
	s.latestRepo = repo
	return s.release, nil
}

func (s *updateServiceGitHubClientStub) FetchRecentReleases(context.Context, string, int) ([]*GitHubRelease, error) {
	return s.recentReleases, s.recentErr
}

func (s *updateServiceGitHubClientStub) DownloadFile(context.Context, string, string, int64) error {
	panic("DownloadFile should not be called when no update is available")
}

func (s *updateServiceGitHubClientStub) FetchChecksumFile(context.Context, string) ([]byte, error) {
	panic("FetchChecksumFile should not be called when no update is available")
}

func TestUpdateServicePerformUpdateNoUpdateReturnsSentinel(t *testing.T) {
	githubClient := &updateServiceGitHubClientStub{
		release: &GitHubRelease{
			TagName: "v0.1.132",
			Name:    "v0.1.132",
		},
	}
	svc := NewUpdateService(
		&updateServiceCacheStub{},
		githubClient,
		"0.1.132",
		"release",
	)

	err := svc.PerformUpdate(context.Background())

	require.Error(t, err)
	require.True(t, errors.Is(err, ErrNoUpdateAvailable))
	require.ErrorIs(t, err, ErrNoUpdateAvailable)
	require.Equal(t, "1057300248/sub2api", githubClient.latestRepo)
}

func newRollbackTestService(current string, releases []*GitHubRelease) *UpdateService {
	return NewUpdateService(
		&updateServiceCacheStub{},
		&updateServiceGitHubClientStub{recentReleases: releases},
		current,
		"release",
	)
}

func TestUpdateServiceListRollbackVersionsFiltersAndCaps(t *testing.T) {
	releases := []*GitHubRelease{
		{TagName: "v0.1.148", PublishedAt: "2026-07-09T00:00:00Z"},                       // newer than current: excluded
		{TagName: "v0.1.147", PublishedAt: "2026-07-08T00:00:00Z"},                       // current: excluded
		{TagName: "v0.1.146-rc1", PublishedAt: "2026-07-07T12:00:00Z", Prerelease: true}, // prerelease: excluded
		{TagName: "v0.1.146", PublishedAt: "2026-07-07T00:00:00Z"},
		{TagName: "v0.1.145", PublishedAt: "2026-07-06T00:00:00Z", Draft: true}, // draft: excluded
		{TagName: "v0.1.144", PublishedAt: "2026-07-05T00:00:00Z"},
		{TagName: "v0.1.144", PublishedAt: "2026-07-05T00:00:00Z"}, // duplicate: excluded
		{TagName: "v0.1.143", PublishedAt: "2026-07-04T00:00:00Z"},
		{TagName: "v0.1.142", PublishedAt: "2026-07-03T00:00:00Z"}, // beyond cap of 3: excluded
	}
	svc := newRollbackTestService("0.1.147", releases)

	versions, err := svc.ListRollbackVersions(context.Background())

	require.NoError(t, err)
	require.Len(t, versions, 3)
	require.Equal(t, "0.1.146", versions[0].Version)
	require.Equal(t, "0.1.144", versions[1].Version)
	require.Equal(t, "0.1.143", versions[2].Version)
}

func TestUpdateServiceListRollbackVersionsSortsUnorderedInput(t *testing.T) {
	releases := []*GitHubRelease{
		{TagName: "v0.1.144"},
		{TagName: "v0.1.146"},
		{TagName: "v0.1.145"},
	}
	svc := newRollbackTestService("0.1.147", releases)

	versions, err := svc.ListRollbackVersions(context.Background())

	require.NoError(t, err)
	require.Len(t, versions, 3)
	require.Equal(t, "0.1.146", versions[0].Version)
	require.Equal(t, "0.1.145", versions[1].Version)
	require.Equal(t, "0.1.144", versions[2].Version)
}

func TestUpdateServiceListRollbackVersionsEmptyWhenNoneOlder(t *testing.T) {
	releases := []*GitHubRelease{
		{TagName: "v0.1.147"},
		{TagName: "v0.1.148"},
	}
	svc := newRollbackTestService("0.1.147", releases)

	versions, err := svc.ListRollbackVersions(context.Background())

	require.NoError(t, err)
	require.Empty(t, versions)
}

func TestUpdateServiceListRollbackVersionsPropagatesFetchError(t *testing.T) {
	svc := NewUpdateService(
		&updateServiceCacheStub{},
		&updateServiceGitHubClientStub{recentErr: errors.New("github unavailable")},
		"0.1.147",
		"release",
	)

	_, err := svc.ListRollbackVersions(context.Background())

	require.Error(t, err)
	require.Contains(t, err.Error(), "github unavailable")
}

func TestUpdateServiceRollbackToVersionRejectsDisallowedTargets(t *testing.T) {
	releases := []*GitHubRelease{
		{TagName: "v0.1.148"},
		{TagName: "v0.1.147"},
		{TagName: "v0.1.146"},
		{TagName: "v0.1.145"},
		{TagName: "v0.1.144"},
		{TagName: "v0.1.143"},
		{TagName: "v0.1.142"},
	}
	svc := newRollbackTestService("0.1.147", releases)

	for _, target := range []string{
		"",         // empty
		"0.1.147",  // current version
		"v0.1.147", // current version with prefix
		"0.1.148",  // newer than current
		"0.1.142",  // older than the 3 most recent
		"9.9.9",    // nonexistent
	} {
		err := svc.RollbackToVersion(context.Background(), target)
		require.ErrorIs(t, err, ErrRollbackVersionNotAllowed, "target %q should be rejected", target)
	}
}

func TestUpdateServiceRollbackToVersionAcceptsVPrefix(t *testing.T) {
	// No platform asset in the release: the target passes the allowlist check
	// and fails later at asset lookup, proving the version itself was accepted.
	releases := []*GitHubRelease{
		{TagName: "v0.1.147"},
		{TagName: "v0.1.146"},
	}
	svc := newRollbackTestService("0.1.147", releases)

	err := svc.RollbackToVersion(context.Background(), "v0.1.146")

	require.Error(t, err)
	require.NotErrorIs(t, err, ErrRollbackVersionNotAllowed)
	require.Contains(t, err.Error(), "no compatible release found")
}


func TestCompareVersionsWanchuanRevision(t *testing.T) {
	tests := []struct {
		name    string
		current string
		latest  string
		want    int
	}{
		{name: "first fork revision follows upstream base", current: "2.9.6", latest: "2.9.6-wanchuan.1", want: -1},
		{name: "fork revisions increase monotonically", current: "2.9.6-wanchuan.1", latest: "2.9.6-wanchuan.2", want: -1},
		{name: "same fork revision is equal", current: "v2.9.6-wanchuan.2", latest: "2.9.6-wanchuan.2", want: 0},
		{name: "next upstream base wins over fork revision", current: "2.9.6-wanchuan.99", latest: "2.9.7", want: -1},
		{name: "older upstream base remains older", current: "2.9.5-wanchuan.99", latest: "2.9.6-wanchuan.1", want: -1},
		{name: "unknown prerelease suffix keeps legacy base comparison", current: "2.9.6-rc.1", latest: "2.9.6", want: 0},
		{name: "malformed fork revision is ignored", current: "2.9.6-wanchuan.bad", latest: "2.9.6", want: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, compareVersions(tt.current, tt.latest))
			require.Equal(t, -tt.want, compareVersions(tt.latest, tt.current))
		})
	}
}


func TestUpdateServiceCheckUpdateRequiresWanchuanReleaseMetadata(t *testing.T) {
	validAssets := []GitHubAsset{
		{Name: "checksums.txt", BrowserDownloadURL: "https://github.com/1057300248/sub2api/releases/download/v2.9.6-wanchuan.1/checksums.txt"},
		{Name: wanchuanReleaseManifestName, BrowserDownloadURL: "https://github.com/1057300248/sub2api/releases/download/v2.9.6-wanchuan.1/wanchuan-release.json"},
	}
	tests := []struct {
		name        string
		tag         string
		assets      []GitHubAsset
		wantUpdate  bool
		wantWarning string
	}{
		{name: "verified Wanchuan metadata", tag: "v2.9.6-wanchuan.1", assets: validAssets, wantUpdate: true},
		{name: "plain owner release rejected", tag: "v2.9.7", assets: validAssets, wantWarning: "not a Wanchuan"},
		{name: "manifest missing", tag: "v2.9.6-wanchuan.1", assets: []GitHubAsset{{Name: "checksums.txt"}}, wantWarning: "missing required"},
		{name: "checksums missing", tag: "v2.9.6-wanchuan.1", assets: []GitHubAsset{{Name: wanchuanReleaseManifestName}}, wantWarning: "missing required"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := &updateServiceGitHubClientStub{release: &GitHubRelease{TagName: tt.tag, Assets: tt.assets}}
			svc := NewUpdateService(&updateServiceCacheStub{}, client, "2.9.6", "release")
			info, err := svc.CheckUpdate(context.Background(), true)
			require.NoError(t, err)
			require.Equal(t, tt.wantUpdate, info.HasUpdate)
			if tt.wantWarning != "" {
				require.Contains(t, info.Warning, tt.wantWarning)
			} else {
				require.Empty(t, info.Warning)
			}
		})
	}
}

func TestValidateWanchuanReleaseManifest(t *testing.T) {
	valid := WanchuanReleaseManifest{
		SchemaVersion:       1,
		Channel:             wanchuanReleaseChannel,
		Version:             "2.9.6-wanchuan.1",
		SourceSHA:           strings.Repeat("a", 40),
		ReleaseRepo:         githubRepo,
		UpstreamRepo:        upstreamGithubRepo,
		UpstreamTag:         "v2.9.6",
		UpstreamSHA:         strings.Repeat("b", 40),
		IntegrationCommit:   strings.Repeat("c", 40),
		PatchManifestSHA256: strings.Repeat("d", 64),
		PatchModules:        []string{"cline-rate-limit-cas"},
	}
	data, err := json.Marshal(valid)
	require.NoError(t, err)
	require.NoError(t, validateWanchuanReleaseManifest(data, valid.Version))

	bad := valid
	bad.Version = "2.9.6-wanchuan.2"
	data, err = json.Marshal(bad)
	require.NoError(t, err)
	require.Error(t, validateWanchuanReleaseManifest(data, valid.Version))

	bad = valid
	bad.ReleaseRepo = "ranxi2001/sub2api"
	data, err = json.Marshal(bad)
	require.NoError(t, err)
	require.Error(t, validateWanchuanReleaseManifest(data, valid.Version))
}

func TestVerifyBytesChecksumAcceptsManifestEntry(t *testing.T) {
	data := []byte("{\"channel\":\"wanchuan\"}")
	digest := sha256.Sum256(data)
	checksums := []byte(fmt.Sprintf("%x  %s\n", digest, wanchuanReleaseManifestName))
	require.NoError(t, verifyBytesChecksum(wanchuanReleaseManifestName, data, checksums))
	require.Error(t, verifyBytesChecksum(wanchuanReleaseManifestName, append(data, '!'), checksums))
}
