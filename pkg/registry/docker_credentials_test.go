package registry

import (
	"context"
	"testing"

	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
)

func TestDockerAuthenticatorWithimagePullSecret(t *testing.T) {
	imagePullSecret := `{"auths":{"registry.example.com":{"username":"user","password":"pass","auth":"YXV0aDp1c2VyOnBhc3M="}}}`
	registry := "registry.example.com"
	image := "myimage"
	tag := "latest"

	authenticator := ImagePullSecretAuthenticator{} // Create an instance of the RegistryAuthenticator
	candidates := make(chan AuthenticationToken)
	go authenticator.Authenticate(context.Background(), imagePullSecret, registry, image, tag, candidates)

	receivedToken, ok := <-candidates
	assert.True(t, ok, "AuthenticationToken not received")

	expectedToken := AuthenticationToken{
		Kind:  "Basic",
		Token: "YXV0aDp1c2VyOnBhc3M=",
	}

	assert.Equal(t, expectedToken, receivedToken)

}

func TestDockerConfigFileWithimagePullSecret(t *testing.T) {
	imagePullSecret := `{"auths":{"registry.example.com":{"username":"user","password":"pass","auth":"YXV0aDp1c2VyOnBhc3M="}}}`
	registry := "registry.example.com"
	image := "myimage"
	tag := "latest"

	fs := afero.NewMemMapFs()
	afero.WriteFile(fs, "/var/lib/kubelet/config.json", []byte(imagePullSecret), 0644)

	authenticator := DockerConfigFileAuthenticator{fs: fs} // Create an instance of the RegistryAuthenticator
	candidates := make(chan AuthenticationToken)
	go authenticator.Authenticate(context.Background(), imagePullSecret, registry, image, tag, candidates)

	receivedToken, ok := <-candidates
	assert.True(t, ok, "AuthenticationToken not received")

	expectedToken := AuthenticationToken{
		Kind:  "Basic",
		Token: "YXV0aDp1c2VyOnBhc3M=",
	}

	assert.Equal(t, expectedToken, receivedToken)

}
