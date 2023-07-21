package registry

import (
	"context"
	"testing"

	"github.com/pelletier/go-toml"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
)

func TestAuthenticateWithimagePullSecret(t *testing.T) {
	imagePullSecret := `{"auths":{"registry.example.com":{"username":"user","password":"pass","auth":"YXV0aDp1c2VyOnBhc3M="}}}`
	registry := "registry.example.com"
	image := "myimage"
	tag := "latest"

	authenticator := RegistryAuthenticator{fs: afero.NewMemMapFs()} // Create an instance of the RegistryAuthenticator

	candidates := authenticator.Authenticate(context.Background(), imagePullSecret, registry, image, tag)

	receivedToken, ok := <-candidates
	assert.True(t, ok, "AuthenticationToken not received")

	expectedToken := AuthenticationToken{
		Kind:  "Basic",
		Token: "YXV0aDp1c2VyOnBhc3M=",
	}

	assert.Equal(t, expectedToken, receivedToken)

}

func TestRegistryAuthenticator_GetHeaderOnContainerdFiles(t *testing.T) {
	fs := afero.NewMemMapFs()

	// Create test directory and files in the in-memory file system
	err := fs.MkdirAll("/etc/containerd", 0755)
	assert.NoError(t, err)

	config := ContainerdConfig{
		Server: "registry.example.com",
		Hosts: map[string]ContainerdHostConfig{
			"example-host": {
				Capabilities: []string{"cap1", "cap2"},
				Header: ContainerdHeader{
					Authorization: "Basic dXNlcjpwYXNz",
				},
			},
		},
	}

	configData, err := toml.Marshal(config)
	assert.NoError(t, err)

	err = afero.WriteFile(fs, "/etc/containerd/config.toml", configData, 0644)
	assert.NoError(t, err)

	authenticator := RegistryAuthenticator{fs: fs} // Create an instance of the RegistryAuthenticator

	imagePullSecret := ""
	registry := "registry.example.com"
	image := "myimage"
	tag := "latest"

	candidates := authenticator.Authenticate(context.Background(), imagePullSecret, registry, image, tag)

	receivedToken, ok := <-candidates
	assert.True(t, ok, "AuthenticationToken not received")

	expectedToken := AuthenticationToken{
		Kind:  "Basic",
		Token: "dXNlcjpwYXNz",
	}

	assert.Equal(t, expectedToken, receivedToken)
}
