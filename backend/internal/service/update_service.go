package service

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

var (
	ErrNoUpdateAvailable         = infraerrors.Conflict("ALREADY_UP_TO_DATE", "no update available; current version is latest")
	ErrRollbackVersionNotAllowed = infraerrors.BadRequest("ROLLBACK_VERSION_NOT_ALLOWED", "version is not in the allowed rollback list")
)

const (
	updateCacheKey = "update_check_cache"
	updateCacheTTL = 1200 // 20 minutes
	// Releases are maintained on the owner-controlled production fork. Keep the
	// updater independent from the upstream repository so production installs
	// see our release stream and can update to our fork's assets.
	githubRepo         = "1057300248/sub2api"
	upstreamGithubRepo = "ranxi2001/sub2api"

	wanchuanReleaseManifestName = "wanchuan-release.json"
	wanchuanReleaseChannel      = "wanchuan"
	maxReleaseMetadataSize      = 2 * 1024 * 1024

	// Security: allowed download domains for updates
	allowedDownloadHost = "github.com"
	allowedAssetHost    = "objects.githubusercontent.com"

	// Security: max download size (500MB)
	maxDownloadSize = 500 * 1024 * 1024

	// Rollback: expose at most the 3 most recent versions older than current
	maxRollbackVersions = 3
	// Fetch a few extra releases so filtering (current/newer/prerelease) still leaves enough candidates
	rollbackFetchPageSize = 15
)

// UpdateCache defines cache operations for update service
type UpdateCache interface {
	GetUpdateInfo(ctx context.Context) (string, error)
	SetUpdateInfo(ctx context.Context, data string, ttl time.Duration) error
}

// GitHubReleaseClient 获取 GitHub release 信息的接口
type GitHubReleaseClient interface {
	FetchLatestRelease(ctx context.Context, repo string) (*GitHubRelease, error)
	FetchRecentReleases(ctx context.Context, repo string, perPage int) ([]*GitHubRelease, error)
	DownloadFile(ctx context.Context, url, dest string, maxSize int64) error
	FetchChecksumFile(ctx context.Context, url string) ([]byte, error)
}

// UpdateService handles software updates
type UpdateService struct {
	cache          UpdateCache
	githubClient   GitHubReleaseClient
	currentVersion string
	buildType      string // "source" for manual builds, "release" for CI builds
}

// NewUpdateService creates a new UpdateService
func NewUpdateService(cache UpdateCache, githubClient GitHubReleaseClient, version, buildType string) *UpdateService {
	return &UpdateService{
		cache:          cache,
		githubClient:   githubClient,
		currentVersion: version,
		buildType:      buildType,
	}
}

// UpdateInfo contains update information
type UpdateInfo struct {
	CurrentVersion string       `json:"current_version"`
	LatestVersion  string       `json:"latest_version"`
	HasUpdate      bool         `json:"has_update"`
	ReleaseInfo    *ReleaseInfo `json:"release_info,omitempty"`
	Cached         bool         `json:"cached"`
	Warning        string       `json:"warning,omitempty"`
	BuildType      string       `json:"build_type"` // "source" or "release"
}

// ReleaseInfo contains GitHub release details
type ReleaseInfo struct {
	Name        string  `json:"name"`
	Body        string  `json:"body"`
	PublishedAt string  `json:"published_at"`
	HTMLURL     string  `json:"html_url"`
	Assets      []Asset `json:"assets,omitempty"`
}

// Asset represents a release asset
type Asset struct {
	Name        string `json:"name"`
	DownloadURL string `json:"download_url"`
	Size        int64  `json:"size"`
}

// GitHubRelease represents GitHub API response
type GitHubRelease struct {
	TagName     string        `json:"tag_name"`
	Name        string        `json:"name"`
	Body        string        `json:"body"`
	PublishedAt string        `json:"published_at"`
	HTMLURL     string        `json:"html_url"`
	Draft       bool          `json:"draft"`
	Prerelease  bool          `json:"prerelease"`
	Assets      []GitHubAsset `json:"assets"`
}

// RollbackVersion describes a release version the system can roll back to
type RollbackVersion struct {
	Version     string `json:"version"` // without "v" prefix, e.g. "0.1.146"
	PublishedAt string `json:"published_at"`
	HTMLURL     string `json:"html_url"`
}

type GitHubAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
	Size               int64  `json:"size"`
}

type WanchuanReleaseManifest struct {
	SchemaVersion       int      `json:"schema_version"`
	Channel             string   `json:"channel"`
	Version             string   `json:"version"`
	SourceSHA           string   `json:"source_sha"`
	ReleaseRepo         string   `json:"release_repo"`
	UpstreamRepo        string   `json:"upstream_repo"`
	UpstreamTag         string   `json:"upstream_tag"`
	UpstreamSHA         string   `json:"upstream_sha"`
	IntegrationCommit   string   `json:"integration_commit"`
	PatchManifestSHA256 string   `json:"patch_manifest_sha256"`
	PatchModules        []string `json:"patch_modules"`
}

// CheckUpdate checks for available updates
func (s *UpdateService) CheckUpdate(ctx context.Context, force bool) (*UpdateInfo, error) {
	// Try cache first
	if !force {
		if cached, err := s.getFromCache(ctx); err == nil && cached != nil {
			return cached, nil
		}
	}

	// Fetch from GitHub
	info, err := s.fetchLatestRelease(ctx)
	if err != nil {
		// Return cached on error
		if cached, cacheErr := s.getFromCache(ctx); cacheErr == nil && cached != nil {
			cached.Warning = "Using cached data: " + err.Error()
			return cached, nil
		}
		return &UpdateInfo{
			CurrentVersion: s.currentVersion,
			LatestVersion:  s.currentVersion,
			HasUpdate:      false,
			Warning:        err.Error(),
			BuildType:      s.buildType,
		}, nil
	}

	// Cache result
	s.saveToCache(ctx, info)
	return info, nil
}

// PerformUpdate downloads and applies the update
// Uses atomic file replacement pattern for safe in-place updates
func (s *UpdateService) PerformUpdate(ctx context.Context) error {
	info, err := s.CheckUpdate(ctx, true)
	if err != nil {
		return err
	}

	if !info.HasUpdate {
		return ErrNoUpdateAvailable
	}

	if info.ReleaseInfo == nil {
		return fmt.Errorf("update release metadata is missing")
	}
	return s.applyReleaseAssets(ctx, info.LatestVersion, info.ReleaseInfo.Assets)
}

// applyReleaseAssets downloads the platform archive from the given release assets,
// verifies its checksum, and atomically swaps the running binary.
// Shared by PerformUpdate (latest) and RollbackToVersion (specific older version).
func (s *UpdateService) applyReleaseAssets(ctx context.Context, expectedVersion string, releaseAssets []Asset) error {
	expectedVersion = strings.TrimPrefix(strings.TrimSpace(expectedVersion), "v")
	if parseWanchuanRevision(expectedVersion) < 1 {
		return fmt.Errorf("release %q is not a Wanchuan revision", expectedVersion)
	}

	downloadURL, checksumURL, manifestURL := selectWanchuanReleaseAssetURLs(
		expectedVersion,
		runtime.GOOS,
		runtime.GOARCH,
		releaseAssets,
	)
	if downloadURL == "" {
		return fmt.Errorf("no compatible release found for %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	if checksumURL == "" {
		return fmt.Errorf("release is missing required checksums.txt")
	}
	if manifestURL == "" {
		return fmt.Errorf("release is missing required %s", wanchuanReleaseManifestName)
	}

	for label, rawURL := range map[string]string{
		"download": downloadURL,
		"checksum": checksumURL,
		"manifest": manifestURL,
	} {
		if err := validateDownloadURL(rawURL); err != nil {
			return fmt.Errorf("invalid %s URL: %w", label, err)
		}
	}

	checksumData, err := s.githubClient.FetchChecksumFile(ctx, checksumURL)
	if err != nil {
		return fmt.Errorf("failed to download checksums: %w", err)
	}
	if len(checksumData) > maxReleaseMetadataSize {
		return fmt.Errorf("checksums.txt exceeds metadata limit")
	}
	manifestData, err := s.githubClient.FetchChecksumFile(ctx, manifestURL)
	if err != nil {
		return fmt.Errorf("failed to download %s: %w", wanchuanReleaseManifestName, err)
	}
	if len(manifestData) > maxReleaseMetadataSize {
		return fmt.Errorf("%s exceeds metadata limit", wanchuanReleaseManifestName)
	}
	if err := verifyBytesChecksum(wanchuanReleaseManifestName, manifestData, checksumData); err != nil {
		return fmt.Errorf("release manifest checksum verification failed: %w", err)
	}
	if err := validateWanchuanReleaseManifest(manifestData, expectedVersion); err != nil {
		return fmt.Errorf("release manifest validation failed: %w", err)
	}

	exePath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to get executable path: %w", err)
	}
	exePath, err = filepath.EvalSymlinks(exePath)
	if err != nil {
		return fmt.Errorf("failed to resolve symlinks: %w", err)
	}

	exeDir := filepath.Dir(exePath)
	tempDir, err := os.MkdirTemp(exeDir, ".sub2api-update-*")
	if err != nil {
		return fmt.Errorf("failed to create temp dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(tempDir) }()

	archivePath := filepath.Join(tempDir, filepath.Base(downloadURL))
	if err := s.downloadFile(ctx, downloadURL, archivePath); err != nil {
		return fmt.Errorf("download failed: %w", err)
	}
	if err := verifyFileChecksum(archivePath, checksumData); err != nil {
		return fmt.Errorf("checksum verification failed: %w", err)
	}

	newBinaryPath := filepath.Join(tempDir, "sub2api")
	if err := s.extractBinary(archivePath, newBinaryPath); err != nil {
		return fmt.Errorf("extraction failed: %w", err)
	}
	if err := os.Chmod(newBinaryPath, 0755); err != nil {
		return fmt.Errorf("chmod failed: %w", err)
	}

	backupPath := exePath + ".backup"
	_ = os.Remove(backupPath)
	if err := os.Rename(exePath, backupPath); err != nil {
		return fmt.Errorf("backup failed: %w", err)
	}
	if err := os.Rename(newBinaryPath, exePath); err != nil {
		if restoreErr := os.Rename(backupPath, exePath); restoreErr != nil {
			return fmt.Errorf("replace failed and restore failed: %w (restore error: %v)", err, restoreErr)
		}
		return fmt.Errorf("replace failed (restored backup): %w", err)
	}
	return nil
}

// Rollback restores the previous version
func (s *UpdateService) Rollback() error {
	exePath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to get executable path: %w", err)
	}
	exePath, err = filepath.EvalSymlinks(exePath)
	if err != nil {
		return fmt.Errorf("failed to resolve symlinks: %w", err)
	}

	backupFile := exePath + ".backup"
	if _, err := os.Stat(backupFile); os.IsNotExist(err) {
		return fmt.Errorf("no backup found")
	}

	// Replace current with backup
	if err := os.Rename(backupFile, exePath); err != nil {
		return fmt.Errorf("rollback failed: %w", err)
	}

	return nil
}

// ListRollbackVersions returns up to maxRollbackVersions release versions that are
// strictly older than the current version (the current version itself is excluded),
// newest first. Draft and prerelease entries are skipped.
func (s *UpdateService) ListRollbackVersions(ctx context.Context) ([]RollbackVersion, error) {
	releases, err := s.fetchRollbackCandidates(ctx)
	if err != nil {
		return nil, err
	}

	versions := make([]RollbackVersion, 0, len(releases))
	for _, r := range releases {
		versions = append(versions, RollbackVersion{
			Version:     strings.TrimPrefix(r.TagName, "v"),
			PublishedAt: r.PublishedAt,
			HTMLURL:     r.HTMLURL,
		})
	}
	return versions, nil
}

// RollbackToVersion downloads and installs a specific older version.
// The target must be one of the versions returned by ListRollbackVersions;
// anything else (including the current version) is rejected.
func (s *UpdateService) RollbackToVersion(ctx context.Context, version string) error {
	target := strings.TrimPrefix(strings.TrimSpace(version), "v")
	if target == "" {
		return ErrRollbackVersionNotAllowed
	}

	releases, err := s.fetchRollbackCandidates(ctx)
	if err != nil {
		return err
	}

	var match *GitHubRelease
	for _, r := range releases {
		if strings.TrimPrefix(r.TagName, "v") == target {
			match = r
			break
		}
	}
	if match == nil {
		return ErrRollbackVersionNotAllowed
	}

	assets := make([]Asset, len(match.Assets))
	for i, a := range match.Assets {
		assets[i] = Asset{
			Name:        a.Name,
			DownloadURL: a.BrowserDownloadURL,
			Size:        a.Size,
		}
	}

	return s.applyReleaseAssets(ctx, target, assets)
}

// fetchRollbackCandidates fetches recent releases and keeps the newest
// maxRollbackVersions entries strictly older than the current version.
func (s *UpdateService) fetchRollbackCandidates(ctx context.Context) ([]*GitHubRelease, error) {
	releases, err := s.githubClient.FetchRecentReleases(ctx, githubRepo, rollbackFetchPageSize)
	if err != nil {
		return nil, err
	}

	seen := make(map[string]bool, len(releases))
	candidates := make([]*GitHubRelease, 0, maxRollbackVersions)
	for _, r := range releases {
		if r == nil || r.Draft || r.Prerelease {
			continue
		}
		v := strings.TrimPrefix(r.TagName, "v")
		if v == "" || seen[v] {
			continue
		}
		if parseWanchuanRevision(s.currentVersion) > 0 &&
			(parseWanchuanRevision(v) < 1 || !hasRequiredWanchuanGitHubAssets(r.Assets)) {
			continue
		}
		// Only versions strictly older than current (also excludes current itself)
		if compareVersions(v, s.currentVersion) >= 0 {
			continue
		}
		seen[v] = true
		candidates = append(candidates, r)
	}

	sort.SliceStable(candidates, func(i, j int) bool {
		return compareVersions(
			strings.TrimPrefix(candidates[i].TagName, "v"),
			strings.TrimPrefix(candidates[j].TagName, "v"),
		) > 0
	})

	if len(candidates) > maxRollbackVersions {
		candidates = candidates[:maxRollbackVersions]
	}
	return candidates, nil
}

func (s *UpdateService) fetchLatestRelease(ctx context.Context) (*UpdateInfo, error) {
	release, err := s.githubClient.FetchLatestRelease(ctx, githubRepo)
	if err != nil {
		return nil, err
	}

	latestVersion := strings.TrimPrefix(release.TagName, "v")
	assets := make([]Asset, len(release.Assets))
	for i, a := range release.Assets {
		assets[i] = Asset{
			Name:        a.Name,
			DownloadURL: a.BrowserDownloadURL,
			Size:        a.Size,
		}
	}

	hasUpdate := compareVersions(s.currentVersion, latestVersion) < 0
	warning := ""
	if parseWanchuanRevision(latestVersion) < 1 {
		hasUpdate = false
		warning = "Latest owner release is not a Wanchuan revision"
	} else if !hasRequiredWanchuanAssets(assets) {
		hasUpdate = false
		warning = "Latest Wanchuan release is missing required manifest or checksums"
	}

	return &UpdateInfo{
		CurrentVersion: s.currentVersion,
		LatestVersion:  latestVersion,
		HasUpdate:      hasUpdate,
		ReleaseInfo: &ReleaseInfo{
			Name:        release.Name,
			Body:        release.Body,
			PublishedAt: release.PublishedAt,
			HTMLURL:     release.HTMLURL,
			Assets:      assets,
		},
		Cached:    false,
		Warning:   warning,
		BuildType: s.buildType,
	}, nil
}

func (s *UpdateService) downloadFile(ctx context.Context, downloadURL, dest string) error {
	return s.githubClient.DownloadFile(ctx, downloadURL, dest, maxDownloadSize)
}

func wanchuanReleaseArchiveName(version, goos, goarch string) string {
	extension := ".tar.gz"
	if goos == "windows" {
		extension = ".zip"
	}
	return fmt.Sprintf("sub2api_%s_%s_%s%s", version, goos, goarch, extension)
}

func selectWanchuanReleaseAssetURLs(version, goos, goarch string, assets []Asset) (downloadURL, checksumURL, manifestURL string) {
	expectedArchive := wanchuanReleaseArchiveName(version, goos, goarch)
	for _, asset := range assets {
		switch asset.Name {
		case expectedArchive:
			downloadURL = asset.DownloadURL
		case "checksums.txt":
			checksumURL = asset.DownloadURL
		case wanchuanReleaseManifestName:
			manifestURL = asset.DownloadURL
		}
	}
	return downloadURL, checksumURL, manifestURL
}

// validateDownloadURL checks if the URL is from an allowed domain
// SECURITY: This prevents SSRF and ensures downloads only come from trusted GitHub domains
func validateDownloadURL(rawURL string) error {
	parsedURL, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}

	// Must be HTTPS
	if parsedURL.Scheme != "https" {
		return fmt.Errorf("only HTTPS URLs are allowed")
	}

	// Check against allowed hosts
	host := parsedURL.Host
	// GitHub release URLs can be from github.com or objects.githubusercontent.com
	if host != allowedDownloadHost &&
		!strings.HasSuffix(host, "."+allowedDownloadHost) &&
		host != allowedAssetHost &&
		!strings.HasSuffix(host, "."+allowedAssetHost) {
		return fmt.Errorf("download from untrusted host: %s", host)
	}

	return nil
}

func checksumForFile(checksumData []byte, fileName string) (string, error) {
	scanner := bufio.NewScanner(strings.NewReader(string(checksumData)))
	for scanner.Scan() {
		parts := strings.Fields(scanner.Text())
		if len(parts) != 2 {
			continue
		}
		name := strings.TrimPrefix(strings.TrimPrefix(parts[1], "*"), "./")
		if name != fileName {
			continue
		}
		expected := strings.ToLower(parts[0])
		decoded, err := hex.DecodeString(expected)
		if err != nil || len(decoded) != sha256.Size {
			return "", fmt.Errorf("invalid checksum for %s", fileName)
		}
		return expected, nil
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	return "", fmt.Errorf("checksum not found for %s", fileName)
}

func verifyBytesChecksum(fileName string, data, checksumData []byte) error {
	expected, err := checksumForFile(checksumData, fileName)
	if err != nil {
		return err
	}
	actual := sha256.Sum256(data)
	actualHash := hex.EncodeToString(actual[:])
	if expected != actualHash {
		return fmt.Errorf("checksum mismatch: expected %s, got %s", expected, actualHash)
	}
	return nil
}

func verifyFileChecksum(filePath string, checksumData []byte) error {
	expected, err := checksumForFile(checksumData, filepath.Base(filePath))
	if err != nil {
		return err
	}
	f, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	actualHash := hex.EncodeToString(h.Sum(nil))
	if expected != actualHash {
		return fmt.Errorf("checksum mismatch: expected %s, got %s", expected, actualHash)
	}
	return nil
}

func validateWanchuanReleaseManifest(data []byte, expectedVersion string) error {
	if len(data) == 0 || len(data) > maxReleaseMetadataSize {
		return fmt.Errorf("invalid manifest size")
	}
	var manifest WanchuanReleaseManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return fmt.Errorf("decode manifest: %w", err)
	}
	expectedVersion = strings.TrimPrefix(strings.TrimSpace(expectedVersion), "v")
	if manifest.SchemaVersion != 1 {
		return fmt.Errorf("unsupported manifest schema %d", manifest.SchemaVersion)
	}
	if manifest.Channel != wanchuanReleaseChannel {
		return fmt.Errorf("unexpected release channel %q", manifest.Channel)
	}
	if manifest.Version != expectedVersion || parseWanchuanRevision(manifest.Version) < 1 {
		return fmt.Errorf("manifest version %q does not match expected Wanchuan version %q", manifest.Version, expectedVersion)
	}
	if manifest.ReleaseRepo != githubRepo {
		return fmt.Errorf("manifest release repo %q does not match %q", manifest.ReleaseRepo, githubRepo)
	}
	if manifest.UpstreamRepo != upstreamGithubRepo {
		return fmt.Errorf("manifest upstream repo %q does not match %q", manifest.UpstreamRepo, upstreamGithubRepo)
	}
	if parseVersion(manifest.UpstreamTag) != parseVersion(manifest.Version) {
		return fmt.Errorf("manifest upstream tag %q does not match release base version", manifest.UpstreamTag)
	}
	for label, value := range map[string]string{
		"source_sha":         manifest.SourceSHA,
		"upstream_sha":       manifest.UpstreamSHA,
		"integration_commit": manifest.IntegrationCommit,
	} {
		decoded, err := hex.DecodeString(value)
		if err != nil || len(decoded) != 20 {
			return fmt.Errorf("manifest %s is not a full commit sha", label)
		}
	}
	patchDigest, err := hex.DecodeString(manifest.PatchManifestSHA256)
	if err != nil || len(patchDigest) != sha256.Size {
		return fmt.Errorf("manifest patch_manifest_sha256 is invalid")
	}
	if len(manifest.PatchModules) == 0 {
		return fmt.Errorf("manifest patch_modules is empty")
	}
	return nil
}

func hasRequiredWanchuanAssets(assets []Asset) bool {
	hasChecksums := false
	hasManifest := false
	for _, asset := range assets {
		switch asset.Name {
		case "checksums.txt":
			hasChecksums = true
		case wanchuanReleaseManifestName:
			hasManifest = true
		}
	}
	return hasChecksums && hasManifest
}

func hasRequiredWanchuanGitHubAssets(assets []GitHubAsset) bool {
	hasChecksums := false
	hasManifest := false
	for _, asset := range assets {
		switch asset.Name {
		case "checksums.txt":
			hasChecksums = true
		case wanchuanReleaseManifestName:
			hasManifest = true
		}
	}
	return hasChecksums && hasManifest
}

func (s *UpdateService) extractBinary(archivePath, destPath string) error {
	f, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	var reader io.Reader = f

	// Handle gzip compression
	if strings.HasSuffix(archivePath, ".gz") || strings.HasSuffix(archivePath, ".tar.gz") || strings.HasSuffix(archivePath, ".tgz") {
		gzr, err := gzip.NewReader(f)
		if err != nil {
			return err
		}
		defer func() { _ = gzr.Close() }()
		reader = gzr
	}

	// Handle tar archive
	if strings.Contains(archivePath, ".tar") {
		tr := tar.NewReader(reader)
		for {
			hdr, err := tr.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				return err
			}

			// SECURITY: Prevent Zip Slip / Path Traversal attack
			// Only allow files with safe base names, no directory traversal
			baseName := filepath.Base(hdr.Name)

			// Check for path traversal attempts
			if strings.Contains(hdr.Name, "..") {
				return fmt.Errorf("path traversal attempt detected: %s", hdr.Name)
			}

			// Validate the entry is a regular file
			if hdr.Typeflag != tar.TypeReg {
				continue // Skip directories and special files
			}

			// Only extract the specific binary we need
			if baseName == "sub2api" || baseName == "sub2api.exe" {
				// Additional security: limit file size (max 500MB)
				const maxBinarySize = 500 * 1024 * 1024
				if hdr.Size > maxBinarySize {
					return fmt.Errorf("binary too large: %d bytes (max %d)", hdr.Size, maxBinarySize)
				}

				out, err := os.Create(destPath)
				if err != nil {
					return err
				}

				// Use LimitReader to prevent decompression bombs
				limited := io.LimitReader(tr, maxBinarySize)
				if _, err := io.Copy(out, limited); err != nil {
					_ = out.Close()
					return err
				}
				if err := out.Close(); err != nil {
					return err
				}
				return nil
			}
		}
		return fmt.Errorf("binary not found in archive")
	}

	// Direct copy for non-tar files (with size limit)
	const maxBinarySize = 500 * 1024 * 1024
	out, err := os.Create(destPath)
	if err != nil {
		return err
	}

	limited := io.LimitReader(reader, maxBinarySize)
	if _, err := io.Copy(out, limited); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

func (s *UpdateService) getFromCache(ctx context.Context) (*UpdateInfo, error) {
	data, err := s.cache.GetUpdateInfo(ctx)
	if err != nil {
		return nil, err
	}

	var cached struct {
		Latest      string       `json:"latest"`
		ReleaseInfo *ReleaseInfo `json:"release_info"`
		Timestamp   int64        `json:"timestamp"`
	}
	if err := json.Unmarshal([]byte(data), &cached); err != nil {
		return nil, err
	}

	if time.Now().Unix()-cached.Timestamp > updateCacheTTL {
		return nil, fmt.Errorf("cache expired")
	}

	hasUpdate := compareVersions(s.currentVersion, cached.Latest) < 0 &&
		parseWanchuanRevision(cached.Latest) > 0 &&
		cached.ReleaseInfo != nil &&
		hasRequiredWanchuanAssets(cached.ReleaseInfo.Assets)
	return &UpdateInfo{
		CurrentVersion: s.currentVersion,
		LatestVersion:  cached.Latest,
		HasUpdate:      hasUpdate,
		ReleaseInfo:    cached.ReleaseInfo,
		Cached:         true,
		BuildType:      s.buildType,
	}, nil
}

func (s *UpdateService) saveToCache(ctx context.Context, info *UpdateInfo) {
	cacheData := struct {
		Latest      string       `json:"latest"`
		ReleaseInfo *ReleaseInfo `json:"release_info"`
		Timestamp   int64        `json:"timestamp"`
	}{
		Latest:      info.LatestVersion,
		ReleaseInfo: info.ReleaseInfo,
		Timestamp:   time.Now().Unix(),
	}

	data, _ := json.Marshal(cacheData)
	_ = s.cache.SetUpdateInfo(ctx, string(data), time.Duration(updateCacheTTL)*time.Second)
}

// compareVersions compares the upstream semantic version first and then the
// owner-controlled Wanchuan patch revision. A fork release such as
// 2.9.6-wanchuan.2 is newer than 2.9.6-wanchuan.1 and 2.9.6, but remains older
// than the next upstream release (for example 2.9.7).
func compareVersions(current, latest string) int {
	currentParts := parseVersion(current)
	latestParts := parseVersion(latest)

	for i := 0; i < 3; i++ {
		if currentParts[i] < latestParts[i] {
			return -1
		}
		if currentParts[i] > latestParts[i] {
			return 1
		}
	}

	currentRevision := parseWanchuanRevision(current)
	latestRevision := parseWanchuanRevision(latest)
	if currentRevision < latestRevision {
		return -1
	}
	if currentRevision > latestRevision {
		return 1
	}
	return 0
}

func parseVersion(v string) [3]int {
	v = strings.TrimPrefix(v, "v")
	if idx := strings.IndexByte(v, '-'); idx != -1 {
		v = v[:idx]
	}
	parts := strings.Split(v, ".")
	result := [3]int{0, 0, 0}
	for i := 0; i < len(parts) && i < 3; i++ {
		if parsed, err := strconv.Atoi(parts[i]); err == nil {
			result[i] = parsed
		}
	}
	return result
}

func parseWanchuanRevision(v string) int {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	_, suffix, ok := strings.Cut(v, "-")
	if !ok {
		return 0
	}
	const prefix = "wanchuan."
	if !strings.HasPrefix(suffix, prefix) {
		return 0
	}
	revisionText := strings.TrimPrefix(suffix, prefix)
	if revisionText == "" || strings.Contains(revisionText, ".") {
		return 0
	}
	revision, err := strconv.Atoi(revisionText)
	if err != nil || revision < 1 {
		return 0
	}
	return revision
}
