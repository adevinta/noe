//go:build license

package maiao

import (
	"math"
	"os"
	"strings"
	"testing"
	"time"

	pkggodevclient "github.com/guseggert/pkggodev-client"
	"github.com/stretchr/testify/require"
	"golang.org/x/mod/modfile"
)

var (
	acceptedLicenses = map[string]struct{}{
		"MIT":          {},
		"Apache-2.0":   {},
		"BSD-3-Clause": {},
		"BSD-2-Clause": {},
		"ISC":          {},
		"CC-BY-SA-4.0": {},
		"MPL-2.0":      {},
	}

	knownUndetectedLicenses = map[string]string{
		// bufpipe was later added the MIT license: https://github.com/acomagu/bufpipe/blob/cd7a5f79d3c413d14c0c60fd31dae7b397fc955a/LICENSE
		"github.com/acomagu/bufpipe@v1.0.3": "MIT",
	}

	maxRetryAttempts = 4
)

func calculateBackoffTime(attempt int) time.Duration {
	return time.Duration(math.Pow(5, float64(attempt)))*time.Second + 10*time.Second
}

func isRateLimitError(err error) bool {
	return strings.Contains(err.Error(), "Too Many Requests")
}

func TestLicenses(t *testing.T) {
	b, err := os.ReadFile("go.mod")
	require.NoError(t, err)
	file, err := modfile.Parse("go.mod", b, nil)
	require.NoError(t, err)
	client := pkggodevclient.New()
	for _, req := range file.Require {
		var pkg *pkggodevclient.Package
		var err error

		attempt := 0
		for ; attempt < maxRetryAttempts; attempt++ {
			pkg, err = client.DescribePackage(pkggodevclient.DescribePackageRequest{
				Package: req.Mod.Path,
			})
			if err == nil {
				break
			}

			if !isRateLimitError(err) {
				break
			}

			if attempt < maxRetryAttempts-1 {
				waitTime := calculateBackoffTime(attempt)
				t.Logf("Rate limited while checking %s, waiting %v before retry %d/%d", req.Mod.Path, waitTime, attempt+1, maxRetryAttempts)
				time.Sleep(waitTime)
			}
		}

		if attempt >= maxRetryAttempts {
			t.Logf("Skipping license check for %s due to persistent rate limiting", req.Mod.Path)
		}

		require.NoError(t, err)
		licences := strings.Split(pkg.License, ",")
		for _, license := range licences {
			license = strings.TrimSpace(license)
			if license == "None detected" {
				if known, ok := knownUndetectedLicenses[req.Mod.String()]; ok {
					license = known
				}
			}
			if _, ok := acceptedLicenses[license]; !ok {
				t.Errorf("dependency %s is using unexpected license %s. Check that this license complies with MIT in which maiao is released and update the checks accordingly or change dependency", req.Mod, license)
			}
		}
	}
}
