package search

import (
	"encoding/base64"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"

	"github.com/apex/log"
	"github.com/tarantool/tt/cli/install_ee"

	"github.com/gocolly/colly"
	"github.com/tarantool/tt/cli/util"
)

// collectBundleReferences crawls all urls from the passed source, collects references
// containing sdk bundles for the host system and returns them in a slice of strings.
func collectBundleReferences(searchCtx *SearchCtx, baseUrl string,
	credentials install_ee.UserCredentials) ([]string, error) {

	eeUrl, err := url.Parse(baseUrl)
	if err != nil {
		return nil, err
	}

	filters, err := prepareDisallowedURLFilters(searchCtx)
	if err != nil {
		return nil, err
	}

	c := colly.NewCollector(colly.Async(true),
		colly.DisallowedURLFilters(filters...),
		colly.MaxDepth(10))

	c.SetRequestTimeout(0)

	refs := make([]string, 0)
	mtx := &sync.Mutex{}
	c.OnHTML("a[href]", func(e *colly.HTMLElement) {
		hrefValue := e.Attr("href")
		isBackLink := strings.HasSuffix(hrefValue, "../")
		isSameLink := strings.HasSuffix(hrefValue, "./")
		isBundleLink := strings.HasSuffix(hrefValue, ".tar.gz")
		isSHA256Link := strings.HasSuffix(hrefValue, ".sha256")

		if isBackLink || isSameLink || isSHA256Link {
			return
		}

		if isBundleLink {
			mtx.Lock()
			refs = append(refs, hrefValue)
			mtx.Unlock()
			return
		}

		e.Request.Visit(e.Attr("href"))
	})

	auth := credentials.Username + ":" + credentials.Password
	authEncoded := base64.StdEncoding.EncodeToString([]byte(auth))
	c.OnRequest(func(r *colly.Request) {
		if r.URL.Host != eeUrl.Host {
			return
		}
		r.Headers.Add("Authorization", "Basic "+authEncoded)
	})

	var responseCode int
	c.OnError(func(response *colly.Response, error error) {
		err = error
		responseCode = response.StatusCode
	})

	c.Visit(eeUrl.String())
	c.Wait()

	if responseCode == http.StatusUnauthorized {
		warnMsg := "tarantool.io login credentials cannot be used for tarantool-ee installation." +
			" Contact your system administrator for the right credentials."
		log.Warn(warnMsg)
	}
	if err != nil {
		return nil, err
	}

	return refs, nil
}

// prepareDisallowedFilters prepares a slice of regular expressions
// for filtering urls by crawling.
func prepareDisallowedURLFilters(searchCtx *SearchCtx) ([]*regexp.Regexp, error) {
	filters := make([]*regexp.Regexp, 0)

	arch, err := util.GetArch()
	if err != nil {
		return nil, err
	}

	switch arch {
	case "x86_64":
		filters = append(filters, regexp.MustCompile("aarch64"))
	case "aarch64":
		filters = append(filters, regexp.MustCompile("x86_64"))
	default:
	}

	osType, err := util.GetOs()
	if err != nil {
		return nil, err
	}

	filters = append(filters, regexp.MustCompile("/enterprise-doc/"))
	filters = append(filters, regexp.MustCompile("/tdg/"))
	filters = append(filters, regexp.MustCompile("/tdg2/"))

	if !searchCtx.Dbg {
		filters = append(filters, regexp.MustCompile("/debug/"))
	}

	if !searchCtx.Dev {
		filters = append(filters, regexp.MustCompile("/dev/"))
	}

	if osType == util.OsMacos {
		filters = append(filters, regexp.MustCompile("/linux/"))
	} else {
		filters = append(filters, regexp.MustCompile("/macos/"))
		filters = append(filters, regexp.MustCompile("/enterprise-macos/"))
	}

	return filters, nil
}
