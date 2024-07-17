package registry

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewAuthenticator(t *testing.T) {
	t.Run("Without kubelet config, no Kubelet authenticator is added", func(t *testing.T) {
		for _, authenticator := range NewAuthenticator("", "", []string{}) {
			switch authenticator.(type) {
			case KubeletAuthenticator:
				t.Errorf("KubeletAuthenticator should not be added")
			case ImagePullSecretAuthenticator:
			case ContainerDAuthenticator:
			case DockerConfigFileAuthenticator:
			case AnonymousAuthenticator:
			default:
				t.Errorf("Unexpected authenticator type")
			}
		}
	})

	t.Run("With kubelet config, no Kubelet authenticator is added", func(t *testing.T) {
		for _, authenticator := range NewAuthenticator("", "", []string{}) {
			switch authenticator.(type) {
			case KubeletAuthenticator:
			case ImagePullSecretAuthenticator:
			case ContainerDAuthenticator:
			case DockerConfigFileAuthenticator:
			case AnonymousAuthenticator:
			default:
				t.Errorf("Unexpected authenticator type")
			}
		}
	})

	t.Run("With private registry patterns, they are added to anonymous authenticator", func(t *testing.T) {
		for _, authenticator := range NewAuthenticator("", "", []string{"", "  *.example.com", "registry.io:8080/path  "}) {
			switch a := authenticator.(type) {
			case KubeletAuthenticator:
			case ImagePullSecretAuthenticator:
			case ContainerDAuthenticator:
			case DockerConfigFileAuthenticator:
			case AnonymousAuthenticator:
				assert.Contains(t, a.PrivateRegistryPatterns, "*.example.com")
				assert.Contains(t, a.PrivateRegistryPatterns, "registry.io:8080/path")
				assert.Len(t, a.PrivateRegistryPatterns, 2)
			default:
				t.Errorf("Unexpected authenticator type")
			}
		}
	})
}
