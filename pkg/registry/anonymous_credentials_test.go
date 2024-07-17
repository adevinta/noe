package registry

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestAnonymousAuthenticator(t *testing.T) {
	authenticator := AnonymousAuthenticator{}
	candidates := make(chan AuthenticationToken, 1)
	authenticator.Authenticate(context.Background(), "", "", "", "", candidates)
	candidate := <-candidates
	assert.Empty(t, candidate.Kind)
	assert.Empty(t, candidate.Token)
	assert.Equal(t, candidate.Ref.Provider, "anonymous")
}

func TestAnonymousAuthenticatorSkipsPrivateRegistries(t *testing.T) {
	authenticator := AnonymousAuthenticator{
		PrivateRegistryPatterns: []string{"*.example.com", "registry.io:8080/path"},
	}
	candidates := make(chan AuthenticationToken, 1)
	go func() {
		authenticator.Authenticate(context.Background(), "", "registry.example.com", "my/image", "latest", candidates)
		authenticator.Authenticate(context.Background(), "", "registry.io:8080", "/path/to/my/image", "latest", candidates)
		close(candidates)
	}()
	foundAuths := 0
	for range candidates {
		foundAuths++
	}
	assert.Equal(t, 0, foundAuths)
}
