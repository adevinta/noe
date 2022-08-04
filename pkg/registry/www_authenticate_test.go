package registry

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseWWWAuthenticateHeader(t *testing.T) {
	kind, authParams, err := parseWWWAuthenticateHeader(`Bearer realm="https://ghcr.io/token",service="ghcr.io",scope="repository:registry/path/image/name:pull"`)
	require.NoError(t, err)
	assert.Equal(t, "Bearer", kind)
	assert.Equal(t, map[string]string{
		"realm":   "https://ghcr.io/token",
		"service": "ghcr.io",
		"scope":   "repository:registry/path/image/name:pull",
	}, authParams)
}
