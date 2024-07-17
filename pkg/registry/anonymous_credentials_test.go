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
