package registry

import (
	"context"
	"testing"

	"github.com/pelletier/go-toml/v2"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
)

func testContainerDConfigUnmarshall(t *testing.T, data string, expectedConfig ContainerdConfig) {
	t.Helper()
	config := ContainerdConfig{}
	err := toml.Unmarshal([]byte(data), &config)
	assert.NoError(t, err)
	assert.Equal(t, expectedConfig, config)
}

func TestContainerDAuthHeadersIsCaseInsensitive(t *testing.T) {
	testContainerDConfigUnmarshall(t,
		`
server = "https://docker.io"

[host."https://registry-1.docker.io"]
capabilities = ["pull", "resolve"]

[host."https://registry-1.docker.io".header]
authorization = "Basic secret"
`,
		ContainerdConfig{
			Server: "https://docker.io",
			Hosts: map[string]ContainerdHostConfig{
				"https://registry-1.docker.io": {
					Capabilities: []string{"pull", "resolve"},
					Header: ContainerdHeader{
						Authorization: "Basic secret",
					},
				},
			},
		},
	)
	testContainerDConfigUnmarshall(t,
		`
server = "https://docker.io"

[host."https://registry-1.docker.io"]
capabilities = ["pull", "resolve"]

[host."https://registry-1.docker.io".header]
Authorization = "Basic secret"
`,
		ContainerdConfig{
			Server: "https://docker.io",
			Hosts: map[string]ContainerdHostConfig{
				"https://registry-1.docker.io": {
					Capabilities: []string{"pull", "resolve"},
					Header: ContainerdHeader{
						Authorization: "Basic secret",
					},
				},
			},
		},
	)
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

	authenticator := ContainerDAuthenticator{fs: fs} // Create an instance of the RegistryAuthenticator

	imagePullSecret := ""
	registry := "registry.example.com"
	image := "myimage"
	tag := "latest"
	candidates := make(chan AuthenticationToken)
	go authenticator.Authenticate(context.Background(), imagePullSecret, registry, image, tag, candidates)

	receivedToken, ok := <-candidates
	assert.True(t, ok, "AuthenticationToken not received")

	expectedToken := AuthenticationToken{
		Kind:  "Basic",
		Token: "dXNlcjpwYXNz",
		Ref: AuthenticationSourceRef{
			Provider: "containerD",
		},
	}

	assert.Equal(t, expectedToken, receivedToken)
}
