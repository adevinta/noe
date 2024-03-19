package registry

import (
	"context"

	"github.com/spf13/afero"
)

type AuthenticationToken struct {
	Kind  string
	Token string
}

type Authenticator interface {
	Authenticate(ctx context.Context, imagePullSecret, registry, image, tag string, candidates chan AuthenticationToken)
}

type AuthenticatorFunc func(ctx context.Context, imagePullSecret, registry, image, tag string, candidates chan AuthenticationToken)

func (f AuthenticatorFunc) Authenticate(ctx context.Context, imagePullSecret, registry, image, tag string, candidates chan AuthenticationToken) {
	f(ctx, imagePullSecret, registry, image, tag, candidates)
}

var _ Authenticator = Authenticators{}

type Authenticators []Authenticator

func (a Authenticators) Authenticate(ctx context.Context, imagePullSecret, registry, image, tag string, candidates chan AuthenticationToken) {
	for _, auth := range a {
		auth.Authenticate(ctx, imagePullSecret, registry, image, tag, candidates)
	}
}

func NewAuthenticator() Authenticators {
	fs := afero.NewOsFs()
	a := Authenticators{
		ImagePullSecretAuthenticator{},
		ContainerDAuthenticator{fs: fs},
		DockerConfigFileAuthenticator{fs: fs},
		AnonymousAuthenticator{},
	}
	return a
}
