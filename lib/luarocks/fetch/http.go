package fetch

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// defaultUserAgent is the User-Agent header sent by httpBackend when
// Options.UserAgent is empty.
const defaultUserAgent = "go-luarocks/0.1"

const (
	// httpStatusErrThreshold is the first status code treated as an error;
	// any response at or above it (4xx/5xx) fails the fetch.
	httpStatusErrThreshold = 400

	// httpClientTimeout bounds a single fetch end-to-end.
	httpClientTimeout = 2 * time.Minute

	// maxRedirects caps the redirect chain followed by the HTTP client.
	maxRedirects = 5
)

// httpBackend downloads rawURL via net/http and unpacks recognized
// archive formats (.zip, .tar.gz, .tgz, .tar) into destDir. Other content
// types are written to destDir/<basename-from-url> and destDir is
// returned as-is.
type httpBackend struct{}

// Fetch performs the GET, writes the body to a temp file under destDir,
// then unpacks if the URL or response content suggests an archive.
//
// TLS verification is enabled by default. If the host appears in
// opts.InsecureServers, a Transport with InsecureSkipVerify=true is used
// instead — opt-in for tarantool's rocks server which historically
// served over HTTP without TLS.
func (httpBackend) Fetch(ctx context.Context, rawURL, destDir string, opts Options) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("fetch.http: parse %q: %w", rawURL, err)
	}

	if err := os.MkdirAll(destDir, dirPerm); err != nil {
		return "", fmt.Errorf("fetch.http: mkdir %q: %w", destDir, err)
	}

	client := buildHTTPClient(u.Host, opts)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", fmt.Errorf("fetch.http: new request: %w", err)
	}

	ua := opts.UserAgent
	if ua == "" {
		ua = defaultUserAgent
	}

	req.Header.Set("User-Agent", ua)

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetch.http: GET %s: %w", rawURL, err)
	}

	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= httpStatusErrThreshold {
		return "", fmt.Errorf("fetch.http: GET %s: status %d", rawURL, resp.StatusCode)
	}

	base := filepath.Base(u.Path)
	if base == "" || base == "/" || base == "." {
		base = "download"
	}

	tmp := filepath.Join(destDir, "."+base+".part")

	// tmp is built from destDir (caller-provided) plus a sanitized basename.
	f, err := os.Create(tmp)
	if err != nil {
		return "", fmt.Errorf("fetch.http: create %q: %w", tmp, err)
	}

	if _, err := io.Copy(f, resp.Body); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)

		return "", fmt.Errorf("fetch.http: copy body: %w", err)
	}

	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)

		return "", fmt.Errorf("fetch.http: close: %w", err)
	}

	dst := filepath.Join(destDir, base)
	if err := os.Rename(tmp, dst); err != nil {
		_ = os.Remove(tmp)

		return "", fmt.Errorf("fetch.http: rename: %w", err)
	}

	lower := strings.ToLower(base)

	switch {
	// `.rock`/`.src.rock` archives are zip files under a different
	// extension — upstream luarocks unpacks them the same way (a .src.rock
	// bundles the rockspec plus the source). Treat them like .zip so the
	// rockspec/source inside become visible to the caller.
	case strings.HasSuffix(lower, ".zip"),
		strings.HasSuffix(lower, ".rock"):
		err := unzip(dst, destDir)
		if err != nil {
			return "", fmt.Errorf("fetch.http: unzip: %w", err)
		}

		_ = os.Remove(dst)
	case strings.HasSuffix(lower, ".tar.gz"),
		strings.HasSuffix(lower, ".tgz"):
		err := untargz(dst, destDir)
		if err != nil {
			return "", fmt.Errorf("fetch.http: untar.gz: %w", err)
		}

		_ = os.Remove(dst)
	case strings.HasSuffix(lower, ".tar"):
		err := untar(dst, destDir)
		if err != nil {
			return "", fmt.Errorf("fetch.http: untar: %w", err)
		}

		_ = os.Remove(dst)
	}

	return destDir, nil
}

func buildHTTPClient(host string, opts Options) *http.Client {
	insecure := false

	for _, h := range opts.InsecureServers {
		if strings.EqualFold(h, host) {
			insecure = true

			break
		}
	}

	tr := &http.Transport{}
	if insecure {
		tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}

	return &http.Client{
		Transport: tr,
		Timeout:   httpClientTimeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= maxRedirects {
				return fmt.Errorf("stopped after %d redirects", maxRedirects)
			}

			return nil
		},
	}
}

func unzip(archive, dst string) error {
	r, err := zip.OpenReader(archive)
	if err != nil {
		return err
	}

	defer func() { _ = r.Close() }()

	for _, f := range r.File {
		// target is validated against dst below to reject path traversal.
		target := filepath.Join(dst, f.Name)
		if !strings.HasPrefix(target, filepath.Clean(dst)+string(os.PathSeparator)) && target != filepath.Clean(dst) {
			return fmt.Errorf("zip entry %q escapes dst", f.Name)
		}

		if f.FileInfo().IsDir() {
			err := os.MkdirAll(target, dirPerm)
			if err != nil {
				return err
			}

			continue
		}

		if err := os.MkdirAll(filepath.Dir(target), dirPerm); err != nil {
			return err
		}

		rc, err := f.Open()
		if err != nil {
			return err
		}

		out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, f.Mode())
		if err != nil {
			_ = rc.Close()

			return err
		}

		// Archive members are rocks sources of bounded size; copying the full
		// entry is intended. A decompression-bomb limit would break valid rocks.
		if _, err := io.Copy(out, rc); err != nil {
			_ = rc.Close()
			_ = out.Close()

			return err
		}

		_ = rc.Close()

		if err := out.Close(); err != nil {
			return err
		}
	}

	return nil
}

func untargz(archive, dst string) error {
	f, err := os.Open(archive)
	if err != nil {
		return err
	}

	defer func() { _ = f.Close() }()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}

	defer func() { _ = gz.Close() }()

	return extractTar(tar.NewReader(gz), dst)
}

func untar(archive, dst string) error {
	f, err := os.Open(archive)
	if err != nil {
		return err
	}

	defer func() { _ = f.Close() }()

	return extractTar(tar.NewReader(f), dst)
}

func extractTar(tr *tar.Reader, dst string) error {
	cleanDst := filepath.Clean(dst)

	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}

		if err != nil {
			return err
		}

		// target is validated against cleanDst below to reject path traversal.
		target := filepath.Join(dst, hdr.Name)
		if !strings.HasPrefix(target, cleanDst+string(os.PathSeparator)) && target != cleanDst {
			return fmt.Errorf("tar entry %q escapes dst", hdr.Name)
		}

		switch hdr.Typeflag {
		case tar.TypeDir:
			// hdr.Mode is a tar permission word; the low bits are the only
			// meaningful ones and always fit in a FileMode.
			err := os.MkdirAll(target, os.FileMode(uint32(hdr.Mode))|ownerSearchBit)
			if err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), dirPerm); err != nil {
				return err
			}

			out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, os.FileMode(uint32(hdr.Mode)))
			if err != nil {
				return err
			}

			if _, err := io.Copy(out, tr); err != nil {
				_ = out.Close()

				return err
			}

			if err := out.Close(); err != nil {
				return err
			}
		case tar.TypeSymlink, tar.TypeLink:
			// Skip links: rocks sources shouldn't need them in v1, and
			// blindly creating symlinks crosses safety lines for archive
			// extraction.
			continue
		}
	}
}
